package data

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

var ErrFileNotFound = errors.New("import file not found")

// FileStore holds the raw uploaded file for an import batch — the durable work
// item the worker reads back. Implementations are swappable (Postgres now, disk
// for dev, object storage later); nothing else in the codebase knows which is
// wired. key is the batch id.
type FileStore interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// PostgresFileStore keeps blobs in the import_files table as bytea (TOAST
// compresses + chunks them). At the file sizes we support the whole blob fits in
// memory, so Put buffers and Open returns a reader over the fetched bytes — the
// io interface stays streaming for callers even though this backend doesn't.
type PostgresFileStore struct{ db *sql.DB }

func NewPostgresFileStore(db *sql.DB) *PostgresFileStore { return &PostgresFileStore{db: db} }

func (s *PostgresFileStore) Put(ctx context.Context, key string, r io.Reader) error {
	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into import_files (batch_id, content, created_at)
		values ($1, $2, $3)
	`, key, content, time.Now().UTC())
	return err
}

func (s *PostgresFileStore) PutTx(ctx context.Context, tx *sql.Tx, key string, r io.Reader) error {
	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		insert into import_files (batch_id, content, created_at)
		values ($1, $2, $3)
	`, key, content, time.Now().UTC())
	return err
}

func (s *PostgresFileStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	var content []byte
	err := s.db.QueryRowContext(ctx, `
		select content
		from import_files
		where batch_id = $1
	`, key).Scan(&content)
	if err == sql.ErrNoRows {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (s *PostgresFileStore) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `
		delete from import_files
		where batch_id = $1
	`, key)
	return err
}

// DiskFileStore keeps blobs as files under dir, one per key. Streams both ways.
type DiskFileStore struct{ dir string }

func NewDiskFileStore(dir string) (*DiskFileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &DiskFileStore{dir: dir}, nil
}

func (s *DiskFileStore) path(key string) string { return filepath.Join(s.dir, key) }

func (s *DiskFileStore) Put(ctx context.Context, key string, r io.Reader) error {
	f, err := os.OpenFile(s.path(key), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (s *DiskFileStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	f, err := os.Open(s.path(key))
	if os.IsNotExist(err) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *DiskFileStore) Delete(ctx context.Context, key string) error {
	err := os.Remove(s.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
