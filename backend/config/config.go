package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	IsProd     bool
	Port       string
	BackendUrl string
	// FrontUrl is empty when the frontend is served from the same origin as the
	// backend. When set (separate origin, e.g. a dev server) it enables CORS and
	// is used as the post-login redirect target.
	FrontUrl string
	// DevViteUrl, when set, makes the backend reverse-proxy every non-API request
	// to the Vite dev server at this URL (for HMR) instead of serving the embedded
	// build. Empty = serve the embedded frontend. Dev only.
	DevViteUrl string
	DbUrl      string

	// ImportStore selects where uploaded import files are kept: "disk" (under
	// ImportDir) or "postgres" (default, bytea).
	ImportStore     string
	ImportDir       string
	DisableRateSync bool

	OIDC          OIDCConfig
	EnableBanking EnableBankingConfig
}

// EnableBankingConfig is optional. The integration is available only when both
// the application ID and private key path are set.
type EnableBankingConfig struct {
	ApplicationID  string
	PrivateKeyPath string
	APIOrigin      string
}

func (c EnableBankingConfig) Enabled() bool {
	return c.ApplicationID != "" && c.PrivateKeyPath != ""
}

// callbackPath must match the OIDC callback route in the router.
const callbackPath = "/api/v1/auth/callback"

type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// EffectiveFrontUrl is where users are sent after login: FrontUrl if set,
// otherwise the backend's own URL (same-origin frontend).
func (c *Config) EffectiveFrontUrl() string {
	if c.FrontUrl != "" {
		return c.FrontUrl
	}
	return c.BackendUrl
}

func (c *Config) SecureCookies() bool {
	u, err := url.Parse(c.BackendUrl)
	return err == nil && u.Scheme == "https"
}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("is_prod", c.IsProd),
		slog.String("port", c.Port),
		slog.String("backend_url", c.BackendUrl),
		slog.String("front_url", c.FrontUrl),
		slog.String("dev_vite_url", c.DevViteUrl),
		slog.String("db_url", redact(c.DbUrl)),
		slog.String("import_store", c.ImportStore),
		slog.String("import_dir", c.ImportDir),
		slog.Bool("disable_rate_sync", c.DisableRateSync),
		slog.Group("oidc",
			slog.String("issuer", c.OIDC.Issuer),
			slog.String("client_id", c.OIDC.ClientID),
			slog.String("client_secret", redact(c.OIDC.ClientSecret)),
			slog.String("redirect_url", c.OIDC.RedirectURL),
		),
		slog.Group("enable_banking",
			slog.String("application_id", c.EnableBanking.ApplicationID),
			slog.String("private_key_path", redact(c.EnableBanking.PrivateKeyPath)),
			slog.String("api_origin", c.EnableBanking.APIOrigin),
		),
	)
}

func redact(value string) string {
	if value == "" {
		return ""
	}
	return "[REDACTED]"
}

func LoadConfig() (*Config, error) {
	isProd := false
	switch value := os.Getenv("IS_PROD"); value {
	case "", "0":
	case "1":
		isProd = true
	default:
		return nil, fmt.Errorf("invalid config: IS_PROD must be 0 or 1")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	config := &Config{
		IsProd:          isProd,
		Port:            port,
		BackendUrl:      os.Getenv("BACKEND_URL"),
		FrontUrl:        os.Getenv("FRONT_URL"),
		DevViteUrl:      os.Getenv("DEV_VITE_URL"),
		DbUrl:           os.Getenv("DB_URL"),
		ImportStore:     os.Getenv("IMPORT_STORE"),
		ImportDir:       os.Getenv("IMPORT_DIR"),
		DisableRateSync: os.Getenv("DISABLE_RATE_SYNC") == "1",
		EnableBanking: EnableBankingConfig{
			ApplicationID:  os.Getenv("ENABLEBANKING_APP_ID"),
			PrivateKeyPath: os.Getenv("ENABLEBANKING_PRIVATE_KEY"),
			APIOrigin:      os.Getenv("ENABLEBANKING_API_ORIGIN"),
		},
		OIDC: OIDCConfig{
			Issuer:       os.Getenv("OIDC_ISSUER"),
			ClientID:     os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("OIDC_REDIRECT_URL"),
		},
	}

	required := map[string]string{
		"BACKEND_URL":        config.BackendUrl,
		"DB_URL":             config.DbUrl,
		"OIDC_ISSUER":        config.OIDC.Issuer,
		"OIDC_CLIENT_ID":     config.OIDC.ClientID,
		"OIDC_CLIENT_SECRET": config.OIDC.ClientSecret,
	}
	for name, value := range required {
		if value == "" {
			return nil, fmt.Errorf("invalid config: %s not set", name)
		}
	}

	if config.EnableBanking.APIOrigin == "" {
		config.EnableBanking.APIOrigin = "https://api.enablebanking.com"
	}
	if (config.EnableBanking.ApplicationID == "") != (config.EnableBanking.PrivateKeyPath == "") {
		return nil, fmt.Errorf("invalid config: ENABLEBANKING_APP_ID and ENABLEBANKING_PRIVATE_KEY must be set together")
	}

	config.BackendUrl = strings.TrimSuffix(config.BackendUrl, "/")
	config.FrontUrl = strings.TrimSuffix(config.FrontUrl, "/")
	config.DevViteUrl = strings.TrimSuffix(config.DevViteUrl, "/")
	config.EnableBanking.APIOrigin = strings.TrimSuffix(config.EnableBanking.APIOrigin, "/")

	// OIDC_REDIRECT_URL defaults to the backend's own callback; set it
	// explicitly only for non-standard setups (proxies, path rewrites, ...).
	if config.OIDC.RedirectURL == "" {
		config.OIDC.RedirectURL = config.BackendUrl + callbackPath
	}

	for name, value := range map[string]string{
		"BACKEND_URL":              config.BackendUrl,
		"OIDC_ISSUER":              config.OIDC.Issuer,
		"OIDC_REDIRECT_URL":        config.OIDC.RedirectURL,
		"FRONT_URL":                config.FrontUrl,
		"DEV_VITE_URL":             config.DevViteUrl,
		"ENABLEBANKING_API_ORIGIN": config.EnableBanking.APIOrigin,
	} {
		if value == "" && (name == "FRONT_URL" || name == "DEV_VITE_URL") {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid config: %s must be an absolute HTTP URL", name)
		}
		if config.IsProd && u.Scheme != "https" {
			return nil, fmt.Errorf("invalid config: %s must use HTTPS in production", name)
		}
	}
	for name, value := range map[string]string{
		"BACKEND_URL":              config.BackendUrl,
		"FRONT_URL":                config.FrontUrl,
		"DEV_VITE_URL":             config.DevViteUrl,
		"ENABLEBANKING_API_ORIGIN": config.EnableBanking.APIOrigin,
	} {
		if value == "" {
			continue
		}
		u, _ := url.Parse(value)
		if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid config: %s must be an origin without a path, query, or fragment", name)
		}
	}
	if config.IsProd && config.DevViteUrl != "" {
		return nil, fmt.Errorf("invalid config: DEV_VITE_URL cannot be set in production")
	}

	return config, nil
}
