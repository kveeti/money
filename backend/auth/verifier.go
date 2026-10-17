package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type tokenVerifier struct {
	issuer string
	keys   *signingKeys
	config oidc.Config
}

func newTokenVerifier(issuer string, keys *signingKeys, config *oidc.Config) *tokenVerifier {
	return &tokenVerifier{issuer: issuer, keys: keys, config: *config}
}

func (v *tokenVerifier) Verify(ctx context.Context, raw string) (*oidc.IDToken, error) {
	return v.verify(ctx, raw, false)
}

func (v *tokenVerifier) verify(ctx context.Context, raw string, allowExpired bool) (*oidc.IDToken, error) {
	keys := &verificationKeys{keys: v.keys}
	config := v.config
	config.SkipExpiryCheck = allowExpired
	verifier := oidc.NewVerifier(v.issuer, keys, &config)
	token, err := verifier.Verify(ctx, raw)
	if err != nil && IsVerificationUnavailable(keys.err) {
		return nil, keys.err
	}
	if err != nil {
		return nil, err
	}
	if allowExpired {
		var claims struct {
			NotBefore int64 `json:"nbf"`
		}
		if err := token.Claims(&claims); err != nil {
			return nil, err
		}
		if token.Expiry.IsZero() || time.Unix(claims.NotBefore, 0).After(time.Now().Add(5*time.Minute)) {
			return nil, fmt.Errorf("token has invalid time bounds")
		}
	}
	return token, nil
}

type verificationKeys struct {
	keys *signingKeys
	err  error
}

func (k *verificationKeys) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	payload, err := k.keys.VerifySignature(ctx, raw)
	k.err = err
	return payload, err
}
