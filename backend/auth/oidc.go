package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"money/backend/config"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC wraps the provider, OAuth config, and token verifiers. Provider
// discovery and JWKS caching are set up once at startup.
type OIDC struct {
	httpClient     *http.Client
	oauth2         oauth2.Config
	idVerifier     *tokenVerifier
	accessVerifier *tokenVerifier
	clientID       string
	audience       string
}

func NewOIDC(ctx context.Context, c config.OIDCConfig) (*OIDC, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := discover(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("error creating oidc provider: %w", err)
	}
	var metadata struct {
		JWKSURL    string   `json:"jwks_uri"`
		Algorithms []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("read oidc provider metadata: %w", err)
	}
	keys, err := newSigningKeys(ctx, client, metadata.JWKSURL, metadata.Algorithms)
	if err != nil {
		return nil, fmt.Errorf("load oidc signing keys: %w", err)
	}

	return &OIDC{
		httpClient: client,
		oauth2: oauth2.Config{
			ClientID:     c.ClientID,
			ClientSecret: c.ClientSecret,
			RedirectURL:  c.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email"},
		},
		idVerifier: newTokenVerifier(c.Issuer, keys, &oidc.Config{
			ClientID:             c.ClientID,
			SupportedSigningAlgs: metadata.Algorithms,
		}),
		accessVerifier: newTokenVerifier(c.Issuer, keys, &oidc.Config{
			SkipClientIDCheck:    true,
			SupportedSigningAlgs: metadata.Algorithms,
		}),
		clientID: c.ClientID,
		audience: c.Audience,
	}, nil
}

// discover retries provider discovery: the issuer may be briefly unreachable at
// startup (dev IdP still booting, provider restarting during a deploy).
func discover(ctx context.Context, issuer string) (*oidc.Provider, error) {
	var err error
	for attempt := 1; attempt <= 10; attempt++ {
		var provider *oidc.Provider
		if provider, err = oidc.NewProvider(ctx, issuer); err == nil {
			return provider, nil
		}
		slog.Warn("oidc discovery failed, retrying", "issuer", issuer, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, err
}

func NewPKCEVerifier() string {
	return oauth2.GenerateVerifier()
}

func (o *OIDC) AuthCodeURL(state, nonce, pkceVerifier string) string {
	return o.oauth2.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(pkceVerifier),
	)
}

type Claims struct {
	Subject string
	Issuer  string
	Email   string
}

type AccessClaims struct {
	Subject string
	Issuer  string
	Expiry  time.Time
}

type TokenSet struct {
	AccessToken  string
	RefreshToken string
	AccessClaims AccessClaims
	IDToken      string
}

type Login struct {
	Claims Claims
	Tokens TokenSet
}

// Exchange completes the code exchange and verifies both the ID token and the
// access token. The ID token nonce and the identity shared by both tokens are
// also checked.
func (o *OIDC) Exchange(ctx context.Context, code, nonce, pkceVerifier string) (*Login, error) {
	ctx = oidc.ClientContext(ctx, o.httpClient)
	tok, err := o.oauth2.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return nil, fmt.Errorf("error exchanging code: %w", err)
	}

	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in token response")
	}
	idToken, err := o.idVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("error verifying id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, fmt.Errorf("nonce mismatch")
	}

	var idClaims struct {
		Email string `json:"email"`
	}
	if err := idToken.Claims(&idClaims); err != nil {
		return nil, fmt.Errorf("error parsing id token claims: %w", err)
	}

	tokens, err := o.verifyTokenResponse(ctx, tok)
	if err != nil {
		return nil, err
	}
	if tokens.AccessClaims.Subject != idToken.Subject || tokens.AccessClaims.Issuer != idToken.Issuer {
		return nil, fmt.Errorf("id and access token identity mismatch")
	}

	return &Login{
		Claims: Claims{Subject: idToken.Subject, Issuer: idToken.Issuer, Email: idClaims.Email},
		Tokens: *tokens,
	}, nil
}

// VerifyAccess verifies the access JWT signature and its issuer, intended
// audience, time bounds, and subject.
func (o *OIDC) VerifyAccess(ctx context.Context, raw string) (*AccessClaims, error) {
	return o.verifyAccess(ctx, raw, false)
}

func (o *OIDC) verifyAccess(ctx context.Context, raw string, allowExpired bool) (*AccessClaims, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	token, err := o.accessVerifier.verify(ctx, raw, allowExpired)
	if err != nil {
		return nil, err
	}
	if token.Subject == "" {
		return nil, fmt.Errorf("access token has no subject")
	}

	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse access token claims: %w", err)
	}

	expectedAudience := o.audience
	if expectedAudience != "" {
		if !contains(token.Audience, expectedAudience) {
			return nil, fmt.Errorf("access token has no %q audience", expectedAudience)
		}
	} else if !contains(token.Audience, o.clientID) && claims.AuthorizedParty != o.clientID {
		return nil, fmt.Errorf("access token was not issued for client %q", o.clientID)
	}

	return &AccessClaims{Subject: token.Subject, Issuer: token.Issuer, Expiry: token.Expiry}, nil
}

func (o *OIDC) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	ctx = oidc.ClientContext(ctx, o.httpClient)
	tok, err := o.oauth2.TokenSource(ctx, &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Unix(0, 0),
	}).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh oidc token: %w", err)
	}

	// OAuth providers may keep the current refresh token by omitting it from
	// the response instead of returning it again.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	rawIDToken, _ := tok.Extra("id_token").(string)
	tokens := &TokenSet{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		IDToken:      rawIDToken,
	}
	claims, err := o.verifyRefreshTokens(ctx, tokens, false)
	if err != nil {
		return tokens, err
	}
	tokens.AccessClaims = *claims
	return tokens, nil
}

func (o *OIDC) VerifyPendingTokens(ctx context.Context, tokens *TokenSet) (*AccessClaims, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return o.verifyRefreshTokens(ctx, tokens, true)
}

func (o *OIDC) verifyRefreshTokens(ctx context.Context, tokens *TokenSet, allowExpired bool) (*AccessClaims, error) {
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return nil, fmt.Errorf("token response is missing access or refresh token")
	}
	claims, err := o.verifyAccess(ctx, tokens.AccessToken, allowExpired)
	if err != nil {
		return nil, err
	}
	if tokens.IDToken != "" {
		idToken, err := o.idVerifier.verify(ctx, tokens.IDToken, allowExpired)
		if err != nil {
			return nil, fmt.Errorf("verify refreshed id token: %w", err)
		}
		if idToken.Subject != claims.Subject || idToken.Issuer != claims.Issuer {
			return nil, fmt.Errorf("refreshed id and access token identity mismatch")
		}
	}
	return claims, nil
}

func (o *OIDC) verifyTokenResponse(ctx context.Context, tok *oauth2.Token) (*TokenSet, error) {
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("no access token in token response")
	}
	if tok.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token in token response")
	}
	claims, err := o.VerifyAccess(ctx, tok.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("verify access token: %w", err)
	}
	return &TokenSet{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		AccessClaims: *claims,
	}, nil
}

func IsExpiredToken(err error) bool {
	var expired *oidc.TokenExpiredError
	return errors.As(err, &expired)
}

func IsInvalidGrant(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	return errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
