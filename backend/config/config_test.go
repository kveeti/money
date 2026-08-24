package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// setRequired sets every mandatory env var to a valid value for the test scope.
func setRequired(t *testing.T) {
	t.Setenv("IS_PROD", "0")
	t.Setenv("DEMO_MODE", "0")
	t.Setenv("CLIENT_IP_HEADER", "")
	t.Setenv("DEMO_RATE_LIMIT_PER_MINUTE", "")
	t.Setenv("DEMO_RATE_LIMIT_PER_HOUR", "")
	t.Setenv("BACKEND_URL", "http://localhost:8000")
	t.Setenv("DB_URL", "postgres://localhost/db")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_CLIENT_ID", "client")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	// Optional vars default to empty.
	t.Setenv("FRONT_URL", "")
	t.Setenv("DEV_VITE_URL", "")
	t.Setenv("OIDC_REDIRECT_URL", "")
	t.Setenv("ENABLEBANKING_APP_ID", "")
	t.Setenv("ENABLEBANKING_PRIVATE_KEY", "")
	t.Setenv("ENABLEBANKING_API_ORIGIN", "")
	t.Setenv("ENABLEBANKING_ALLOWED_OIDC_SUBJECTS", "")
}

func TestLoadConfigClientIPRateLimit(t *testing.T) {
	setRequired(t)
	t.Setenv("CLIENT_IP_HEADER", "x-forwarded-for")
	t.Setenv("DEMO_RATE_LIMIT_PER_MINUTE", "7")
	t.Setenv("DEMO_RATE_LIMIT_PER_HOUR", "42")

	c, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "X-Forwarded-For", c.ClientIPHeader)
	require.Equal(t, 7, c.DemoRateLimitPerMinute)
	require.Equal(t, 42, c.DemoRateLimitPerHour)

	t.Setenv("CLIENT_IP_HEADER", "not a header")
	_, err = LoadConfig()
	require.ErrorContains(t, err, "CLIENT_IP_HEADER")

	setRequired(t)
	t.Setenv("DEMO_RATE_LIMIT_PER_MINUTE", "0")
	_, err = LoadConfig()
	require.ErrorContains(t, err, "DEMO_RATE_LIMIT_PER_MINUTE")
}

func TestLoadConfigDemoMode(t *testing.T) {
	setRequired(t)
	t.Setenv("DEMO_MODE", "1")

	c, err := LoadConfig()
	require.NoError(t, err)
	require.True(t, c.DemoMode)

	t.Setenv("DEMO_MODE", "true")
	_, err = LoadConfig()
	require.ErrorContains(t, err, "DEMO_MODE must be 0 or 1")
}

func TestLoadConfig_RedirectURLDefaultsToBackend(t *testing.T) {
	setRequired(t)

	c, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8000/api/v1/auth/callback", c.OIDC.RedirectURL)
}

func TestLoadConfig_RedirectURLHonoredWhenSet(t *testing.T) {
	setRequired(t)
	t.Setenv("OIDC_REDIRECT_URL", "https://proxy.example/weird/cb")

	c, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "https://proxy.example/weird/cb", c.OIDC.RedirectURL)
}

func TestLoadConfig_FrontURLOptional(t *testing.T) {
	setRequired(t) // FRONT_URL empty

	c, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "", c.FrontUrl)
	// Same-origin: effective front url is the backend's own url.
	require.Equal(t, "http://localhost:8000", c.EffectiveFrontUrl())
}

func TestLoadConfig_FrontURLUsedWhenSet(t *testing.T) {
	setRequired(t)
	t.Setenv("FRONT_URL", "http://localhost:3000")

	c, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:3000", c.EffectiveFrontUrl())
}

func TestLoadConfig_MissingRequiredErrors(t *testing.T) {
	for _, missing := range []string{"BACKEND_URL", "DB_URL", "OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET"} {
		t.Run(missing, func(t *testing.T) {
			setRequired(t)
			t.Setenv(missing, "")

			_, err := LoadConfig()
			require.ErrorContains(t, err, missing)
		})
	}
}

func TestLoadConfigParsesEnableBankingSubjectAllowlist(t *testing.T) {
	setRequired(t)
	t.Setenv("ENABLEBANKING_ALLOWED_OIDC_SUBJECTS", " subject-1,subject-2, subject-1, ")

	c, err := LoadConfig()
	require.NoError(t, err)
	require.True(t, c.EnableBanking.AllowsSubject("subject-1"))
	require.True(t, c.EnableBanking.AllowsSubject("subject-2"))
	require.False(t, c.EnableBanking.AllowsSubject("other"))
	require.Len(t, c.EnableBanking.AllowedSubjects, 2)
}

func TestLoadConfigSecureCookiesFollowBackendURL(t *testing.T) {
	setRequired(t)
	c, err := LoadConfig()
	require.NoError(t, err)
	require.False(t, c.SecureCookies())

	t.Setenv("BACKEND_URL", "https://dash.example/")
	c, err = LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "https://dash.example", c.BackendUrl)
	require.True(t, c.SecureCookies())
}

func TestLoadConfigProductionRequiresHTTPS(t *testing.T) {
	setRequired(t)
	t.Setenv("IS_PROD", "1")

	_, err := LoadConfig()
	require.ErrorContains(t, err, "must use HTTPS in production")

	t.Setenv("BACKEND_URL", "https://dash.example")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	c, err := LoadConfig()
	require.NoError(t, err)
	require.True(t, c.IsProd)
}

func TestLoadConfigRejectsDevProxyInProduction(t *testing.T) {
	setRequired(t)
	t.Setenv("IS_PROD", "1")
	t.Setenv("BACKEND_URL", "https://dash.example")
	t.Setenv("DEV_VITE_URL", "https://vite.example")

	_, err := LoadConfig()
	require.ErrorContains(t, err, "DEV_VITE_URL cannot be set in production")
}

func TestLoadConfigRejectsInvalidProdFlagAndOrigins(t *testing.T) {
	setRequired(t)
	t.Setenv("IS_PROD", "true")
	_, err := LoadConfig()
	require.ErrorContains(t, err, "IS_PROD must be 0 or 1")

	setRequired(t)
	t.Setenv("BACKEND_URL", "https://dash.example/path")
	_, err = LoadConfig()
	require.ErrorContains(t, err, "must be an origin")
}

func TestConfigLogValueRedactsSensitiveValues(t *testing.T) {
	c := &Config{
		BackendUrl: "https://dash.example",
		DbUrl:      "postgres://user:db-password@db.example/dash",
		OIDC: OIDCConfig{
			ClientSecret: "oidc-secret",
		},
		EnableBanking: EnableBankingConfig{
			PrivateKeyPath: "/secret/private-key.pem",
		},
	}

	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("loaded config", "config", c)

	var event map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	loggedConfig, ok := event["config"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://dash.example", loggedConfig["backend_url"])
	require.Equal(t, "[REDACTED]", loggedConfig["db_url"])

	loggedOIDC, ok := loggedConfig["oidc"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED]", loggedOIDC["client_secret"])

	loggedEnableBanking, ok := loggedConfig["enable_banking"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED]", loggedEnableBanking["private_key_path"])

	require.NotContains(t, output.String(), "db-password")
	require.NotContains(t, output.String(), "oidc-secret")
	require.NotContains(t, output.String(), "private-key.pem")
}
