package data

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Data struct {
	*Users
	*Sessions
	db         *sql.DB
	files      FileStore
	importKick chan struct{}
}

// NewData connects to Postgres, applies migrations, and selects the import file
// store: "disk" (under importDir) or Postgres bytea (default). importDir is only
// read for the disk store.
func NewData(ctx context.Context, dbUrl, importStore, importDir string) (*Data, error) {
	db, err := connectPostgres(ctx, dbUrl)
	if err != nil {
		return nil, fmt.Errorf("error connecting to postgres: %w", err)
	}

	if err := applyMigrations(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("error applying migrations: %w", err)
	}

	var files FileStore
	if importStore == "disk" {
		files, err = NewDiskFileStore(importDir)
		if err != nil {
			return nil, fmt.Errorf("error creating import file store: %w", err)
		}
	} else {
		files = NewPostgresFileStore(db)
	}

	return &Data{
		Users:      NewUsers(db),
		Sessions:   NewSessions(db),
		db:         db,
		files:      files,
		importKick: make(chan struct{}, 1),
	}, nil
}

func (d *Data) Close() error {
	return d.db.Close()
}

func connectPostgres(ctx context.Context, dbURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("error pinging database: %w", err)
	}

	return db, nil
}
