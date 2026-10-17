package endpoints

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"money/backend/auth"

	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"
	"github.com/stretchr/testify/require"
)

func TestCancelledRequestKeepsRotatedTokens(t *testing.T) {
	for _, areKeysAvailable := range []bool{true, false} {
		name := "verified"
		if !areKeysAvailable {
			name = "pending verification"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var app *testApp
			var isRotated atomic.Bool
			var refreshCalls atomic.Int32
			app = newTestAppWith(t, appOpts{oidcMiddleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !isRotated.Load() {
						next.ServeHTTP(w, r)
						return
					}
					if r.URL.Path == mockoidc.JWKSEndpoint {
						cancel()
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if r.URL.Path != mockoidc.TokenEndpoint {
						next.ServeHTTP(w, r)
						return
					}
					refreshCalls.Add(1)
					token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
						Audience:  jwt.ClaimStrings{app.mock.ClientID},
						Issuer:    app.mock.Issuer(),
						Subject:   "1234567890",
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
					})
					kid, err := app.mock.Keypair.KeyID()
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if !areKeysAvailable {
						kid = "rotated"
					}
					token.Header["kid"] = kid
					raw, err := token.SignedString(app.mock.Keypair.PrivateKey)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if areKeysAvailable {
						cancel()
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{
						"access_token":  raw,
						"refresh_token": "rotated-refresh",
						"token_type":    "Bearer",
						"expires_in":    3600,
					})
				})
			}})
			response := completeLogin(t, app)
			cookie := findCookie(response.Cookies(), auth.CookieName)
			require.NotNil(t, cookie)
			session, _ := replaceSessionAccessToken(t, app, cookie, time.Now().Add(-time.Minute))
			isRotated.Store(true)
			_, err := authenticateWithLock(ctx, app.state, session.ID)
			if areKeysAvailable {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, authUnavailable)
			}
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.Equal(t, int32(1), refreshCalls.Load())
			saved, err := app.d.GetSessionByTokenHash(t.Context(), auth.HashToken(cookie.Value))
			require.NoError(t, err)
			require.NotNil(t, saved)
			raw, err := app.state.TokenCipher.Decrypt(saved.RefreshToken, session.ID+":refresh")
			require.NoError(t, err)
			require.Equal(t, "rotated-refresh", raw)
			require.Equal(t, !areKeysAvailable, saved.TokensNeedVerification)
		})
	}
}

func TestCancelledRequestStopsWaitingForSessionLock(t *testing.T) {
	app := newTestAppWith(t, appOpts{})
	response := completeLogin(t, app)
	cookie := findCookie(response.Cookies(), auth.CookieName)
	require.NotNil(t, cookie)
	session, _ := replaceSessionAccessToken(t, app, cookie, time.Now().Add(-time.Minute))
	lock, err := app.d.LockSession(t.Context(), session.ID, false)
	require.NoError(t, err)
	require.NotNil(t, lock)
	defer lock.Rollback()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := authenticateWithLock(ctx, app.state, session.ID)
		result <- err
	}()
	require.Eventually(t, func() bool {
		var count int
		err := app.d.Sessions.QueryRow(`
			select count(*) from pg_stat_activity
			where datname = current_database()
			  and wait_event_type = 'Lock'
			  and query like '%for update of session%'
		`).Scan(&count)
		return err == nil && count == 1
	}, time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled request kept waiting for the session lock")
	}
	require.NoError(t, lock.Commit())
	saved, err := app.d.GetSessionByTokenHash(t.Context(), auth.HashToken(cookie.Value))
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, session.RefreshToken, saved.RefreshToken)
}
