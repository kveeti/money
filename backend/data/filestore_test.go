package data

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiskFileStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewDiskFileStore(dir)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, store.Put(ctx, "k1", strings.NewReader("hello world")))
	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(store.path("k1"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	rc, err := store.Open(ctx, "k1")
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, "hello world", string(b))

	require.NoError(t, store.Delete(ctx, "k1"))
	_, err = store.Open(ctx, "k1")
	require.ErrorIs(t, err, ErrFileNotFound)

	require.NoError(t, store.Delete(ctx, "k1")) // idempotent
}
