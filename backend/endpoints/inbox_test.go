package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"money/backend/data"

	"github.com/stretchr/testify/require"
)

type inboxRow struct {
	ID           string `json:"id"`
	Date         string `json:"date"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	Counterparty string `json:"counterparty"`
	Description  string `json:"description"`
	Account      string `json:"account"`
	Kind         string `json:"kind"`
}

func getInbox(t *testing.T, app *testApp, query string) []inboxRow {
	t.Helper()
	resp := authed(t, app, http.MethodGet, "/api/v1/inbox"+query, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Rows []inboxRow `json:"rows"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Rows
}

func TestInboxFilters(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	cash := createBucket(t, app, "asset", "Cash")
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2026/01/01", "-30,00", "Old", "")+
		nordeaRow("2026/02/01", "-12,00", "Food", ""))
	doImport(t, app, cash, nordeaHeader+
		nordeaRow("2026/02/02", "50,00", "Salary", ""))

	list := func(values url.Values) []inboxRow {
		return getInbox(t, app, "?"+values.Encode())
	}
	parties := func(rows []inboxRow) []string {
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.Counterparty
		}
		return out
	}

	require.ElementsMatch(t, []string{"Old", "Food"}, parties(list(url.Values{"account": {bank}})))
	require.Equal(t, []string{"Salary"}, parties(list(url.Values{"direction": {"in"}})))
	require.Equal(t, []string{"Salary"}, parties(list(url.Values{"currency": {"EUR"}, "amount": {"50"}})))
	require.Equal(t, []string{"Food"}, parties(list(url.Values{"currency": {"EUR"}, "amount_min": {"10"}, "amount_max": {"20"}})))

	food := inboxByParty(getInbox(t, app, ""))["Food"]
	foodDate, err := time.Parse(time.DateOnly, food.Date)
	require.NoError(t, err)
	require.Equal(t, []string{"Food"}, parties(list(url.Values{
		"occurred_from":   {foodDate.Format(time.DateOnly)},
		"occurred_before": {foodDate.AddDate(0, 0, 1).Format(time.DateOnly)},
	})))
}

func TestInboxFiltersRejectInvalidValues(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	for _, query := range []string{
		"direction=sideways",
		"account=nope",
		"occurred_from=not-a-time",
		"amount=1&amount_min=1&currency=EUR",
		"category=nope",
		"tag=nope",
	} {
		response := authed(t, app, http.MethodGet, "/api/v1/inbox?"+query, nil)
		require.Equal(t, http.StatusBadRequest, response.StatusCode, query)
	}
}

func categorizeInboxTransactions(t *testing.T, app *testApp, rowIDs []string, bucketID string) []string {
	t.Helper()
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/categorize", map[string]any{
		"row_ids":   rowIDs,
		"bucket_id": bucketID,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Categorized    int             `json:"categorized"`
		TransactionIDs json.RawMessage `json:"transaction_ids"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Nil(t, out.TransactionIDs)

	rows, err := app.d.Users.Query("select transaction_id from postings where import_row_id = any($1::uuid[]) order by import_row_id", rowIDs)
	require.NoError(t, err)
	defer rows.Close()
	var transactionIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		transactionIDs = append(transactionIDs, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, out.Categorized, len(transactionIDs))
	return transactionIDs
}

func postingByBucket(t *testing.T, postings []postingResponse, bucketID string) postingResponse {
	t.Helper()
	for _, posting := range postings {
		if posting.Bucket.ID == bucketID {
			return posting
		}
	}
	t.Fatalf("posting for bucket %s not found", bucketID)
	return postingResponse{}
}

func inboxByParty(rows []inboxRow) map[string]inboxRow {
	m := map[string]inboxRow{}
	for _, r := range rows {
		m[r.Counterparty] = r
	}
	return m
}

func categorizeInbox(t *testing.T, app *testApp, rowIDs []string, bucketID string) int {
	t.Helper()
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/categorize", map[string]any{
		"row_ids":   rowIDs,
		"bucket_id": bucketID,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Categorized int `json:"categorized"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Categorized
}

// Categorizing pending inbox rows creates ledger transactions with an asset leg on
// the import bucket and a category leg on the chosen bucket.
func TestInboxCategorizeCreatesTransactions(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	groceries := createBucket(t, app, "expense", "Groceries")

	csv := nordeaHeader +
		nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries") +
		nordeaRow("2026/07/02", "100,00", "Employer", "Salary")
	doImport(t, app, bank, csv)

	rows := getInbox(t, app, "")
	require.Len(t, rows, 2)

	ids := []string{rows[0].ID, rows[1].ID}
	require.Equal(t, 2, categorizeInbox(t, app, ids, groceries))

	require.Empty(t, getInbox(t, app, ""))

	resp := authed(t, app, http.MethodGet, "/api/v1/transactions", nil)
	require.Len(t, decodeTxns(t, resp), 2)

	balances := postingTotals(t, app)
	require.Equal(t, int64(-1234+10000), balances[bank]["EUR"])
	require.Equal(t, int64(1234-10000), balances[groceries]["EUR"])
}

func TestSplitInboxRowCreatesBalancedCategoryPostings(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	food := createBucket(t, app, "expense", "Food")
	travel := createBucket(t, app, "expense", "Travel")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-10,00", "Market", "Split"))
	rows := getInbox(t, app, "")
	require.Len(t, rows, 1)

	response := authed(t, app, http.MethodGet, "/api/v1/inbox/"+rows[0].ID, nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var source inboxRowResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&source))
	require.Equal(t, int64(-1000), source.Amount)

	response = authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows[0].ID+"/split", map[string]any{"postings": []map[string]any{
		{"bucket_id": food, "amount": 600},
		{"bucket_id": travel, "amount": 400},
	}})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Empty(t, getInbox(t, app, ""))

	page := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, page.Transactions, 1)
	require.Len(t, page.Transactions[0].Postings, 3)
	require.Equal(t, int64(-1000), postingByBucket(t, page.Transactions[0].Postings, bank).Amount)
	require.Equal(t, int64(600), postingByBucket(t, page.Transactions[0].Postings, food).Amount)
	require.Equal(t, int64(400), postingByBucket(t, page.Transactions[0].Postings, travel).Amount)
}

func TestInboxEntriesCanSettleAnAssetWithAnExpense(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	deposit := createBucket(t, app, "asset", "Rental security deposit")
	water := createBucket(t, app, "expense", "Water")
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2024/07/01", "-580,00", "Landlord", "Security deposit")+
		nordeaRow("2026/07/01", "570,00", "Landlord", "Deposit return"))
	var depositRow, returnRow inboxRow
	for _, row := range getInbox(t, app, "") {
		if row.Amount < 0 {
			depositRow = row
		} else {
			returnRow = row
		}
	}
	response := authed(t, app, http.MethodPost, "/api/v1/inbox/"+depositRow.ID+"/entries", map[string]any{"postings": []map[string]any{
		{"bucket_id": deposit, "amount": 58000},
	}})
	require.Equal(t, http.StatusOK, response.StatusCode)
	response = authed(t, app, http.MethodPost, "/api/v1/inbox/"+returnRow.ID+"/entries", map[string]any{"postings": []map[string]any{
		{"bucket_id": deposit, "amount": -58000},
		{"bucket_id": water, "amount": 1000},
	}})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Empty(t, getInbox(t, app, ""))

	balances := postingTotals(t, app)
	require.Equal(t, int64(-1000), balances[bank]["EUR"])
	require.Equal(t, int64(0), balances[deposit]["EUR"])
	require.Equal(t, int64(1000), balances[water]["EUR"])

	var returnedTransactionID string
	require.NoError(t, app.d.Users.QueryRow(`select transaction_id from postings where import_row_id=$1`, returnRow.ID).Scan(&returnedTransactionID))
	returned := getTransaction(t, app, returnedTransactionID)
	depositPosting := postingByBucket(t, returned.Postings, deposit)
	waterPosting := postingByBucket(t, returned.Postings, water)
	response = authed(t, app, http.MethodPut, "/api/v1/transactions/"+returnedTransactionID+"/postings", map[string]any{
		"expected_latest_posting_timestamp": returned.LatestPostingTimestamp,
		"postings": []map[string]any{
			{"id": depositPosting.ID, "bucket_id": deposit, "amount": -59000, "currency": "EUR"},
			{"id": waterPosting.ID, "bucket_id": water, "amount": 2000, "currency": "EUR"},
		},
	})
	require.Equal(t, http.StatusOK, response.StatusCode)
	balances = postingTotals(t, app)
	require.Equal(t, int64(-1000), balances[bank]["EUR"])
	require.Equal(t, int64(-1000), balances[deposit]["EUR"])
	require.Equal(t, int64(2000), balances[water]["EUR"])
}

func TestInboxEntriesRejectAnUnbalancedTransaction(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	deposit := createBucket(t, app, "asset", "Rental security deposit")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "570,00", "Landlord", "Deposit return"))
	row := getInbox(t, app, "")[0]

	response := authed(t, app, http.MethodPost, "/api/v1/inbox/"+row.ID+"/entries", map[string]any{"postings": []map[string]any{
		{"bucket_id": deposit, "amount": -58000},
	}})
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.Len(t, getInbox(t, app, ""), 1)
	require.Empty(t, decodeTxns(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil)))
}

func TestSplitInboxRowRejectsAnUnbalancedSplit(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	food := createBucket(t, app, "expense", "Food")
	travel := createBucket(t, app, "expense", "Travel")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-10,00", "Market", "Split"))
	row := getInbox(t, app, "")[0]

	response := authed(t, app, http.MethodPost, "/api/v1/inbox/"+row.ID+"/split", map[string]any{"postings": []map[string]any{
		{"bucket_id": food, "amount": 600},
		{"bucket_id": travel, "amount": 300},
	}})
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.Len(t, getInbox(t, app, ""), 1)
	require.Empty(t, decodeTxns(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil)))
}

func TestRemoveTransactionsRestoresImportedRows(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	groceries := createBucket(t, app, "expense", "Groceries")
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries")+
		nordeaRow("2026/07/02", "-5,00", "Cafe", "Coffee"))

	rows := getInbox(t, app, "")
	rowIDs := []string{rows[0].ID, rows[1].ID}
	transactionIDs := categorizeInboxTransactions(t, app, rowIDs, groceries)
	require.Len(t, transactionIDs, 2)

	resp := authed(t, app, http.MethodPost, "/api/v1/transactions/tags", map[string]any{
		"posting_ids": []string{postingIDForTransaction(t, app, transactionIDs[0])}, "tag": "temporary",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = authed(t, app, http.MethodDelete, "/api/v1/transactions", map[string]any{
		"transaction_ids": transactionIDs,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var removed struct {
		Removed  int `json:"removed"`
		Restored int `json:"restored"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&removed))
	require.Equal(t, 2, removed.Removed)
	require.Equal(t, 2, removed.Restored)
	require.Len(t, getInbox(t, app, ""), 2)
	require.Empty(t, decodeTxns(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil)))
	require.Empty(t, postingTotals(t, app))

	resp = authed(t, app, http.MethodGet, "/api/v1/tags", nil)
	var tags struct {
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tags))
	require.Empty(t, tags.Tags)
}

func TestRemoveMatchedTransactionRestoresOnlyThatSide(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-500,00", "Transfer", "Savings"))
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/02", "500,00", "Transfer", "Checking"))

	rows := getInbox(t, app, "")
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows[0].ID+"/match", map[string]any{"match_id": rows[1].ID})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var matched struct {
		Matched       bool            `json:"matched"`
		TransactionID json.RawMessage `json:"transaction_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&matched))
	require.True(t, matched.Matched)
	require.Nil(t, matched.TransactionID)
	var transactionID string
	require.NoError(t, app.d.Users.QueryRow("select transaction_id from postings where import_row_id = $1", rows[0].ID).Scan(&transactionID))
	resp = authed(t, app, http.MethodPost, "/api/v1/transactions/tags", map[string]any{
		"posting_ids": []string{postingIDForTransaction(t, app, transactionID)}, "tag": "transfer",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = authed(t, app, http.MethodDelete, "/api/v1/transactions", map[string]any{
		"transaction_ids": []string{transactionID},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, getInbox(t, app, ""), 1)
	remaining := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, remaining.Transactions, 1)
	require.NotNil(t, remaining.Transactions[0].Transfer)
	require.True(t, remaining.Transactions[0].Transfer.Unmatched)
	expectedSide := "outgoing"
	if remaining.Transactions[0].Postings[0].Amount > 0 {
		expectedSide = "incoming"
	}
	require.Equal(t, expectedSide, remaining.Transactions[0].Transfer.Side)
}

func TestRestoreInboxRows(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	groceries := createBucket(t, app, "expense", "Groceries")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries"))

	row := getInbox(t, app, "")[0]
	transactionIDs := categorizeInboxTransactions(t, app, []string{row.ID}, groceries)
	require.Len(t, transactionIDs, 1)
	transactionID := transactionIDs[0]
	tagPostingID := postingIDForTransaction(t, app, transactionID)
	resp := authed(t, app, http.MethodPost, "/api/v1/transactions/tags", map[string]any{
		"posting_ids": []string{tagPostingID}, "tag": "temporary",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = authed(t, app, http.MethodPost, "/api/v1/inbox/restore", map[string]any{"row_ids": []string{row.ID}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Restored int `json:"restored"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, 1, out.Restored)
	require.Len(t, getInbox(t, app, ""), 1)
	require.Empty(t, decodeTxns(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil)))

	var transactionDeletes, postingDeletes, tagDeletes, rowUpdates int
	require.NoError(t, app.d.Users.QueryRow(`select count(*) from audit_logs
		where table_name = 'transactions' and operation = 'delete' and row_id = $1`, transactionID).Scan(&transactionDeletes))
	require.NoError(t, app.d.Users.QueryRow(`select count(*) from audit_logs
		where table_name = 'postings' and operation = 'delete' and before->>'transaction_id' = $1`, transactionID).Scan(&postingDeletes))
	require.NoError(t, app.d.Users.QueryRow(`select count(*) from audit_logs
		where table_name = 'posting_tags' and operation = 'delete' and before->>'posting_id' = $1`, tagPostingID).Scan(&tagDeletes))
	require.NoError(t, app.d.Users.QueryRow(`select count(*) from audit_logs
		where table_name = 'import_rows' and operation = 'update' and row_id = $1`, row.ID).Scan(&rowUpdates))
	require.Equal(t, 1, transactionDeletes)
	require.Equal(t, 2, postingDeletes)
	require.Equal(t, 1, tagDeletes)
	require.Equal(t, 2, rowUpdates)
}

func TestRestoreInboxMatchRestoresOnlySelectedSide(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-500,00", "Transfer", "Savings"))
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/02", "500,00", "Transfer", "Checking"))

	rows := getInbox(t, app, "")
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows[0].ID+"/match", map[string]any{"match_id": rows[1].ID})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = authed(t, app, http.MethodPost, "/api/v1/inbox/restore", map[string]any{"row_ids": []string{rows[0].ID}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, getInbox(t, app, ""), 1)
	remaining := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, remaining.Transactions, 1)
	require.True(t, remaining.Transactions[0].Transfer.Unmatched)
}

func TestRestoreInboxRejectsAnotherUsersRows(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	otherUserID := data.NewPrivateID()
	require.NoError(t, app.d.CreateUser(context.Background(), data.User{
		ID: otherUserID, Subject: "restore-other", Issuer: "test", Email: "restore-other@example.com", CreatedAt: time.Now(),
	}))
	assetID := data.NewPrivateID()
	categoryID := data.NewPrivateID()
	require.NoError(t, app.d.CreateBucket(context.Background(), data.Bucket{
		ID: assetID, OwnerUserID: otherUserID, Kind: data.KindAsset, Name: "Other asset", CreatedAt: time.Now(),
	}))
	require.NoError(t, app.d.CreateBucket(context.Background(), data.Bucket{
		ID: categoryID, OwnerUserID: otherUserID, Kind: data.KindExpense, Name: "Other expense", CreatedAt: time.Now(),
	}))
	batchID := data.NewPrivateID()
	rowID := data.NewPrivateID()
	_, err := app.d.Users.Exec(`insert into import_batches
		(id, user_id, bucket_id, source, filename, created_at, status)
		values ($1, $2, $3, 'csv', 'other.csv', now(), 'done')`, batchID, otherUserID, assetID)
	require.NoError(t, err)
	_, err = app.d.Users.Exec(`insert into import_rows
		(id, batch_id, occurred_on, amount, currency, counterparty, note, dedup_hash, status)
		values ($1, $2, '2026-07-01', -500, 'EUR', 'Other row', '', $3, 'pending')`, rowID, batchID, rowID)
	require.NoError(t, err)
	categorized, err := app.d.CategorizeInboxRows(context.Background(), otherUserID, []string{rowID}, categoryID)
	require.NoError(t, err)
	require.Equal(t, 1, categorized)

	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/restore", map[string]any{"row_ids": []string{rowID}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var status, transactionID string
	require.NoError(t, app.d.Users.QueryRow(`select r.status,p.transaction_id from import_rows r join postings p on p.import_row_id=r.id where r.id=$1`, rowID).Scan(&status, &transactionID))
	require.Equal(t, "categorized", status)
	require.NotEmpty(t, transactionID)
}

func TestInboxCategorizeCreatesBucket(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-12,34", "K-Market", "Groceries"))

	rows := getInbox(t, app, "")
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/categorize", map[string]any{
		"row_ids": []string{rows[0].ID},
		"bucket":  map[string]any{"kind": "expense", "name": "Groceries"},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Categorized int            `json:"categorized"`
		Bucket      bucketResponse `json:"bucket"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, 1, out.Categorized)
	require.Equal(t, data.KindExpense, out.Bucket.Kind)
	require.Equal(t, "Groceries", out.Bucket.Name)

	balances := postingTotals(t, app)
	require.Equal(t, int64(-1234), balances[bank]["EUR"])
	require.Equal(t, int64(1234), balances[out.Bucket.ID]["EUR"])
}

func TestInboxCategorizeDoesNotCreateBucketWithoutRows(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/categorize", map[string]any{
		"row_ids": []string{data.NewPrivateID()},
		"bucket":  map[string]any{"kind": "expense", "name": "Groceries"},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Categorized int             `json:"categorized"`
		Bucket      *bucketResponse `json:"bucket"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Zero(t, out.Categorized)
	require.Nil(t, out.Bucket)

	buckets := decodeBuckets(t, authed(t, app, http.MethodGet, "/api/v1/buckets", nil))
	require.Empty(t, buckets)
}

func TestInboxCategorizeRejectsInvalidTarget(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	for _, body := range []map[string]any{
		{"row_ids": []string{data.NewPrivateID()}},
		{"row_ids": []string{data.NewPrivateID()}, "bucket_id": data.NewPrivateID(), "bucket": map[string]any{"kind": "expense", "name": "Food"}},
		{"row_ids": []string{data.NewPrivateID()}, "bucket": map[string]any{"kind": "asset", "name": "Bank"}},
	} {
		resp := authed(t, app, http.MethodPost, "/api/v1/inbox/categorize", body)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
}

func TestInboxCategorizeToPerson(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	bob := createBucket(t, app, "person", "Bob")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-50,00", "Restaurant", "Dinner"))

	rows := getInbox(t, app, "")
	require.Len(t, rows, 1)
	require.Equal(t, 1, categorizeInbox(t, app, []string{rows[0].ID}, bob))

	balances := postingTotals(t, app)
	require.Equal(t, int64(-5000), balances[bank]["EUR"])
	require.Equal(t, int64(5000), balances[bob]["EUR"])
}

func TestInboxCategorizeRejectsNonCategory(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	doImport(t, app, bank, nordeaHeader+nordeaRow("2026/07/01", "-5,00", "Cafe", "Coffee"))

	rows := getInbox(t, app, "")
	require.Len(t, rows, 1)

	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/categorize", map[string]any{
		"row_ids":   []string{rows[0].ID},
		"bucket_id": bank,
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestInboxMatchesReturnsSourceWithoutCandidates(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-5,00", "Only row", ""))
	source := getInbox(t, app, "")[0]

	resp := authed(t, app, http.MethodGet, "/api/v1/inbox/"+source.ID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var suggestions struct {
		Source  inboxRow   `json:"source"`
		Matches []inboxRow `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Equal(t, source.ID, suggestions.Source.ID)
	require.Empty(t, suggestions.Matches)
}

func TestMatchInboxTransfer(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-500,00", "Transfer", "Savings"))
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/02", "500,00", "Transfer", "Checking"))

	rows := getInbox(t, app, "")
	require.Len(t, rows, 2)
	var sourceID, matchID string
	for _, row := range rows {
		if row.Amount < 0 {
			sourceID = row.ID
		} else {
			matchID = row.ID
		}
	}

	resp := authed(t, app, http.MethodGet, "/api/v1/inbox/"+sourceID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var suggestions struct {
		Matches []inboxRow `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Len(t, suggestions.Matches, 1)
	require.Equal(t, matchID, suggestions.Matches[0].ID)

	resp = authed(t, app, http.MethodGet, "/api/v1/inbox/"+matchID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Len(t, suggestions.Matches, 1)
	require.Equal(t, sourceID, suggestions.Matches[0].ID)

	resp = authed(t, app, http.MethodPost, "/api/v1/inbox/"+sourceID+"/match", map[string]any{"match_id": matchID})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, getInbox(t, app, ""))

	var transactionAudits, postingAudits, movementAudits, rowAudits int
	require.NoError(t, app.d.Users.QueryRow(`
		with transaction_ids as (
			select transaction_id
			from postings
			where import_row_id = any($1::uuid[])
		)
		select
			count(*) filter (
				where table_name = 'transactions'
				  and row_id in (select transaction_id from transaction_ids)
			),
			count(*) filter (
				where table_name = 'postings'
				  and row_id in (
					select id from postings
					where transaction_id in (select transaction_id from transaction_ids)
				  )
			),
			count(*) filter (where table_name = 'account_movement_matches'),
			count(*) filter (
				where table_name = 'import_rows'
				  and row_id = any($1::uuid[])
			)
		from audit_logs
	`, []string{sourceID, matchID}).Scan(&transactionAudits, &postingAudits, &movementAudits, &rowAudits))
	require.Equal(t, 2, transactionAudits)
	require.Equal(t, 4, postingAudits)
	require.Equal(t, 1, movementAudits)
	require.Equal(t, 2, rowAudits)

	page := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, page.Transactions, 2)
	require.Nil(t, page.Transactions[0].OccurredAt)
	require.Nil(t, page.Transactions[1].OccurredAt)
	require.Equal(t, map[string]bool{"2026-07-01": true, "2026-07-02": true}, map[string]bool{
		page.Transactions[0].OccurredOn: true, page.Transactions[1].OccurredOn: true,
	})
	for _, txn := range page.Transactions {
		require.Len(t, txn.Postings, 1)
		require.NotNil(t, txn.Transfer)
		require.NotEmpty(t, txn.Transfer.MatchID)
		require.NotEmpty(t, txn.Transfer.CounterpartID)
		require.NotNil(t, txn.Transfer.CounterpartBucket)
		require.NotEqual(t, txn.Postings[0].Bucket.ID, txn.Transfer.CounterpartBucket.ID)
		require.Equal(t, -txn.Postings[0].Amount, txn.Transfer.CounterpartAmount)
		require.Equal(t, txn.Postings[0].Currency, txn.Transfer.CounterpartCurrency)
	}
	detail := getTransaction(t, app, page.Transactions[0].ID)
	require.Equal(t, page.Transactions[0].Transfer, detail.Transfer)
	require.Equal(t, page.Transactions[0].Postings, detail.Postings)
	transit := systemBucketID(t, app, "transit")
	midpoint := postingTotalsAt(t, app, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	require.Equal(t, int64(-50000), midpoint[checking]["EUR"])
	require.Equal(t, int64(50000), midpoint[transit]["EUR"])
	require.Zero(t, midpoint[savings]["EUR"])

	balances := postingTotals(t, app)
	require.Equal(t, int64(-50000), balances[checking]["EUR"])
	require.Equal(t, int64(50000), balances[savings]["EUR"])
	require.Zero(t, balances[transit]["EUR"])
}

func TestMatchInboxTransferIncomingFirst(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/01", "500,00", "Arrived", ""))
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/03", "-500,00", "Left", ""))
	rows := getInbox(t, app, "")
	require.Equal(t, http.StatusOK, authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows[0].ID+"/match", map[string]any{"match_id": rows[1].ID}).StatusCode)

	transit := systemBucketID(t, app, "transit")
	midpoint := postingTotalsAt(t, app, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	require.Equal(t, int64(50000), midpoint[savings]["EUR"])
	require.Equal(t, int64(-50000), midpoint[transit]["EUR"])
	require.Zero(t, midpoint[checking]["EUR"])
	require.Zero(t, postingTotals(t, app)[transit]["EUR"])
}

func TestTransactionListCollapsesSameDayMovement(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-500,00", "Transfer", "Savings"))
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/01", "500,00", "Transfer", "Checking"))
	rows := getInbox(t, app, "")
	require.Equal(t, http.StatusOK, authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows[0].ID+"/match", map[string]any{"match_id": rows[1].ID}).StatusCode)

	page := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, page.Transactions, 1)
	require.Equal(t, "outgoing", page.Transactions[0].Transfer.Side)
	require.Less(t, page.Transactions[0].Postings[0].Amount, int64(0))
}

func TestRestoreMatchedInboxRowsRestoresBothRows(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-500,00", "Transfer", "Savings"))
	doImport(t, app, savings, nordeaHeader+nordeaRow("2026/07/02", "500,00", "Transfer", "Checking"))
	rows := getInbox(t, app, "")
	require.Equal(t, http.StatusOK, authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows[0].ID+"/match", map[string]any{"match_id": rows[1].ID}).StatusCode)

	page := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, page.Transactions, 2)
	matchID := page.Transactions[0].Transfer.MatchID
	require.NotEmpty(t, matchID)

	transitPostingID := ""
	require.NoError(t, app.d.Users.QueryRow(`select p.id from postings p join buckets b on b.id=p.bucket_id where p.transaction_id=$1 and b.kind='transit'`, page.Transactions[0].ID).Scan(&transitPostingID))
	resp := authed(t, app, http.MethodPatch, "/api/v1/postings/"+transitPostingID, map[string]any{"memo": "no"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp = authed(t, app, http.MethodPut, "/api/v1/transactions/"+page.Transactions[0].ID+"/postings", map[string]any{"expected_latest_posting_timestamp": page.Transactions[0].LatestPostingTimestamp, "postings": []any{}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = authed(t, app, http.MethodDelete, "/api/v1/transfer-matches/"+matchID, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]int
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, 2, out["restored"])
	require.Len(t, getInbox(t, app, ""), 2)
	require.Empty(t, decodeTxns(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil)))

	var audits int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from audit_logs where table_name='account_movement_matches' and row_id=$1 and operation='delete'", matchID).Scan(&audits))
	require.Equal(t, 1, audits)
}

func TestMatchInboxExchange(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2026/07/01", "-100,00", "Sold EUR", "Exchange")+
		nordeaRowCurrency("2026/07/01", "430,00", "Bought PLN", "Exchange", "PLN"))

	rows := inboxByParty(getInbox(t, app, ""))
	source, match := rows["Sold EUR"], rows["Bought PLN"]
	resp := authed(t, app, http.MethodGet, "/api/v1/inbox/"+source.ID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var suggestions struct {
		Matches []inboxRow `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Len(t, suggestions.Matches, 1)
	require.Equal(t, match.ID, suggestions.Matches[0].ID)
	require.Equal(t, "exchange", suggestions.Matches[0].Kind)

	resp = authed(t, app, http.MethodPost, "/api/v1/inbox/"+source.ID+"/match", map[string]any{"match_id": match.ID})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, getInbox(t, app, ""))
	page := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
	require.Len(t, page.Transactions, 1)
	require.Equal(t, "outgoing", page.Transactions[0].Transfer.Side)

	counterpartResponse := authed(t, app, http.MethodGet, "/api/v1/transactions/"+page.Transactions[0].Transfer.CounterpartID, nil)
	require.Equal(t, http.StatusOK, counterpartResponse.StatusCode)
	var counterpart transactionResponse
	require.NoError(t, json.NewDecoder(counterpartResponse.Body).Decode(&counterpart))
	for _, txn := range []transactionResponse{page.Transactions[0], counterpart} {
		require.Equal(t, "2026-07-01", txn.OccurredOn)
		require.Nil(t, txn.OccurredAt)
		require.Len(t, txn.Postings, 1)
		require.NotNil(t, txn.Transfer)
		require.NotNil(t, txn.Transfer.CounterpartBucket)
		require.Equal(t, bank, txn.Transfer.CounterpartBucket.ID)
		require.Equal(t, map[string]string{"EUR": "PLN", "PLN": "EUR"}[txn.Postings[0].Currency], txn.Transfer.CounterpartCurrency)
		require.Equal(t, map[string]int64{"EUR": 43000, "PLN": -10000}[txn.Postings[0].Currency], txn.Transfer.CounterpartAmount)
	}
	require.NotEqual(t, page.Transactions[0].Postings[0].Currency, page.Transactions[0].Transfer.CounterpartCurrency)

	var sourceTransactionID string
	require.NoError(t, app.d.Users.QueryRow("select transaction_id from postings where import_row_id=$1", source.ID).Scan(&sourceTransactionID))
	var sourceTransit, sourceFX int
	require.NoError(t, app.d.Users.QueryRow(`select count(*) filter(where b.kind='transit'),count(*) filter(where b.kind='fx_conversion')
		from postings p join buckets b on b.id=p.bucket_id where p.transaction_id=$1`, sourceTransactionID).Scan(&sourceTransit, &sourceFX))
	require.Equal(t, 1, sourceTransit)
	require.Zero(t, sourceFX)

	balances := postingTotals(t, app)
	fx := systemBucketID(t, app, "fx_conversion")
	transit := systemBucketID(t, app, "transit")
	require.Equal(t, int64(-10000), balances[bank]["EUR"])
	require.Equal(t, int64(43000), balances[bank]["PLN"])
	require.Equal(t, int64(10000), balances[fx]["EUR"])
	require.Equal(t, int64(-43000), balances[fx]["PLN"])
	require.Zero(t, balances[transit]["EUR"])
	require.Zero(t, balances[transit]["PLN"])
}

func TestMatchInboxDelayedExchangeBothOrders(t *testing.T) {
	for _, tt := range []struct {
		name, soldDate, boughtDate string
		incomingFirst              bool
	}{
		{name: "outgoing first", soldDate: "2026/07/01", boughtDate: "2026/07/03"},
		{name: "incoming first", soldDate: "2026/07/03", boughtDate: "2026/07/01", incomingFirst: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
			bank := createBucket(t, app, "asset", "Bank")
			doImport(t, app, bank, nordeaHeader+
				nordeaRow(tt.soldDate, "-100,00", "Sold EUR", "Exchange")+
				nordeaRowCurrency(tt.boughtDate, "430,00", "Bought PLN", "Exchange", "PLN"))
			rows := inboxByParty(getInbox(t, app, ""))
			resp := authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows["Sold EUR"].ID+"/match", map[string]any{"match_id": rows["Bought PLN"].ID})
			require.Equal(t, http.StatusOK, resp.StatusCode)

			transit := systemBucketID(t, app, "transit")
			fx := systemBucketID(t, app, "fx_conversion")
			midpoint := postingTotalsAt(t, app, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
			if tt.incomingFirst {
				require.Equal(t, int64(43000), midpoint[bank]["PLN"])
				require.Equal(t, int64(-43000), midpoint[transit]["PLN"])
			} else {
				require.Equal(t, int64(-10000), midpoint[bank]["EUR"])
				require.Equal(t, int64(10000), midpoint[transit]["EUR"])
			}
			require.Zero(t, midpoint[fx]["EUR"])
			require.Zero(t, midpoint[fx]["PLN"])

			balances := postingTotals(t, app)
			require.Zero(t, balances[transit]["EUR"])
			require.Zero(t, balances[transit]["PLN"])
			require.Equal(t, int64(10000), balances[fx]["EUR"])
			require.Equal(t, int64(-43000), balances[fx]["PLN"])
			for _, currency := range []string{"EUR", "PLN"} {
				var total int64
				for _, amounts := range balances {
					total += amounts[currency]
				}
				require.Zero(t, total, currency)
			}
		})
	}
}

func TestRemoveEitherExchangeSideLeavesBalancedUnmatchedSide(t *testing.T) {
	for _, removedParty := range []string{"Sold EUR", "Bought PLN"} {
		t.Run(removedParty, func(t *testing.T) {
			app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
			bank := createBucket(t, app, "asset", "Bank")
			doImport(t, app, bank, nordeaHeader+
				nordeaRow("2026/07/01", "-100,00", "Sold EUR", "Exchange")+
				nordeaRowCurrency("2026/07/03", "430,00", "Bought PLN", "Exchange", "PLN"))
			rows := inboxByParty(getInbox(t, app, ""))
			require.Equal(t, http.StatusOK, authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows["Sold EUR"].ID+"/match", map[string]any{"match_id": rows["Bought PLN"].ID}).StatusCode)

			var removedTransactionID string
			require.NoError(t, app.d.Users.QueryRow("select transaction_id from postings where import_row_id=$1", rows[removedParty].ID).Scan(&removedTransactionID))
			resp := authed(t, app, http.MethodDelete, "/api/v1/transactions/"+removedTransactionID, nil)
			require.Equal(t, http.StatusNoContent, resp.StatusCode)

			remaining := decodeTransactionPage(t, authed(t, app, http.MethodGet, "/api/v1/transactions", nil))
			require.Len(t, remaining.Transactions, 1)
			require.NotNil(t, remaining.Transactions[0].Transfer)
			require.True(t, remaining.Transactions[0].Transfer.Unmatched)
			var unbalancedCurrencies int
			require.NoError(t, app.d.Users.QueryRow(`select count(*) from (
				select currency from postings where transaction_id=$1 group by currency having sum(amount)<>0
			) unbalanced`, remaining.Transactions[0].ID).Scan(&unbalancedCurrencies))
			require.Zero(t, unbalancedCurrencies)
			require.Len(t, getInbox(t, app, ""), 1)
		})
	}
}

func TestExchangeMatchesRankByValueUsingCurrencyExponent(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	_, err := app.d.Users.Exec("insert into rates (date, currency, rate) values ('2026-07-01', 'EUR', 1), ('2026-07-01', 'JPY', 200)")
	require.NoError(t, err)
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2026/07/01", "-100,00", "Sold EUR", "Exchange")+
		nordeaRowCurrency("2026/07/01", "200", "Wrong JPY", "Exchange", "JPY")+
		nordeaRowCurrency("2026/07/01", "20000", "Bought JPY", "Exchange", "JPY"))

	rows := inboxByParty(getInbox(t, app, ""))
	resp := authed(t, app, http.MethodGet, "/api/v1/inbox/"+rows["Sold EUR"].ID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var suggestions struct {
		Matches []inboxRow `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Len(t, suggestions.Matches, 2)
	require.Equal(t, rows["Bought JPY"].ID, suggestions.Matches[0].ID)
}

func TestExchangeMatchesPutMissingRatesLast(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	_, err := app.d.Users.Exec("insert into rates (date, currency, rate) values ('2026-07-01', 'EUR', 1), ('2026-07-01', 'JPY', 200)")
	require.NoError(t, err)
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2026/07/01", "-100,00", "Sold EUR", "Exchange")+
		nordeaRowCurrency("2026/07/03", "20000", "Bought JPY", "Exchange", "JPY")+
		nordeaRowCurrency("2026/07/01", "430,00", "Bought PLN", "Exchange", "PLN"))

	rows := inboxByParty(getInbox(t, app, ""))
	resp := authed(t, app, http.MethodGet, "/api/v1/inbox/"+rows["Sold EUR"].ID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var suggestions struct {
		Matches []inboxRow `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Len(t, suggestions.Matches, 2)
	require.Equal(t, rows["Bought JPY"].ID, suggestions.Matches[0].ID)
	require.Equal(t, rows["Bought PLN"].ID, suggestions.Matches[1].ID)
}

func TestMatchInboxRejectsInvalidPairsWithoutWriting(t *testing.T) {
	tests := []struct {
		name           string
		firstDate      string
		firstAmount    string
		firstCurrency  string
		secondDate     string
		secondAmount   string
		secondCurrency string
		sameAccount    bool
	}{
		{"same-sign exchange", "2026/07/01", "-5,00", "EUR", "2026/07/01", "-4,00", "PLN", false},
		{"unequal transfer", "2026/07/01", "-5,00", "EUR", "2026/07/01", "4,00", "EUR", false},
		{"same-account transfer", "2026/07/01", "-5,00", "EUR", "2026/07/01", "5,00", "EUR", true},
		{"too far apart", "2026/07/01", "-5,00", "EUR", "2026/07/09", "5,00", "EUR", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
			firstAccount := createBucket(t, app, "asset", "First")
			secondAccount := firstAccount
			if !tt.sameAccount {
				secondAccount = createBucket(t, app, "asset", "Second")
			}
			doImport(t, app, firstAccount, nordeaHeader+nordeaRowCurrency(tt.firstDate, tt.firstAmount, "First row", "", tt.firstCurrency))
			doImport(t, app, secondAccount, nordeaHeader+nordeaRowCurrency(tt.secondDate, tt.secondAmount, "Second row", "", tt.secondCurrency))
			rows := inboxByParty(getInbox(t, app, ""))

			resp := authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows["First row"].ID+"/match", map[string]any{"match_id": rows["Second row"].ID})
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Len(t, getInbox(t, app, ""), 2)

			resp = authed(t, app, http.MethodGet, "/api/v1/transactions", nil)
			require.Empty(t, decodeTxns(t, resp))
		})
	}
}

func TestMatchInboxUsesImportCalendarDates(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/10/19", "-5,00", "Source", ""))
	doImport(t, app, savings, nordeaHeader+
		nordeaRow("2026/10/26", "5,00", "Seven days", "")+
		nordeaRow("2026/10/27", "5,00", "Eight days", ""))

	rows := inboxByParty(getInbox(t, app, ""))
	resp := authed(t, app, http.MethodGet, "/api/v1/inbox/"+rows["Source"].ID+"/matches", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var suggestions struct {
		Matches []inboxRow `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&suggestions))
	require.Len(t, suggestions.Matches, 1)
	require.Equal(t, rows["Seven days"].ID, suggestions.Matches[0].ID)

	resp = authed(t, app, http.MethodPost, "/api/v1/inbox/"+rows["Source"].ID+"/match", map[string]any{"match_id": rows["Seven days"].ID})
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMatchInboxRejectsAnotherUsersRow(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	ownAccount := createBucket(t, app, "asset", "Own")
	doImport(t, app, ownAccount, nordeaHeader+nordeaRow("2026/07/01", "-5,00", "Own row", ""))
	ownRow := getInbox(t, app, "")[0]

	otherUserID := data.NewPrivateID()
	require.NoError(t, app.d.CreateUser(context.Background(), data.User{
		ID: otherUserID, Subject: "other", Issuer: "test", Email: "other@example.com", CreatedAt: time.Now(),
	}))
	otherAccountID := data.NewPrivateID()
	require.NoError(t, app.d.CreateBucket(context.Background(), data.Bucket{
		ID: otherAccountID, OwnerUserID: otherUserID, Kind: data.KindAsset, Name: "Other", CreatedAt: time.Now(),
	}))
	otherBatchID := data.NewPrivateID()
	otherRowID := data.NewPrivateID()
	_, err := app.d.Users.Exec(`insert into import_batches
		(id, user_id, bucket_id, source, filename, created_at, status)
		values ($1, $2, $3, 'csv', 'other.csv', now(), 'done')`,
		otherBatchID, otherUserID, otherAccountID)
	require.NoError(t, err)
	_, err = app.d.Users.Exec(`insert into import_rows
		(id, batch_id, occurred_on, amount, currency, counterparty, note, dedup_hash, status)
		values ($1, $2, '2026-07-01', 500, 'EUR', 'Other row', '', $3, 'pending')`,
		otherRowID, otherBatchID, otherRowID)
	require.NoError(t, err)

	resp := authed(t, app, http.MethodPost, "/api/v1/inbox/"+ownRow.ID+"/match", map[string]any{"match_id": otherRowID})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Len(t, getInbox(t, app, ""), 1)

	var otherStatus string
	require.NoError(t, app.d.Users.QueryRow("select status from import_rows where id = $1", otherRowID).Scan(&otherStatus))
	require.Equal(t, "pending", otherStatus)
}

func TestConcurrentInboxMatchesOnlyUseSourceOnce(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	checking := createBucket(t, app, "asset", "Checking")
	savings := createBucket(t, app, "asset", "Savings")
	doImport(t, app, checking, nordeaHeader+nordeaRow("2026/07/01", "-5,00", "Source", ""))
	doImport(t, app, savings, nordeaHeader+
		nordeaRow("2026/07/01", "5,00", "Candidate one", "")+
		nordeaRow("2026/07/01", "5,00", "Candidate two", ""))
	rows := inboxByParty(getInbox(t, app, ""))

	var userID string
	require.NoError(t, app.d.Users.QueryRow("select id from users limit 1").Scan(&userID))
	results := make(chan error, 2)
	go func() {
		results <- app.d.MatchInboxRows(context.Background(), userID, rows["Source"].ID, rows["Candidate one"].ID)
	}()
	go func() {
		results <- app.d.MatchInboxRows(context.Background(), userID, rows["Source"].ID, rows["Candidate two"].ID)
	}()

	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else {
			require.True(t, errors.Is(err, data.ErrInvalidPostings), err)
		}
	}
	require.Equal(t, 1, successes)
	require.Len(t, getInbox(t, app, ""), 1)
	resp := authed(t, app, http.MethodGet, "/api/v1/transactions", nil)
	require.Len(t, decodeTxns(t, resp), 1)
}

func TestInboxSearch(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bank := createBucket(t, app, "asset", "Bank")
	doImport(t, app, bank, nordeaHeader+
		nordeaRow("2026/07/01", "-1,00", "K-Market Kamppi", "shop")+
		nordeaRow("2026/07/02", "-2,00", "Shell", "fuel"))

	rows := getInbox(t, app, "?q=market")
	require.Len(t, rows, 1)
	require.Equal(t, "K-Market Kamppi", rows[0].Counterparty)
}
