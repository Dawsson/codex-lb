package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Host                   string
	Port                   int
	DatabasePath           string
	EncryptionKeyPath      string
	RunMigrations          bool
	MigrationsDir          string
	AuthDisabled           bool
	ConversationArchiveDir string
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

	return Config{
		Host:                   getenvDefault("CODEX_LB_GO_HOST", "127.0.0.1"),
		Port:                   port,
		DatabasePath:           databasePath,
		EncryptionKeyPath:      encryptionKeyPath,
		RunMigrations:          parseBool(os.Getenv("CODEX_LB_GO_RUN_MIGRATIONS")),
		MigrationsDir:          getenvDefault("CODEX_LB_GO_MIGRATIONS_DIR", "migrations"),
		AuthDisabled:           parseBool(os.Getenv("CODEX_LB_GO_AUTH_DISABLED")),
		ConversationArchiveDir: conversationArchiveDir,
	}, nil
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
