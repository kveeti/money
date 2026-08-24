package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

const (
	MaxActiveCSVImports = 3
	MaxHourlyCSVImports = 10
)

var (
	ErrImportBucket    = errors.New("import target must be an asset or liability bucket you own")
	ErrImportLimit     = errors.New("too many imports; try again later")
	ErrImportClaimLost = errors.New("import job claim was lost")
	ErrNotDuplicate    = errors.New("import row is not a duplicate")
	ErrImportNotFound  = errors.New("import batch not found")
)

type ImportBatch struct {
	ID              string
	UserID          string
	BucketID        string
	Source          string
	Filename        string
	CreatedAt       time.Time
	Status          string
	Error           *string
	Imported        int
	Duplicates      int
	ParseErrors     json.RawMessage
	ParseErrorCount int
	ClaimID         string
}

type ImportRow struct {
	ID            string
	BatchID       string
	OccurredOn    time.Time
	OccurredAt    *time.Time
	Amount        int64
	Currency      string
	Counterparty  string
	Note          string
	DedupHash     string
	Occurrence    int
	Status        string
	TransactionID *string
	DuplicateOf   *string
	Target        *DupTarget
}

type DupTarget struct {
	OccurredOn    time.Time
	OccurredAt    *time.Time
	Amount        int64
	Currency      string
	Counterparty  string
	Note          string
	TransactionID *string
	BatchID       string
	CreatedAt     time.Time
}

// CreateImport validates the user's target and quotas before reading and storing
// the upload, then creates the batch and audit in the same transaction.
func (d *Data) CreateImport(ctx context.Context, userID, bucketID, source, filename string, r io.Reader) (*ImportBatch, error) {
	batch := ImportBatch{ID: NewPrivateID(), UserID: userID, BucketID: bucketID, Source: source, Filename: filename, CreatedAt: time.Now().UTC(), Status: "uploaded"}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	externalFile := false
	committed := false
	defer func() {
		tx.Rollback()
		if externalFile && !committed {
			if cleanupErr := d.files.Delete(ctx, batch.ID); cleanupErr != nil {
				slog.Error("cleaning up import file", "batch", batch.ID, "err", cleanupErr)
			}
		}
	}()

	// Serialize quota checks for one user so parallel requests cannot all pass the
	// same count and exceed the limit.
	var lockedUser string
	if err := tx.QueryRowContext(ctx, `
		select id from users where id = $1 for update
	`, userID).Scan(&lockedUser); err != nil {
		return nil, err
	}

	var validBucket bool
	var active, recent int
	if err := tx.QueryRowContext(ctx, `
		select
			exists (
				select 1 from buckets
				where id = $2
				  and owner_user_id = $1
				  and kind in ('asset', 'liability')
				  and hidden = false
			),
			count(*) filter (
				where source = 'csv' and status in ('uploaded', 'processing')
			),
			count(*) filter (
				where source = 'csv' and created_at >= now() - interval '1 hour'
			)
		from import_batches
		where user_id = $1
	`, userID, bucketID).Scan(&validBucket, &active, &recent); err != nil {
		return nil, err
	}
	if !validBucket {
		return nil, ErrImportBucket
	}
	if source == "csv" && (active >= MaxActiveCSVImports || recent >= MaxHourlyCSVImports) {
		return nil, ErrImportLimit
	}

	if files, ok := d.files.(interface {
		PutTx(context.Context, *sql.Tx, string, io.Reader) error
	}); ok {
		if err = files.PutTx(ctx, tx, batch.ID, r); err != nil {
			return nil, err
		}
	} else {
		if err = d.files.Put(ctx, batch.ID, r); err != nil {
			return nil, err
		}
		externalFile = true
	}

	var inserted int
	err = tx.QueryRowContext(ctx, `
		with added as (
			insert into import_batches (
				id, user_id, bucket_id, source, filename, created_at, status
			)
			values ($1, $2, $3, $4, $5, $6, 'uploaded')
			returning id
		), audited as (
			insert into audit_logs (
				id, actor_user_id, table_name, row_id, operation, before, created_at
			)
			select uuidv7(), $2, 'import_batches', id, 'insert', null, now()
			from added
		)
		select count(*) from added
	`, batch.ID, batch.UserID, batch.BucketID, batch.Source, batch.Filename, batch.CreatedAt).Scan(&inserted)
	if err != nil {
		return nil, err
	}
	if inserted != 1 {
		return nil, fmt.Errorf("create import inserted %d batches", inserted)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true

	d.kickImport()
	return &batch, nil
}

func (d *Data) kickImport() {
	select {
	case d.importKick <- struct{}{}:
	default:
	}
}
