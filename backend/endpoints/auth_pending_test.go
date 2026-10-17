package endpoints

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"money/backend/auth"
	"money/backend/config"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"
	"github.com/stretchr/testify/require"
)

func TestRotatedTokensSurviveUnavailableSigningKeys(t *testing.T) {
	for _, scenario := range []string{"valid", "expired", "wrong subject", "wrong audience", "bad signature", "wrong id token subject"} {
		t.Run(scenario, func(t *testing.T) {
			var app *testApp
			var mode atomic.Int32
			var refreshCalls atomic.Int32
			refreshInputs := make(chan string, 4)
			pair, err := mockoidc.RandomKeypair(2048)
			require.NoError(t, err)
			keyBody, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
				{Key: pair.PublicKey, KeyID: "rotated", Algorithm: "RS256", Use: "sig"},
			}})
			require.NoError(t, err)
			app = newTestAppWith(t, appOpts{
				oidcMiddleware: func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if mode.Load() == 0 {
							next.ServeHTTP(w, r)
							return
						}
						if r.URL.Path == mockoidc.JWKSEndpoint {
							if mode.Load() == 1 {
								w.WriteHeader(http.StatusForbidden)
								return
							}
							w.Write(keyBody)
							return
						}
						if r.URL.Path != mockoidc.TokenEndpoint {
							next.ServeHTTP(w, r)
							return
						}
						if err := r.ParseForm(); err != nil {
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						refreshInputs <- r.Form.Get("refresh_token")
						call := refreshCalls.Add(1)
						claims := jwt.RegisteredClaims{
							Audience:  jwt.ClaimStrings{app.mock.ClientID},
							Issuer:    app.mock.Issuer(),
							Subject:   "1234567890",
							ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
						}
						if scenario == "expired" && call == 1 {
							claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
						}
						if scenario == "wrong subject" {
							claims.Subject = "different-user"
						}
						if scenario == "wrong audience" {
							claims.Audience = jwt.ClaimStrings{"other-client"}
						}
						token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
						token.Header["kid"] = "rotated"
						privateKey := pair.PrivateKey
						if scenario == "bad signature" {
							privateKey = app.mock.Keypair.PrivateKey
						}
						rawAccess, err := token.SignedString(privateKey)
						if err != nil {
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						if scenario == "wrong id token subject" {
							claims.Subject = "different-user"
						}
						idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
						idToken.Header["kid"] = "rotated"
						rawID, err := idToken.SignedString(pair.PrivateKey)
						if err != nil {
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(map[string]any{
							"access_token":  rawAccess,
							"refresh_token": "rotated-refresh",
							"id_token":      rawID,
							"token_type":    "Bearer",
							"expires_in":    3600,
						})
					})
				},
			})
			loginResponse := completeLogin(t, app)
			cookie := findCookie(loginResponse.Cookies(), auth.CookieName)
			require.NotNil(t, cookie)
			session, _ := replaceSessionAccessToken(t, app, cookie, time.Now().Add(-time.Minute))
			oldRefresh, err := app.state.TokenCipher.Decrypt(session.RefreshToken, session.ID+":refresh")
			require.NoError(t, err)
			mode.Store(1)
			_, err = authenticateWithLock(t.Context(), app.state, session.ID)
			require.ErrorIs(t, err, authUnavailable)
			require.Equal(t, oldRefresh, <-refreshInputs)
			pending, err := app.d.GetSessionByTokenHash(t.Context(), auth.HashToken(cookie.Value))
			require.NoError(t, err)
			require.NotNil(t, pending)
			require.True(t, pending.TokensNeedVerification)
			require.NotEmpty(t, pending.PendingIDToken)
			require.NotEqual(t, "rotated-refresh", pending.RefreshToken)
			savedRefresh, err := app.state.TokenCipher.Decrypt(pending.RefreshToken, session.ID+":refresh")
			require.NoError(t, err)
			require.Equal(t, "rotated-refresh", savedRefresh)
			_, err = authenticateWithLock(t.Context(), app.state, session.ID)
			require.ErrorIs(t, err, authUnavailable)
			require.Equal(t, int32(1), refreshCalls.Load())

			mode.Store(2)
			app.state.OIDC, err = auth.NewOIDC(t.Context(), config.OIDCConfig{
				Issuer:       app.mock.Issuer(),
				ClientID:     app.mock.ClientID,
				ClientSecret: app.mock.ClientSecret,
			})
			require.NoError(t, err)
			req, err := http.NewRequest(http.MethodGet, app.url+"/api/v1/users/@me", nil)
			require.NoError(t, err)
			req.AddCookie(cookie)
			response, err := app.client.Do(req)
			require.NoError(t, err)
			defer response.Body.Close()
			saved, err := app.d.GetSessionByTokenHash(t.Context(), auth.HashToken(cookie.Value))
			require.NoError(t, err)
			switch scenario {
			case "valid", "expired":
				require.Equal(t, http.StatusOK, response.StatusCode)
				require.NotNil(t, saved)
				require.False(t, saved.TokensNeedVerification)
				require.Empty(t, saved.PendingIDToken)
				if scenario == "expired" {
					require.Equal(t, "rotated-refresh", <-refreshInputs)
					require.Equal(t, int32(2), refreshCalls.Load())
				} else {
					require.Equal(t, int32(1), refreshCalls.Load())
				}
			default:
				require.Equal(t, http.StatusUnauthorized, response.StatusCode)
				require.Nil(t, saved)
				require.Equal(t, int32(1), refreshCalls.Load())
			}
		})
	}
}
