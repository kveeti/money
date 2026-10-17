package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const (
	keyCacheLifetime    = time.Hour
	keyAttemptTimeout   = 3 * time.Second
	keyRefreshTimeout   = 8 * time.Second
	keyFailureCooldown  = time.Minute
	maxKeyResponseBytes = 1024 * 1024
)

var keyRetryDelays = []time.Duration{250 * time.Millisecond, 2 * time.Second}

type verificationUnavailable struct {
	err error
}

func (e *verificationUnavailable) Error() string { return e.err.Error() }
func (e *verificationUnavailable) Unwrap() error { return e.err }

func IsVerificationUnavailable(err error) bool {
	var unavailable *verificationUnavailable
	return errors.As(err, &unavailable)
}

type keyRefresh struct {
	done chan struct{}
	err  error
}

type signingKeys struct {
	client               *http.Client
	url                  string
	algorithms           []jose.SignatureAlgorithm
	refresh              *keyRefresh
	missingKeyRetryAfter time.Time
	refreshContext       context.Context
	mu                   sync.RWMutex
	keys                 jose.JSONWebKeySet
	validatedAt          time.Time
	retryAfter           time.Time
	etag                 string
	lastModified         string
}

func newSigningKeys(ctx context.Context, client *http.Client, url string, algorithms []string) (*signingKeys, error) {
	keys := &signingKeys{client: client, url: url, refreshContext: context.WithoutCancel(ctx)}
	for _, algorithm := range algorithms {
		keys.algorithms = append(keys.algorithms, jose.SignatureAlgorithm(algorithm))
	}
	if len(keys.algorithms) == 0 {
		keys.algorithms = []jose.SignatureAlgorithm{jose.RS256}
	}
	if err := keys.revalidate(ctx); err != nil {
		return nil, err
	}
	return keys, nil
}

func (k *signingKeys) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jwt, err := jose.ParseSigned(raw, k.algorithms)
	if err != nil {
		return nil, err
	}
	if len(jwt.Signatures) != 1 {
		return nil, fmt.Errorf("token must have one signature")
	}
	kid := jwt.Signatures[0].Header.KeyID
	if kid == "" {
		return nil, fmt.Errorf("token has no signing key ID")
	}
	if err := k.ensureFresh(ctx, kid); err != nil {
		return nil, &verificationUnavailable{err: err}
	}
	k.mu.RLock()
	keys := k.keys.Key(kid)
	k.mu.RUnlock()
	if len(keys) == 0 {
		return nil, &verificationUnavailable{err: fmt.Errorf("signing key is not cached")}
	}
	for _, key := range keys {
		if payload, err := jwt.Verify(key.Key); err == nil {
			return payload, nil
		}
	}
	return nil, fmt.Errorf("invalid token signature")
}

func (k *signingKeys) ensureFresh(ctx context.Context, kid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	isFresh := time.Since(k.validatedAt) < keyCacheLifetime
	isMissing := len(k.keys.Key(kid)) == 0
	if isFresh && !isMissing {
		k.mu.Unlock()
		return nil
	}
	refresh := k.refresh
	if refresh == nil {
		if time.Now().Before(k.retryAfter) || (isFresh && time.Now().Before(k.missingKeyRetryAfter)) {
			k.mu.Unlock()
			return fmt.Errorf("signing key refresh is waiting to retry")
		}
		refresh = &keyRefresh{done: make(chan struct{})}
		k.refresh = refresh
		if isMissing {
			k.missingKeyRetryAfter = time.Now().Add(keyFailureCooldown)
		}
		go func() {
			refresh.err = k.revalidate(k.refreshContext)
			k.mu.Lock()
			k.refresh = nil
			close(refresh.done)
			k.mu.Unlock()
		}()
	}
	k.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-refresh.done:
		return refresh.err
	}
}

func (k *signingKeys) revalidate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, keyRefreshTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		isRetryable, err := k.fetch(ctx)
		if err == nil {
			return nil
		}
		if !isRetryable || attempt == len(keyRetryDelays) || ctx.Err() != nil {
			k.mu.Lock()
			k.retryAfter = time.Now().Add(keyFailureCooldown)
			k.mu.Unlock()
			return err
		}
		timer := time.NewTimer(keyRetryDelays[attempt])
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			k.mu.Lock()
			k.retryAfter = time.Now().Add(keyFailureCooldown)
			k.mu.Unlock()
			return ctx.Err()
		}
	}
}

func (k *signingKeys) fetch(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, keyAttemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return false, err
	}
	k.mu.RLock()
	req.Header.Set("If-None-Match", k.etag)
	req.Header.Set("If-Modified-Since", k.lastModified)
	hasCachedKeys := len(k.keys.Keys) != 0
	k.mu.RUnlock()
	response, err := k.client.Do(req)
	if err != nil {
		return true, err
	}
	defer response.Body.Close()

	var keys jose.JSONWebKeySet
	if response.StatusCode == http.StatusNotModified {
		if !hasCachedKeys {
			return false, fmt.Errorf("signing key endpoint returned 304 without cached keys")
		}
	} else {
		if response.StatusCode != http.StatusOK {
			return response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
				fmt.Errorf("signing key endpoint returned HTTP %d", response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxKeyResponseBytes+1))
		if err != nil {
			return true, err
		}
		if len(body) > maxKeyResponseBytes {
			return false, fmt.Errorf("signing key response is too large")
		}
		if err := json.Unmarshal(body, &keys); err != nil {
			return true, err
		}
		if len(keys.Keys) == 0 {
			return true, fmt.Errorf("signing key endpoint returned no keys")
		}
		for _, key := range keys.Keys {
			if !key.IsPublic() || !key.Valid() {
				return false, fmt.Errorf("signing key endpoint returned an invalid public key")
			}
		}
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if response.StatusCode == http.StatusOK {
		k.keys = keys
		k.etag = response.Header.Get("ETag")
		k.lastModified = response.Header.Get("Last-Modified")
	} else {
		if etag := response.Header.Get("ETag"); etag != "" {
			k.etag = etag
		}
		if lastModified := response.Header.Get("Last-Modified"); lastModified != "" {
			k.lastModified = lastModified
		}
	}
	k.validatedAt = time.Now()
	k.retryAfter = time.Time{}
	return false, nil
}
