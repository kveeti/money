package endpoints

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"money/backend/auth"
	"money/backend/config"
	"money/backend/state"

	"github.com/stretchr/testify/require"
)

// With DevViteUrl set, non-navigation requests are reverse-proxied to Vite
// untouched (assets + HMR flow through the backend origin).
func TestFrontendDevProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "vite:"+r.URL.Path)
	}))
	defer upstream.Close()

	st := state.NewState(nil, &config.Config{DevViteUrl: upstream.URL}, nil)
	srv := httptest.NewServer(FrontendHandler(st, nil))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/src/index.tsx")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, "vite:/src/index.tsx", string(body))
}

// A document navigation without a session is bounced to login instead of
// loading the app.
func TestFrontendGateRedirectsUnauthenticated(t *testing.T) {
	app := newTestAppWith(t, appOpts{})

	req, _ := http.NewRequest(http.MethodGet, app.url+"/", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := app.client.Do(req)
	require.NoError(t, err)

	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/login", resp.Header.Get("Location"))
}

func TestFrontendGateIncludesDemoModeInLoginURL(t *testing.T) {
	app := newTestAppWith(t, appOpts{demoMode: true})

	req, _ := http.NewRequest(http.MethodGet, app.url+"/", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/login?demo=1", resp.Header.Get("Location"))

	req, _ = http.NewRequest(http.MethodGet, app.url+"/login", nil)
	req.Header.Set("Accept", "text/html")
	resp, err = app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/login?demo=1", resp.Header.Get("Location"))

	req, _ = http.NewRequest(http.MethodGet, app.url+"/login?demo=1", nil)
	req.Header.Set("Accept", "text/html")
	resp, err = app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoginPageIsPublic(t *testing.T) {
	app := newTestAppWith(t, appOpts{})

	req, _ := http.NewRequest(http.MethodGet, app.url+"/login", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "<!doctype html>")
}

// A session-bearing navigation is served the app shell.
func TestFrontendGateServesAuthenticated(t *testing.T) {
	app := newTestAppWith(t, appOpts{})
	authCookie := findCookie(completeLogin(t, app).Cookies(), auth.CookieName)
	require.NotNil(t, authCookie)

	req, _ := http.NewRequest(http.MethodGet, app.url+"/", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(authCookie)
	resp, err := app.client.Do(req)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	body, _ := io.ReadAll(resp.Body)
	require.True(t, strings.Contains(string(body), "<!doctype html>"))
}

// Asset requests (non-navigations) are not gated.
func TestFrontendAssetsNotGated(t *testing.T) {
	app := newTestAppWith(t, appOpts{})

	req, _ := http.NewRequest(http.MethodGet, app.url+"/assets/app-abc123.js", nil)
	req.Header.Set("Accept", "*/*")
	resp, err := app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "public, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))

	req, _ = http.NewRequest(http.MethodGet, app.url+"/assets/missing.js", nil)
	req.Header.Set("Accept", "*/*")
	resp, err = app.client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Cache-Control"))
}
