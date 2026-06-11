package authguardian

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/oauth"
	"github.com/soju06/codex-lb/internal/proxy"
	"github.com/soju06/codex-lb/internal/scheduling"
)

const leaderTTL = 90 * time.Second

type Refresher interface {
	Refresh(ctx context.Context, account accounts.Account) error
}

type Scheduler struct {
	store      *db.Store
	logger     *slog.Logger
	cfg        config.Config
	refresher  Refresher
	leaderID   string
	invalidate func()
	now        func() time.Time

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	failures map[string]failureBackoff
}

type failureBackoff struct {
	attempts   int
	retryAfter time.Time
}

func NewScheduler(store *db.Store, logger *slog.Logger, cfg config.Config, refresher Refresher, leaderID string, invalidate func()) *Scheduler {
	if refresher == nil {
		encryptor, err := crypto.NewEncryptor(cfg.EncryptionKeyPath)
		if err != nil {
			logger.Error("failed to initialize auth guardian encryptor", "error", err)
			refresher = noopRefresher{}
		} else {
			refresher = NewOAuthRefresher(accounts.NewRepository(store), encryptor, cfg, nil)
		}
	}
	if leaderID == "" {
		leaderID = "auth-guardian"
	}
	if invalidate == nil {
		invalidate = func() {}
	}
	return &Scheduler{
		store:      store,
		logger:     logger,
		cfg:        cfg,
		refresher:  refresher,
		leaderID:   leaderID,
		invalidate: invalidate,
		now:        func() time.Time { return time.Now().UTC() },
		failures:   map[string]failureBackoff{},
	}
}

func (s *Scheduler) WithClock(now func() time.Time) *Scheduler {
	s.now = now
	return s
}

func (s *Scheduler) Start(ctx context.Context) {
	if !s.cfg.AuthGuardianEnabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(runCtx)
}

func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) run(ctx context.Context) {
	defer close(s.done)
	s.refreshOnce(ctx)
	ticker := time.NewTicker(s.cfg.AuthGuardianInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshOnce(ctx)
		}
	}
}

func (s *Scheduler) refreshOnce(ctx context.Context) {
	acquired, err := scheduling.NewRepository(s.store).TryAcquireLeader(ctx, s.leaderID, leaderTTL)
	if err != nil {
		s.logger.Warn("auth guardian leader acquire failed", "error", err)
		return
	}
	if !acquired {
		return
	}
	repo := accounts.NewRepository(s.store)
	allAccounts, err := repo.List(ctx)
	if err != nil {
		s.logger.Warn("auth guardian account list failed", "error", err)
		return
	}
	candidates := SelectCandidates(allAccounts, s.now(), s.cfg.AuthGuardianMaxRefreshAge, s.cfg.AuthGuardianBatchSize)
	filtered := candidates[:0]
	for _, account := range candidates {
		if !s.inBackoff(account.ID) {
			filtered = append(filtered, account)
		}
	}
	if len(filtered) == 0 {
		return
	}
	concurrency := s.cfg.AuthGuardianConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, account := range filtered {
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if err := s.refresher.Refresh(ctx, account); err != nil {
				s.recordFailure(account.ID)
				s.logger.Warn("auth guardian refresh failed", "account_id", account.ID, "error", err)
				return
			}
			s.clearFailure(account.ID)
			s.invalidate()
		}()
	}
	wg.Wait()
}

func SelectCandidates(all []accounts.Account, now time.Time, maxAge time.Duration, limit int) []accounts.Account {
	if limit < 0 {
		limit = 0
	}
	out := make([]accounts.Account, 0, len(all))
	for _, account := range all {
		if strings.ToLower(account.Status) != proxy.AccountStatusActive {
			continue
		}
		lastRefresh, err := parseDBTime(account.LastRefresh)
		if err != nil {
			out = append(out, account)
			continue
		}
		if now.Sub(lastRefresh) > maxAge {
			out = append(out, account)
		}
	}
	sortAccountsByRefresh(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortAccountsByRefresh(items []accounts.Account) {
	for i := 1; i < len(items); i++ {
		item := items[i]
		j := i - 1
		for ; j >= 0; j-- {
			if !refreshBefore(item.LastRefresh, items[j].LastRefresh) {
				break
			}
			items[j+1] = items[j]
		}
		items[j+1] = item
	}
}

func refreshBefore(a, b string) bool {
	ta, ea := parseDBTime(a)
	tb, eb := parseDBTime(b)
	if ea != nil {
		return eb == nil
	}
	if eb != nil {
		return false
	}
	return ta.Before(tb)
}

func (s *Scheduler) inBackoff(accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	failure, ok := s.failures[accountID]
	return ok && failure.retryAfter.After(s.now())
}

func (s *Scheduler) recordFailure(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := 1
	if previous, ok := s.failures[accountID]; ok {
		attempts = previous.attempts + 1
	}
	delay := time.Duration(attempts) * 5 * time.Minute
	if delay > time.Hour {
		delay = time.Hour
	}
	s.failures[accountID] = failureBackoff{attempts: attempts, retryAfter: s.now().Add(delay)}
}

func (s *Scheduler) clearFailure(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, accountID)
}

type OAuthRefresher struct {
	repo      accounts.Repository
	encryptor *crypto.Encryptor
	cfg       config.Config
	client    *http.Client
}

func NewOAuthRefresher(repo accounts.Repository, encryptor *crypto.Encryptor, cfg config.Config, client *http.Client) *OAuthRefresher {
	if client == nil {
		client = &http.Client{Timeout: time.Duration(cfg.OAuthTimeoutSeconds * float64(time.Second))}
	}
	return &OAuthRefresher{repo: repo, encryptor: encryptor, cfg: cfg, client: client}
}

func (r *OAuthRefresher) Refresh(ctx context.Context, account accounts.Account) error {
	refreshToken, err := r.encryptor.Decrypt(account.RefreshTokenEncrypted)
	if err != nil {
		return err
	}
	tokens, err := r.refreshAccessToken(ctx, refreshToken)
	if err != nil {
		var refreshErr *RefreshError
		if asRefreshError(err, &refreshErr) && refreshErr.Permanent {
			reason := sql.NullString{String: proxy.PermanentFailureCodes[refreshErr.Code], Valid: true}
			if reason.String == "" {
				reason.String = refreshErr.Message
			}
			_, _ = r.repo.UpdateStatus(ctx, account.ID, proxy.AccountStatusForPermanentFailure(refreshErr.Code), reason)
		}
		return err
	}
	claims := oauth.ExtractIDTokenClaimsForGuardian(tokens.IDToken)
	accessEncrypted, err := r.encryptor.Encrypt(tokens.AccessToken)
	if err != nil {
		return err
	}
	refreshEncrypted, err := r.encryptor.Encrypt(tokens.RefreshToken)
	if err != nil {
		return err
	}
	idEncrypted, err := r.encryptor.Encrypt(tokens.IDToken)
	if err != nil {
		return err
	}
	planType := firstNonEmpty(claims.PlanType, account.PlanType, "free")
	email := firstNonEmpty(claims.Email, account.Email)
	_, err = r.repo.UpdateTokens(ctx, accounts.TokenUpdate{
		AccountID:             account.ID,
		AccessTokenEncrypted:  accessEncrypted,
		RefreshTokenEncrypted: refreshEncrypted,
		IDTokenEncrypted:      idEncrypted,
		LastRefresh:           time.Now().UTC().Format("2006-01-02 15:04:05"),
		PlanType:              planType,
		Email:                 email,
		ChatGPTAccountID:      nullableString(firstNonEmpty(claims.ChatGPTAccountID, nullStringValue(account.ChatGPTAccountID))),
		WorkspaceID:           nullableString(firstNonEmpty(claims.WorkspaceID, nullStringValue(account.WorkspaceID))),
		WorkspaceLabel:        nullableString(firstNonEmpty(claims.WorkspaceLabel, nullStringValue(account.WorkspaceLabel))),
		SeatType:              nullableString(firstNonEmpty(claims.SeatType, nullStringValue(account.SeatType))),
	})
	return err
}

type refreshTokenResponse struct {
	AccessToken      string          `json:"access_token"`
	RefreshToken     string          `json:"refresh_token"`
	IDToken          string          `json:"id_token"`
	Error            json.RawMessage `json:"error"`
	ErrorDescription string          `json:"error_description"`
	Message          string          `json:"message"`
	ErrorCode        string          `json:"error_code"`
	Code             string          `json:"code"`
}

type RefreshError struct {
	Code      string
	Message   string
	Permanent bool
}

func (e *RefreshError) Error() string {
	return e.Message
}

func (r *OAuthRefresher) refreshAccessToken(ctx context.Context, refreshToken string) (oauth.OAuthTokens, error) {
	payload := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     r.cfg.OAuthClientID,
		"refresh_token": refreshToken,
		"scope":         r.cfg.OAuthScope,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.OAuthAuthBaseURL, "/")+"/oauth/token", bytes.NewReader(body))
	if err != nil {
		return oauth.OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return oauth.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauth.OAuthTokens{}, err
	}
	var parsed refreshTokenResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return oauth.OAuthTokens{}, err
	}
	if resp.StatusCode >= 400 {
		code := extractRefreshErrorCode(parsed, resp.StatusCode)
		return oauth.OAuthTokens{}, &RefreshError{Code: code, Message: extractRefreshErrorMessage(parsed, resp.StatusCode), Permanent: proxy.PermanentFailureCodes[code] != ""}
	}
	if parsed.AccessToken == "" || parsed.RefreshToken == "" || parsed.IDToken == "" {
		return oauth.OAuthTokens{}, &RefreshError{Code: "invalid_response", Message: "Refresh response missing tokens"}
	}
	return oauth.OAuthTokens{AccessToken: parsed.AccessToken, RefreshToken: parsed.RefreshToken, IDToken: parsed.IDToken}, nil
}

func extractRefreshErrorCode(payload refreshTokenResponse, statusCode int) string {
	if payload.ErrorCode != "" {
		return payload.ErrorCode
	}
	if payload.Code != "" {
		return payload.Code
	}
	if len(payload.Error) > 0 {
		var errMap map[string]any
		if json.Unmarshal(payload.Error, &errMap) == nil {
			for _, key := range []string{"code", "error"} {
				if value, _ := errMap[key].(string); value != "" {
					return value
				}
			}
		}
		var errString string
		if json.Unmarshal(payload.Error, &errString) == nil && errString != "" {
			return errString
		}
	}
	return fmt.Sprintf("http_%d", statusCode)
}

func extractRefreshErrorMessage(payload refreshTokenResponse, statusCode int) string {
	if payload.Message != "" {
		return payload.Message
	}
	if payload.ErrorDescription != "" {
		return payload.ErrorDescription
	}
	return fmt.Sprintf("Token refresh failed (%d)", statusCode)
}

func parseDBTime(value string) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.UTC); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nullableString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func asRefreshError(err error, target **RefreshError) bool {
	if err == nil {
		return false
	}
	if typed, ok := err.(*RefreshError); ok {
		*target = typed
		return true
	}
	return false
}

type noopRefresher struct{}

func (noopRefresher) Refresh(context.Context, accounts.Account) error { return nil }
