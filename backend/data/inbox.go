package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// InboxRow is a pending import row: a transaction-to-be. Categorizing one creates
// the ledger transaction; until then it holds only the parsed bank fields.
type InboxRow struct {
	ID           string
	Date         time.Time
	OccurredAt   *time.Time
	Amount       int64
	Currency     string
	Counterparty string
	Description  string
	BucketID     string
	Account      string
}

type InboxMatch struct {
	InboxRow
	Kind string
}

type InboxSplitPosting struct {
	ID       string `json:"id"`
	BucketID string `json:"bucket_id"`
	Amount   int64  `json:"amount"`
}

const InboxPageSize = 100

// ListInbox returns one keyset page of the user's pending import rows across all
// their batches, ordered (date desc, id desc). cursorID == "" starts at the top.
func (d *Data) ListInbox(ctx context.Context, userID string, cursorDate time.Time, cursorID string, filter TransactionFilter) ([]InboxRow, error) {
	args := []any{userID}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	clauses := []string{}
	if cursorID != "" {
		clauses = append(clauses, "and (r.occurred_on, r.id) < ("+addArg(cursorDate)+"::date, "+addArg(cursorID)+"::uuid)")
	}
	if filter.Search != "" {
		arg := addArg("%" + filter.Search + "%")
		clauses = append(clauses, "and (r.counterparty ilike "+arg+" or r.note ilike "+arg+")")
	}
	if len(filter.Accounts) > 0 {
		clauses = append(clauses, "and b.bucket_id=any("+addArg(filter.Accounts)+"::uuid[])")
	}
	if filter.Direction == "in" {
		clauses = append(clauses, "and r.amount > 0")
	} else if filter.Direction == "out" {
		clauses = append(clauses, "and r.amount < 0")
	}
	if filter.Currency != "" {
		clauses = append(clauses, "and r.currency="+addArg(filter.Currency))
	}
	if filter.Amount != nil {
		clauses = append(clauses, "and abs(r.amount)="+addArg(*filter.Amount))
	}
	if filter.AmountMin != nil {
		clauses = append(clauses, "and abs(r.amount)>="+addArg(*filter.AmountMin))
	}
	if filter.AmountMax != nil {
		clauses = append(clauses, "and abs(r.amount)<="+addArg(*filter.AmountMax))
	}
	if filter.OccurredFrom != nil {
		clauses = append(clauses, "and r.occurred_on >= "+addArg(*filter.OccurredFrom)+"::date")
	}
	if filter.OccurredBefore != nil {
		clauses = append(clauses, "and r.occurred_on < "+addArg(*filter.OccurredBefore)+"::date")
	}

	rows, err := d.db.QueryContext(ctx,
		`select r.id, r.occurred_on, r.occurred_at, r.amount, r.currency, r.counterparty, r.note, b.bucket_id, bucket.name
		 from import_rows r
		 join import_batches b on b.id = r.batch_id
		 join buckets bucket on bucket.id = b.bucket_id
		 where b.user_id = $1 and r.status = 'pending' `+strings.Join(clauses, " ")+`
		 order by r.occurred_on desc, r.id desc
		 limit `+strconv.Itoa(InboxPageSize), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InboxRow
	for rows.Next() {
		var r InboxRow
		if err := rows.Scan(&r.ID, &r.Date, &r.OccurredAt, &r.Amount, &r.Currency, &r.Counterparty, &r.Description, &r.BucketID, &r.Account); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *Data) GetInboxRow(ctx context.Context, userID, rowID string) (InboxRow, error) {
	var row InboxRow
	err := d.db.QueryRowContext(ctx, `
		select r.id, r.occurred_on, r.occurred_at, r.amount, r.currency,
			r.counterparty, r.note, batch.bucket_id, bucket.name
		from import_rows r
		join import_batches batch on batch.id = r.batch_id
		join buckets bucket on bucket.id = batch.bucket_id
		where r.id = $2 and batch.user_id = $1 and r.status = 'pending'
	`, userID, rowID).Scan(
		&row.ID, &row.Date, &row.OccurredAt, &row.Amount, &row.Currency,
		&row.Counterparty, &row.Description, &row.BucketID, &row.Account,
	)
	if err == sql.ErrNoRows {
		return InboxRow{}, ErrNotFound
	}
	return row, err
}

// GetInboxMatches returns transfer and exchange candidates. Transfers must be
// equal and opposite in one currency across two accounts. Exchanges must have
// opposite signs in different currencies. ECB rates only rank exchange
// candidates by value; missing rates never hide a candidate.
func (d *Data) GetInboxMatches(ctx context.Context, userID, rowID, q string) (InboxRow, []InboxMatch, error) {
	rows, err := d.db.QueryContext(ctx, `
		with source as materialized (
			select r.id, r.occurred_on, r.occurred_at, r.amount, r.currency,
				r.counterparty, r.note as description,
				b.bucket_id, bucket.name as account, c.exponent
			from import_rows r
			join import_batches b on b.id = r.batch_id
			join buckets bucket on bucket.id = b.bucket_id
			join currencies c on c.code = r.currency
			where r.id = $2 and b.user_id = $1 and r.status = 'pending'
		)
		select s.id, s.occurred_on, s.occurred_at, s.amount, s.currency, s.counterparty, s.description, s.bucket_id, s.account,
			m.id, m.occurred_on, m.occurred_at, m.amount, m.currency, m.counterparty, m.description, m.bucket_id, m.account, m.kind
		from source s
		left join lateral (
			select r.id, r.occurred_on, r.occurred_at, r.amount, r.currency,
				r.counterparty, r.note as description,
				b.bucket_id, bucket.name as account,
				case when r.currency = s.currency then 'transfer' else 'exchange' end as kind,
				case when r.currency = s.currency then 0::numeric else
					abs(
						abs(s.amount)::numeric / power(10::numeric, s.exponent) / fx.source_rate
						- abs(r.amount)::numeric / power(10::numeric, c.exponent) / fx.candidate_rate
					) / greatest(
						abs(s.amount)::numeric / power(10::numeric, s.exponent) / fx.source_rate,
						abs(r.amount)::numeric / power(10::numeric, c.exponent) / fx.candidate_rate
					)
				end as value_distance,
				abs(r.occurred_on - s.occurred_on) as time_distance,
				(b.bucket_id = s.bucket_id) as same_account
			from import_rows r
			join import_batches b on b.id = r.batch_id
			join buckets bucket on bucket.id = b.bucket_id
			join currencies c on c.code = r.currency
			left join lateral (
				select source_rate.rate as source_rate, candidate_rate.rate as candidate_rate
				from rates source_rate
				join rates candidate_rate on candidate_rate.date = source_rate.date and candidate_rate.currency = r.currency
				where source_rate.currency = s.currency
				  and source_rate.date <= s.occurred_on
				order by source_rate.date desc
				limit 1
			) fx on r.currency <> s.currency
			where b.user_id = $1 and r.status = 'pending' and r.id <> s.id and s.amount <> 0
			  and r.occurred_on between (s.occurred_on - 7) and (s.occurred_on + 7)
			  and (
				(r.currency = s.currency and r.amount = -s.amount and b.bucket_id <> s.bucket_id)
				or (r.currency <> s.currency and ((r.amount < 0 and s.amount > 0) or (r.amount > 0 and s.amount < 0)))
			  )
			  and ($3 = '' or r.counterparty ilike '%' || $3 || '%'
				or r.note ilike '%' || $3 || '%' or bucket.name ilike '%' || $3 || '%')
			order by value_distance nulls last, time_distance, same_account desc, r.occurred_on desc
			limit 30
		) m on true
		order by m.value_distance nulls last, m.time_distance, m.same_account desc, m.occurred_on desc
	`, userID, rowID, q)
	if err != nil {
		return InboxRow{}, nil, err
	}
	defer rows.Close()

	var source InboxRow
	var matches []InboxMatch
	found := false
	for rows.Next() {
		found = true
		var (
			matchID, matchCurrency, matchCounterparty, matchDescription sql.NullString
			matchBucketID, matchAccount, matchKind                      sql.NullString
			matchDate, matchOccurredAt                                  sql.NullTime
			matchAmount                                                 sql.NullInt64
		)
		if err := rows.Scan(
			&source.ID, &source.Date, &source.OccurredAt, &source.Amount, &source.Currency, &source.Counterparty, &source.Description, &source.BucketID, &source.Account,
			&matchID, &matchDate, &matchOccurredAt, &matchAmount, &matchCurrency, &matchCounterparty, &matchDescription, &matchBucketID, &matchAccount, &matchKind,
		); err != nil {
			return source, nil, err
		}
		if matchID.Valid {
			match := InboxRow{
				ID: matchID.String, Date: matchDate.Time, Amount: matchAmount.Int64,
				Currency: matchCurrency.String, Counterparty: matchCounterparty.String,
				Description: matchDescription.String, BucketID: matchBucketID.String, Account: matchAccount.String,
			}
			if matchOccurredAt.Valid {
				match.OccurredAt = &matchOccurredAt.Time
			}
			matches = append(matches, InboxMatch{InboxRow: match, Kind: matchKind.String})
		}
	}
	if err := rows.Err(); err != nil {
		return source, nil, err
	}
	if !found {
		return source, nil, ErrNotFound
	}
	return source, matches, nil
}

func (d *Data) MatchInboxRows(ctx context.Context, userID, rowID, matchID string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type row struct {
		id, currency, counterparty, description, bucket string
		date                                            time.Time
		occurredAt                                      *time.Time
		amount                                          int64
	}
	rows, err := tx.QueryContext(ctx, `
		select
			r.id, r.occurred_on, r.occurred_at, r.amount, r.currency,
			r.counterparty, r.note, batch.bucket_id, transit.id, fx.id
		from import_rows r
		join import_batches batch on batch.id = r.batch_id
		join buckets transit on transit.owner_user_id = $1 and transit.kind = 'transit'
		join buckets fx on fx.owner_user_id = $1 and fx.kind = 'fx_conversion'
		where r.id = any($2::uuid[])
		  and batch.user_id = $1
		  and r.status = 'pending'
		for update of r
	`, userID, []string{rowID, matchID})
	if err != nil {
		return err
	}
	var pair []row
	var transit, fx string
	for rows.Next() {
		var r row
		if err = rows.Scan(
			&r.id, &r.date, &r.occurredAt, &r.amount, &r.currency,
			&r.counterparty, &r.description, &r.bucket, &transit, &fx,
		); err != nil {
			rows.Close()
			return err
		}
		pair = append(pair, r)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(pair) != 2 {
		return ErrInvalidPostings
	}

	a, b := pair[0], pair[1]
	distance := a.date.Sub(b.date)
	if distance < -7*24*time.Hour || distance > 7*24*time.Hour {
		return ErrInvalidPostings
	}
	transfer := a.currency == b.currency && a.bucket != b.bucket && a.amount == -b.amount && a.amount != 0
	exchange := a.currency != b.currency && ((a.amount < 0 && b.amount > 0) || (a.amount > 0 && b.amount < 0))
	if !transfer && !exchange {
		return ErrInvalidPostings
	}

	// Exchange settlement follows source time, with the outgoing row first on a tie.
	first, second := a, b
	if second.date.Before(first.date) || (second.date.Equal(first.date) && second.amount < 0) {
		first, second = second, first
	}

	type desiredTransaction struct {
		ID           string     `json:"id"`
		OccurredOn   time.Time  `json:"occurred_on"`
		OccurredAt   *time.Time `json:"occurred_at"`
		Counterparty string     `json:"counterparty"`
		Description  string     `json:"description"`
	}
	type desiredPosting struct {
		ID            string  `json:"id"`
		TransactionID string  `json:"transaction_id"`
		BucketID      string  `json:"bucket_id"`
		Amount        int64   `json:"amount"`
		Currency      string  `json:"currency"`
		ImportRowID   *string `json:"import_row_id"`
	}

	transactionIDs := map[string]string{}
	var transactions []desiredTransaction
	var desiredPostings []desiredPosting
	addTransaction := func(r row, postings []Posting) error {
		if err := validatePostings(postings); err != nil {
			return err
		}
		transactionID := NewPrivateID()
		transactionIDs[r.id] = transactionID
		transactions = append(transactions, desiredTransaction{
			ID: transactionID, OccurredOn: r.date, OccurredAt: r.occurredAt,
			Counterparty: r.counterparty, Description: r.description,
		})
		for i, posting := range postings {
			var importRowID *string
			if i == 0 {
				rowID := r.id
				importRowID = &rowID
			}
			desiredPostings = append(desiredPostings, desiredPosting{
				ID: NewPrivateID(), TransactionID: transactionID, BucketID: posting.BucketID,
				Amount: posting.Amount, Currency: posting.Currency, ImportRowID: importRowID,
			})
		}
		return nil
	}

	if transfer {
		if err = addTransaction(a, []Posting{
			{BucketID: a.bucket, Amount: a.amount, Currency: a.currency},
			{BucketID: transit, Amount: -a.amount, Currency: a.currency},
		}); err != nil {
			return err
		}
		if err = addTransaction(b, []Posting{
			{BucketID: b.bucket, Amount: b.amount, Currency: b.currency},
			{BucketID: transit, Amount: -b.amount, Currency: b.currency},
		}); err != nil {
			return err
		}
	} else {
		if err = addTransaction(first, []Posting{
			{BucketID: first.bucket, Amount: first.amount, Currency: first.currency},
			{BucketID: transit, Amount: -first.amount, Currency: first.currency},
		}); err != nil {
			return err
		}
		if err = addTransaction(second, []Posting{
			{BucketID: second.bucket, Amount: second.amount, Currency: second.currency},
			{BucketID: fx, Amount: -second.amount, Currency: second.currency},
			{BucketID: transit, Amount: first.amount, Currency: first.currency},
			{BucketID: fx, Amount: -first.amount, Currency: first.currency},
		}); err != nil {
			return err
		}
	}

	out, in := a, b
	if in.amount < 0 {
		out, in = in, out
	}
	transactionPayload, err := json.Marshal(transactions)
	if err != nil {
		return err
	}
	postingPayload, err := json.Marshal(desiredPostings)
	if err != nil {
		return err
	}
	movementID := NewPrivateID()
	rowIDs := []string{a.id, b.id}
	var insertedTransactions, insertedPostings, updatedRows, insertedMovements int
	err = tx.QueryRowContext(ctx, `
		with desired_transactions as materialized (
			select *
			from jsonb_to_recordset($2::jsonb) as txn (
				id uuid, occurred_on date, occurred_at timestamptz,
				counterparty text, description text
			)
		), desired_postings as materialized (
			select *
			from jsonb_to_recordset($3::jsonb) as posting (
				id uuid, transaction_id uuid, bucket_id uuid, amount bigint,
				currency text, import_row_id uuid
			)
		), old_rows as materialized (
			select *
			from import_rows
			where id = any($7::uuid[])
		), added_transactions as (
			insert into transactions (
				id, owner_user_id, occurred_on, occurred_at, counterparty,
				description, memo, created_at
			)
			select id, $1, occurred_on, occurred_at, counterparty, description, '', now()
			from desired_transactions
			returning id
		), added_postings as (
			insert into postings (
				id, transaction_id, bucket_id, amount, currency, import_row_id,
				created_at, updated_at
			)
			select posting.id, posting.transaction_id, posting.bucket_id,
				posting.amount, posting.currency, posting.import_row_id, now(), now()
			from desired_postings posting
			join added_transactions txn on txn.id = posting.transaction_id
			returning id
		), added_movement as (
			insert into account_movement_matches (
				id, owner_user_id, outgoing_transaction_id,
				incoming_transaction_id, created_at
			)
			select $4, $1, outgoing.id, incoming.id, now()
			from added_transactions outgoing
			join added_transactions incoming on incoming.id = $6
			where outgoing.id = $5
			returning id
		), updated_rows as (
			update import_rows row
			set status = 'categorized'
			from old_rows old
			where row.id = old.id
			returning row.id
		), audited_transactions as (
			insert into audit_logs
			select uuidv7(), $1, 'transactions', id, 'insert', null, now()
			from added_transactions
		), audited_postings as (
			insert into audit_logs
			select uuidv7(), $1, 'postings', id, 'insert', null, now()
			from added_postings
		), audited_movement as (
			insert into audit_logs
			select uuidv7(), $1, 'account_movement_matches', id, 'insert', null, now()
			from added_movement
		), audited_rows as (
			insert into audit_logs
			select uuidv7(), $1, 'import_rows', id, 'update', to_jsonb(old_rows), now()
			from old_rows
		)
		select
			(select count(*) from added_transactions),
			(select count(*) from added_postings),
			(select count(*) from updated_rows),
			(select count(*) from added_movement)
	`, userID, transactionPayload, postingPayload, movementID,
		transactionIDs[out.id], transactionIDs[in.id], rowIDs).Scan(
		&insertedTransactions, &insertedPostings, &updatedRows, &insertedMovements,
	)
	if err != nil {
		return err
	}
	if insertedTransactions != len(transactions) ||
		insertedPostings != len(desiredPostings) ||
		updatedRows != 2 ||
		insertedMovements != 1 {
		return ErrInvalidPostings
	}
	return tx.Commit()
}

func (d *Data) RestoreInboxRows(ctx context.Context, userID string, rowIDs []string) (int, error) {
	if len(rowIDs) == 0 {
		return 0, nil
	}
	result, err := removeLedgerData(ctx, d.db, userID, removalTargets{importRowIDs: rowIDs})
	return result.restoredImportRows, err
}

func (d *Data) SplitInboxRow(ctx context.Context, userID, rowID string, postings []InboxSplitPosting) error {
	return d.createInboxTransaction(ctx, userID, rowID, postings, true)
}

func (d *Data) CreateInboxEntries(ctx context.Context, userID, rowID string, postings []InboxSplitPosting) error {
	return d.createInboxTransaction(ctx, userID, rowID, postings, false)
}

func (d *Data) createInboxTransaction(ctx context.Context, userID, rowID string, postings []InboxSplitPosting, split bool) error {
	if (split && len(postings) < 2) || (!split && len(postings) < 1) {
		return ErrInvalidPostings
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var row InboxRow
	err = tx.QueryRowContext(ctx, `
		select r.id, r.occurred_on, r.occurred_at, r.amount, r.currency,
			r.counterparty, r.note, batch.bucket_id
		from import_rows r
		join import_batches batch on batch.id = r.batch_id
		where r.id = $2 and batch.user_id = $1 and r.status = 'pending'
		for update of r
	`, userID, rowID).Scan(
		&row.ID, &row.Date, &row.OccurredAt, &row.Amount, &row.Currency,
		&row.Counterparty, &row.Description, &row.BucketID,
	)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.Amount == 0 {
		return ErrInvalidPostings
	}
	bucketIDs := make([]string, 0, len(postings))
	seenBuckets := map[string]bool{}
	if split {
		total := row.Amount
		if total < 0 {
			total = -total
		}
		var allocated int64
		for i := range postings {
			posting := &postings[i]
			if posting.Amount <= 0 || posting.Amount > total-allocated {
				return ErrUnbalanced
			}
			allocated += posting.Amount
			if row.Amount > 0 {
				posting.Amount = -posting.Amount
			}
		}
		if allocated != total {
			return ErrUnbalanced
		}
	}
	for i := range postings {
		posting := &postings[i]
		if posting.Amount == 0 {
			return ErrInvalidPostings
		}
		posting.ID = NewPrivateID()
		if !seenBuckets[posting.BucketID] {
			seenBuckets[posting.BucketID] = true
			bucketIDs = append(bucketIDs, posting.BucketID)
		}
	}
	allowedKinds := []string{"asset", "liability", "expense", "income", "person"}
	if split {
		allowedKinds = []string{"expense", "income", "person"}
	}
	var validBuckets int
	if err = tx.QueryRowContext(ctx, `
		select count(*)
		from buckets
		where id = any($1::uuid[])
		  and owner_user_id = $2
		  and kind = any($3::text[])
		  and hidden = false
	`, bucketIDs, userID, allowedKinds).Scan(&validBuckets); err != nil {
		return err
	}
	if validBuckets != len(bucketIDs) {
		return ErrInvalidCategory
	}
	final := []Posting{{BucketID: row.BucketID, Amount: row.Amount, Currency: row.Currency}}
	for _, posting := range postings {
		final = append(final, Posting{
			BucketID: posting.BucketID,
			Amount:   posting.Amount,
			Currency: row.Currency,
		})
	}
	if err = validatePostings(final); err != nil {
		return err
	}

	payload, err := json.Marshal(postings)
	if err != nil {
		return err
	}
	transactionID := NewPrivateID()
	accountPostingID := NewPrivateID()
	_, err = tx.ExecContext(ctx, `
		with desired as materialized (
			select *
			from jsonb_to_recordset($12::jsonb) as posting (
				id uuid, bucket_id uuid, amount bigint
			)
		), inserted_transaction as (
			insert into transactions (
				id, owner_user_id, occurred_on, occurred_at, counterparty,
				description, memo, created_at
			)
			values ($3, $1, $4, $5, $6, $7, '', now())
			returning id
		), inserted_postings as (
			insert into postings (
				id, transaction_id, bucket_id, amount, currency,
				import_row_id, created_at
			)
			select $8, $3, $9, $10, $11, $2::uuid, now()
			union all
			select desired.id, $3, desired.bucket_id, desired.amount, $11, null::uuid, now()
			from desired
			returning id
		), audited_transaction as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), $1, 'transactions', id, 'insert', null, now()
			from inserted_transaction
		), audited_postings as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), $1, 'postings', id, 'insert', null, now()
			from inserted_postings
		), audited_row as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), $1, 'import_rows', id, 'update', to_jsonb(import_rows), now()
			from import_rows
			where id = $2
		)
		update import_rows set status = 'categorized' where id = $2
	`, userID, row.ID, transactionID, row.Date, row.OccurredAt,
		row.Counterparty, row.Description, accountPostingID, row.BucketID,
		row.Amount, row.Currency, payload)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Data) CategorizeInboxRows(ctx context.Context, userID string, rowIDs []string, bucketID string) (int, error) {
	return d.categorizeInboxRows(ctx, userID, rowIDs, bucketID, nil)
}

func (d *Data) CreateBucketAndCategorizeInboxRows(ctx context.Context, userID string, rowIDs []string, bucket Bucket) (int, error) {
	return d.categorizeInboxRows(ctx, userID, rowIDs, bucket.ID, &bucket)
}

func (d *Data) categorizeInboxRows(ctx context.Context, userID string, rowIDs []string, bucketID string, bucket *Bucket) (int, error) {
	if len(rowIDs) == 0 {
		return 0, nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if bucket == nil {
		var valid bool
		if err := tx.QueryRowContext(ctx, `
			select exists (
				select 1
				from buckets
				where id = $1
				  and owner_user_id = $2
				  and kind in ('expense', 'income', 'person')
				  and hidden = false
			)
		`, bucketID, userID).Scan(&valid); err != nil {
			return 0, err
		}
		if !valid {
			return 0, ErrInvalidCategory
		}
	} else {
		validKind := bucket.Kind == KindExpense || bucket.Kind == KindIncome || bucket.Kind == KindPerson
		if bucket.OwnerUserID != userID || !validKind || bucket.Hidden {
			return 0, ErrInvalidCategory
		}
		if err := insertBucketTx(ctx, tx, *bucket); err != nil {
			return 0, err
		}
	}

	var categorized int
	if err := tx.QueryRowContext(ctx, `
with rows as materialized (
    select r.id as row_id, r.occurred_on, r.occurred_at, r.amount, r.currency,
           r.counterparty, r.note, b.bucket_id,
           uuidv7() as txn_id, uuidv7() as p1, uuidv7() as p2
    from import_rows r
    join import_batches b on b.id = r.batch_id
    where r.id = any($2::uuid[]) and b.user_id = $1 and r.status = 'pending'
    for update of r
),
ins_txn as (
    insert into transactions (id, owner_user_id, occurred_on, occurred_at, counterparty, description, memo, created_at)
    select txn_id, $1, occurred_on, occurred_at, counterparty, note, '', now() from rows
),
ins_post as (
    insert into postings (id, transaction_id, bucket_id, amount, currency, import_row_id, created_at)
    select p1, txn_id, bucket_id, amount, currency, row_id, now() from rows
    union all
    select p2, txn_id, $3, -amount, currency, null::uuid, now() from rows
),
aud_txn as (
    insert into audit_logs (id, actor_user_id, table_name, row_id, operation, before, created_at)
    select uuidv7(), $1, 'transactions', txn_id, 'insert', null::jsonb, now() from rows
),
aud_post as (
    insert into audit_logs (id, actor_user_id, table_name, row_id, operation, before, created_at)
    select uuidv7(), $1, 'postings', p1, 'insert', null::jsonb, now() from rows
    union all
    select uuidv7(), $1, 'postings', p2, 'insert', null::jsonb, now() from rows
),
aud_rows as (
    insert into audit_logs (id, actor_user_id, table_name, row_id, operation, before, created_at)
    select uuidv7(), $1, 'import_rows', orig.id, 'update', to_jsonb(orig), now()
    from import_rows orig where orig.id in (select row_id from rows)
),
upd as (
    update import_rows set status = 'categorized'
    from rows r where import_rows.id = r.row_id
)
select count(*) from rows`, userID, rowIDs, bucketID).Scan(&categorized); err != nil {
		return 0, err
	}
	if bucket != nil && categorized == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return categorized, nil
}
