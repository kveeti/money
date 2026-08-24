package data

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
)

const maintenanceInterval = time.Hour

func (d *Data) StartMaintenance(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(maintenanceInterval)
		defer ticker.Stop()
		for {
			if err := d.runMaintenance(ctx); err != nil && ctx.Err() == nil {
				slog.Error("database maintenance failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (d *Data) runMaintenance(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `
		with expired_sessions as (
			delete from sessions
			where expires_at <= now()
		), stale_import_files as (
			delete from import_files stored
			using import_batches batch
			where batch.id = stored.batch_id
			  and batch.status in ('done', 'failed')
			  and batch.created_at < now() - interval '1 hour'
		)
		select 1
	`)
	if err != nil {
		return err
	}
	if store, ok := d.files.(*DiskFileStore); ok {
		return d.cleanupDiskImports(ctx, store)
	}
	return nil
}

func (d *Data) cleanupDiskImports(ctx context.Context, store *DiskFileStore) error {
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			if _, err := uuid.Parse(entry.Name()); err == nil {
				keys = append(keys, entry.Name())
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}

	rows, err := d.db.QueryContext(ctx, `
		select id
		from import_batches
		where id = any($1::uuid[])
		  and not (
		    status in ('done', 'failed')
		    and created_at < now() - interval '1 hour'
		  )
	`, keys)
	if err != nil {
		return err
	}
	defer rows.Close()
	keep := make(map[string]bool, len(keys))
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return err
		}
		keep[key] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range keys {
		if !keep[key] {
			if err := store.Delete(ctx, key); err != nil {
				return err
			}
		}
	}
	return nil
}
