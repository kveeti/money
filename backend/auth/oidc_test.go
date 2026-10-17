package auth

import (
	"context"
	"testing"
	"time"

	"money/backend/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"
	"github.com/stretchr/testify/require"
)

type keycloakAccessClaims struct {
	jwt.RegisteredClaims
	AuthorizedParty string `json:"azp"`
}

func TestVerifyAccessAcceptsKeycloakAuthorizedPartyByDefault(t *testing.T) {
	provider, err := mockoidc.Run()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown()) })

	client, err := NewOIDC(t.Context(), config.OIDCConfig{
		Issuer:       provider.Issuer(),
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
	})
	require.NoError(t, err)

	now := time.Now()
	raw, err := provider.Keypair.SignJWT(keycloakAccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"account"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    provider.Issuer(),
			NotBefore: jwt.NewNumericDate(now),
			Subject:   "user-1",
		},
		AuthorizedParty: provider.ClientID,
	})
	require.NoError(t, err)

	claims, err := client.VerifyAccess(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.Subject)
}

func TestVerifyAccessRequiresConfiguredAudience(t *testing.T) {
	provider, err := mockoidc.Run()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown()) })

	client, err := NewOIDC(t.Context(), config.OIDCConfig{
		Issuer:       provider.Issuer(),
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		Audience:     "money-api",
	})
	require.NoError(t, err)

	now := time.Now()
	claims := keycloakAccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"account"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    provider.Issuer(),
			NotBefore: jwt.NewNumericDate(now),
			Subject:   "user-1",
		},
		AuthorizedParty: provider.ClientID,
	}
	raw, err := provider.Keypair.SignJWT(claims)
	require.NoError(t, err)
	_, err = client.VerifyAccess(t.Context(), raw)
	require.ErrorContains(t, err, "money-api")

	claims.Audience = jwt.ClaimStrings{"money-api"}
	raw, err = provider.Keypair.SignJWT(claims)
	require.NoError(t, err)
	_, err = client.VerifyAccess(t.Context(), raw)
	require.NoError(t, err)
}
