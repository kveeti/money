package endpoints

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"mime/multipart"
	"money/backend/data"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const nordeaHeader = "date,occurred_at,amount,currency,counterparty,note\n"

func nordeaRow(date, amount, payee, message string) string {
	return nordeaRowCurrency(date, amount, payee, message, "EUR")
}

func nordeaRowCurrency(date, amount, payee, message, currency string) string {
	var out strings.Builder
	writer := csv.NewWriter(&out)
	_ = writer.Write([]string{strings.ReplaceAll(date, "/", "-"), "", strings.ReplaceAll(amount, ",", "."), currency, payee, message})
	writer.Flush()
	return out.String()
}

func importCSV(t *testing.T, app *testApp, bucketID, csv string) *http.Response {
	t.Helper()
	return importCSVFormat(t, app, bucketID, "nordea", csv)
}

func importCSVFormat(t *testing.T, app *testApp, bucketID, _ string, csv string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("bucket_id", bucketID))
	fw, err := mw.CreateFormFile("file", "export.csv")
	require.NoError(t, err)
	_, err = fw.Write([]byte(csv))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, app.url+"/api/v1/imports", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(login(t, app))
	resp, err := app.client.Do(req)
	require.NoError(t, err)
	return resp
}

type importResult struct {
	BatchID string `json:"batch_id"`
	Status  string `json:"status"`
}

// doImport uploads a CSV (the request returns as soon as the file is stored) and
// waits for the background worker to promote the batch to done, returning the
// final report.
func doImport(t *testing.T, app *testApp, bucketID, csv string) batchReport {
	t.Helper()
	return doImportFormat(t, app, bucketID, "nordea", csv)
}

func doImportFormat(t *testing.T, app *testApp, bucketID, format, csv string) batchReport {
	t.Helper()
	resp := importCSVFormat(t, app, bucketID, format, csv)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out importResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return waitImport(t, app, out.BatchID)
}

func waitImport(t *testing.T, app *testApp, batchID string) batchReport {
	t.Helper()
	var rep batchReport
	require.Eventually(t, func() bool {
		rep = getBatch(t, app, batchID)
		return rep.Status == "done"
	}, 5*time.Second, 15*time.Millisecond, "import batch never reached done")
	return rep
}

func TestImportNordea(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	csv := nordeaHeader +
		nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries") +
		nordeaRow("2026/07/02", "100,00", "Employer", "Salary")

	res := doImport(t, app, bank, csv)
	require.Equal(t, 2, res.Imported)
	require.Equal(t, 0, res.Duplicates)

	// Import stages rows into the inbox; no transactions exist until categorized.
	resp := authed(t, app, http.MethodGet, "/api/v1/transactions", nil)
	require.Empty(t, decodeTxns(t, resp))

	byParty := inboxByParty(getInbox(t, app, ""))
	require.Len(t, byParty, 2)
	require.Equal(t, "Groceries", byParty["K-Market"].Description)
	require.Equal(t, int64(-1234), byParty["K-Market"].Amount)
	require.Equal(t, "Salary", byParty["Employer"].Description)
	require.Equal(t, int64(10000), byParty["Employer"].Amount)
}

func TestImportIdenticalRowsBothImport(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	row := nordeaRow("2026/07/01", "-5,00", "Cafe", "Coffee")
	res := doImport(t, app, bank, nordeaHeader+row+row)
	require.Equal(t, 2, res.Imported)
	require.Equal(t, 0, res.Duplicates)

	require.Len(t, getInbox(t, app, ""), 2)
}

func TestImportDoesNotDedupAdjacentDatesAcrossSources(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	first := doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/02", "-12,34", "Cafe", "CSV"))
	_, err := app.d.Users.Exec("update import_batches set source='legacy_csv' where id=$1", first.ID)
	require.NoError(t, err)

	second := doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-12,34", "Cafe", "API"))
	require.Equal(t, 1, second.Imported)
	require.Zero(t, second.Duplicates)
	require.Len(t, getInbox(t, app, ""), 2)
}

func TestImportDedupsReimport(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	csv := nordeaHeader +
		nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries") +
		nordeaRow("2026/07/02", "100,00", "Employer", "Salary")

	doImport(t, app, bank, csv)
	res := doImport(t, app, bank, csv)
	require.Equal(t, 0, res.Imported)
	require.Equal(t, 2, res.Duplicates)

	// The first import's two rows are still the only inbox items.
	require.Len(t, getInbox(t, app, ""), 2)

	dups := dupRows(t, app, res.ID)
	require.Len(t, dups, 2)
	for _, row := range dups {
		require.NotNil(t, row.DuplicateOf)
		require.NotNil(t, row.DuplicateTarget, "duplicate row exposes its target inline")
		require.Nil(t, row.DuplicateTarget.TransactionID, "target is a pending row, not yet a transaction")
	}
}

func TestForceImportDuplicate(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	row := nordeaRow("2026/07/01", "-5,00", "Cafe", "Coffee")
	doImport(t, app, bank, nordeaHeader+row)        // occ 0 pending
	res := doImport(t, app, bank, nordeaHeader+row) // occ 0 duplicate
	require.Equal(t, 1, res.Duplicates)

	dups := dupRows(t, app, res.ID)
	require.Len(t, dups, 1)
	dupID := dups[0].ID

	resp := authed(t, app, http.MethodPost, "/api/v1/imports/rows/"+dupID+"/import", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	require.Len(t, getInbox(t, app, ""), 2) // original + forced
	var status string
	var occurrence, audits int
	var duplicateOf *string
	require.NoError(t, app.d.Users.QueryRow(`
		select status, occurrence, duplicate_of
		from import_rows
		where id = $1
	`, dupID).Scan(&status, &occurrence, &duplicateOf))
	require.Equal(t, "pending", status)
	require.Equal(t, 1, occurrence)
	require.Nil(t, duplicateOf)
	require.NoError(t, app.d.Users.QueryRow(`
		select count(*)
		from audit_logs
		where table_name = 'import_rows'
		  and row_id = $1
		  and operation = 'update'
		  and before->>'status' = 'duplicate'
	`, dupID).Scan(&audits))
	require.Equal(t, 1, audits)

	resp = authed(t, app, http.MethodPost, "/api/v1/imports/rows/"+dupID+"/import", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// A third import of the same single-row file dups against both copies.
	res = doImport(t, app, bank, nordeaHeader+row+row)
	require.Equal(t, 0, res.Imported)
	require.Equal(t, 2, res.Duplicates)
}

func TestDeleteImportBatch(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	csv := nordeaHeader + nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries")
	res := doImport(t, app, bank, csv)
	require.Len(t, getInbox(t, app, ""), 1)

	resp := authed(t, app, http.MethodDelete, "/api/v1/imports/"+res.ID, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	require.Empty(t, getInbox(t, app, ""))

	// Dedup memory cleared: re-import works fresh.
	res = doImport(t, app, bank, csv)
	require.Equal(t, 1, res.Imported)
	require.Equal(t, 0, res.Duplicates)
}

func TestDeleteImportNotFound(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	resp := authed(t, app, http.MethodDelete, "/api/v1/imports/"+data.NewPrivateID(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeleteImportRemovesTaggedTransaction(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	groceries := createBucket(t, app, "expense", "Groceries")
	batch := doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries"))
	rows := getInbox(t, app, "")
	require.Len(t, rows, 1)
	require.Equal(t, 1, categorizeInbox(t, app, []string{rows[0].ID}, groceries))

	resp := authed(t, app, http.MethodGet, "/api/v1/transactions", nil)
	txns := decodeTxns(t, resp)
	require.Len(t, txns, 1)
	txnID := txns[0]["id"].(string)
	tagPostingID := postingIDForTransaction(t, app, txnID)
	resp = authed(t, app, http.MethodPost, "/api/v1/transactions/tags", map[string]any{
		"posting_ids": []string{tagPostingID}, "tag": "imported",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = authed(t, app, http.MethodDelete, "/api/v1/imports/"+batch.ID, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp = authed(t, app, http.MethodGet, "/api/v1/transactions", nil)
	require.Empty(t, decodeTxns(t, resp))
	resp = authed(t, app, http.MethodGet, "/api/v1/tags", nil)
	var tags struct {
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tags))
	require.Empty(t, tags.Tags)

	var batchDeletes, rowDeletes, transactionDeletes, postingDeletes, tagDeletes int
	require.NoError(t, app.d.Users.QueryRow(`
		select
			count(*) filter (
				where table_name = 'import_batches'
				  and operation = 'delete'
				  and row_id = $1
			),
			count(*) filter (
				where table_name = 'import_rows'
				  and operation = 'delete'
				  and row_id = $2
			),
			count(*) filter (
				where table_name = 'transactions'
				  and operation = 'delete'
				  and row_id = $3
			),
			count(*) filter (
				where table_name = 'postings'
				  and operation = 'delete'
				  and before->>'transaction_id' = $3::text
			),
			count(*) filter (
				where table_name = 'posting_tags'
				  and operation = 'delete'
				  and before->>'posting_id' = $4
			)
		from audit_logs
	`, batch.ID, rows[0].ID, txnID, tagPostingID).Scan(
		&batchDeletes,
		&rowDeletes,
		&transactionDeletes,
		&postingDeletes,
		&tagDeletes,
	))
	require.Equal(t, 1, batchDeletes)
	require.Equal(t, 1, rowDeletes)
	require.Equal(t, 1, transactionDeletes)
	require.Equal(t, 2, postingDeletes)
	require.Equal(t, 1, tagDeletes)
}

func TestDeleteImportLeavesOtherTransferSideUnmatched(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	checkingImport := doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-500,00", "Transfer", "Savings"))
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/02", "500,00", "Transfer", "Checking"))

	rows := getInbox(t, app, "")
	require.Len(t, rows, 2)
	var outgoing, incoming string
	for _, row := range rows {
		if row.Amount < 0 {
			outgoing = row.ID
		} else {
			incoming = row.ID
		}
	}
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/"+outgoing+"/match", map[string]any{"match_id": incoming})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, getInbox(t, app, ""))

	resp = authed(t, app, http.MethodDelete, "/api/v1/imports/"+checkingImport.ID, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	require.Empty(t, getInbox(t, app, ""))
	page := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, page.Transactions, 1)
	require.NotNil(t, page.Transactions[0].Transfer)
	require.True(t, page.Transactions[0].Transfer.Unmatched)
}

func TestImportCollectsRowErrors(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	csv := nordeaHeader +
		nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries") +
		nordeaRow("bad-date", "-1,00", "Broken", "x") +
		nordeaRow("2026/07/03", "50,00", "Refund", "y") +
		"2026-07-04,2026-07-04 12:00:00,-1.00,EUR,No offset,z\n" +
		",2026-07-05T12:00:00Z,-1.00,EUR,UTC timestamp,z\n"

	// Parsing is async now, so per-row errors surface on the finished report.
	rep := doImport(t, app, bank, csv)
	require.Equal(t, 3, rep.Imported)
	require.Len(t, rep.ParseErrors, 2)
	require.Equal(t, 3, rep.ParseErrors[0].Line)
	require.Equal(t, 5, rep.ParseErrors[1].Line)
	require.Equal(t, "invalid occurred_at: must be an RFC3339 UTC timestamp", rep.ParseErrors[1].Error)
}

func TestImportBoundsReportedRowErrors(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	var csv strings.Builder
	csv.WriteString(nordeaHeader)
	for range data.MaxStoredRowErrors + 5 {
		csv.WriteString("bad,,-1,EUR,,\n")
	}
	rep := doImport(t, app, bank, csv.String())
	require.Len(t, rep.ParseErrors, data.MaxStoredRowErrors)
	require.Equal(t, data.MaxStoredRowErrors+5, rep.ParseErrorCount)
}

func TestImportRejectsNonNordeaFile(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	resp := importCSV(t, app, bank, "date,amount,desc\n2026-07-01,-1,coffee\n")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestImportRejectsOversizeFile(t *testing.T) {
	old := maxImportBytes
	maxImportBytes = 512
	defer func() { maxImportBytes = old }()

	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	body := nordeaHeader + strings.Repeat(nordeaRow("2026/07/01", "-1,00", "A", "a"), 40)
	resp := importCSV(t, app, bank, body)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestImportWorkersOnlyReclaimExpiredLeases(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select owner_user_id from buckets where id=$1", bank).Scan(&userID))
	batchID, claimID := data.NewPrivateID(), data.NewPrivateID()
	_, err := app.d.Users.Exec(`
		insert into import_batches (
			id, user_id, bucket_id, source, filename, created_at, status,
			claim_id, claim_expires_at
		)
		values ($1, $2, $3, 'csv', 'leased.csv', now(), 'processing', $4, now() + interval '10 minutes')
	`, batchID, userID, bank, claimID)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.d.StartImportWorkers(ctx)
	time.Sleep(50 * time.Millisecond)
	var status, currentClaim string
	require.NoError(t, app.d.Users.QueryRow("select status, claim_id from import_batches where id=$1", batchID).Scan(&status, &currentClaim))
	require.Equal(t, "processing", status)
	require.Equal(t, claimID, currentClaim)

	_, err = app.d.Users.Exec("update import_batches set claim_expires_at = now() - interval '1 minute' where id=$1", batchID)
	require.NoError(t, err)
	app.d.StartImportWorkers(ctx)
	require.Eventually(t, func() bool {
		return app.d.Users.QueryRow("select status from import_batches where id=$1", batchID).Scan(&status) == nil && status == "failed"
	}, time.Second, 10*time.Millisecond)
}

func TestImportRejectsWhenUserHasTooManyActiveImports(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select owner_user_id from buckets where id=$1", bank).Scan(&userID))
	for range data.MaxActiveCSVImports {
		_, err := app.d.Users.Exec(`
			insert into import_batches (id, user_id, bucket_id, source, filename, created_at, status)
			values ($1, $2, $3, 'csv', 'held.csv', now(), 'processing')
		`, data.NewPrivateID(), userID, bank)
		require.NoError(t, err)
	}

	resp := importCSV(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-1,00", "A", "a"))
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestImportRejectsHourlyLimit(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select owner_user_id from buckets where id=$1", bank).Scan(&userID))
	for range data.MaxHourlyCSVImports {
		_, err := app.d.Users.Exec(`
			insert into import_batches (id, user_id, bucket_id, source, filename, created_at, status)
			values ($1, $2, $3, 'csv', 'recent.csv', now(), 'done')
		`, data.NewPrivateID(), userID, bank)
		require.NoError(t, err)
	}

	resp := importCSV(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-1,00", "A", "a"))
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestImportRejectsWhenUploadSlotsAreFull(t *testing.T) {
	oldSlots := importUploadSlots
	importUploadSlots = make(chan struct{}, maxConcurrentUploads)
	for range maxConcurrentUploads {
		importUploadSlots <- struct{}{}
	}
	defer func() { importUploadSlots = oldSlots }()

	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	resp := importCSV(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-1,00", "A", "a"))
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestImportRejectsBadBucket(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	groceries := createBucket(t, app, "expense", "Groceries")

	csv := nordeaHeader + nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries")

	resp := importCSV(t, app, groceries, csv)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = importCSV(t, app, "00000000-0000-0000-0000-000000000000", csv)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var batches, files int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from import_batches").Scan(&batches))
	require.NoError(t, app.d.Users.QueryRow("select count(*) from import_files").Scan(&files))
	require.Zero(t, batches)
	require.Zero(t, files)
}

func TestListImports(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries"))

	resp := authed(t, app, http.MethodGet, "/api/v1/imports", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var page struct {
		Rows       []map[string]any `json:"rows"`
		NextCursor *cursor          `json:"next_cursor"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Rows, 1)
	require.Nil(t, page.NextCursor)
	require.Equal(t, "export.csv", page.Rows[0]["filename"])
	require.Equal(t, float64(1), page.Rows[0]["imported"])
}

// batchReport mirrors the counts-only poll payload — the report endpoint never
// ships rows (they can number in the millions). Row-level detail comes from the
// data layer in tests via dupRows.
type batchReport struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Imported    int    `json:"imported"`
	Duplicates  int    `json:"duplicates"`
	Error       string `json:"error"`
	ParseErrors []struct {
		Line  int    `json:"line"`
		Error string `json:"error"`
	} `json:"parse_errors"`
	ParseErrorCount int `json:"parse_error_count"`
}

func getBatch(t *testing.T, app *testApp, id string) batchReport {
	t.Helper()
	resp := authed(t, app, http.MethodGet, "/api/v1/imports/"+id, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out batchReport
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

type dupRow struct {
	ID              string  `json:"id"`
	Date            string  `json:"date"`
	DuplicateOf     *string `json:"duplicate_of"`
	DuplicateTarget *struct {
		TransactionID *string `json:"transaction_id"`
	} `json:"duplicate_target"`
}

type dupPage struct {
	Rows       []dupRow `json:"rows"`
	NextCursor *string  `json:"next_cursor"`
}

func getDuplicates(t *testing.T, app *testApp, id, query string) dupPage {
	t.Helper()
	resp := authed(t, app, http.MethodGet, "/api/v1/imports/"+id+"/duplicates"+query, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out dupPage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestListDuplicatesPaginates(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")

	csv := nordeaHeader +
		nordeaRow("2026/07/01", "-1,00", "A", "a") +
		nordeaRow("2026/07/02", "-2,00", "B", "b") +
		nordeaRow("2026/07/03", "-3,00", "C", "c")

	doImport(t, app, bank, csv)
	res := doImport(t, app, bank, csv)
	require.Equal(t, 3, res.Duplicates)

	p1 := getDuplicates(t, app, res.ID, "?limit=2")
	require.Len(t, p1.Rows, 2)
	require.Equal(t, "2026-07-03", p1.Rows[0].Date)
	require.Equal(t, "2026-07-02", p1.Rows[1].Date)
	require.NotNil(t, p1.NextCursor)
	for _, r := range p1.Rows {
		require.NotNil(t, r.DuplicateOf)
		require.NotNil(t, r.DuplicateTarget, "duplicate row exposes its target inline")
	}

	p2 := getDuplicates(t, app, res.ID, "?limit=2&cursor="+*p1.NextCursor)
	require.Len(t, p2.Rows, 1)
	require.Equal(t, "2026-07-01", p2.Rows[0].Date)
	require.Nil(t, p2.NextCursor)

	require.NotEqual(t, p1.Rows[0].ID, p2.Rows[0].ID)
}

func dupRows(t *testing.T, app *testApp, batchID string) []dupRow {
	t.Helper()
	return getDuplicates(t, app, batchID, "?limit=200").Rows
}
