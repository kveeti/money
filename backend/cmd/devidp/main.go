// devidp is a mock OIDC provider for local development. It implements just
// enough of the spec (discovery, JWKS, authorize, token) that the real backend
// auth path talks to it unmodified — no PKCE/signature bypass on our side. Never
// run in production: it logs in anyone with no password.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type user struct {
	Sub   string
	Email string
}

// seeded personas match the app's sharing use case: alice & bob are mutual
// counterparts for testing debt mirrors; add more from the picker as needed.
var seeded = []user{
	{Sub: "alice", Email: "alice@dev.local"},
	{Sub: "bob", Email: "bob@dev.local"},
}

type server struct {
	issuer string
	key    *rsa.PrivateKey

	mu      sync.Mutex
	codes   map[string]pending // one-time auth code -> what /token should mint
	refresh map[string]pending // refresh token -> identity and client
	users   []user
}

// pending is the authorize-time state a code redeems for at the token endpoint.
type pending struct {
	sub      string
	email    string
	nonce    string
	clientID string
}

func main() {
	port := env("DEVIDP_PORT", "5557")
	addr := env("DEVIDP_ADDR", ":"+port)
	issuer := env("DEVIDP_ISSUER", "http://localhost:"+port)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("devidp: generating key: %v", err)
	}

	s := &server{
		issuer:  issuer,
		key:     key,
		codes:   map[string]pending{},
		refresh: map[string]pending{},
		users:   seeded,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("/jwks.json", s.jwks)
	mux.HandleFunc("/authorize", s.authorize)
	mux.HandleFunc("/token", s.token)

	log.Printf("devidp: issuer %s listening on %s", issuer, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (s *server) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                s.issuer,
		"authorization_endpoint":                s.issuer + "/authorize",
		"token_endpoint":                        s.issuer + "/token",
		"jwks_uri":                              s.issuer + "/jwks.json",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email"},
	})
}

func (s *server) jwks(w http.ResponseWriter, r *http.Request) {
	pub := s.key.Public().(*rsa.PublicKey)
	writeJSON(w, map[string]any{"keys": []map[string]any{{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": "dev",
		"n":   b64(pub.N.Bytes()),
		"e":   b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// authorize either shows the user picker (no ?sub) or issues a code and redirects
// straight back (?sub=alice), which covers both manual and scripted logins.
func (s *server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI, state, nonce := q.Get("redirect_uri"), q.Get("state"), q.Get("nonce")
	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}

	sub := q.Get("sub")
	if sub == "" {
		s.picker(w, q)
		return
	}

	email := q.Get("email")
	if email == "" {
		email = sub + "@dev.local"
	}

	code := randString()
	s.mu.Lock()
	s.codes[code] = pending{sub: sub, email: email, nonce: nonce}
	if !hasSub(s.users, sub) {
		s.users = append(s.users, user{Sub: sub, Email: email})
	}
	s.mu.Unlock()

	dst, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	rq := dst.Query()
	rq.Set("code", code)
	rq.Set("state", state)
	dst.RawQuery = rq.Encode()
	http.Redirect(w, r, dst.String(), http.StatusFound)
}

func (s *server) picker(w http.ResponseWriter, q url.Values) {
	// preserve the OIDC params so a picked user re-hits /authorize with ?sub added.
	var pairs [][2]string
	for _, k := range []string{"redirect_uri", "state", "nonce", "client_id", "scope", "code_challenge", "code_challenge_method"} {
		if v := q.Get(k); v != "" {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	s.mu.Lock()
	users := append([]user(nil), s.users...)
	s.mu.Unlock()

	// Build each link's full URL here: html/template only percent-escapes a value
	// spliced into a query, mangling the & separators. A whole URL it just normalizes.
	type link struct{ Sub, Email, Href string }
	var links []link
	for _, u := range users {
		vals := url.Values{}
		for _, p := range pairs {
			vals.Set(p[0], p[1])
		}
		vals.Set("sub", u.Sub)
		links = append(links, link{u.Sub, u.Email, "/authorize?" + vals.Encode()})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pickerTmpl.Execute(w, map[string]any{"Links": links, "Pairs": pairs})
}

func (s *server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request")
		return
	}

	clientID := r.Form.Get("client_id")
	if clientID == "" {
		clientID, _, _ = r.BasicAuth()
	}

	var p pending
	var refreshToken string
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		code := r.Form.Get("code")
		s.mu.Lock()
		var ok bool
		p, ok = s.codes[code]
		delete(s.codes, code) // one-time use
		if ok {
			p.clientID = clientID
			refreshToken = randString()
			s.refresh[refreshToken] = p
		}
		s.mu.Unlock()
		if !ok {
			writeOAuthError(w, "invalid_grant")
			return
		}
	case "refresh_token":
		refreshToken = r.Form.Get("refresh_token")
		s.mu.Lock()
		var ok bool
		p, ok = s.refresh[refreshToken]
		s.mu.Unlock()
		if !ok || p.clientID != clientID {
			writeOAuthError(w, "invalid_grant")
			return
		}
	default:
		writeOAuthError(w, "unsupported_grant_type")
		return
	}

	now := time.Now()
	accessToken := s.sign(map[string]any{
		"iss": s.issuer,
		"sub": p.sub,
		"aud": p.clientID,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	})
	response := map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
	}
	if r.Form.Get("grant_type") == "authorization_code" {
		response["id_token"] = s.sign(map[string]any{
			"iss":   s.issuer,
			"sub":   p.sub,
			"aud":   p.clientID,
			"email": p.email,
			"nonce": p.nonce,
			"iat":   now.Unix(),
			"exp":   now.Add(time.Hour).Unix(),
		})
	}
	writeJSON(w, response)
}

func writeOAuthError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// sign builds a minimal RS256 JWT. No library: header + claims + PKCS1v15 sig.
func (s *server) sign(claims map[string]any) string {
	header := b64(mustJSON(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "dev"}))
	body := b64(mustJSON(claims))
	signing := header + "." + body

	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + b64(sig)
}

var pickerTmpl = template.Must(template.New("picker").Parse(`<!doctype html>
<title>devidp login</title>
<style>body{font:16px system-ui;max-width:24rem;margin:4rem auto}a,form{display:block;margin:.5rem 0}input{padding:.4rem}</style>
<h1>Pick a dev user</h1>
{{range .Links}}<a href="{{.Href}}">{{.Sub}} &mdash; {{.Email}}</a>{{end}}
<form action="/authorize">
  {{range .Pairs}}<input type="hidden" name="{{index . 0}}" value="{{index . 1}}">{{end}}
  <input name="sub" placeholder="new-user-sub" required>
  <button>Log in as new user</button>
</form>
`))

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func hasSub(us []user, sub string) bool {
	for _, u := range us {
		if u.Sub == sub {
			return true
		}
	}
	return false
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randString() string {
	b := make([]byte, 24)
	rand.Read(b)
	return b64(b)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
