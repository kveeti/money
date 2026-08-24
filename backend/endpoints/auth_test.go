package endpoints

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"testing"
	"testing/fstest"

	"money/backend/auth"
	"money/backend/config"
	"money/backend/data"
	"money/backend/state"

	"github.com/oauth2-proxy/mockoidc"
	"github.com/stretchr/testify/require"
)

const testFrontURL = "http://frontend.test"

// testFrontendFS stands in for the embedded build in router tests.
func testFrontendFS() fs.FS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>test</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log('test')")},
	}
}

type testApp struct {
	url    string
	mock   *mockoidc.MockOIDC
	client *http.Client
	d      *data.Data
	state  *state.State
}

// appOpts tweaks the config a test app is built with. Zero values mean: CORS
// off (same-origin) and the redirect URL defaulted to the app's own callback.
type appOpts struct {
	frontURL          string
	redirectURL       string
	denyEnableBanking bool
}

// newTestAppWith wires a mock OIDC provider, a fresh database and the real
// router onto a local listener using the given config options. The client never
// auto-follows redirects so each step can be asserted.
func newTestAppWith(t *testing.T, opts appOpts) *testApp {
	t.Helper()

	m, err := mockoidc.Run()
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	appURL := "http://" + ln.Addr().String()

	redirectURL := opts.redirectURL
	if redirectURL == "" {
		redirectURL = appURL + "/api/v1/auth/callback"
	}

	d := newTestData(t)
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	t.Cleanup(stopWorkers)
	d.StartImportWorkers(workerCtx)
	oidcClient, err := auth.NewOIDC(context.Background(), config.OIDCConfig{
		Issuer:       m.Issuer(),
		ClientID:     m.ClientID,
		ClientSecret: m.ClientSecret,
		RedirectURL:  redirectURL,
	})
	require.NoError(t, err)

	allowedSubjects := map[string]struct{}{"1234567890": {}}
	if opts.denyEnableBanking {
		allowedSubjects = map[string]struct{}{}
	}
	appConfig := &config.Config{
		BackendUrl: appURL,
		FrontUrl:   opts.frontURL,
		EnableBanking: config.EnableBankingConfig{
			AllowedSubjects: allowedSubjects,
		},
	}
	st := state.NewState(d, appConfig, oidcClient)

	srv := &http.Server{Handler: GetRouter(st, testFrontendFS())}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	return &testApp{
		url:  appURL,
		mock: m,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		d:     d,
		state: st,
	}
}

// completeLogin runs login -> authorize -> callback and returns the callback
// response (302 to the frontend, auth cookie set).
func completeLogin(t *testing.T, app *testApp) *http.Response {
	t.Helper()

	resp, err := app.client.Get(app.url + "/api/v1/auth/login")
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), app.mock.Issuer())

	flowCookie := findCookie(resp.Cookies(), auth.FlowCookieName)
	require.NotNil(t, flowCookie)

	resp, err = app.client.Get(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.NotEmpty(t, queryParam(t, resp.Header.Get("Location"), "code"))

	req, _ := http.NewRequest(http.MethodGet, resp.Header.Get("Location"), nil)
	req.AddCookie(flowCookie)
	resp, err = app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	return resp
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestOIDCFullFlow(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})

	resp := completeLogin(t, app)
	require.Equal(t, testFrontURL, resp.Header.Get("Location"))

	authCookie := findCookie(resp.Cookies(), auth.CookieName)
	require.NotNil(t, authCookie)
	require.NotEmpty(t, authCookie.Value)

	// the session authenticates a request, returning the upserted user.
	req, _ := http.NewRequest(http.MethodGet, app.url+"/api/v1/users/@me", nil)
	req.AddCookie(authCookie)
	resp, err := app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "jane.doe@example.com", body["email"])
}

// With FRONT_URL unset the frontend is same-origin, so login redirects back to
// the backend's own URL.
func TestSameOriginRedirect(t *testing.T) {
	app := newTestAppWith(t, appOpts{})

	resp := completeLogin(t, app)
	require.Equal(t, app.url, resp.Header.Get("Location"))
	require.NotNil(t, findCookie(resp.Cookies(), auth.CookieName))
}

// A configured OIDC_REDIRECT_URL must be the redirect_uri sent to the provider,
// even when it points somewhere other than this server (proxy setups).
func TestUsesConfiguredRedirectURL(t *testing.T) {
	const weird = "https://proxy.example/weird/cb"
	app := newTestAppWith(t, appOpts{redirectURL: weird})

	resp, err := app.client.Get(app.url + "/api/v1/auth/login")
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, weird, queryParam(t, resp.Header.Get("Location"), "redirect_uri"))
}

// CORS is enabled only when FRONT_URL is set (cross-origin); same-origin sends
// no CORS headers.
func TestCorsReflectsFrontUrl(t *testing.T) {
	t.Run("cross-origin", func(t *testing.T) {
		app := newTestAppWith(t, appOpts{frontURL: testFrontURL})
		resp, err := app.client.Get(app.url + "/api/health")
		require.NoError(t, err)
		require.Equal(t, testFrontURL, resp.Header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("same-origin", func(t *testing.T) {
		app := newTestAppWith(t, appOpts{})
		resp, err := app.client.Get(app.url + "/api/health")
		require.NoError(t, err)
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	})
}

func TestCallbackMissingFlowCookie(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})

	resp, err := app.client.Get(app.url + "/api/v1/auth/callback?code=x&state=y")
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCallbackStateMismatch(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})

	resp, err := app.client.Get(app.url + "/api/v1/auth/login")
	require.NoError(t, err)
	flowCookie := findCookie(resp.Cookies(), auth.FlowCookieName)
	require.NotNil(t, flowCookie)

	req, _ := http.NewRequest(http.MethodGet, app.url+"/api/v1/auth/callback?code=x&state=tampered", nil)
	req.AddCookie(flowCookie)
	resp, err = app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMeRequiresAuth(t *testing.T) {
	app := newTestAppWith(t, appOpts{frontURL: testFrontURL})

	resp, err := app.client.Get(app.url + "/api/v1/users/@me")
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func queryParam(t *testing.T, rawURL, key string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Query().Get(key)
}
