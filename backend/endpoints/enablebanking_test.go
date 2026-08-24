package endpoints

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"money/backend/data"
	"money/backend/enablebanking"

	"github.com/stretchr/testify/require"
)

func TestEnableBankingRequiresAllowedOIDCSubject(t *testing.T) {
	denied := newTestAppWith(t, appOpts{frontURL: testFrontURL, denyEnableBanking: true})
	denied.state.Config.EnableBanking.ApplicationID = "app"
	denied.state.Config.EnableBanking.PrivateKeyPath = "key.pem"
	resp := authed(t, denied, http.MethodGet, "/api/v1/enablebanking/connections", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp = authed(t, denied, http.MethodGet, "/api/v1/users/@me", nil)
	var deniedUser struct {
		Available bool `json:"enable_banking_available"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&deniedUser))
	require.False(t, deniedUser.Available)

	allowed := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	allowed.state.Config.EnableBanking.ApplicationID = "app"
	allowed.state.Config.EnableBanking.PrivateKeyPath = "key.pem"
	resp = authed(t, allowed, http.MethodGet, "/api/v1/enablebanking/connections", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = authed(t, allowed, http.MethodGet, "/api/v1/users/@me", nil)
	var allowedUser struct {
		Available bool `json:"enable_banking_available"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&allowedUser))
	require.True(t, allowedUser.Available)
}

func TestListEnableBankingConnectionsMapsBucketsByIBAN(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	bucketID := createBucket(t, app, "asset", "Bank")
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select owner_user_id from buckets where id=$1", bucketID).Scan(&userID))
	integrationID := data.NewPrivateID()
	iban := "FI2112345600000785"
	saved, err := json.Marshal(enableBankingData{
		SessionID: "session", Bank: "Test bank", Country: "FI", PSUType: "personal", ExpiresAt: time.Now().Add(time.Hour),
		Accounts: []enableBankingAccount{{UID: "account", IdentificationHash: "account-hash", IBAN: iban, Name: "Main", Currency: "EUR"}},
	})
	require.NoError(t, err)
	require.NoError(t, app.d.CreateBankIntegration(t.Context(), data.BankIntegration{ID: integrationID, UserID: userID, Provider: enableBankingProvider, Data: saved}, nil))
	require.NoError(t, app.d.MapIntegrationAccount(t.Context(), userID, integrationID, bucketID, iban))

	response := authed(t, app, http.MethodGet, "/api/v1/enablebanking/connections", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var connections []struct {
		ID       string `json:"id"`
		Accounts []struct {
			BucketID string `json:"bucket_id"`
		} `json:"accounts"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&connections))
	require.Len(t, connections, 1)
	require.Equal(t, integrationID, connections[0].ID)
	require.Equal(t, bucketID, connections[0].Accounts[0].BucketID)

	newBucketID := createBucket(t, app, "asset", "New bank")
	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/accounts/account/map", map[string]string{"bucket_id": newBucketID})
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	var oldIBAN, newIBAN *string
	require.NoError(t, app.d.Users.QueryRow("select iban from buckets where id=$1", bucketID).Scan(&oldIBAN))
	require.NoError(t, app.d.Users.QueryRow("select iban from buckets where id=$1", newBucketID).Scan(&newIBAN))
	require.Nil(t, oldIBAN)
	require.Equal(t, iban, *newIBAN)

	response = authed(t, app, http.MethodGet, "/api/v1/enablebanking/connections", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	connections = nil
	require.NoError(t, json.NewDecoder(response.Body).Decode(&connections))
	require.Equal(t, newBucketID, connections[0].Accounts[0].BucketID)
}

func TestEnableBankingAuthorizationCreatesConnection(t *testing.T) {
	var stateValue string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aspsps":
			_, _ = w.Write([]byte(`{"aspsps":[{"name":"Test bank","country":"FI","maximum_consent_validity":3600}]}`))
		case "/auth":
			var body struct {
				State string `json:"state"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			stateValue = body.State
			_, _ = w.Write([]byte(`{"url":"https://bank.test/authorize"}`))
		case "/sessions":
			_, _ = w.Write([]byte(`{"session_id":"session","accounts":[{"uid":"account","identification_hash":"account-hash","account_id":{"iban":"FI2112345600000785"},"name":"Main","currency":"EUR"}],"aspsp":{"name":"Test bank","country":"FI"},"psu_type":"personal","access":{"valid_until":"2026-08-01T00:00:00Z"}}`))
		default:
			t.Fatalf("unexpected Enable Banking path %s", r.URL.Path)
		}
	}))
	defer provider.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := t.TempDir() + "/key.pem"
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))

	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	app.state.EnableBankingClient, err = enablebanking.New("app", keyPath, provider.URL)
	require.NoError(t, err)
	loginCookie := login(t, app)
	request, err := http.NewRequest(http.MethodGet, app.url+"/api/v1/enablebanking/connect?country=FI&bank=Test+bank&psu_type=personal", nil)
	require.NoError(t, err)
	request.AddCookie(loginCookie)
	response, err := app.client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusSeeOther, response.StatusCode)
	require.Equal(t, "https://bank.test/authorize", response.Header.Get("Location"))
	require.NotEmpty(t, stateValue)

	callback, err := http.NewRequest(http.MethodGet, app.url+"/api/v1/enablebanking/callback?state="+stateValue+"&code=code", nil)
	require.NoError(t, err)
	callback.AddCookie(loginCookie)
	for _, cookie := range response.Cookies() {
		callback.AddCookie(cookie)
	}
	callbackResponse, err := app.client.Do(callback)
	require.NoError(t, err)
	require.Equal(t, http.StatusSeeOther, callbackResponse.StatusCode)
	require.Equal(t, "/connections", callbackResponse.Header.Get("Location"))

	var count int
	var identificationHash string
	require.NoError(t, app.d.Users.QueryRow(`
		select count(*), min(data #>> '{accounts,0,identification_hash}')
		from bank_integrations
		where provider = 'enable_banking'
	`).Scan(&count, &identificationHash))
	require.Equal(t, 1, count)
	require.Equal(t, "account-hash", identificationHash)
}

func newEnableBankingSyncTest(t *testing.T, providerURL string) (*testApp, *enablebanking.Syncer, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := t.TempDir() + "/key.pem"
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))

	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	client, err := enablebanking.New("app", keyPath, providerURL)
	require.NoError(t, err)
	syncer := enablebanking.NewSyncer(app.d, client)
	app.state.EnableBankingClient = client
	app.state.EnableBankingSyncer = syncer

	bucketID := createBucket(t, app, "asset", "Bank")
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select owner_user_id from buckets where id=$1", bucketID).Scan(&userID))
	integrationID := data.NewPrivateID()
	iban := "FI2112345600000785"
	saved, err := json.Marshal(enableBankingData{
		SessionID: "session", Bank: "Test", Country: "FI", PSUType: "personal",
		Accounts: []enableBankingAccount{{UID: "account", IdentificationHash: "account-hash", IBAN: iban, Currency: "EUR"}},
	})
	require.NoError(t, err)
	require.NoError(t, app.d.CreateBankIntegration(t.Context(), data.BankIntegration{
		ID: integrationID, UserID: userID, Provider: enableBankingProvider, Data: saved,
	}, nil))
	require.NoError(t, app.d.MapIntegrationAccount(t.Context(), userID, integrationID, bucketID, iban))
	return app, syncer, integrationID, bucketID
}

func enqueueEnableBankingSync(t *testing.T, app *testApp, integrationID string) string {
	t.Helper()
	return enqueueEnableBankingAccountSync(t, app, integrationID, "account")
}

func enqueueEnableBankingAccountSync(t *testing.T, app *testApp, integrationID, accountUID string) string {
	t.Helper()
	response := authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/accounts/"+accountUID+"/sync", nil)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var result importResult
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return result.BatchID
}

func enableBankingSyncing(t *testing.T, app *testApp) bool {
	t.Helper()
	response := authed(t, app, http.MethodGet, "/api/v1/enablebanking/sync-status", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var result struct {
		Syncing bool `json:"syncing"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return result.Syncing
}

func TestEnableBankingSyncStatusTracksQueuedBatches(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	app, _, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)

	require.False(t, enableBankingSyncing(t, app))
	batchID := enqueueEnableBankingSync(t, app, integrationID)
	require.True(t, enableBankingSyncing(t, app))
	_, err := app.d.Users.Exec("update import_batches set status='done' where id=$1", batchID)
	require.NoError(t, err)
	require.False(t, enableBankingSyncing(t, app))
}

func waitFailedImport(t *testing.T, app *testApp, batchID string) batchReport {
	t.Helper()
	var report batchReport
	require.Eventually(t, func() bool {
		report = getBatch(t, app, batchID)
		return report.Status == "failed"
	}, 7*time.Second, 15*time.Millisecond, "bank sync never reached failed")
	return report
}

func TestSyncEnableBankingTracksRevolutWalletsSeparately(t *testing.T) {
	var requests atomic.Int32
	wallets := []enableBankingAccount{
		{UID: "eur-account", IdentificationHash: "eur-hash", IBAN: "FI2112345600000785", Currency: "EUR"},
		{UID: "pln-account", IdentificationHash: "pln-hash", IBAN: "FI2112345600000785", Currency: "PLN"},
		{UID: "aed-account", IdentificationHash: "aed-hash", IBAN: "FI2112345600000785", Currency: "AED"},
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := requests.Add(1)
		require.Equal(t, "longest", r.URL.Query().Get("strategy"))
		require.NotEmpty(t, r.URL.Query().Get("date_from"))
		require.Empty(t, r.URL.Query().Get("date_to"))
		if requestNumber == 4 {
			require.Equal(t, "/accounts/eur-account/transactions", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":""}`))
	}))
	defer provider.Close()

	app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select user_id from bank_integrations where id=$1", integrationID).Scan(&userID))
	saved, err := json.Marshal(enableBankingData{
		SessionID: "session", Bank: "Revolut", Country: "FI", PSUType: "personal", Accounts: wallets,
	})
	require.NoError(t, err)
	require.NoError(t, app.d.UpdateBankIntegration(t.Context(), userID, integrationID, enableBankingProvider, saved))

	invalidSelection := map[string]any{"accounts": []map[string]string{
		{"connection_id": integrationID, "account_uid": wallets[0].UID},
		{"connection_id": integrationID, "account_uid": "missing"},
	}}
	response := authed(t, app, http.MethodPost, "/api/v1/enablebanking/sync", invalidSelection)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	var jobs int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from enable_banking_syncs where integration_id=$1", integrationID).Scan(&jobs))
	require.Zero(t, jobs)

	duplicateSelection := map[string]any{"accounts": []map[string]string{
		{"connection_id": integrationID, "account_uid": wallets[0].UID},
		{"connection_id": integrationID, "account_uid": wallets[0].UID},
	}}
	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/sync", duplicateSelection)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.NoError(t, app.d.Users.QueryRow("select count(*) from enable_banking_syncs where integration_id=$1", integrationID).Scan(&jobs))
	require.Zero(t, jobs)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.Start(ctx)
	selection := map[string]any{"accounts": []map[string]string{
		{"connection_id": integrationID, "account_uid": wallets[0].UID},
		{"connection_id": integrationID, "account_uid": wallets[1].UID},
		{"connection_id": integrationID, "account_uid": wallets[2].UID},
	}}
	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/sync", selection)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var allResult struct {
		BatchIDs []string `json:"batch_ids"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&allResult))
	require.Len(t, allResult.BatchIDs, 3)
	for _, batchID := range allResult.BatchIDs {
		waitImport(t, app, batchID)
	}
	batchID := enqueueEnableBankingAccountSync(t, app, integrationID, wallets[0].UID)
	waitImport(t, app, batchID)
	require.Equal(t, int32(4), requests.Load())
}

func TestSyncSelectedEnableBankingAccountsEnqueuesAtomicallyAcrossConnections(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	app, _, firstIntegrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select user_id from bank_integrations where id=$1", firstIntegrationID).Scan(&userID))

	secondBucketID := createBucket(t, app, "asset", "Second bank")
	secondIntegrationID := data.NewPrivateID()
	secondIBAN := "LT813250066911093214"
	saved, err := json.Marshal(enableBankingData{
		SessionID: "second-session", Bank: "Second", Country: "LT", PSUType: "personal",
		Accounts: []enableBankingAccount{{UID: "second-account", IdentificationHash: "second-hash", IBAN: secondIBAN, Currency: "EUR"}},
	})
	require.NoError(t, err)
	require.NoError(t, app.d.CreateBankIntegration(t.Context(), data.BankIntegration{
		ID: secondIntegrationID, UserID: userID, Provider: enableBankingProvider, Data: saved,
	}, nil))
	require.NoError(t, app.d.MapIntegrationAccount(t.Context(), userID, secondIntegrationID, secondBucketID, secondIBAN))

	selection := map[string]any{"accounts": []map[string]string{
		{"connection_id": firstIntegrationID, "account_uid": "account"},
		{"connection_id": secondIntegrationID, "account_uid": "second-account"},
	}}
	response := authed(t, app, http.MethodPost, "/api/v1/enablebanking/sync", selection)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var result struct {
		BatchIDs []string `json:"batch_ids"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	require.Len(t, result.BatchIDs, 2)
	var jobs int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from enable_banking_syncs where integration_id in ($1, $2)", firstIntegrationID, secondIntegrationID).Scan(&jobs))
	require.Equal(t, 2, jobs)

	_, err = app.d.Users.Exec("delete from import_batches where id=$1", result.BatchIDs[1])
	require.NoError(t, err)
	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/sync", selection)
	require.Equal(t, http.StatusConflict, response.StatusCode)
	require.NoError(t, app.d.Users.QueryRow("select count(*) from enable_banking_syncs where integration_id=$1", secondIntegrationID).Scan(&jobs))
	require.Zero(t, jobs, "a conflict must roll back every selected account")
}

func TestSyncEnableBankingUsesUpdatedAccountUIDAfterReauthorization(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := requests.Add(1)
		require.Equal(t, "longest", r.URL.Query().Get("strategy"))
		require.NotEmpty(t, r.URL.Query().Get("date_from"))
		require.Empty(t, r.URL.Query().Get("date_to"))
		if requestNumber == 1 {
			require.Equal(t, "/accounts/account/transactions", r.URL.Path)
		} else {
			require.Equal(t, "/accounts/new-account/transactions", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":""}`))
	}))
	defer provider.Close()

	app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.Start(ctx)
	waitImport(t, app, enqueueEnableBankingSync(t, app, integrationID))

	var userID string
	require.NoError(t, app.d.Users.QueryRow("select user_id from bank_integrations where id=$1", integrationID).Scan(&userID))
	saved, err := json.Marshal(enableBankingData{
		SessionID: "new-session", Bank: "Test", Country: "FI", PSUType: "personal",
		Accounts: []enableBankingAccount{{
			UID: "new-account", IdentificationHash: "account-hash", IBAN: "FI2112345600000785", Currency: "EUR",
		}},
	})
	require.NoError(t, err)
	require.NoError(t, app.d.UpdateBankIntegration(t.Context(), userID, integrationID, enableBankingProvider, saved))

	waitImport(t, app, enqueueEnableBankingAccountSync(t, app, integrationID, "new-account"))
	require.Equal(t, int32(2), requests.Load())
}

func TestInitialEnableBankingSyncUsesLongestFromTwoYearsAgo(t *testing.T) {
	var requests atomic.Int32
	dateTo := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "longest", r.URL.Query().Get("strategy"))
		require.Equal(t, dateTo.AddDate(-2, 0, 0).Format(time.DateOnly), r.URL.Query().Get("date_from"))
		require.Empty(t, r.URL.Query().Get("date_to"))
		if requests.Add(1) == 1 {
			require.Empty(t, r.URL.Query().Get("continuation_key"))
			_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":"next"}`))
			return
		}
		require.Equal(t, "next", r.URL.Query().Get("continuation_key"))
		_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":""}`))
	}))
	defer provider.Close()

	app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select user_id from bank_integrations where id=$1", integrationID).Scan(&userID))
	batchIDs, err := app.d.EnqueueEnableBankingSyncs(t.Context(), userID, []data.EnableBankingSyncTarget{{
		IntegrationID: integrationID,
		AccountUID:    "account",
	}}, dateTo)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.Start(ctx)
	waitImport(t, app, batchIDs[0])
	require.Equal(t, int32(2), requests.Load())
}

func TestSyncEnableBankingRejectsSecondActiveSync(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	app, _, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	enqueueEnableBankingSync(t, app, integrationID)

	response := authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/accounts/account/sync", nil)
	require.Equal(t, http.StatusConflict, response.StatusCode)
}

func TestSyncEnableBankingRetriesServerErrorFromLastCompletedPage(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			require.Empty(t, r.URL.Query().Get("continuation_key"))
			_, _ = w.Write([]byte(`{"transactions":[{"transaction_amount":{"currency":"EUR","amount":"12.34"},"credit_debit_indicator":"DBIT","booking_date":"2026-07-01","status":"BOOK"}],"continuation_key":"next"}`))
		case 2:
			require.Equal(t, "next", r.URL.Query().Get("continuation_key"))
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"ASPSP_ERROR","message":"temporary bank error"}`))
		case 3:
			require.Equal(t, "next", r.URL.Query().Get("continuation_key"))
			_, _ = w.Write([]byte(`{"transactions":[{"transaction_amount":{"currency":"EUR","amount":"100.00"},"credit_debit_indicator":"CRDT","booking_date":"2026-07-02","status":"BOOK"}],"continuation_key":""}`))
		default:
			require.Empty(t, r.URL.Query().Get("continuation_key"))
			_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":""}`))
		}
	}))
	defer provider.Close()

	app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	batchID := enqueueEnableBankingSync(t, app, integrationID)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.Start(ctx)

	report := waitImport(t, app, batchID)
	require.Equal(t, 2, report.Imported)
	require.Equal(t, int32(3), requests.Load())
}

func TestSyncEnableBankingDoesNotRetryPermanentErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		error  string
	}{
		{name: "bad request", status: http.StatusBadRequest, body: `{"error":"WRONG_REQUEST_PARAMETERS","message":"bad range"}`, error: "bad range"},
		{name: "current period rejected", status: http.StatusUnprocessableEntity, body: `{"error":"WRONG_TRANSACTIONS_PERIOD","message":"wrong period"}`, error: "wrong period"},
		{name: "expired session", status: http.StatusUnauthorized, body: `{"error":"EXPIRED_SESSION","message":"expired"}`, error: "needs re-authentication"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer provider.Close()

			app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
			batchID := enqueueEnableBankingSync(t, app, integrationID)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			syncer.Start(ctx)

			report := waitFailedImport(t, app, batchID)
			require.Contains(t, report.Error, test.error)
			require.Equal(t, int32(1), requests.Load())
		})
	}
}

func TestEnableBankingSyncDateFromIsAlwaysClampedToTwoYears(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	app, _, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select user_id from bank_integrations where id=$1", integrationID).Scan(&userID))
	request := []data.EnableBankingSyncTarget{{
		IntegrationID: integrationID,
		AccountUID:    "account",
	}}
	enqueue := func(dateTo time.Time) string {
		batchIDs, err := app.d.EnqueueEnableBankingSyncs(t.Context(), userID, request, dateTo)
		require.NoError(t, err)
		return batchIDs[0]
	}
	complete := func(batchID string, dateTo time.Time) {
		_, err := app.d.Users.Exec(`
			update enable_banking_syncs
			set date_to = $2, completed_at = now()
			where batch_id = $1
		`, batchID, dateTo)
		require.NoError(t, err)
		_, err = app.d.Users.Exec("update import_batches set status = 'done' where id = $1", batchID)
		require.NoError(t, err)
	}
	dateFrom := func(batchID string) time.Time {
		var date time.Time
		require.NoError(t, app.d.Users.QueryRow(
			"select date_from from enable_banking_syncs where batch_id=$1",
			batchID,
		).Scan(&date))
		return date
	}

	firstDateTo := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	firstBatch := enqueue(firstDateTo)
	require.Equal(t, firstDateTo.AddDate(-2, 0, 0), dateFrom(firstBatch))
	complete(firstBatch, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC))

	clampedBatch := enqueue(firstDateTo)
	require.Equal(t, firstDateTo.AddDate(-2, 0, 0), dateFrom(clampedBatch))
	complete(clampedBatch, firstDateTo)

	laterDateTo := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	incrementalBatch := enqueue(laterDateTo)
	require.Equal(t, firstDateTo.AddDate(0, 0, -2), dateFrom(incrementalBatch))
	complete(incrementalBatch, laterDateTo)

	_, err := app.d.Users.Exec("update bank_integrations set updated_at = now() where id = $1", integrationID)
	require.NoError(t, err)
	postReauthorizationDateTo := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	postReauthorizationBatch := enqueue(postReauthorizationDateTo)
	require.Equal(t, postReauthorizationDateTo.AddDate(-2, 0, 0), dateFrom(postReauthorizationBatch))
	complete(postReauthorizationBatch, postReauthorizationDateTo)

	nextDateTo := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	nextBatch := enqueue(nextDateTo)
	require.Equal(t, postReauthorizationDateTo.AddDate(0, 0, -2), dateFrom(nextBatch))
}

func TestSyncEnableBankingSkipsMalformedRows(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"transactions":[{"transaction_amount":{"currency":"EUR","amount":"12.34"},"credit_debit_indicator":"DBIT","booking_date":"2026-07-01","status":"BOOK"},{"transaction_amount":{"currency":"EUR","amount":"5.00"},"credit_debit_indicator":"NOPE","booking_date":"2026-07-02","status":"BOOK"}],"continuation_key":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":""}`))
	}))
	defer provider.Close()

	app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	batchID := enqueueEnableBankingSync(t, app, integrationID)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.Start(ctx)

	report := waitImport(t, app, batchID)
	require.Equal(t, 1, report.Imported)
	require.Len(t, report.ParseErrors, 1)
	require.Equal(t, 2, report.ParseErrors[0].Line)
	require.Equal(t, "invalid bank transaction direction", report.ParseErrors[0].Error)
}

func TestSyncEnableBankingRejectsRepeatedContinuation(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":"same"}`))
	}))
	defer provider.Close()

	app, syncer, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	batchID := enqueueEnableBankingSync(t, app, integrationID)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.Start(ctx)

	report := waitFailedImport(t, app, batchID)
	require.Contains(t, report.Error, "repeated continuation key")
	require.Equal(t, int32(2), requests.Load())
	var staged, jobs int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from import_staged_rows where batch_id=$1", batchID).Scan(&staged))
	require.NoError(t, app.d.Users.QueryRow("select count(*) from enable_banking_syncs where batch_id=$1", batchID).Scan(&jobs))
	require.Zero(t, staged)
	require.Zero(t, jobs)
}

func TestEnableBankingSyncExpiredLeaseKeepsCompletedPage(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	app, _, integrationID, _ := newEnableBankingSyncTest(t, provider.URL)
	batchID := enqueueEnableBankingSync(t, app, integrationID)

	job, ok, err := app.d.ClaimEnableBankingSync(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	row := data.ParsedRow{OccurredOn: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Amount: -1234, Currency: "EUR"}
	require.NoError(t, app.d.StageEnableBankingPage(t.Context(), job, []data.ParsedRow{row}, nil, "next", false))
	_, err = app.d.Users.Exec("update import_batches set claim_expires_at = now() - interval '1 minute' where id = $1", batchID)
	require.NoError(t, err)

	recovered, ok, err := app.d.ClaimEnableBankingSync(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, batchID, recovered.BatchID)
	require.NotEqual(t, job.ClaimID, recovered.ClaimID)
	require.Equal(t, "next", recovered.ContinuationKey)
	require.ErrorIs(t, app.d.StageEnableBankingPage(t.Context(), job, nil, nil, "", true), data.ErrBankSyncNotFound)
	require.Equal(t, int64(1), recovered.NextSequence)
	var staged int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from import_staged_rows where batch_id=$1", batchID).Scan(&staged))
	require.Equal(t, 1, staged)

	secondRow := data.ParsedRow{OccurredOn: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), Amount: 10000, Currency: "EUR"}
	require.NoError(t, app.d.StageEnableBankingPage(t.Context(), recovered, []data.ParsedRow{secondRow}, nil, "", true))
	added, duplicates, err := app.d.FinalizeEnableBankingSync(t.Context(), recovered)
	require.NoError(t, err)
	require.Equal(t, 2, added)
	require.Zero(t, duplicates)
}

func TestSyncEnableBankingAccountPagesIntoImportWorker(t *testing.T) {
	var page atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/accounts/account/transactions", r.URL.Path)
		require.Equal(t, "BOOK", r.URL.Query().Get("transaction_status"))
		require.Equal(t, "longest", r.URL.Query().Get("strategy"))
		require.Empty(t, r.URL.Query().Get("date_to"))
		page.Add(1)
		require.NotEmpty(t, r.URL.Query().Get("date_from"))
		if r.URL.Query().Get("continuation_key") == "" {
			_, _ = w.Write([]byte(`{"transactions":[{"transaction_amount":{"currency":"EUR","amount":"12.34"},"credit_debit_indicator":"DBIT","booking_date":"2026-07-01","creditor":{"name":"Cafe"},"status":"BOOK"}],"continuation_key":"next"}`))
			return
		}
		if r.URL.Query().Get("continuation_key") == "next" {
			_, _ = w.Write([]byte(`{"transactions":[{"transaction_amount":{"currency":"EUR","amount":"100.00"},"credit_debit_indicator":"CRDT","booking_date":"2026-07-02","debtor":{"name":"Employer"},"status":"BOOK"}],"continuation_key":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":""}`))
	}))
	defer provider.Close()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := t.TempDir() + "/key.pem"
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))

	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
	client, err := enablebanking.New("app", keyPath, provider.URL)
	require.NoError(t, err)
	syncContext, stopSync := context.WithCancel(context.Background())
	t.Cleanup(stopSync)
	app.state.EnableBankingClient = client
	app.state.EnableBankingSyncer = enablebanking.NewSyncer(app.d, client)
	bucketID := createBucket(t, app, "asset", "Bank")
	var userID string
	require.NoError(t, app.d.Users.QueryRow("select owner_user_id from buckets where id=$1", bucketID).Scan(&userID))
	integrationID := data.NewPrivateID()
	iban := "FI2112345600000785"
	saved, err := json.Marshal(enableBankingData{SessionID: "session", Bank: "Test", Country: "FI", PSUType: "personal", Accounts: []enableBankingAccount{{UID: "account", IdentificationHash: "account-hash", IBAN: iban, Currency: "EUR"}}})
	require.NoError(t, err)
	require.NoError(t, app.d.CreateBankIntegration(t.Context(), data.BankIntegration{ID: integrationID, UserID: userID, Provider: enableBankingProvider, Data: saved}, nil))
	require.NoError(t, app.d.MapIntegrationAccount(t.Context(), userID, integrationID, bucketID, iban))

	response := authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/accounts/account/sync", nil)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var result importResult
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	require.Zero(t, page.Load(), "the request handler must not fetch bank pages")
	app.state.EnableBankingSyncer.Start(syncContext)
	report := waitImport(t, app, result.BatchID)
	require.Equal(t, 2, report.Imported)
	rows := inboxByParty(getInbox(t, app, ""))
	require.Equal(t, int64(-1234), rows["Cafe"].Amount)
	require.Equal(t, int64(10000), rows["Employer"].Amount)
	require.Equal(t, int32(2), page.Load())
	var staged, jobs int
	require.NoError(t, app.d.Users.QueryRow("select count(*) from import_staged_rows where batch_id=$1", result.BatchID).Scan(&staged))
	require.NoError(t, app.d.Users.QueryRow("select count(*) from enable_banking_syncs where batch_id=$1 and completed_at is not null", result.BatchID).Scan(&jobs))
	require.Zero(t, staged)
	require.Equal(t, 1, jobs)

	newBucketID := createBucket(t, app, "asset", "Other bank")
	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/accounts/account/map", map[string]string{"bucket_id": newBucketID})
	require.Equal(t, http.StatusConflict, response.StatusCode)
	var mappedIBAN *string
	require.NoError(t, app.d.Users.QueryRow("select iban from buckets where id=$1", bucketID).Scan(&mappedIBAN))
	require.Equal(t, iban, *mappedIBAN)

	response = authed(t, app, http.MethodDelete, "/api/v1/imports/"+result.BatchID, nil)
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/accounts/account/map", map[string]string{"bucket_id": newBucketID})
	require.Equal(t, http.StatusNoContent, response.StatusCode)

	response = authed(t, app, http.MethodPost, "/api/v1/enablebanking/connections/"+integrationID+"/sync", nil)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var allResult struct {
		BatchIDs []string `json:"batch_ids"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&allResult))
	require.Len(t, allResult.BatchIDs, 1)
	secondReport := waitImport(t, app, allResult.BatchIDs[0])
	require.Equal(t, 2, secondReport.Imported)
	require.Zero(t, secondReport.Duplicates)
	require.Equal(t, int32(4), page.Load())
}
