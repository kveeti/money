package endpoints

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewHandlerLimitsJSONBody(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/test",
		strings.NewReader(`"`+strings.Repeat("x", maxJSONBodyBytes)+`"`),
	)
	response := httptest.NewRecorder()
	var decodeErr error

	handler := NewHandler(func(_ http.ResponseWriter, r *http.Request) error {
		var body string
		decodeErr = json.NewDecoder(r.Body).Decode(&body)
		if decodeErr != nil {
			return NewErr("invalid request body", http.StatusBadRequest)
		}
		return nil
	})
	handler.ServeHTTP(response, request)

	var tooLarge *http.MaxBytesError
	require.True(t, errors.As(decodeErr, &tooLarge))
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestOriginGuardRejectsUnsafeForeignRequests(t *testing.T) {
	handler := OriginGuard("https://money.example", "https://front.example")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)

	for _, origin := range []string{"https://money.example", "https://front.example", ""} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusNoContent, response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(true, true, "https://auth.example/realms/main")(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/@me", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", response.Header().Get("X-Frame-Options"))
	require.Contains(t, response.Header().Get("Content-Security-Policy"), "form-action 'self' https://auth.example;")
	require.NotEmpty(t, response.Header().Get("Strict-Transport-Security"))
}

func TestNewHandlerLeavesImportBodyLimitToImportHandler(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/imports",
		strings.NewReader(`"`+strings.Repeat("x", maxJSONBodyBytes)+`"`),
	)
	response := httptest.NewRecorder()

	handler := NewHandler(func(_ http.ResponseWriter, r *http.Request) error {
		var body string
		return json.NewDecoder(r.Body).Decode(&body)
	})
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
}
