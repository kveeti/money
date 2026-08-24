package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// csvCopyRows streams parsed rows into import_staged_rows without holding the
// full file in memory.
type csvCopyRows struct {
	src      *GenericCSVParser
	batchID  string
	bucketID string
	sequence int64
	cur      []any
	err      error
}

func (c *csvCopyRows) Next() bool {
	if c.err != nil {
		return false
	}
	if !c.src.Next() {
		c.err = c.src.Err()
		return false
	}
	row := c.src.Row()
	hash := dedupHash(c.bucketID, row.OccurredOn, row.Amount, row.Currency, row.Counterparty)
	c.cur = []any{NewPrivateID(), c.batchID, c.sequence, row.OccurredOn, row.OccurredAt, row.Amount, row.Currency, row.Counterparty, row.Note, hash}
	c.sequence++
	return true
}

func (c *csvCopyRows) Values() ([]any, error) { return c.cur, nil }
func (c *csvCopyRows) Err() error             { return c.err }

// importCSV parses, stages, and dedupes one uploaded file in one transaction.
func (d *Data) importCSV(ctx context.Context, batch ImportBatch, parser *GenericCSVParser) (int, int, error) {
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

		rows := &csvCopyRows{src: parser, batchID: batch.ID, bucketID: batch.BucketID}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"import_staged_rows"},
			[]string{"id", "batch_id", "sequence", "occurred_on", "occurred_at", "amount", "currency", "counterparty", "note", "dedup_hash"}, rows); err != nil {
			return err
		}
		if rows.err != nil {
			return rows.err
		}

		added, duplicates, err = finalizeStagedRows(ctx, tx, batch.ID)
		if err != nil {
			return err
		}
		var parseErrors any
		if len(parser.Errors()) > 0 {
			encoded, err := json.Marshal(parser.Errors())
			if err != nil {
				return err
			}
			parseErrors = string(encoded)
		}
		if _, err := tx.Exec(ctx, `
			update import_batches
			set status = 'done',
			    error = null,
			    parse_errors = $2::jsonb,
			    parse_error_count = $3
			where id = $1
		`, batch.ID, parseErrors, parser.ErrorCount()); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	return added, duplicates, err
}

func (d *Data) claimCSVImport(ctx context.Context) (ImportBatch, bool, error) {
	var batch ImportBatch
	err := d.db.QueryRowContext(ctx, `
		with ready_batch as (
			select batch.id
			from import_batches batch
			where batch.status = 'uploaded'
			order by
				(
					select count(*)
					from import_batches active
					where active.user_id = batch.user_id
					  and active.status = 'processing'
				),
				batch.created_at
			limit 1
			for update skip locked
		)
		update import_batches
		set status = 'processing'
		where id in (select id from ready_batch)
		returning id, user_id, bucket_id, source, filename
	`).Scan(
		&batch.ID,
		&batch.UserID,
		&batch.BucketID,
		&batch.Source,
		&batch.Filename,
	)
	if err == sql.ErrNoRows {
		return ImportBatch{}, false, nil
	}
	if err != nil {
		return ImportBatch{}, false, err
	}
	batch.Status = "processing"
	return batch, true, nil
}

func (d *Data) processNextCSVImport(ctx context.Context) (bool, error) {
	batch, ok, err := d.claimCSVImport(ctx)
	if err != nil || !ok {
		return false, err
	}
	start := time.Now()

	added, duplicates, parseErrors, parseErrorCount, err := d.importCSVWithRetry(ctx, batch)
	if err != nil {
		slog.Error("CSV import failed", "batch", batch.ID, "took", time.Since(start), "err", err)
		d.failCSVImport(ctx, batch.ID, err)
		return true, nil
	}

	if err := d.files.Delete(ctx, batch.ID); err != nil {
		slog.Error("import blob delete failed", "batch", batch.ID, "err", err)
	}
	slog.Info("CSV import done", "batch", batch.ID, "added", added,
		"duplicates", duplicates, "parse_errors", parseErrorCount,
		"stored_parse_errors", len(parseErrors), "took", time.Since(start))
	return true, nil
}

func (d *Data) importCSVWithRetry(ctx context.Context, batch ImportBatch) (int, int, []RowError, int, error) {
	if batch.Source != "csv" {
		return 0, 0, nil, 0, fmt.Errorf("unsupported file import source %q", batch.Source)
	}
	currencies, err := d.CurrencyExponents(ctx)
	if err != nil {
		return 0, 0, nil, 0, err
	}

	var lastErr error
	for attempt := 0; attempt < importMaxAttempts; attempt++ {
		file, err := d.files.Open(ctx, batch.ID)
		if err != nil {
			return 0, 0, nil, 0, err
		}
		parser := NewGenericCSVParser(file, currencies)
		added, duplicates, err := d.importCSV(ctx, batch, parser)
		file.Close()
		if err == nil {
			return added, duplicates, parser.Errors(), parser.ErrorCount(), nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return 0, 0, nil, 0, ctx.Err()
		}
		slog.Warn("CSV import failed, retrying", "batch", batch.ID, "attempt", attempt+1, "err", err)
		time.Sleep(importBackoff)
	}
	return 0, 0, nil, 0, lastErr
}

func (d *Data) failCSVImport(ctx context.Context, batchID string, cause error) {
	if _, err := d.db.ExecContext(ctx, `
		update import_batches
		set status = 'failed',
		    error = $2
		where id = $1
	`, batchID, cause.Error()); err != nil {
		slog.Error("marking CSV import failed", "batch", batchID, "err", err)
	}
}
