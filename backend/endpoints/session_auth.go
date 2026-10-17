package endpoints

import (
	"context"
	"fmt"
	"log/slog"
	"money/backend/auth"
	"money/backend/data"
	"money/backend/state"
	"net/http"
	"time"
)

const (
	earlyRefreshWindow       = time.Minute
	refreshRetryCooldown     = 10 * time.Second
	backgroundRefreshTimeout = 15 * time.Second
	sessionRefreshTimeout    = 30 * time.Second
)

var (
	invalidAuth     = NewErr("invalid auth", http.StatusUnauthorized)
	authUnavailable = NewErr("authentication service unavailable", http.StatusServiceUnavailable)
)

func Authenticate(st *state.State, r *http.Request) (*AuthInfo, error) {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return nil, invalidAuth
	}

	session, err := st.Data.GetSessionByTokenHash(r.Context(), auth.HashToken(cookie.Value))
	if err != nil {
		return nil, NewUnexpectedErr("error getting session: %w", err)
	}
	if session == nil {
		return nil, invalidAuth
	}
	if session.IsDemo {
		return sessionInfo(session), nil
	}
	if session.TokensNeedVerification {
		return authenticateWithLock(r.Context(), st, session.ID)
	}

	accessToken, _, err := decryptSessionTokens(st, session)
	if err != nil {
		return nil, NewUnexpectedErr("error decrypting session tokens: %w", err)
	}
	claims, verifyErr := st.OIDC.VerifyAccess(r.Context(), accessToken)
	if auth.IsVerificationUnavailable(verifyErr) {
		return nil, authUnavailable
	}
	if verifyErr == nil && tokenMatchesSession(claims, session) {
		if time.Until(claims.Expiry) <= earlyRefreshWindow && retryReady(session) {
			startEarlyRefresh(st, session.ID)
		}
		return sessionInfo(session), nil
	}

	// The token expired or became invalid while another process may have been
	// refreshing it. Lock and reload the row before acting on this copy.
	return authenticateWithLock(r.Context(), st, session.ID)
}

func authenticateWithLock(requestCtx context.Context, st *state.State, sessionID string) (*AuthInfo, error) {
	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(st.Context, sessionRefreshTimeout)
	defer cancel()
	stopWaiting := context.AfterFunc(requestCtx, cancel)
	defer stopWaiting()

	lock, err := st.Data.LockSession(ctx, sessionID, false)
	stopWaiting()
	if lock != nil {
		defer lock.Rollback()
	}
	if requestErr := requestCtx.Err(); requestErr != nil {
		return nil, requestErr
	}
	if err != nil {
		return nil, NewUnexpectedErr("error locking session: %w", err)
	}
	if lock == nil {
		return nil, invalidAuth
	}
	return refreshLocked(ctx, requestCtx, st, lock, false)
}

func startEarlyRefresh(st *state.State, sessionID string) {
	if !st.BeginEarlyRefresh(sessionID) {
		return
	}
	go func() {
		defer st.EndEarlyRefresh(sessionID)
		ctx, cancel := context.WithTimeout(st.Context, backgroundRefreshTimeout)
		defer cancel()

		lock, err := st.Data.LockSession(ctx, sessionID, true)
		if err != nil {
			slog.WarnContext(ctx, "locking session for early token refresh", "session_id", sessionID, "err", err)
			return
		}
		if lock == nil {
			// The session was removed, or another process owns the optional
			// early-refresh work. A later request can retry if that work fails.
			return
		}
		defer lock.Rollback()

		if _, err := refreshLocked(ctx, ctx, st, lock, true); err != nil {
			slog.WarnContext(ctx, "early token refresh failed", "session_id", sessionID, "err", err)
		}
	}()
}

// refreshLocked rechecks the token from the locked row. early permits refresh
// while it is still valid; the blocking request path refreshes only if needed.
func refreshLocked(ctx, requestCtx context.Context, st *state.State, lock *data.SessionLock, early bool) (*AuthInfo, error) {
	session := lock.Session
	if !session.ExpiresAt.After(time.Now()) {
		return nil, revokeLockedSession(ctx, lock)
	}
	if session.IsDemo {
		if err := lock.Commit(); err != nil {
			return nil, NewUnexpectedErr("error releasing demo session lock: %w", err)
		}
		return sessionInfo(session), nil
	}

	accessToken, refreshToken, err := decryptSessionTokens(st, session)
	if err != nil {
		return nil, NewUnexpectedErr("error decrypting locked session tokens: %w", err)
	}
	var claims *auth.AccessClaims
	var verifyErr error
	if session.TokensNeedVerification {
		idToken := ""
		if session.PendingIDToken != "" {
			idToken, err = st.TokenCipher.Decrypt(session.PendingIDToken, session.ID+":id")
			if err != nil {
				return nil, NewUnexpectedErr("error decrypting pending id token: %w", err)
			}
		}
		claims, verifyErr = st.OIDC.VerifyPendingTokens(requestCtx, &auth.TokenSet{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			IDToken:      idToken,
		})
		if auth.IsVerificationUnavailable(verifyErr) {
			return nil, authUnavailable
		}
		if verifyErr != nil || !tokenMatchesSession(claims, session) {
			return nil, revokeLockedSession(ctx, lock)
		}
		if err := lock.SaveTokens(ctx, session.AccessToken, session.RefreshToken); err != nil {
			return nil, NewUnexpectedErr("error marking session tokens verified: %w", err)
		}
	} else {
		claims, verifyErr = st.OIDC.VerifyAccess(requestCtx, accessToken)
	}
	if verifyErr == nil {
		if !tokenMatchesSession(claims, session) {
			return nil, revokeLockedSession(ctx, lock)
		}
		isExpired := !claims.Expiry.After(time.Now())
		if !isExpired && (!early || time.Until(claims.Expiry) > earlyRefreshWindow || !retryReady(session)) {
			if err := lock.Commit(); err != nil {
				return nil, NewUnexpectedErr("error releasing session lock: %w", err)
			}
			return sessionInfo(session), nil
		}
	} else if auth.IsVerificationUnavailable(verifyErr) {
		return nil, authUnavailable
	} else if !auth.IsExpiredToken(verifyErr) {
		return nil, revokeLockedSession(ctx, lock)
	}

	if !retryReady(session) {
		if err := lock.Commit(); err != nil {
			return nil, NewUnexpectedErr("error releasing session retry lock: %w", err)
		}
		return nil, authUnavailable
	}
	if refreshToken == "" {
		return nil, revokeLockedSession(ctx, lock)
	}

	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, externalRequestTimeout)
	defer cancel()
	newTokens, err := st.OIDC.Refresh(refreshCtx, refreshToken)
	if err != nil {
		if newTokens != nil && auth.IsVerificationUnavailable(err) {
			encryptedAccess, encryptedRefresh, encryptErr := encryptSessionTokens(st, session.ID, newTokens)
			if encryptErr != nil {
				return nil, NewUnexpectedErr("error encrypting pending tokens: %w", encryptErr)
			}
			encryptedID := ""
			if newTokens.IDToken != "" {
				encryptedID, encryptErr = st.TokenCipher.Encrypt(newTokens.IDToken, session.ID+":id")
				if encryptErr != nil {
					return nil, NewUnexpectedErr("error encrypting pending id token: %w", encryptErr)
				}
			}
			if err := lock.SavePendingTokens(ctx, encryptedAccess, encryptedRefresh, encryptedID); err != nil {
				return nil, NewUnexpectedErr("error saving pending tokens: %w", err)
			}
			if err := lock.Commit(); err != nil {
				return nil, NewUnexpectedErr("error committing pending tokens: %w", err)
			}
			return nil, authUnavailable
		}
		if auth.IsInvalidGrant(err) || newTokens != nil {
			return nil, revokeLockedSession(ctx, lock)
		}
		retryAfter := time.Now().Add(refreshRetryCooldown)
		if saveErr := lock.SetRefreshRetryAfter(ctx, retryAfter); saveErr != nil {
			return nil, NewUnexpectedErr("error saving refresh retry time: %w", saveErr)
		}
		if commitErr := lock.Commit(); commitErr != nil {
			return nil, NewUnexpectedErr("error committing refresh retry time: %w", commitErr)
		}
		return nil, authUnavailable
	}
	if !tokenMatchesSession(&newTokens.AccessClaims, session) {
		return nil, revokeLockedSession(ctx, lock)
	}

	encryptedAccess, encryptedRefresh, err := encryptSessionTokens(st, session.ID, newTokens)
	if err != nil {
		return nil, NewUnexpectedErr("error encrypting refreshed tokens: %w", err)
	}
	if err := lock.SaveTokens(ctx, encryptedAccess, encryptedRefresh); err != nil {
		return nil, NewUnexpectedErr("error saving refreshed tokens: %w", err)
	}
	if err := lock.Commit(); err != nil {
		return nil, NewUnexpectedErr("error committing refreshed tokens: %w", err)
	}
	return sessionInfo(session), nil
}

func revokeLockedSession(ctx context.Context, lock *data.SessionLock) error {
	if err := lock.Delete(ctx); err != nil {
		return NewUnexpectedErr("error revoking session: %w", err)
	}
	if err := lock.Commit(); err != nil {
		return NewUnexpectedErr("error committing session revocation: %w", err)
	}
	return invalidAuth
}

func decryptSessionTokens(st *state.State, session *data.Session) (string, string, error) {
	if session.AccessToken == "" || session.RefreshToken == "" {
		return "", "", fmt.Errorf("oidc session has no provider tokens")
	}
	accessToken, err := st.TokenCipher.Decrypt(session.AccessToken, session.ID+":access")
	if err != nil {
		return "", "", err
	}
	refreshToken, err := st.TokenCipher.Decrypt(session.RefreshToken, session.ID+":refresh")
	if err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

func encryptSessionTokens(st *state.State, sessionID string, tokens *auth.TokenSet) (string, string, error) {
	accessToken, err := st.TokenCipher.Encrypt(tokens.AccessToken, sessionID+":access")
	if err != nil {
		return "", "", err
	}
	refreshToken, err := st.TokenCipher.Encrypt(tokens.RefreshToken, sessionID+":refresh")
	if err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

func tokenMatchesSession(claims *auth.AccessClaims, session *data.Session) bool {
	return claims.Subject == session.Subject && claims.Issuer == session.Issuer
}

func retryReady(session *data.Session) bool {
	return session.RefreshRetryAfter == nil || !time.Now().Before(*session.RefreshRetryAfter)
}

func sessionInfo(session *data.Session) *AuthInfo {
	return &AuthInfo{UserID: session.UserID, Subject: session.Subject, SessionID: session.ID}
}

func GetUserIDMiddleware(st *state.State) GetUserID {
	return func(r *http.Request) (string, error) {
		info, err := Authenticate(st, r)
		if err != nil {
			return "", err
		}
		return info.UserID, nil
	}
}

func GetEnableBankingUserIDMiddleware(st *state.State) GetUserID {
	return func(r *http.Request) (string, error) {
		info, err := Authenticate(st, r)
		if err != nil {
			return "", err
		}
		if !st.Config.EnableBanking.AllowsSubject(info.Subject) {
			return "", NewErr("Enable Banking is not available for this account", http.StatusForbidden)
		}
		return info.UserID, nil
	}
}

type GetUserID func(r *http.Request) (string, error)

type AuthInfo struct {
	UserID    string
	Subject   string
	SessionID string
}
