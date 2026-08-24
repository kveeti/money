package endpoints

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"money/backend/data"

	"github.com/stretchr/testify/require"
)

func TestInitialMigrationRunsOnce(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	d := newTestData(t)
	require.Contains(t, logs.String(), `"msg":"applying database migration"`)
	require.Contains(t, logs.String(), `"version":1`)
	require.Contains(t, logs.String(), `"msg":"database migrations ready"`)
	require.Contains(t, logs.String(), `"applied":1`)

	var database string
	var appliedAt time.Time
	require.NoError(t, d.Users.QueryRow(`
		select current_database(), applied_at
		from schema_migrations
		where version = 1
	`).Scan(&database, &appliedAt))

	dsn, err := dsnForDB(baseDSN, database)
	require.NoError(t, err)

	logs.Reset()
	second, err := data.NewData(context.Background(), dsn, "", "")
	require.NoError(t, err)
	require.NotContains(t, logs.String(), `"msg":"applying database migration"`)
	require.Contains(t, logs.String(), `"msg":"database migrations ready"`)
	require.Contains(t, logs.String(), `"applied":0`)
	t.Cleanup(func() { require.NoError(t, second.Close()) })

	var count int
	var reappliedAt time.Time
	require.NoError(t, second.Users.QueryRow(`
		select count(*), max(applied_at)
		from schema_migrations
		where version = 1
	`).Scan(&count, &reappliedAt))
	require.Equal(t, 1, count)
	require.Equal(t, appliedAt, reappliedAt)
}
