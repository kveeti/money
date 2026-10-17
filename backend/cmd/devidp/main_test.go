package main

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"

	appauth "money/backend/auth"
	"money/backend/config"

	"github.com/stretchr/testify/require"
)

func TestAuthorizationCodeAndRefreshTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	s := &server{
		key:     key,
		codes:   map[string]pending{"code": {sub: "alice", email: "alice@dev.local", nonce: "nonce"}},
		refresh: map[string]pending{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("/jwks.json", s.jwks)
	mux.HandleFunc("/token", s.token)
	provider := httptest.NewServer(mux)
	t.Cleanup(provider.Close)
	s.issuer = provider.URL

	client, err := appauth.NewOIDC(t.Context(), config.OIDCConfig{
		Issuer:       provider.URL,
		ClientID:     "dev",
		ClientSecret: "dev",
	})
	require.NoError(t, err)

	login, err := client.Exchange(t.Context(), "code", "nonce", "verifier")
	require.NoError(t, err)
	require.Equal(t, "alice", login.Claims.Subject)
	require.NotEmpty(t, login.Tokens.AccessToken)
	require.NotEmpty(t, login.Tokens.RefreshToken)

	refreshed, err := client.Refresh(t.Context(), login.Tokens.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, "alice", refreshed.AccessClaims.Subject)
	require.Equal(t, login.Tokens.RefreshToken, refreshed.RefreshToken)
}
