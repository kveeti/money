package data

import (
	"context"
	"log/slog"
	"time"
)

const (
	importWorkers      = 4
	importMaxAttempts  = 5
	importBackoff      = 200 * time.Millisecond
	importPollFallback = 30 * time.Second
)

// StartImportWorkers launches the background pool that stages uploaded import
// batches into pending inbox rows. Workers wake on a kick or periodic poll and
// reclaim only jobs whose lease has expired.
func (d *Data) StartImportWorkers(ctx context.Context) {
	for i := 0; i < importWorkers; i++ {
		go d.importWorker(ctx)
	}
	go d.pollImports(ctx)
	d.kickImport()
}

func (d *Data) pollImports(ctx context.Context) {
	ticker := time.NewTicker(importPollFallback)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.kickImport()
		}
	}
}

func (d *Data) importWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.importKick:
		}
		d.drainImports(ctx)
	}
}

func (d *Data) drainImports(ctx context.Context) {
	for {
		did, err := d.processNextCSVImport(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Error("import staging failed, will retry on next poll", "err", err)
			return
		}
		if !did {
			return
		}
		d.kickImport() // pull peers in to parallelize a backlog of batches
	}
}
