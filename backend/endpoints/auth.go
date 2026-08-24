package endpoints

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"money/backend/auth"
	"money/backend/data"
	"money/backend/state"
	"net/http"
	"time"
)

const sessionDuration = 7 * 24 * time.Hour

// flowState is the short-lived per-login state stored in the flow cookie and
// checked when the provider redirects back to the callback.
type flowState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

// HandleLogin starts the OIDC authorization-code flow: it generates state,
// nonce and a PKCE verifier, stores them in a short-lived cookie and redirects
// the user to the provider.
func HandleLogin(st *state.State) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		stateTok, err := auth.RandomString()
		if err != nil {
			return NewUnexpectedErr("error generating state: %w", err)
		}
		nonce, err := auth.RandomString()
		if err != nil {
			return NewUnexpectedErr("error generating nonce: %w", err)
		}
		verifier := auth.NewPKCEVerifier()

		flow, err := json.Marshal(flowState{State: stateTok, Nonce: nonce, Verifier: verifier})
		if err != nil {
			return NewUnexpectedErr("error encoding flow state: %w", err)
		}

		http.SetCookie(w, auth.CreateFlowCookie(base64.RawURLEncoding.EncodeToString(flow), st.Config.SecureCookies()))
		http.Redirect(w, r, st.OIDC.AuthCodeURL(stateTok, nonce, verifier), http.StatusFound)
		return nil
	}
}

// HandleCallback completes the OIDC flow: it validates state, exchanges the
// code, verifies the ID token, upserts the user, issues a session and redirects
// back to the frontend.
func HandleCallback(st *state.State) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		cookie, err := r.Cookie(auth.FlowCookieName)
		if err != nil {
			return NewErr("missing oidc flow cookie", http.StatusBadRequest)
		}

		raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
		if err != nil {
			return NewErr("invalid oidc flow cookie", http.StatusBadRequest)
		}
		var flow flowState
		if err := json.Unmarshal(raw, &flow); err != nil {
			return NewErr("invalid oidc flow cookie", http.StatusBadRequest)
		}

		if r.URL.Query().Get("state") != flow.State {
			return NewErr("oidc state mismatch", http.StatusBadRequest)
		}

		ctx, cancel := context.WithTimeout(r.Context(), externalRequestTimeout)
		defer cancel()
		claims, err := st.OIDC.Exchange(ctx, r.URL.Query().Get("code"), flow.Nonce, flow.Verifier)
		if err != nil {
			return NewErr("oidc verification failed", http.StatusUnauthorized)
		}

		user, err := st.Data.GetUserBySubject(r.Context(), claims.Issuer, claims.Subject)
		if err != nil {
			return NewUnexpectedErr("error getting user: %w", err)
		}
		if user == nil {
			user = &data.User{
				ID:        data.NewPrivateID(),
				Subject:   claims.Subject,
				Issuer:    claims.Issuer,
				Email:     claims.Email,
				CreatedAt: time.Now(),
			}
			if err := st.Data.CreateUser(r.Context(), *user); err != nil {
				return NewUnexpectedErr("error inserting user: %w", err)
			}
		}

		if err := issueSession(w, st, r, user.ID); err != nil {
			return err
		}

		http.SetCookie(w, auth.ClearFlowCookie(st.Config.SecureCookies()))
		http.Redirect(w, r, st.Config.EffectiveFrontUrl(), http.StatusFound)
		return nil
	}
}

func HandleLogout(state *state.State) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		info, err := Authenticate(state, r)
		if err != nil {
			return err
		}

		if err := state.Data.DeleteSession(r.Context(), info.SessionID); err != nil {
			return NewUnexpectedErr("error deleting session: %w", err)
		}

		expired := time.Unix(0, 0)
		http.SetCookie(w, auth.CreateCookie("", &expired, state.Config.SecureCookies()))

		Json(w, JSON{"ok": true})
		return nil
	}
}

// issueSession creates a session row keyed by the hash of a new opaque token
// and sets that token as the auth cookie. Call it once the OIDC identity is
// verified.
func issueSession(w http.ResponseWriter, st *state.State, r *http.Request, userID string) error {
	rawToken, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		return NewUnexpectedErr("error generating session token: %w", err)
	}

	now := time.Now()
	expiry := now.Add(sessionDuration)

	session := data.Session{
		ID:        data.NewPrivateID(),
		UserID:    userID,
		TokenHash: tokenHash,
		CreatedAt: now,
		ExpiresAt: expiry,
	}
	if err := st.Data.InsertSession(r.Context(), session); err != nil {
		return NewUnexpectedErr("error inserting session: %w", err)
	}

	http.SetCookie(w, auth.CreateCookie(rawToken, &expiry, st.Config.SecureCookies()))
	return nil
}
