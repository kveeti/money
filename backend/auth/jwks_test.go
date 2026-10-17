package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"
	"github.com/stretchr/testify/require"
)

func signingKeyFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	pair, err := mockoidc.DefaultKeypair()
	require.NoError(t, err)
	kid, err := pair.KeyID()
	require.NoError(t, err)
	body, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: pair.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"},
	}})
	require.NoError(t, err)
	raw, err := pair.SignJWT(jwt.RegisteredClaims{Subject: "user-1"})
	require.NoError(t, err)
	return body, raw
}

func expireSigningKeys(keys *signingKeys) {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	keys.validatedAt = time.Now().Add(-2 * keyCacheLifetime)
}

func TestSigningKeysRevalidateWithETag(t *testing.T) {
	body, raw := signingKeyFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") == `"keys-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"keys-1"`)
		w.Write(body)
	}))
	defer server.Close()
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	_, err = keys.VerifySignature(t.Context(), raw)
	require.NoError(t, err)
	require.Equal(t, int32(1), requests.Load())
	expireSigningKeys(keys)
	_, err = keys.VerifySignature(t.Context(), raw)
	require.NoError(t, err)
	require.Equal(t, int32(2), requests.Load())
	server.Close()
	_, err = keys.VerifySignature(t.Context(), raw)
	require.NoError(t, err)
}

func TestSigningKeyOutageKeepsCacheAndUsesCooldown(t *testing.T) {
	body, raw := signingKeyFixture(t)
	var isUnavailable atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if isUnavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	expireSigningKeys(keys)
	isUnavailable.Store(true)
	_, err = keys.VerifySignature(t.Context(), raw)
	require.True(t, IsVerificationUnavailable(err), "%v", err)
	require.Equal(t, int32(4), requests.Load())
	_, err = keys.VerifySignature(t.Context(), raw)
	require.True(t, IsVerificationUnavailable(err), "%v", err)
	require.Equal(t, int32(4), requests.Load())
	keys.mu.Lock()
	require.NotEmpty(t, keys.keys.Keys)
	keys.retryAfter = time.Now().Add(-time.Second)
	keys.mu.Unlock()
	isUnavailable.Store(false)
	_, err = keys.VerifySignature(t.Context(), raw)
	require.NoError(t, err)
}

func TestSigningKeyRefreshIsSharedByConcurrentRequests(t *testing.T) {
	body, raw := signingKeyFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(body)
	}))
	defer server.Close()
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	expireSigningKeys(keys)
	var workers sync.WaitGroup
	errors := make(chan error, 10)
	for range 10 {
		workers.Go(func() {
			_, err := keys.VerifySignature(t.Context(), raw)
			errors <- err
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), requests.Load())
}

func TestCancelledCallerDoesNotCancelSharedKeyFetch(t *testing.T) {
	body, raw := signingKeyFixture(t)
	var requests atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 2 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Write(body)
	}))
	defer server.Close()
	defer close(release)
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	expireSigningKeys(keys)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstResult := make(chan error, 1)
	go func() {
		_, err := keys.VerifySignature(ctx, raw)
		firstResult <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("key fetch did not start")
	}
	cancel()
	require.ErrorIs(t, <-firstResult, context.Canceled)
	secondResult := make(chan error, 1)
	go func() {
		_, err := keys.VerifySignature(t.Context(), raw)
		secondResult <- err
	}()
	release <- struct{}{}
	require.NoError(t, <-secondResult)
	require.Equal(t, int32(2), requests.Load())
	keys.mu.RLock()
	require.True(t, keys.retryAfter.IsZero())
	keys.mu.RUnlock()
}

func TestSharedSigningKeyFetchHasItsOwnDeadline(t *testing.T) {
	body, raw := signingKeyFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			<-r.Context().Done()
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	expireSigningKeys(keys)
	result := make(chan error, 1)
	go func() {
		_, err := keys.VerifySignature(t.Context(), raw)
		result <- err
	}()
	select {
	case err := <-result:
		require.True(t, IsVerificationUnavailable(err), "%v", err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(keyRefreshTimeout + 2*time.Second):
		t.Fatal("shared key fetch exceeded its deadline")
	}
	require.LessOrEqual(t, requests.Load(), int32(4))
	requestsBeforeRetry := requests.Load()
	_, err = keys.VerifySignature(t.Context(), raw)
	require.True(t, IsVerificationUnavailable(err), "%v", err)
	require.Equal(t, requestsBeforeRetry, requests.Load())
}

func TestSigningKeysRefreshForUnknownKeyAndRateLimitMisses(t *testing.T) {
	body, _ := signingKeyFixture(t)
	pair, err := mockoidc.DefaultKeypair()
	require.NoError(t, err)
	rotatedBody, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: pair.PublicKey, KeyID: "rotated", Algorithm: "RS256", Use: "sig"},
	}})
	require.NoError(t, err)
	var isRotated atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if isRotated.Load() {
			w.Write(rotatedBody)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	isRotated.Store(true)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{Subject: "user-1"})
	token.Header["kid"] = "rotated"
	raw, err := token.SignedString(pair.PrivateKey)
	require.NoError(t, err)
	_, err = keys.VerifySignature(t.Context(), raw)
	require.NoError(t, err)
	require.Equal(t, int32(2), requests.Load())
	for _, kid := range []string{"missing-1", "missing-2"} {
		token.Header["kid"] = kid
		raw, err := token.SignedString(pair.PrivateKey)
		require.NoError(t, err)
		_, err = keys.VerifySignature(t.Context(), raw)
		require.True(t, IsVerificationUnavailable(err), "%v", err)
	}
	require.Equal(t, int32(2), requests.Load())
	keys.mu.Lock()
	keys.missingKeyRetryAfter = time.Now().Add(-time.Second)
	keys.mu.Unlock()
	token.Header["kid"] = "still-missing"
	raw, err = token.SignedString(pair.PrivateKey)
	require.NoError(t, err)
	_, err = keys.VerifySignature(t.Context(), raw)
	require.True(t, IsVerificationUnavailable(err), "%v", err)
	require.Equal(t, int32(3), requests.Load())
}

func TestSigningKeysDistinguishMissingKeyFromInvalidSignature(t *testing.T) {
	body, raw := signingKeyFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer server.Close()
	keys, err := newSigningKeys(t.Context(), server.Client(), server.URL, nil)
	require.NoError(t, err)
	pair, err := mockoidc.DefaultKeypair()
	require.NoError(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{Subject: "user-1"})
	token.Header["kid"] = "new-key"
	unknownKeyToken, err := token.SignedString(pair.PrivateKey)
	require.NoError(t, err)
	_, err = keys.VerifySignature(t.Context(), unknownKeyToken)
	require.True(t, IsVerificationUnavailable(err), "%v", err)
	otherPair, err := mockoidc.RandomKeypair(2048)
	require.NoError(t, err)
	kid, err := pair.KeyID()
	require.NoError(t, err)
	token.Header["kid"] = kid
	invalidToken, err := token.SignedString(otherPair.PrivateKey)
	require.NoError(t, err)
	_, err = keys.VerifySignature(t.Context(), invalidToken)
	require.Error(t, err)
	require.False(t, IsVerificationUnavailable(err))
	_, err = keys.VerifySignature(t.Context(), raw)
	require.NoError(t, err)
}
