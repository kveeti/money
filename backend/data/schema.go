package data

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"time"
)

type migration struct {
	version int
	sql     string
}

//go:embed migrations/001_initial.sql
var initialMigration string

//go:embed migrations/002_import_limits.sql
var importLimitsMigration string

var migrations = []migration{
	{version: 1, sql: initialMigration},
	{version: 2, sql: importLimitsMigration},
}

func applyMigrations(ctx context.Context, db *sql.DB) (err error) {
	startedAt := time.Now()
	slog.InfoContext(ctx, "checking database migrations")
	defer func() {
		if err != nil {
			slog.ErrorContext(ctx, "database migrations failed", "took", time.Since(startedAt), "err", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
create table if not exists schema_migrations (
    version integer primary key,
    applied_at timestamptz not null default now()
)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	appliedCount := 0
	for _, migration := range migrations {
		var applied bool
		if err := tx.QueryRowContext(ctx,
			"select exists(select 1 from schema_migrations where version = $1)",
			migration.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if applied {
			continue
		}

		slog.InfoContext(ctx, "applying database migration", "version", migration.version)
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"insert into schema_migrations (version) values ($1)",
			migration.version,
		); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
		appliedCount++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	slog.InfoContext(ctx, "database migrations ready", "applied", appliedCount, "took", time.Since(startedAt))
	return nil
}
