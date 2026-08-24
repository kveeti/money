package data

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
)

const (
	maintenanceInterval     = time.Hour
	demoMaintenanceInterval = time.Minute
	demoCleanupBatchSize    = 100
)

func (d *Data) StartMaintenance(ctx context.Context) {
	go func() {
		maintenanceTicker := time.NewTicker(maintenanceInterval)
		demoTicker := time.NewTicker(demoMaintenanceInterval)
		defer maintenanceTicker.Stop()
		defer demoTicker.Stop()

		if err := d.runMaintenance(ctx); err != nil && ctx.Err() == nil {
			slog.Error("database maintenance failed", "err", err)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-maintenanceTicker.C:
				if err := d.runMaintenance(ctx); err != nil && ctx.Err() == nil {
					slog.Error("database maintenance failed", "err", err)
				}
			case <-demoTicker.C:
				if err := d.cleanupExpiredDemoUsers(ctx); err != nil && ctx.Err() == nil {
					slog.Error("demo cleanup failed", "err", err)
				}
			}
		}
	}()
}

func (d *Data) runMaintenance(ctx context.Context) error {
	if err := d.cleanupExpiredDemoUsers(ctx); err != nil {
		return err
	}
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
		), stale_demo_limits as (
			delete from demo_rate_limits
			where updated_at < now() - interval '2 hours'
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

func (d *Data) cleanupExpiredDemoUsers(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, fmt.Sprintf(`
		do $$
		declare
			expired_ids uuid[];
		begin
			select array_agg(expired.id)
			into expired_ids
			from (
				select id
				from users
				where is_demo
				  and demo_expires_at <= now()
				order by demo_expires_at, id
				limit %d
				for update skip locked
			) expired;

			if expired_ids is null then
				return;
			end if;

			update buckets
			set counterpart_user_id = null
			where counterpart_user_id = any(expired_ids);

			delete from sessions
			where user_id = any(expired_ids);

			delete from account_movement_matches
			where owner_user_id = any(expired_ids);

			delete from postings posting
			using transactions transaction
			where posting.transaction_id = transaction.id
			  and transaction.owner_user_id = any(expired_ids);

			delete from transactions
			where owner_user_id = any(expired_ids);

			delete from import_rows row
			using import_batches batch
			where row.batch_id = batch.id
			  and batch.user_id = any(expired_ids);

			delete from import_batches
			where user_id = any(expired_ids);

			update buckets
			set active_bank_integration_id = null
			where owner_user_id = any(expired_ids);

			delete from buckets
			where owner_user_id = any(expired_ids);

			delete from bank_integrations
			where user_id = any(expired_ids);

			delete from audit_logs
			where actor_user_id = any(expired_ids);

			delete from users
			where id = any(expired_ids);
		end
		$$
	`, demoCleanupBatchSize))
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
