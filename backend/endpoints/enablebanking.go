package endpoints

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"money/backend/data"
	"money/backend/enablebanking"
	"money/backend/state"
	"net/http"
	"strings"
	"time"
)

const (
	enableBankingProvider = data.EnableBankingSource
	ebStateCookie         = "money_eb_state"
	ebAttemptCookie       = "money_eb_attempt"
)

type enableBankingData struct {
	SessionID string                 `json:"session_id"`
	Bank      string                 `json:"bank"`
	Country   string                 `json:"country"`
	PSUType   string                 `json:"psu_type"`
	ExpiresAt time.Time              `json:"expires_at"`
	Accounts  []enableBankingAccount `json:"accounts"`
}

type enableBankingAccount struct {
	UID                string `json:"uid"`
	IdentificationHash string `json:"identification_hash"`
	IBAN               string `json:"iban"`
	Name               string `json:"name"`
	Currency           string `json:"currency"`
}

type ebAttempt struct {
	IntegrationID string `json:"integration_id,omitempty"`
	Bank          string `json:"bank"`
	Country       string `json:"country"`
	PSUType       string `json:"psu_type"`
}

func enableBankingClient(s *state.State) (*enablebanking.Client, error) {
	if s.EnableBankingClient == nil {
		return nil, NewErr("Enable Banking is not configured", http.StatusNotFound)
	}
	return s.EnableBankingClient, nil
}

func HandleListEnableBankingBanks(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if _, err := getUserID(r); err != nil {
			return err
		}
		country := strings.ToUpper(r.URL.Query().Get("country"))
		if len(country) != 2 {
			return NewErr("country is required", http.StatusBadRequest)
		}
		psuType := r.URL.Query().Get("psu_type")
		if psuType == "" {
			psuType = "personal"
		}
		client, err := enableBankingClient(s)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), externalRequestTimeout)
		defer cancel()
		banks, err := client.ListASPSPs(ctx, country, psuType)
		if err != nil {
			return NewUnexpectedErr("list Enable Banking banks: %w", err)
		}
		query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		out := make([]enablebanking.ASPSP, 0, len(banks))
		for _, bank := range banks {
			if query == "" || strings.Contains(strings.ToLower(bank.Name), query) {
				out = append(out, bank)
			}
		}
		Json(w, out)
		return nil
	}
}

func HandleStartEnableBanking(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		attempt := ebAttempt{Bank: r.URL.Query().Get("bank"), Country: strings.ToUpper(r.URL.Query().Get("country")), PSUType: r.URL.Query().Get("psu_type")}
		if attempt.PSUType == "" {
			attempt.PSUType = "personal"
		}
		if id := r.URL.Query().Get("integration_id"); id != "" {
			integration, err := s.Data.GetBankIntegration(r.Context(), userID, id, enableBankingProvider)
			if err != nil {
				return mapIntegrationError(err)
			}
			var saved enableBankingData
			if err := json.Unmarshal(integration.Data, &saved); err != nil {
				return NewUnexpectedErr("decode Enable Banking integration: %w", err)
			}
			attempt = ebAttempt{IntegrationID: id, Bank: saved.Bank, Country: saved.Country, PSUType: saved.PSUType}
		}
		if attempt.Bank == "" || len(attempt.Country) != 2 || (attempt.PSUType != "personal" && attempt.PSUType != "business") {
			return NewErr("invalid bank authorization request", http.StatusBadRequest)
		}
		client, err := enableBankingClient(s)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), externalRequestTimeout)
		defer cancel()
		banks, err := client.ListASPSPs(ctx, attempt.Country, attempt.PSUType)
		if err != nil {
			return NewUnexpectedErr("list Enable Banking banks: %w", err)
		}
		var selected *enablebanking.ASPSP
		for i := range banks {
			if banks[i].Name == attempt.Bank && banks[i].Country == attempt.Country {
				selected = &banks[i]
				break
			}
		}
		if selected == nil {
			return NewErr("bank not found", http.StatusBadRequest)
		}
		stateValue, err := randomHex(32)
		if err != nil {
			return NewUnexpectedErr("authorization state: %w", err)
		}
		redirectURL := s.Config.BackendUrl + "/api/v1/enablebanking/callback"
		slog.InfoContext(ctx, "starting Enable Banking authorization", "redirect_url", redirectURL)
		authURL, err := client.StartAuthorization(ctx, *selected, attempt.PSUType, stateValue, redirectURL)
		if err != nil {
			return NewUnexpectedErr("start Enable Banking authorization: %w", err)
		}
		encodedAttempt, _ := json.Marshal(attempt)
		http.SetCookie(w, authCookie(ebStateCookie, stateValue, s.Config.SecureCookies()))
		http.SetCookie(w, authCookie(ebAttemptCookie, base64.RawURLEncoding.EncodeToString(encodedAttempt), s.Config.SecureCookies()))
		http.Redirect(w, r, authURL, http.StatusSeeOther)
		return nil
	}
}

func HandleEnableBankingCallback(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		stateCookie, err := r.Cookie(ebStateCookie)
		if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
			return NewErr("invalid bank authorization state", http.StatusBadRequest)
		}
		attemptCookie, err := r.Cookie(ebAttemptCookie)
		if err != nil {
			return NewErr("bank authorization expired", http.StatusBadRequest)
		}
		rawAttempt, err := base64.RawURLEncoding.DecodeString(attemptCookie.Value)
		if err != nil {
			return NewErr("invalid bank authorization", http.StatusBadRequest)
		}
		var attempt ebAttempt
		if json.Unmarshal(rawAttempt, &attempt) != nil {
			return NewErr("invalid bank authorization", http.StatusBadRequest)
		}
		if apiError := r.URL.Query().Get("error"); apiError != "" {
			return NewErr("bank authorization failed: "+apiError, http.StatusBadRequest)
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			return NewErr("bank authorization returned no code", http.StatusBadRequest)
		}
		client, err := enableBankingClient(s)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), externalRequestTimeout)
		defer cancel()
		session, err := client.CreateSession(ctx, code)
		if err != nil {
			return NewUnexpectedErr("create Enable Banking session: %w", err)
		}
		saved := enableBankingData{SessionID: session.SessionID, Bank: attempt.Bank, Country: attempt.Country, PSUType: attempt.PSUType, ExpiresAt: session.Access.ValidUntil}
		var ibans []string
		for _, account := range session.Accounts {
			iban := data.NormalizeIBAN(account.AccountID.IBAN)
			if !data.ValidIBAN(iban) || account.UID == "" || account.IdentificationHash == "" {
				continue
			}
			saved.Accounts = append(saved.Accounts, enableBankingAccount{
				UID: account.UID, IdentificationHash: account.IdentificationHash,
				IBAN: iban, Name: account.Name, Currency: account.Currency,
			})
			ibans = append(ibans, iban)
		}
		encoded, _ := json.Marshal(saved)
		if attempt.IntegrationID == "" {
			integration := data.BankIntegration{ID: data.NewPrivateID(), UserID: userID, Provider: enableBankingProvider, Data: encoded}
			if err := s.Data.CreateBankIntegration(r.Context(), integration, ibans); err != nil {
				return NewUnexpectedErr("save Enable Banking integration: %w", err)
			}
			attempt.IntegrationID = integration.ID
		} else {
			old, err := s.Data.GetBankIntegration(r.Context(), userID, attempt.IntegrationID, enableBankingProvider)
			if err != nil {
				return mapIntegrationError(err)
			}
			var oldData enableBankingData
			_ = json.Unmarshal(old.Data, &oldData)
			if err := s.Data.UpdateBankIntegration(r.Context(), userID, attempt.IntegrationID, enableBankingProvider, encoded); err != nil {
				return mapIntegrationError(err)
			}
			if oldData.SessionID != "" && oldData.SessionID != saved.SessionID {
				_ = client.DeleteSession(ctx, oldData.SessionID)
			}
		}
		expireAuthCookies(w, s.Config.SecureCookies())
		http.Redirect(w, r, "/connections", http.StatusSeeOther)
		return nil
	}
}

func HandleListEnableBankingConnections(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		integrations, err := s.Data.ListBankIntegrations(r.Context(), userID, enableBankingProvider)
		if err != nil {
			return NewUnexpectedErr("list bank integrations: %w", err)
		}
		buckets, err := s.Data.ListBuckets(r.Context(), userID)
		if err != nil {
			return NewUnexpectedErr("list buckets: %w", err)
		}
		bucketByIBAN := map[string]data.Bucket{}
		for _, bucket := range buckets {
			if bucket.IBAN != nil && bucket.ActiveBankIntegrationID != nil {
				bucketByIBAN[*bucket.IBAN] = bucket
			}
		}
		out := make([]JSON, 0, len(integrations))
		for _, integration := range integrations {
			var saved enableBankingData
			if err := json.Unmarshal(integration.Data, &saved); err != nil {
				return NewUnexpectedErr("decode Enable Banking integration: %w", err)
			}
			accounts := make([]JSON, len(saved.Accounts))
			for i, account := range saved.Accounts {
				accounts[i] = JSON{"uid": account.UID, "iban": account.IBAN, "name": account.Name, "currency": account.Currency}
				if bucket, ok := bucketByIBAN[account.IBAN]; ok && bucket.ActiveBankIntegrationID != nil && *bucket.ActiveBankIntegrationID == integration.ID {
					accounts[i]["bucket_id"] = bucket.ID
					accounts[i]["bucket_name"] = bucket.Name
				}
			}
			out = append(out, JSON{"id": integration.ID, "bank": saved.Bank, "country": saved.Country, "psu_type": saved.PSUType, "expires_at": saved.ExpiresAt, "accounts": accounts})
		}
		Json(w, out)
		return nil
	}
}

func HandleMapEnableBankingAccount(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		integration, err := s.Data.GetBankIntegration(r.Context(), userID, r.PathValue("id"), enableBankingProvider)
		if err != nil {
			return mapIntegrationError(err)
		}
		var saved enableBankingData
		if json.Unmarshal(integration.Data, &saved) != nil {
			return NewUnexpectedErr("decode Enable Banking integration")
		}
		var body struct {
			BucketID string `json:"bucket_id"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.BucketID == "" {
			return NewErr("bucket_id is required", http.StatusBadRequest)
		}
		var account *enableBankingAccount
		for i := range saved.Accounts {
			if saved.Accounts[i].UID == r.PathValue("uid") {
				account = &saved.Accounts[i]
			}
		}
		if account == nil {
			return NewErr("bank account not found", http.StatusNotFound)
		}
		if err := s.Data.MapIntegrationAccount(r.Context(), userID, integration.ID, body.BucketID, account.IBAN); err != nil {
			return mapIntegrationError(err)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func HandleEnableBankingSyncStatus(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		active, err := s.Data.HasActiveEnableBankingSyncs(r.Context(), userID)
		if err != nil {
			return NewUnexpectedErr("check Enable Banking sync status: %w", err)
		}
		Json(w, JSON{"syncing": active})
		return nil
	}
}

func HandleSyncEnableBankingAccount(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		if s.EnableBankingSyncer == nil {
			return NewErr("Enable Banking sync worker is unavailable", http.StatusServiceUnavailable)
		}
		batchIDs, err := s.Data.EnqueueEnableBankingSyncs(
			r.Context(),
			userID,
			[]data.EnableBankingSyncTarget{{
				IntegrationID: r.PathValue("id"),
				AccountUID:    r.PathValue("uid"),
			}},
			time.Now().UTC(),
		)
		if err != nil {
			return mapBankSyncError(err)
		}
		s.EnableBankingSyncer.Kick()
		JsonStatus(w, http.StatusAccepted, JSON{"batch_id": batchIDs[0], "status": "queued"})
		return nil
	}
}

func HandleSyncAllEnableBankingAccounts(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		if s.EnableBankingSyncer == nil {
			return NewErr("Enable Banking sync worker is unavailable", http.StatusServiceUnavailable)
		}
		batchIDs, err := s.Data.EnqueueEnableBankingSyncs(
			r.Context(),
			userID,
			[]data.EnableBankingSyncTarget{{
				IntegrationID: r.PathValue("id"),
				AllAccounts:   true,
			}},
			time.Now().UTC(),
		)
		if err != nil {
			return mapBankSyncError(err)
		}
		s.EnableBankingSyncer.Kick()
		JsonStatus(w, http.StatusAccepted, JSON{"batch_ids": batchIDs})
		return nil
	}
}

func HandleSyncSelectedEnableBankingAccounts(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		if s.EnableBankingSyncer == nil {
			return NewErr("Enable Banking sync worker is unavailable", http.StatusServiceUnavailable)
		}
		var body struct {
			Accounts []struct {
				ConnectionID string `json:"connection_id"`
				AccountUID   string `json:"account_uid"`
			} `json:"accounts"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Accounts) == 0 {
			return NewErr("select at least one bank account", http.StatusBadRequest)
		}

		targets := make([]data.EnableBankingSyncTarget, len(body.Accounts))
		for i, selected := range body.Accounts {
			targets[i] = data.EnableBankingSyncTarget{
				IntegrationID: selected.ConnectionID,
				AccountUID:    selected.AccountUID,
			}
		}
		batchIDs, err := s.Data.EnqueueEnableBankingSyncs(r.Context(), userID, targets, time.Now().UTC())
		if err != nil {
			return mapBankSyncError(err)
		}
		s.EnableBankingSyncer.Kick()
		JsonStatus(w, http.StatusAccepted, JSON{"batch_ids": batchIDs, "status": "queued"})
		return nil
	}
}

func HandleDeleteEnableBankingConnection(s *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		integration, err := s.Data.GetBankIntegration(r.Context(), userID, r.PathValue("id"), enableBankingProvider)
		if err != nil {
			return mapIntegrationError(err)
		}
		var saved enableBankingData
		_ = json.Unmarshal(integration.Data, &saved)
		if err := s.Data.DeleteBankIntegration(r.Context(), userID, integration.ID, enableBankingProvider); err != nil {
			return mapIntegrationError(err)
		}
		if client, err := enableBankingClient(s); err == nil && saved.SessionID != "" {
			ctx, cancel := context.WithTimeout(r.Context(), externalRequestTimeout)
			defer cancel()
			_ = client.DeleteSession(ctx, saved.SessionID)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func mapBankSyncError(err error) error {
	switch {
	case errors.Is(err, data.ErrBankSyncInProgress):
		return NewErr(err.Error(), http.StatusConflict)
	case errors.Is(err, data.ErrIntegrationNotFound), errors.Is(err, data.ErrBankSyncAccountNotFound):
		return NewErr(err.Error(), http.StatusNotFound)
	case errors.Is(err, data.ErrIntegrationAccount), errors.Is(err, data.ErrBankSyncDuplicateAccount):
		return NewErr(err.Error(), http.StatusBadRequest)
	default:
		return NewUnexpectedErr("queue bank sync: %w", err)
	}
}

func mapIntegrationError(err error) error {
	switch {
	case errors.Is(err, data.ErrIntegrationNotFound):
		return NewErr(err.Error(), http.StatusNotFound)
	case errors.Is(err, data.ErrIntegrationAccount):
		return NewErr(err.Error(), http.StatusBadRequest)
	case errors.Is(err, data.ErrIntegrationAccountImported), errors.Is(err, data.ErrBankSyncInProgress):
		return NewErr(err.Error(), http.StatusConflict)
	default:
		return NewUnexpectedErr("bank integration: %w", err)
	}
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func authCookie(name, value string, secure bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/api/v1/enablebanking", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 600}
}

func expireAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{ebStateCookie, ebAttemptCookie} {
		cookie := authCookie(name, "", secure)
		cookie.MaxAge = -1
		http.SetCookie(w, cookie)
	}
}
