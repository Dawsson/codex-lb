package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

type BootstrapService struct {
	repo        Repository
	encryptor   Encryptor
	manualToken string
	logger      *slog.Logger
}

func NewBootstrapService(repo Repository, encryptor Encryptor, manualToken string, logger *slog.Logger) BootstrapService {
	return BootstrapService{
		repo:        repo,
		encryptor:   encryptor,
		manualToken: strings.TrimSpace(manualToken),
		logger:      logger,
	}
}

func (s BootstrapService) HasActiveToken(ctx context.Context) (bool, error) {
	if s.manualToken != "" {
		return true, nil
	}
	settings, err := s.repo.Settings(ctx)
	if err != nil {
		return false, err
	}
	return !settings.PasswordHash.Valid && len(settings.BootstrapTokenHash) > 0, nil
}

func (s BootstrapService) ValidationStatus(ctx context.Context, submittedToken string) (string, error) {
	submittedToken = strings.TrimSpace(submittedToken)
	if s.manualToken != "" {
		if subtle.ConstantTimeCompare([]byte(submittedToken), []byte(s.manualToken)) == 1 {
			return "valid", nil
		}
		return "invalid", nil
	}
	settings, err := s.repo.Settings(ctx)
	if err != nil {
		return "", err
	}
	if len(settings.BootstrapTokenHash) == 0 {
		if settings.PasswordHash.Valid {
			return "password_already_configured", nil
		}
		return "unavailable", nil
	}
	if subtle.ConstantTimeCompare(hashBootstrapToken(submittedToken), settings.BootstrapTokenHash) == 1 {
		return "valid", nil
	}
	if settings.PasswordHash.Valid {
		return "password_already_configured", nil
	}
	return "invalid", nil
}

func (s BootstrapService) EnsureAutoToken(ctx context.Context) (string, error) {
	settings, err := s.repo.Settings(ctx)
	if err != nil {
		return "", err
	}
	if s.manualToken != "" || settings.PasswordHash.Valid {
		if len(settings.BootstrapTokenHash) > 0 {
			_, err := s.repo.ClearBootstrapToken(ctx)
			return "", err
		}
		return "", nil
	}
	if len(settings.BootstrapTokenHash) > 0 {
		if len(settings.BootstrapTokenEncrypted) == 0 {
			if s.logger != nil {
				s.logger.Warn("stored bootstrap token hash exists without encrypted token; keeping existing hash valid")
			}
			return "", nil
		}
		token, err := s.encryptor.Decrypt(settings.BootstrapTokenEncrypted)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("stored bootstrap token could not be decrypted; keeping existing hash valid", "error", err)
			}
			return "", nil
		}
		return token, nil
	}
	token, err := generateBootstrapToken()
	if err != nil {
		return "", err
	}
	encrypted, err := s.encryptor.Encrypt(token)
	if err != nil {
		return "", err
	}
	stored, err := s.repo.StoreBootstrapTokenIfAbsent(ctx, encrypted, hashBootstrapToken(token))
	if err != nil {
		return "", err
	}
	if !stored {
		return "", nil
	}
	return token, nil
}

func (s BootstrapService) LogToken(token string, reason string) {
	if token == "" || s.logger == nil {
		return
	}
	s.logger.Warn("dashboard bootstrap token", "reason", reason, "token", token)
}

func hashBootstrapToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func generateBootstrapToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate bootstrap token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	hostHeader := strings.ToLower(strings.TrimSpace(r.Host))
	if strings.HasPrefix(hostHeader, "[::1]") {
		return true
	}
	if strings.Contains(hostHeader, ":") {
		hostHeader, _, _ = net.SplitHostPort(hostHeader)
	}
	switch hostHeader {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
