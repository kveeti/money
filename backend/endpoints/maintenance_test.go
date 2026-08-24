package endpoints

import (
	"context"
	"net/http"
	"testing"
	"time"

	"money/backend/data"

	"github.com/stretchr/testify/require"
)

func TestHealthChecksDatabase(t *testing.T) {
	app := newTestAppWith(t, appOpts{})
	require.NoError(t, app.d.Close())

	resp, err := app.client.Get(app.url + "/api/health")
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestDatabasePoolIsBounded(t *testing.T) {
	d := newTestData(t)
	require.Equal(t, 20, d.Users.DB.Stats().MaxOpenConnections)
}

func TestMaintenanceRemovesExpiredSessionsAndStoredImports(t *testing.T) {
	d := newTestData(t)
	userID := data.NewPrivateID()
	require.NoError(t, d.CreateUser(t.Context(), data.User{
		ID: userID, Subject: "maintenance", Issuer: "test", Email: "maintenance@example.com", CreatedAt: time.Now(),
	}))
	bucketID := data.NewPrivateID()
	require.NoError(t, d.CreateBucket(t.Context(), data.Bucket{
		ID: bucketID, OwnerUserID: userID, Kind: data.KindAsset, Name: "Bank", CreatedAt: time.Now(),
	}))
	_, err := d.Users.Exec(`
		with inserted_session as (
			insert into sessions (id, user_id, token_hash, created_at, expires_at)
			values ($1, $2, 'expired', now() - interval '2 hours', now() - interval '1 hour')
		), inserted_batch as (
			insert into import_batches (id, user_id, bucket_id, source, filename, created_at, status)
			values ($3, $2, $4, 'csv', 'done.csv', now() - interval '2 hours', 'done')
			returning id
		)
		insert into import_files (batch_id, content, created_at)
		select id, 'old', now() - interval '2 hours' from inserted_batch
	`, data.NewPrivateID(), userID, data.NewPrivateID(), bucketID)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.StartMaintenance(ctx)
	require.Eventually(t, func() bool {
		var sessions, files int
		err := d.Users.QueryRow(`
			select
				(select count(*) from sessions where token_hash = 'expired'),
				(select count(*) from import_files)
		`).Scan(&sessions, &files)
		return err == nil && sessions == 0 && files == 0
	}, time.Second, 10*time.Millisecond)
}
