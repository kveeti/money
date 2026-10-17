package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Sessions struct {
	*sql.DB
}

func NewSessions(s *sql.DB) *Sessions {
	return &Sessions{DB: s}
}

type Session struct {
	ID                     string
	UserID                 string
	Subject                string
	Issuer                 string
	IsDemo                 bool
	TokenHash              string
	AccessToken            string
	RefreshToken           string
	TokensNeedVerification bool
	PendingIDToken         string
	RefreshRetryAfter      *time.Time
	CreatedAt              time.Time
	ExpiresAt              time.Time
}

func (s *Sessions) InsertSession(ctx context.Context, session Session) error {
	_, err := s.ExecContext(ctx, `
		insert into sessions (
			id, user_id, token_hash, access_token, refresh_token,
			created_at, expires_at
		)
		values ($1, $2, $3, $4, $5, $6, $7)
	`, session.ID, session.UserID, session.TokenHash,
		nullableString(session.AccessToken), nullableString(session.RefreshToken),
		session.CreatedAt.UTC(), session.ExpiresAt.UTC())
	return err
}

// GetSessionByTokenHash returns the unexpired session matching the token hash,
// or nil if none exists or it has expired.
func (s *Sessions) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	return scanSession(s.QueryRowContext(ctx, sessionSelect+`
		where session.token_hash = $1
		  and session.expires_at > $2
		limit 1
	`, tokenHash, time.Now().UTC()))
}

// SessionLock owns a row lock until Commit or Rollback. Callers must always
// release it, normally with a deferred Rollback followed by Commit on success.
type SessionLock struct {
	tx      *sql.Tx
	Session *Session
}

// LockSession starts a transaction and reloads the latest session row while
// locking it. skipLocked is for optional background work only.
func (s *Sessions) LockSession(ctx context.Context, sessionID string, skipLocked bool) (*SessionLock, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin session lock: %w", err)
	}

	query := sessionSelect + `
		where session.id = $1
		  and session.expires_at > $2
		for update of session`
	if skipLocked {
		query += ` skip locked`
	}

	session, err := scanSession(tx.QueryRowContext(ctx, query, sessionID, time.Now().UTC()))
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if session == nil {
		tx.Rollback()
		return nil, nil
	}
	return &SessionLock{tx: tx, Session: session}, nil
}

func (l *SessionLock) SaveTokens(ctx context.Context, accessToken, refreshToken string) error {
	_, err := l.tx.ExecContext(ctx, `
		update sessions
		set access_token = $2,
		    refresh_token = $3,
		    refresh_retry_after = null,
		    tokens_need_verification = false,
		    pending_id_token = null
		where id = $1
	`, l.Session.ID, accessToken, refreshToken)
	if err == nil {
		l.Session.AccessToken = accessToken
		l.Session.RefreshToken = refreshToken
		l.Session.RefreshRetryAfter = nil
		l.Session.TokensNeedVerification = false
		l.Session.PendingIDToken = ""
	}
	return err
}

func (l *SessionLock) SavePendingTokens(ctx context.Context, accessToken, refreshToken, idToken string) error {
	_, err := l.tx.ExecContext(ctx, `
		update sessions
		set access_token = $2,
		    refresh_token = $3,
		    pending_id_token = $4,
		    tokens_need_verification = true,
		    refresh_retry_after = null
		where id = $1
	`, l.Session.ID, accessToken, refreshToken, nullableString(idToken))
	return err
}

func (l *SessionLock) SetRefreshRetryAfter(ctx context.Context, retryAfter time.Time) error {
	_, err := l.tx.ExecContext(ctx, `
		update sessions
		set refresh_retry_after = $2
		where id = $1
	`, l.Session.ID, retryAfter.UTC())
	if err == nil {
		l.Session.RefreshRetryAfter = &retryAfter
	}
	return err
}

func (l *SessionLock) Delete(ctx context.Context) error {
	_, err := l.tx.ExecContext(ctx, `delete from sessions where id = $1`, l.Session.ID)
	return err
}

func (l *SessionLock) Commit() error {
	return l.tx.Commit()
}

func (l *SessionLock) Rollback() error {
	return l.tx.Rollback()
}

func (s *Sessions) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.ExecContext(ctx, `delete from sessions where token_hash = $1`, tokenHash)
	return err
}

const sessionSelect = `
	select session.id,
	       session.user_id,
	       users.subject,
	       users.issuer,
	       users.is_demo,
	       session.token_hash,
	       coalesce(session.access_token, ''),
	       coalesce(session.refresh_token, ''),
	       session.refresh_retry_after,
	       session.tokens_need_verification,
	       coalesce(session.pending_id_token, ''),
	       session.created_at,
	       session.expires_at
	from sessions session
	join users on users.id = session.user_id
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (*Session, error) {
	var session Session
	var retryAfter sql.NullTime
	err := row.Scan(
		&session.ID, &session.UserID, &session.Subject, &session.Issuer,
		&session.IsDemo, &session.TokenHash, &session.AccessToken,
		&session.RefreshToken, &retryAfter, &session.TokensNeedVerification,
		&session.PendingIDToken, &session.CreatedAt, &session.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if retryAfter.Valid {
		session.RefreshRetryAfter = &retryAfter.Time
	}
	return &session, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
