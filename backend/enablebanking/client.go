package enablebanking

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	origin string
	appID  string
	key    *rsa.PrivateKey
	http   *http.Client
	now    func() time.Time
}

type ASPSP struct {
	Name                   string `json:"name"`
	Country                string `json:"country"`
	MaximumConsentValidity int64  `json:"maximum_consent_validity"`
}

type Account struct {
	UID                string `json:"uid"`
	IdentificationHash string `json:"identification_hash"`
	AccountID          struct {
		IBAN string `json:"iban"`
	} `json:"account_id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

type Session struct {
	SessionID string    `json:"session_id"`
	Accounts  []Account `json:"accounts"`
	ASPSP     struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"aspsp"`
	PSUType string `json:"psu_type"`
	Access  struct {
		ValidUntil time.Time `json:"valid_until"`
	} `json:"access"`
}

type Transaction struct {
	TransactionAmount struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	} `json:"transaction_amount"`
	Creditor *struct {
		Name string `json:"name"`
	} `json:"creditor"`
	Debtor *struct {
		Name string `json:"name"`
	} `json:"debtor"`
	CreditDebitIndicator  string   `json:"credit_debit_indicator"`
	BookingDate           string   `json:"booking_date"`
	TransactionDate       string   `json:"transaction_date"`
	ValueDate             string   `json:"value_date"`
	Note                  string   `json:"note"`
	RemittanceInformation []string `json:"remittance_information"`
	Status                string   `json:"status"`
}

type TransactionPage struct {
	Transactions    []Transaction `json:"transactions"`
	ContinuationKey string        `json:"continuation_key"`
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("Enable Banking %s (%d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("Enable Banking error %d: %s", e.Status, e.Message)
}

func New(applicationID, privateKeyPath, origin string) (*Client, error) {
	raw, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read Enable Banking private key: %w", err)
	}
	key, err := parsePrivateKey(raw)
	if err != nil {
		return nil, err
	}
	return &Client{origin: strings.TrimRight(origin, "/"), appID: applicationID, key: key, http: &http.Client{Timeout: 90 * time.Second}, now: time.Now}, nil
}

func (c *Client) ListASPSPs(ctx context.Context, country, psuType string) ([]ASPSP, error) {
	var response struct {
		ASPSPs []ASPSP `json:"aspsps"`
	}
	err := c.request(ctx, http.MethodGet, "/aspsps", url.Values{"country": {country}, "psu_type": {psuType}, "service": {"AIS"}}, nil, &response)
	return response.ASPSPs, err
}

func (c *Client) StartAuthorization(ctx context.Context, aspsp ASPSP, psuType, state, redirectURL string) (string, error) {
	validity := time.Duration(aspsp.MaximumConsentValidity) * time.Second
	if validity > time.Minute {
		validity -= time.Minute
	}
	body := map[string]any{
		"access": map[string]any{"transactions": true, "valid_until": c.now().UTC().Add(validity).Format(time.RFC3339)},
		"aspsp":  map[string]string{"name": aspsp.Name, "country": aspsp.Country},
		"state":  state, "redirect_url": redirectURL, "psu_type": psuType,
	}
	var response struct {
		URL string `json:"url"`
	}
	if err := c.request(ctx, http.MethodPost, "/auth", nil, body, &response); err != nil {
		return "", err
	}
	if response.URL == "" {
		return "", errors.New("Enable Banking authorization response has no URL")
	}
	return response.URL, nil
}

func (c *Client) CreateSession(ctx context.Context, code string) (Session, error) {
	var session Session
	err := c.request(ctx, http.MethodPost, "/sessions", nil, map[string]string{"code": code}, &session)
	return session, err
}

func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.request(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(sessionID), nil, nil, nil)
}

func (c *Client) LongestTransactions(ctx context.Context, accountUID, from, continuation string) (TransactionPage, error) {
	if from == "" {
		return TransactionPage{}, errors.New("transaction start date is required")
	}
	query := url.Values{
		"transaction_status": {"BOOK"},
		"strategy":           {"longest"},
		"date_from":          {from},
	}
	if continuation != "" {
		query.Set("continuation_key", continuation)
	}
	var page TransactionPage
	err := c.request(ctx, http.MethodGet, "/accounts/"+url.PathEscape(accountUID)+"/transactions", query, nil, &page)
	return page, err
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body, output any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := c.origin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	token, err := c.token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	slog.InfoContext(ctx, "Enable Banking request", "method", method)
	response, err := c.http.Do(req)
	if err != nil {
		safeErr := err
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			safeErr = urlErr.Err
		}
		slog.ErrorContext(ctx, "Enable Banking request failed", "method", method, "err", safeErr)
		return fmt.Errorf("Enable Banking request failed: %w", safeErr)
	}
	slog.InfoContext(ctx, "Enable Banking response", "method", method, "status", response.StatusCode)
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var api struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &api)
		if api.Message == "" {
			api.Message = strings.TrimSpace(string(raw))
		}
		return &APIError{Status: response.StatusCode, Code: api.Error, Message: api.Message}
	}
	if output == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode Enable Banking response: %w", err)
	}
	return nil
}

func (c *Client) token() (string, error) {
	now := c.now().UTC().Unix()
	header, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "RS256", "kid": c.appID})
	claims, _ := json.Marshal(map[string]any{"iss": "enablebanking.com", "aud": "api.enablebanking.com", "iat": now, "exp": now + 300})
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + encode(signature), nil
}

func parsePrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("Enable Banking private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("Enable Banking private key is not RSA")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("Enable Banking private key is not RSA")
	}
	return key, nil
}
