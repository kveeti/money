package data

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

func (d *Data) ListImports(ctx context.Context, userID string, cursorCreatedAt time.Time, cursorID string, limit int) ([]ImportBatch, error) {
	args := []any{userID}
	cursorClause := ""
	if cursorID != "" {
		cursorClause = "and (batch.created_at, batch.id) < ($2::timestamptz, $3::uuid)"
		args = append(args, cursorCreatedAt, cursorID)
	}

	rows, err := d.db.QueryContext(ctx, `
		select
			batch.id,
			batch.bucket_id,
			batch.filename,
			batch.created_at,
			batch.status,
			count(*) filter (where row.status <> 'duplicate'),
			count(*) filter (where row.status = 'duplicate')
		from import_batches batch
		left join import_rows row on row.batch_id = batch.id
		where batch.user_id = $1
		  `+cursorClause+`
		group by batch.id
		order by batch.created_at desc, batch.id desc
		limit `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var batches []ImportBatch
	for rows.Next() {
		var batch ImportBatch
		if err := rows.Scan(
			&batch.ID,
			&batch.BucketID,
			&batch.Filename,
			&batch.CreatedAt,
			&batch.Status,
			&batch.Imported,
			&batch.Duplicates,
		); err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

func (d *Data) GetImportStatus(ctx context.Context, userID, batchID string) (*ImportBatch, error) {
	var batch ImportBatch
	err := d.db.QueryRowContext(ctx, `
		select
			batch.id,
			batch.bucket_id,
			batch.filename,
			batch.created_at,
			batch.status,
			batch.error,
			coalesce(batch.parse_errors, '[]'::jsonb),
			batch.parse_error_count,
			count(*) filter (where row.status <> 'duplicate'),
			count(*) filter (where row.status = 'duplicate')
		from import_batches batch
		left join import_rows row on row.batch_id = batch.id
		where batch.id = $1
		  and batch.user_id = $2
		group by batch.id
	`, batchID, userID).Scan(
		&batch.ID,
		&batch.BucketID,
		&batch.Filename,
		&batch.CreatedAt,
		&batch.Status,
		&batch.Error,
		&batch.ParseErrors,
		&batch.ParseErrorCount,
		&batch.Imported,
		&batch.Duplicates,
	)
	if err == sql.ErrNoRows {
		return nil, ErrImportNotFound
	}
	if err != nil {
		return nil, err
	}
	return &batch, nil
}

func (d *Data) ListDuplicates(ctx context.Context, userID, batchID, cursor string, limit int) ([]ImportRow, error) {
	rows, err := d.db.QueryContext(ctx, `
		select
			row.id,
			row.batch_id,
			row.occurred_on,
			row.occurred_at,
			row.amount,
			row.currency,
			row.counterparty,
			row.note,
			row.status,
			posting.transaction_id,
			row.duplicate_of,
			target.occurred_on,
			target.occurred_at,
			target.amount,
			target.currency,
			target.counterparty,
			target.note,
			target_posting.transaction_id,
			target_batch.id,
			target_batch.created_at
		from import_rows row
		join import_batches batch on batch.id = row.batch_id
		left join postings posting on posting.import_row_id = row.id
		left join import_rows target on target.id = row.duplicate_of
		left join postings target_posting on target_posting.import_row_id = target.id
		left join import_batches target_batch on target_batch.id = target.batch_id
		where row.batch_id = $1
		  and batch.user_id = $2
		  and row.status = 'duplicate'
		  and (
		    $3 = ''
		    or (row.occurred_on, row.id) < (
		      (
		        select cursor_row.occurred_on
		        from import_rows cursor_row
		        where cursor_row.id = $3::uuid
		          and cursor_row.batch_id = $1
		          and cursor_row.status = 'duplicate'
		      ),
		      $3::uuid
		    )
		  )
		order by row.occurred_on desc, row.id desc
		limit $4
	`, batchID, userID, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var duplicates []ImportRow
	for rows.Next() {
		var row ImportRow
		var (
			targetOccurredOn, targetOccurredAt, targetCreatedAt sql.NullTime
			targetAmount                                        sql.NullInt64
			targetCurrency, targetCounterparty, targetNote      sql.NullString
			targetBatchID, targetTransactionID                  sql.NullString
		)
		if err := rows.Scan(
			&row.ID,
			&row.BatchID,
			&row.OccurredOn,
			&row.OccurredAt,
			&row.Amount,
			&row.Currency,
			&row.Counterparty,
			&row.Note,
			&row.Status,
			&row.TransactionID,
			&row.DuplicateOf,
			&targetOccurredOn,
			&targetOccurredAt,
			&targetAmount,
			&targetCurrency,
			&targetCounterparty,
			&targetNote,
			&targetTransactionID,
			&targetBatchID,
			&targetCreatedAt,
		); err != nil {
			return nil, err
		}
		if targetOccurredOn.Valid {
			row.Target = &DupTarget{
				OccurredOn:   targetOccurredOn.Time,
				Amount:       targetAmount.Int64,
				Currency:     targetCurrency.String,
				Counterparty: targetCounterparty.String,
				Note:         targetNote.String,
				BatchID:      targetBatchID.String,
				CreatedAt:    targetCreatedAt.Time,
			}
			if targetOccurredAt.Valid {
				row.Target.OccurredAt = &targetOccurredAt.Time
			}
			if targetTransactionID.Valid {
				row.Target.TransactionID = &targetTransactionID.String
			}
		}
		duplicates = append(duplicates, row)
	}
	return duplicates, rows.Err()
}
