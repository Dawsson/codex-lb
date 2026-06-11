package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                       string
	Port                       int
	DatabasePath               string
	EncryptionKeyPath          string
	RunMigrations              bool
	MigrationsDir              string
	AuthDisabled               bool
	DashboardBootstrapToken    string
	DashboardDistDir           string
	ConversationArchiveDir     string
	UsageRefreshEnabled        bool
	UsageRefreshInterval       time.Duration
	UsageFetchTimeout          time.Duration
	UsageFetchMaxRetries       int
	APIKeyLimitResetEnabled    bool
	APIKeyLimitResetInterval   time.Duration
	APIKeyReservationStaleAge  time.Duration
	CacheInvalidationEnabled   bool
	CacheInvalidationInterval  time.Duration
	StickyCleanupEnabled       bool
	StickyCleanupInterval      time.Duration
	ModelRefreshEnabled        bool
	ModelRefreshInterval       time.Duration
	ModelRegistryClientVersion string
	AuthGuardianEnabled        bool
	AuthGuardianInterval       time.Duration
	AuthGuardianMaxRefreshAge  time.Duration
	AuthGuardianBatchSize      int
	AuthGuardianConcurrency    int
	QuotaPlannerEnabled        bool
	QuotaPlannerInterval       time.Duration
	UpstreamBaseURL            string
	FirewallTrustProxyHeaders  bool
	FirewallTrustedProxyCIDRs  []string
	FirewallIPCacheTTL         time.Duration

	OAuthAuthBaseURL    string
	OAuthClientID       string
	OAuthOriginator     string
	OAuthScope          string
	OAuthTimeoutSeconds float64
	OAuthRedirectURI    string
	OAuthCallbackHost   string
	OAuthCallbackPort   int
}

func Load() (Config, error) {
	port := 2455
	if raw := os.Getenv("CODEX_LB_GO_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("invalid CODEX_LB_GO_PORT: %q", raw)
		}
		port = parsed
	}

	databasePath, err := resolveDatabasePath()
	if err != nil {
		return Config{}, err
	}
	encryptionKeyPath, err := resolveEncryptionKeyPath(databasePath)
	if err != nil {
		return Config{}, err
	}
	conversationArchiveDir, err := resolveConversationArchiveDir(databasePath)
	if err != nil {
		return Config{}, err
	}

	oauthCallbackPort := 1455
	if raw := os.Getenv("CODEX_LB_OAUTH_CALLBACK_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("invalid CODEX_LB_OAUTH_CALLBACK_PORT: %q", raw)
		}
		oauthCallbackPort = parsed
	}
	oauthTimeoutSeconds := 30.0
	if raw := os.Getenv("CODEX_LB_OAUTH_TIMEOUT_SECONDS"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("invalid CODEX_LB_OAUTH_TIMEOUT_SECONDS: %q", raw)
		}
		oauthTimeoutSeconds = parsed
	}
	usageRefreshIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_USAGE_REFRESH_INTERVAL_SECONDS", 60)
	if err != nil {
		return Config{}, err
	}
	usageFetchTimeoutSeconds, err := parsePositiveIntEnv("CODEX_LB_USAGE_FETCH_TIMEOUT_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	usageFetchMaxRetries, err := parseNonNegativeIntEnv("CODEX_LB_USAGE_FETCH_MAX_RETRIES", 2)
	if err != nil {
		return Config{}, err
	}
	apiKeyLimitResetIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_API_KEY_LIMIT_RESET_INTERVAL_SECONDS", 3600)
	if err != nil {
		return Config{}, err
	}
	apiKeyReservationStaleAgeSeconds, err := parsePositiveIntEnv("CODEX_LB_API_KEY_USAGE_RESERVATION_STALE_SECONDS", 21600)
	if err != nil {
		return Config{}, err
	}
	cacheInvalidationIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_CACHE_INVALIDATION_POLL_INTERVAL_SECONDS", 1)
	if err != nil {
		return Config{}, err
	}
	stickyCleanupIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_STICKY_SESSION_CLEANUP_INTERVAL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	modelRefreshIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_MODEL_REFRESH_INTERVAL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	authGuardianIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_AUTH_GUARDIAN_INTERVAL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	authGuardianMaxAgeSeconds, err := parsePositiveIntEnv("CODEX_LB_AUTH_GUARDIAN_MAX_REFRESH_AGE_SECONDS", 7*24*60*60)
	if err != nil {
		return Config{}, err
	}
	authGuardianBatchSize, err := parsePositiveIntEnv("CODEX_LB_AUTH_GUARDIAN_BATCH_SIZE", 25)
	if err != nil {
		return Config{}, err
	}
	authGuardianConcurrency, err := parsePositiveIntEnv("CODEX_LB_AUTH_GUARDIAN_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}
	quotaPlannerIntervalSeconds, err := parsePositiveIntEnv("CODEX_LB_QUOTA_PLANNER_TICK_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	firewallCacheTTLSeconds, err := parsePositiveIntEnv("CODEX_LB_FIREWALL_IP_CACHE_TTL_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Host:                       getenvDefault("CODEX_LB_GO_HOST", "127.0.0.1"),
		Port:                       port,
		DatabasePath:               databasePath,
		EncryptionKeyPath:          encryptionKeyPath,
		RunMigrations:              parseBool(os.Getenv("CODEX_LB_GO_RUN_MIGRATIONS")),
		MigrationsDir:              getenvDefault("CODEX_LB_GO_MIGRATIONS_DIR", "migrations"),
		AuthDisabled:               parseBool(os.Getenv("CODEX_LB_GO_AUTH_DISABLED")),
		DashboardBootstrapToken:    strings.TrimSpace(os.Getenv("CODEX_LB_DASHBOARD_BOOTSTRAP_TOKEN")),
		DashboardDistDir:           strings.TrimSpace(os.Getenv("CODEX_LB_DASHBOARD_DIST_DIR")),
		ConversationArchiveDir:     conversationArchiveDir,
		UsageRefreshEnabled:        parseBoolDefault(os.Getenv("CODEX_LB_USAGE_REFRESH_ENABLED"), true),
		UsageRefreshInterval:       time.Duration(usageRefreshIntervalSeconds) * time.Second,
		UsageFetchTimeout:          time.Duration(usageFetchTimeoutSeconds) * time.Second,
		UsageFetchMaxRetries:       usageFetchMaxRetries,
		APIKeyLimitResetEnabled:    parseBoolDefault(os.Getenv("CODEX_LB_API_KEY_LIMIT_RESET_ENABLED"), true),
		APIKeyLimitResetInterval:   time.Duration(apiKeyLimitResetIntervalSeconds) * time.Second,
		APIKeyReservationStaleAge:  time.Duration(apiKeyReservationStaleAgeSeconds) * time.Second,
		CacheInvalidationEnabled:   parseBoolDefault(os.Getenv("CODEX_LB_CACHE_INVALIDATION_ENABLED"), true),
		CacheInvalidationInterval:  time.Duration(cacheInvalidationIntervalSeconds) * time.Second,
		StickyCleanupEnabled:       parseBoolDefault(os.Getenv("CODEX_LB_STICKY_SESSION_CLEANUP_ENABLED"), true),
		StickyCleanupInterval:      time.Duration(stickyCleanupIntervalSeconds) * time.Second,
		ModelRefreshEnabled:        parseBoolDefault(os.Getenv("CODEX_LB_MODEL_REFRESH_ENABLED"), true),
		ModelRefreshInterval:       time.Duration(modelRefreshIntervalSeconds) * time.Second,
		ModelRegistryClientVersion: getenvDefault("CODEX_LB_MODEL_REGISTRY_CLIENT_VERSION", "0.101.0"),
		AuthGuardianEnabled:        parseBoolDefault(os.Getenv("CODEX_LB_AUTH_GUARDIAN_ENABLED"), true),
		AuthGuardianInterval:       time.Duration(authGuardianIntervalSeconds) * time.Second,
		AuthGuardianMaxRefreshAge:  time.Duration(authGuardianMaxAgeSeconds) * time.Second,
		AuthGuardianBatchSize:      authGuardianBatchSize,
		AuthGuardianConcurrency:    authGuardianConcurrency,
		QuotaPlannerEnabled:        parseBoolDefault(os.Getenv("CODEX_LB_QUOTA_PLANNER_SCHEDULER_ENABLED"), true),
		QuotaPlannerInterval:       time.Duration(quotaPlannerIntervalSeconds) * time.Second,
		UpstreamBaseURL:            getenvDefault("CODEX_LB_UPSTREAM_BASE_URL", "https://chatgpt.com/backend-api"),
		FirewallTrustProxyHeaders:  parseBool(os.Getenv("CODEX_LB_FIREWALL_TRUST_PROXY_HEADERS")),
		FirewallTrustedProxyCIDRs:  splitCSV(os.Getenv("CODEX_LB_FIREWALL_TRUSTED_PROXY_CIDRS")),
		FirewallIPCacheTTL:         time.Duration(firewallCacheTTLSeconds) * time.Second,

		OAuthAuthBaseURL:    getenvDefault("CODEX_LB_AUTH_BASE_URL", "https://auth.openai.com"),
		OAuthClientID:       getenvDefault("CODEX_LB_OAUTH_CLIENT_ID", "app_EMoamEEZ73f0CkXaXp7hrann"),
		OAuthOriginator:     getenvDefault("CODEX_LB_OAUTH_ORIGINATOR", "codex_chatgpt_desktop"),
		OAuthScope:          getenvDefault("CODEX_LB_OAUTH_SCOPE", "openid profile email"),
		OAuthTimeoutSeconds: oauthTimeoutSeconds,
		OAuthRedirectURI:    getenvDefault("CODEX_LB_OAUTH_REDIRECT_URI", "http://localhost:1455/auth/callback"),
		OAuthCallbackHost:   getenvDefault("CODEX_LB_OAUTH_CALLBACK_HOST", "127.0.0.1"),
		OAuthCallbackPort:   oauthCallbackPort,
	}, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func resolveDatabasePath() (string, error) {
	if raw := os.Getenv("CODEX_LB_DATABASE_URL"); raw != "" {
		return sqlitePathFromURL(raw)
	}
	dataDir := os.Getenv("CODEX_LB_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".codex-lb")
	}
	return filepath.Join(dataDir, "store.db"), nil
}

func resolveConversationArchiveDir(databasePath string) (string, error) {
	if raw := os.Getenv("CODEX_LB_CONVERSATION_ARCHIVE_DIR"); raw != "" {
		return raw, nil
	}
	return filepath.Join(filepath.Dir(databasePath), "conversation-archive"), nil
}

func resolveEncryptionKeyPath(databasePath string) (string, error) {
	if raw := os.Getenv("CODEX_LB_ENCRYPTION_KEY_FILE"); raw != "" {
		return raw, nil
	}
	return filepath.Join(filepath.Dir(databasePath), "encryption.key"), nil
}

func sqlitePathFromURL(raw string) (string, error) {
	trimmed := strings.TrimPrefix(raw, "sqlite+aiosqlite:")
	trimmed = strings.TrimPrefix(trimmed, "sqlite:")
	parsed, err := url.Parse("sqlite:" + trimmed)
	if err != nil {
		return "", fmt.Errorf("parse sqlite database url: %w", err)
	}
	if parsed.Scheme != "sqlite" || parsed.Path == "" {
		return "", fmt.Errorf("CODEX_LB_DATABASE_URL must be a sqlite file URL for the Go API")
	}
	return parsed.Path, nil
}

func getenvDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseBoolDefault(raw string, fallback bool) bool {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return parseBool(raw)
}

func parsePositiveIntEnv(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid %s: %q", key, raw)
	}
	return parsed, nil
}

func parseNonNegativeIntEnv(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid %s: %q", key, raw)
	}
	return parsed, nil
}
