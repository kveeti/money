package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

const EnableBankingSource = "enable_banking"

var (
	ErrBankSyncInProgress           = errors.New("bank account sync is already in progress")
	ErrBankSyncNotFound             = errors.New("bank sync job not found")
	ErrBankSyncAccountNotFound      = errors.New("bank account not found")
	ErrBankSyncDuplicateAccount     = errors.New("bank account was selected more than once")
	ErrBankSyncRepeatedContinuation = errors.New("Enable Banking returned a repeated continuation key")
)

type EnableBankingSyncTarget struct {
	IntegrationID string `json:"integration_id"`
	AccountUID    string `json:"account_uid,omitempty"`
	AllAccounts   bool   `json:"all_accounts,omitempty"`
}

type EnableBankingSyncJob struct {
	BatchID         string
	ClaimID         string
	BucketID        string
	AccountUID      string
	DateFrom        time.Time
	ContinuationKey string
	NextSequence    int64
	FetchedAll      bool
	Attempts        int
}

func (d *Data) HasActiveEnableBankingSyncs(ctx context.Context, userID string) (bool, error) {
	var active bool
	err := d.db.QueryRowContext(ctx, `
		select exists (
			select 1
			from import_batches
			where user_id = $1
			  and source = $2
			  and status in ('queued', 'syncing')
		)
	`, userID, EnableBankingSource).Scan(&active)
	return active, err
}

// EnqueueEnableBankingSyncs resolves saved bank accounts and their mapped
// buckets, validates the full selection, and creates every sync job atomically.
func (d *Data) EnqueueEnableBankingSyncs(ctx context.Context, userID string, targets []EnableBankingSyncTarget, dateTo time.Time) ([]string, error) {
	if len(targets) == 0 {
		return []string{}, nil
	}
	allAccounts := targets[0].AllAccounts
	if allAccounts && len(targets) != 1 {
		return nil, ErrIntegrationAccount
	}
	seen := make(map[string]bool, len(targets))
	integrations := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target.IntegrationID == "" || target.AllAccounts != allAccounts || (!allAccounts && target.AccountUID == "") {
			return nil, ErrIntegrationAccount
		}
		key := target.IntegrationID + ":" + target.AccountUID
		if seen[key] {
			return nil, ErrBankSyncDuplicateAccount
		}
		seen[key] = true
		integrations[target.IntegrationID] = true
	}
	encodedTargets, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}

	var ownedIntegrations, resolvedAccounts, mappedAccounts int
	var encodedBatchIDs []byte
	err = d.db.QueryRowContext(ctx, `
		with requested as materialized (
			select
				target.integration_id,
				target.account_uid,
				raw_target.ordinality as request_ordinality
			from jsonb_array_elements($3::jsonb) with ordinality as raw_target(value, ordinality)
			cross join lateral jsonb_to_record(raw_target.value) as target(
				integration_id uuid,
				account_uid text,
				all_accounts boolean
			)
		), owned_integrations as materialized (
			select
				requested.request_ordinality,
				requested.account_uid as requested_account_uid,
				integration.id,
				integration.data,
				integration.updated_at
			from requested
			join bank_integrations integration
			  on integration.id = requested.integration_id
			 and integration.user_id = $1
			 and integration.provider = $2
			 and integration.deleted_at is null
		), resolved_accounts as materialized (
			select
				integration.request_ordinality,
				raw_account.ordinality as account_ordinality,
				integration.id as integration_id,
				integration.data->>'bank' as bank,
				integration.updated_at,
				account.uid as account_uid,
				account.identification_hash,
				account.iban
			from owned_integrations integration
			cross join lateral jsonb_array_elements(
				coalesce(integration.data->'accounts', '[]'::jsonb)
			) with ordinality as raw_account(value, ordinality)
			cross join lateral jsonb_to_record(raw_account.value) as account(
				uid text,
				identification_hash text,
				iban text
			)
			where $4::boolean
			   or account.uid = integration.requested_account_uid
		), mapped_accounts as materialized (
			select
				account.*,
				bucket.id as bucket_id
			from resolved_accounts account
			join buckets bucket
			  on bucket.owner_user_id = $1
			 and bucket.iban = account.iban
			 and bucket.active_bank_integration_id = account.integration_id
			 and bucket.kind in ('asset', 'liability')
			 and not bucket.hidden
			where account.account_uid <> ''
			  and account.identification_hash <> ''
		), selection as (
			select
				(select count(distinct id) from owned_integrations) as owned_integrations,
				(select count(*) from resolved_accounts) as resolved_accounts,
				(select count(*) from mapped_accounts) as mapped_accounts,
				(select count(*) from requested) as requested_accounts
		), valid_selection as (
			select
				selection.*,
				owned_integrations = (select count(distinct integration_id) from requested)
				and (
					$4::boolean
					or (
						resolved_accounts = requested_accounts
						and mapped_accounts = requested_accounts
					)
				) as valid
			from selection
		), prepared as materialized (
			select
				uuidv7() as batch_id,
				account.*,
				greatest(
					coalesce(
						(
							select max(previous.date_to) - 2
							from enable_banking_syncs previous
							where previous.integration_id = account.integration_id
							  and previous.identification_hash = account.identification_hash
							  and previous.completed_at is not null
							  and previous.completed_at >= account.updated_at
						),
						($5::date - interval '2 years')::date
					),
					($5::date - interval '2 years')::date
				) as date_from
			from mapped_accounts account
			cross join valid_selection selection
			where selection.valid
		), created_batches as (
			insert into import_batches (
				id, user_id, bucket_id, source, filename, created_at, status
			)
			select batch_id, $1, bucket_id, $2, bank || ' sync', $6, 'queued'
			from prepared
			returning id
		), created_syncs as (
			insert into enable_banking_syncs (
				batch_id, integration_id, identification_hash, account_uid, date_from, date_to
			)
			select
				sync.batch_id,
				sync.integration_id,
				sync.identification_hash,
				sync.account_uid,
				sync.date_from,
				$5
			from prepared sync
			join created_batches batch on batch.id = sync.batch_id
			returning batch_id
		), audited as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), $1, 'import_batches', batch_id, 'insert', null, $6
			from created_syncs
		)
		select
			selection.owned_integrations,
			selection.resolved_accounts,
			selection.mapped_accounts,
			coalesce(
				(
					select jsonb_agg(sync.batch_id order by prepared.request_ordinality, prepared.account_ordinality)
					from created_syncs sync
					join prepared on prepared.batch_id = sync.batch_id
				),
				'[]'::jsonb
			)
		from valid_selection selection
	`,
		userID,
		EnableBankingSource,
		encodedTargets,
		allAccounts,
		dateTo.UTC().Format(time.DateOnly),
		time.Now().UTC(),
	).Scan(&ownedIntegrations, &resolvedAccounts, &mappedAccounts, &encodedBatchIDs)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrBankSyncInProgress
		}
		return nil, err
	}
	if ownedIntegrations != len(integrations) {
		return nil, ErrIntegrationNotFound
	}
	if !allAccounts && resolvedAccounts != len(targets) {
		return nil, ErrBankSyncAccountNotFound
	}
	if !allAccounts && mappedAccounts != len(targets) {
		return nil, ErrIntegrationAccount
	}
	var batchIDs []string
	if err := json.Unmarshal(encodedBatchIDs, &batchIDs); err != nil {
		return nil, err
	}
	return batchIDs, nil
}

const enableBankingClaimLease = 5 * time.Minute

func (d *Data) ClaimEnableBankingSync(ctx context.Context) (EnableBankingSyncJob, bool, error) {
	var job EnableBankingSyncJob
	job.ClaimID = NewPrivateID()
	err := d.db.QueryRowContext(ctx, `
		with ready_job as (
			select sync.batch_id
			from enable_banking_syncs sync
			join import_batches batch on batch.id = sync.batch_id
			where (
			    batch.status = 'queued'
			    or (
			      batch.status = 'syncing'
			      and (batch.claim_expires_at is null or batch.claim_expires_at <= now())
			    )
			  )
			  and sync.completed_at is null
			  and sync.next_attempt_at <= now()
			order by
				(
					select count(*)
					from import_batches active
					where active.user_id = batch.user_id
					  and active.status = 'syncing'
				),
				batch.created_at
			limit 1
			for update of batch skip locked
		), claimed_batch as (
			update import_batches batch
			set status = 'syncing', error = null,
			    claim_id = $1,
			    claim_expires_at = now() + $2 * interval '1 second'
			where batch.id in (select batch_id from ready_job)
			returning batch.id, batch.bucket_id
		)
		select
			batch.id,
			batch.bucket_id,
			sync.account_uid,
			sync.date_from,
			sync.continuation_key,
			sync.next_sequence,
			sync.fetched_all,
			sync.attempts
		from claimed_batch batch
		join enable_banking_syncs sync on sync.batch_id = batch.id
	`, job.ClaimID, int(enableBankingClaimLease/time.Second)).Scan(
		&job.BatchID,
		&job.BucketID,
		&job.AccountUID,
		&job.DateFrom,
		&job.ContinuationKey,
		&job.NextSequence,
		&job.FetchedAll,
		&job.Attempts,
	)
	if err == sql.ErrNoRows {
		return EnableBankingSyncJob{}, false, nil
	}
	if err != nil {
		return EnableBankingSyncJob{}, false, err
	}
	return job, true, nil
}

// StageEnableBankingPage commits normalized rows and the next continuation key
// together, making one complete provider page the retry unit.
func (d *Data) StageEnableBankingPage(ctx context.Context, job EnableBankingSyncJob, rows []ParsedRow, rowErrors []RowError, nextContinuation string, fetchedAll bool) error {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		tx, err := pgxConn.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)

		var continuation string
		var seenContinuations []string
		var sequence int64
		var dateFrom time.Time
		err = tx.QueryRow(ctx, `
			select sync.continuation_key, sync.seen_continuation_keys, sync.next_sequence, sync.date_from
			from enable_banking_syncs sync
			join import_batches batch on batch.id = sync.batch_id
			where sync.batch_id = $1
			  and sync.completed_at is null
			  and batch.status = 'syncing'
			  and batch.claim_id = $2
			for update of sync, batch
		`, job.BatchID, job.ClaimID).Scan(&continuation, &seenContinuations, &sequence, &dateFrom)
		if err == pgx.ErrNoRows {
			return ErrBankSyncNotFound
		}
		if err != nil {
			return err
		}
		if continuation != job.ContinuationKey || sequence != job.NextSequence || !dateFrom.Equal(job.DateFrom) {
			return fmt.Errorf("bank sync page changed while processing")
		}
		if nextContinuation != "" {
			for _, seen := range seenContinuations {
				if seen == nextContinuation {
					return ErrBankSyncRepeatedContinuation
				}
			}
		}

		values := make([][]any, len(rows))
		for i, row := range rows {
			values[i] = []any{
				NewPrivateID(), job.BatchID, sequence + int64(i), row.OccurredOn, row.OccurredAt,
				row.Amount, row.Currency, row.Counterparty, row.Note,
				dedupHash(job.BucketID, row.OccurredOn, row.Amount, row.Currency, row.Counterparty),
			}
		}
		if len(values) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"import_staged_rows"},
				[]string{"id", "batch_id", "sequence", "occurred_on", "occurred_at", "amount", "currency", "counterparty", "note", "dedup_hash"},
				pgx.CopyFromRows(values)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			update enable_banking_syncs
			set continuation_key = $2,
			    seen_continuation_keys = case
			        when $2 = '' then seen_continuation_keys
			        else array_append(seen_continuation_keys, $2)
			    end,
			    next_sequence = $3,
			    fetched_all = $4,
			    attempts = 0,
			    next_attempt_at = now()
			where batch_id = $1
		`, job.BatchID, nextContinuation, sequence+int64(len(rows)), fetchedAll); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update import_batches
			set claim_expires_at = now() + $3 * interval '1 second'
			where id = $1 and claim_id = $2
		`, job.BatchID, job.ClaimID, int(enableBankingClaimLease/time.Second)); err != nil {
			return err
		}
		if len(rowErrors) > 0 {
			encodedErrors, err := json.Marshal(rowErrors)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				update import_batches
				set parse_errors = (
				        select coalesce(jsonb_agg(item.value order by item.ordinality), '[]'::jsonb)
				        from jsonb_array_elements(
				            coalesce(import_batches.parse_errors, '[]'::jsonb) || $2::jsonb
				        ) with ordinality as item(value, ordinality)
				        where item.ordinality <= $4
				    ),
				    parse_error_count = parse_error_count + $3
				where id = $1
			`, job.BatchID, string(encodedErrors), len(rowErrors), MaxStoredRowErrors); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	})
}

// FinalizeEnableBankingSync runs the existing whole-batch occurrence and dedupe
// logic over durable provider staging rows.
func (d *Data) FinalizeEnableBankingSync(ctx context.Context, job EnableBankingSyncJob) (int, int, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()

	var added, duplicates int
	err = conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		tx, err := pgxConn.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)

		var fetchedAll bool
		err = tx.QueryRow(ctx, `
			select sync.fetched_all
			from enable_banking_syncs sync
			join import_batches batch on batch.id = sync.batch_id
			where sync.batch_id = $1
			  and sync.completed_at is null
			  and batch.status = 'syncing'
			  and batch.claim_id = $2
			for update of sync, batch
		`, job.BatchID, job.ClaimID).Scan(&fetchedAll)
		if err == pgx.ErrNoRows {
			return ErrBankSyncNotFound
		}
		if err != nil {
			return err
		}
		if !fetchedAll {
			return fmt.Errorf("bank sync fetch is not complete")
		}

		added, duplicates, err = finalizeStagedRows(ctx, tx, job.BatchID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update import_batches
			set status = 'done', error = null,
			    claim_id = null, claim_expires_at = null
			where id = $1 and claim_id = $2
		`, job.BatchID, job.ClaimID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update enable_banking_syncs
			set completed_at = now()
			where batch_id = $1
		`, job.BatchID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	return added, duplicates, err
}

func (d *Data) RetryEnableBankingSync(ctx context.Context, job EnableBankingSyncJob, nextAttempt time.Time, cause error) error {
	result, err := d.db.ExecContext(ctx, `
		with current_claim as (
			select id
			from import_batches
			where id = $1 and claim_id = $2 and status = 'syncing'
		), retried_job as (
			update enable_banking_syncs
			set attempts = attempts + 1,
			    next_attempt_at = $3
			where batch_id in (select id from current_claim)
			returning batch_id
		)
		update import_batches
		set status = 'queued', error = $4,
		    claim_id = null, claim_expires_at = null
		where id in (select batch_id from retried_job)
	`, job.BatchID, job.ClaimID, nextAttempt, cause.Error())
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrBankSyncNotFound
	}
	return nil
}

func (d *Data) FailEnableBankingSync(ctx context.Context, job EnableBankingSyncJob, cause error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		update import_batches
		set status = 'failed', error = $3,
		    claim_id = null, claim_expires_at = null
		where id = $1 and claim_id = $2 and status = 'syncing'
	`, job.BatchID, job.ClaimID, cause.Error())
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrBankSyncNotFound
	}
	if _, err := tx.ExecContext(ctx, `delete from import_staged_rows where batch_id = $1`, job.BatchID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from enable_banking_syncs where batch_id = $1`, job.BatchID); err != nil {
		return err
	}
	return tx.Commit()
}
