package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

var (
	ErrIntegrationNotFound        = errors.New("bank integration not found")
	ErrIntegrationAccount         = errors.New("bank account cannot be mapped to that bucket")
	ErrIntegrationAccountImported = errors.New("bank account has imported transactions and cannot be moved")
)

type BankIntegration struct {
	ID        string
	UserID    string
	Provider  string
	Data      json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NormalizeIBAN(value string) string {
	return strings.ToUpper(strings.Join(strings.Fields(value), ""))
}

func ValidIBAN(value string) bool {
	value = NormalizeIBAN(value)
	if len(value) < 15 || len(value) > 34 {
		return false
	}
	for _, char := range value {
		if !unicode.IsDigit(char) && !unicode.IsUpper(char) {
			return false
		}
	}
	rearranged := value[4:] + value[:4]
	remainder := 0
	for _, char := range rearranged {
		if unicode.IsDigit(char) {
			remainder = (remainder*10 + int(char-'0')) % 97
		} else {
			number := int(char-'A') + 10
			remainder = (remainder*100 + number) % 97
		}
	}
	return remainder == 1
}

func (d *Data) CreateBankIntegration(ctx context.Context, integration BankIntegration, ibans []string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	integration.CreatedAt = time.Now().UTC()
	integration.UpdatedAt = integration.CreatedAt
	if _, err := tx.ExecContext(ctx, `
		insert into bank_integrations (
			id, user_id, provider, data, created_at, updated_at
		)
		values ($1, $2, $3, $4, $5, $5)
	`, integration.ID, integration.UserID, integration.Provider, integration.Data, integration.CreatedAt); err != nil {
		return err
	}
	if len(ibans) > 0 {
		if _, err := tx.ExecContext(ctx, `
			update buckets
			set active_bank_integration_id = $1
			where owner_user_id = $2
			  and iban = any($3::text[])
			  and kind in ('asset', 'liability')
		`, integration.ID, integration.UserID, ibans); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Data) UpdateBankIntegration(ctx context.Context, userID, id, provider string, value json.RawMessage) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		update bank_integrations
		set data = $4, updated_at = now()
		where id = $1
		  and user_id = $2
		  and provider = $3
		  and deleted_at is null
	`, id, userID, provider, value)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrIntegrationNotFound
	}
	if err := cancelIntegrationSyncs(ctx, tx, id, "bank connection was reauthorized"); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Data) GetBankIntegration(ctx context.Context, userID, id, provider string) (*BankIntegration, error) {
	var value BankIntegration
	err := d.db.QueryRowContext(ctx, `
		select id, user_id, provider, data, created_at, updated_at
		from bank_integrations
		where id = $1
		  and user_id = $2
		  and provider = $3
		  and deleted_at is null
	`, id, userID, provider).Scan(&value.ID, &value.UserID, &value.Provider, &value.Data, &value.CreatedAt, &value.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrIntegrationNotFound
	}
	return &value, err
}

func (d *Data) ListBankIntegrations(ctx context.Context, userID, provider string) ([]BankIntegration, error) {
	rows, err := d.db.QueryContext(ctx, `
		select id, user_id, provider, data, created_at, updated_at
		from bank_integrations
		where user_id = $1
		  and provider = $2
		  and deleted_at is null
		order by created_at
	`, userID, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []BankIntegration
	for rows.Next() {
		var value BankIntegration
		if err := rows.Scan(&value.ID, &value.UserID, &value.Provider, &value.Data, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (d *Data) MapIntegrationAccount(ctx context.Context, userID, integrationID, bucketID, iban string) error {
	iban = NormalizeIBAN(iban)
	if !ValidIBAN(iban) {
		return ErrIntegrationAccount
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentBucketID string
	err = tx.QueryRowContext(ctx, `
		select id
		from buckets
		where owner_user_id = $1
		  and iban = $2
		for update
	`, userID, iban).Scan(&currentBucketID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	var targetIBAN sql.NullString
	err = tx.QueryRowContext(ctx, `
		select bucket.iban
		from buckets bucket
		where bucket.id = $1
		  and bucket.owner_user_id = $2
		  and bucket.kind in ('asset', 'liability')
		  and not bucket.hidden
		  and exists (
			  select 1
			  from bank_integrations integration
			  where integration.id = $3
			    and integration.user_id = $2
			    and integration.deleted_at is null
		  )
		for update
	`, bucketID, userID, integrationID).Scan(&targetIBAN)
	if err == sql.ErrNoRows || (targetIBAN.Valid && targetIBAN.String != iban) {
		return ErrIntegrationAccount
	}
	if err != nil {
		return err
	}
	if currentBucketID != "" && currentBucketID != bucketID {
		var syncing bool
		if err := tx.QueryRowContext(ctx, `
			select exists (
				select 1
				from enable_banking_syncs
				where integration_id = $1
				  and completed_at is null
			)
		`, integrationID).Scan(&syncing); err != nil {
			return err
		}
		if syncing {
			return ErrBankSyncInProgress
		}

		var imported bool
		if err := tx.QueryRowContext(ctx, `
			select exists (
				select 1
				from import_rows row
				join import_batches batch on batch.id = row.batch_id
				where batch.bucket_id = $1
				  and row.status <> 'duplicate'
			)
		`, currentBucketID).Scan(&imported); err != nil {
			return err
		}
		if imported {
			return ErrIntegrationAccountImported
		}

		if _, err := tx.ExecContext(ctx, `
			update buckets
			set iban = null, active_bank_integration_id = null
			where id = $1
		`, currentBucketID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			delete from enable_banking_syncs
			where integration_id = $1
		`, integrationID); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		update buckets
		set iban = $3, active_bank_integration_id = $4
		where id = $1
		  and owner_user_id = $2
	`, bucketID, userID, iban, integrationID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Data) DeleteBankIntegration(ctx context.Context, userID, id, provider string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		update bank_integrations
		set deleted_at = now()
		where id = $1
		  and user_id = $2
		  and provider = $3
		  and deleted_at is null
	`, id, userID, provider)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrIntegrationNotFound
	}
	if err := cancelIntegrationSyncs(ctx, tx, id, "bank connection was disconnected"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		update buckets
		set active_bank_integration_id = null
		where owner_user_id = $1
		  and active_bank_integration_id = $2
	`, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func cancelIntegrationSyncs(ctx context.Context, tx *sql.Tx, integrationID, reason string) error {
	if _, err := tx.ExecContext(ctx, `
		update import_batches batch
		set status = 'failed', error = $2,
		    claim_id = null, claim_expires_at = null
		from enable_banking_syncs sync
		where sync.integration_id = $1
		  and sync.completed_at is null
		  and batch.id = sync.batch_id
	`, integrationID, reason); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		delete from import_staged_rows staged
		using enable_banking_syncs sync
		where sync.integration_id = $1
		  and sync.completed_at is null
		  and staged.batch_id = sync.batch_id
	`, integrationID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		delete from enable_banking_syncs
		where integration_id = $1
		  and completed_at is null
	`, integrationID)
	return err
}
