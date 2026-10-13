package endpoints

import (
	"net/http"
	"testing"

	"money/backend/data"

	"github.com/stretchr/testify/require"
)

func TestMalformedPathUUIDsReturnBadRequest(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})

	for _, test := range []struct {
		name   string
		method string
		path   string
	}{
		{"get transaction", http.MethodGet, "/api/v1/transactions/bad"},
		{"patch transaction", http.MethodPatch, "/api/v1/transactions/bad"},
		{"split transaction", http.MethodPut, "/api/v1/transactions/bad/postings"},
		{"delete transaction", http.MethodDelete, "/api/v1/transactions/bad"},
		{"patch posting", http.MethodPatch, "/api/v1/postings/bad"},
		{"unmatch transfer", http.MethodDelete, "/api/v1/transfer-matches/bad"},
		{"get inbox row", http.MethodGet, "/api/v1/inbox/bad"},
		{"split inbox row", http.MethodPost, "/api/v1/inbox/bad/split"},
		{"inbox entries", http.MethodPost, "/api/v1/inbox/bad/entries"},
		{"get inbox matches", http.MethodGet, "/api/v1/inbox/bad/matches"},
		{"match inbox rows", http.MethodPost, "/api/v1/inbox/bad/match"},
		{"get import", http.MethodGet, "/api/v1/imports/bad"},
		{"list import duplicates", http.MethodGet, "/api/v1/imports/bad/duplicates"},
		{"force import row", http.MethodPost, "/api/v1/imports/rows/bad/import"},
		{"delete import", http.MethodDelete, "/api/v1/imports/bad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := authed(t, app, test.method, test.path, nil)
			defer resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestMalformedBodyUUIDsReturnBadRequest(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	validID := data.NewPrivateID()
	timestamp := "2026-01-01T00:00:00Z"

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"posting bucket", http.MethodPatch, "/api/v1/postings/" + validID, map[string]any{"bucket_id": "bad"}},
		{"bulk transaction", http.MethodPost, "/api/v1/transactions/categorize", map[string]any{"transaction_ids": []string{"bad"}, "bucket_id": validID}},
		{"bulk bucket", http.MethodPost, "/api/v1/transactions/categorize", map[string]any{"transaction_ids": []string{validID}, "bucket_id": "bad"}},
		{"remove transactions", http.MethodDelete, "/api/v1/transactions", map[string]any{"transaction_ids": []string{"bad"}}},
		{"add tag", http.MethodPost, "/api/v1/transactions/tags", map[string]any{"posting_ids": []string{"bad"}, "tag": "test"}},
		{"remove tag", http.MethodDelete, "/api/v1/transactions/tags", map[string]any{"posting_ids": []string{"bad"}, "tag": "test"}},
		{"split posting", http.MethodPut, "/api/v1/transactions/" + validID + "/postings", map[string]any{"expected_latest_posting_timestamp": timestamp, "postings": []map[string]any{{"id": "bad", "bucket_id": validID, "amount": 1, "currency": "EUR"}}}},
		{"split bucket", http.MethodPut, "/api/v1/transactions/" + validID + "/postings", map[string]any{"expected_latest_posting_timestamp": timestamp, "postings": []map[string]any{{"bucket_id": "bad", "amount": 1, "currency": "EUR"}}}},
		{"split inbox bucket", http.MethodPost, "/api/v1/inbox/" + validID + "/split", map[string]any{"postings": []map[string]any{{"bucket_id": "bad", "amount": 1}}}},
		{"inbox entries bucket", http.MethodPost, "/api/v1/inbox/" + validID + "/entries", map[string]any{"postings": []map[string]any{{"bucket_id": "bad", "amount": 1}}}},
		{"match inbox row", http.MethodPost, "/api/v1/inbox/" + validID + "/match", map[string]any{"match_id": "bad"}},
		{"restore inbox rows", http.MethodPost, "/api/v1/inbox/restore", map[string]any{"row_ids": []string{"bad"}}},
		{"categorize inbox row", http.MethodPost, "/api/v1/inbox/categorize", map[string]any{"row_ids": []string{"bad"}, "bucket": map[string]any{"kind": "expense", "name": "Food"}}},
		{"categorize inbox bucket", http.MethodPost, "/api/v1/inbox/categorize", map[string]any{"row_ids": []string{validID}, "bucket_id": "bad"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := authed(t, app, test.method, test.path, test.body)
			defer resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}

	resp := importCSV(t, app, "bad", nordeaHeader)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMalformedCursorUUIDsReturnBadRequest(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	validID := data.NewPrivateID()

	for _, path := range []string{
		"/api/v1/transactions?before_date=2026-01-01&before_id=bad",
		"/api/v1/inbox?before_date=2026-01-01&before_id=bad",
		"/api/v1/imports?before_date=2026-01-01T00:00:00Z&before_id=bad",
		"/api/v1/imports/" + validID + "/duplicates?cursor=bad",
	} {
		resp := authed(t, app, http.MethodGet, path, nil)
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, path)
	}
}
