package usagerefresh

import (
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

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/authguardian"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/proxy"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/scheduling"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/usage"
)

const usageRefreshLeaderTTL = 90 * time.Second

type RefreshScheduler struct {
	store      *db.Store
	logger     *slog.Logger
	cfg        config.Config
	updater    AccountsRefresher
	encryptor  *crypto.Encryptor
	leaderID   string
	invalidate func()

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type AccountsRefresher interface {
	RefreshAccounts(ctx context.Context, accounts []accounts.ProxyRecord, latest map[string]usage.Entry) (bool, error)
}

func NewRefreshScheduler(
	store *db.Store,
	logger *slog.Logger,
	cfg config.Config,
	updater AccountsRefresher,
	invalidate func(),
) *RefreshScheduler {
	var encryptor *crypto.Encryptor
	if updater == nil {
		var err error
		encryptor, err = crypto.NewEncryptor(cfg.EncryptionKeyPath)
		if err != nil {
			logger.Error("failed to initialize usage refresh encryptor", "error", err)
			updater = noopAccountsRefresher{}
		} else {
			updater = NewHTTPUsageUpdater(usage.NewRepository(store), accounts.NewRepository(store), encryptor, cfg)
		}
	} else {
		var err error
		encryptor, err = crypto.NewEncryptor(cfg.EncryptionKeyPath)
		if err != nil {
			logger.Warn("failed to initialize usage refresh warmup encryptor", "error", err)
		}
	}
	if invalidate == nil {
		invalidate = func() {}
	}
	return &RefreshScheduler{
		store:      store,
		logger:     logger,
		cfg:        cfg,
		updater:    updater,
		encryptor:  encryptor,
		leaderID:   uuid.NewString(),
		invalidate: invalidate,
	}
}

func (s *RefreshScheduler) SetLeaderID(leaderID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leaderID != "" {
		s.leaderID = leaderID
	}
}

func (s *RefreshScheduler) Start(ctx context.Context) {
	if !s.cfg.UsageRefreshEnabled {
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

func (s *RefreshScheduler) Stop(ctx context.Context) error {
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

func (s *RefreshScheduler) run(ctx context.Context) {
	defer close(s.done)
	s.refreshOnce(ctx)
	ticker := time.NewTicker(s.cfg.UsageRefreshInterval)
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

func (s *RefreshScheduler) refreshOnce(ctx context.Context) {
	leaderRepo := scheduling.NewRepository(s.store)
	acquired, err := leaderRepo.TryAcquireLeader(ctx, s.leaderID, usageRefreshLeaderTTL)
	if err != nil {
		s.logger.Warn("usage refresh leader acquire failed", "error", err)
		return
	}
	if !acquired {
		return
	}

	usageRepo := usage.NewRepository(s.store)
	accountRepo := accounts.NewRepository(s.store)
	beforePrimary, err := usageRepo.LatestByAccount(ctx, "primary", nil)
	if err != nil {
		s.logger.Warn("usage refresh latest primary failed", "error", err)
		return
	}
	beforeSecondary, err := usageRepo.LatestByAccount(ctx, "secondary", nil)
	if err != nil {
		s.logger.Warn("usage refresh latest secondary failed", "error", err)
		return
	}
	beforeMonthly, err := usageRepo.LatestByAccount(ctx, "monthly", nil)
	if err != nil {
		s.logger.Warn("usage refresh latest monthly failed", "error", err)
		return
	}
	records, err := accountRepo.ListProxyRecords(ctx)
	if err != nil {
		s.logger.Warn("usage refresh account list failed", "error", err)
		return
	}
	written, err := s.updater.RefreshAccounts(ctx, records, beforePrimary)
	if err != nil {
		s.logger.Warn("usage refresh failed", "error", err)
	}
	if written {
		afterPrimary, err := usageRepo.LatestByAccount(ctx, "primary", nil)
		if err != nil {
			s.logger.Warn("usage refresh latest primary after refresh failed", "error", err)
			return
		}
		afterSecondary, err := usageRepo.LatestByAccount(ctx, "secondary", nil)
		if err != nil {
			s.logger.Warn("usage refresh latest secondary after refresh failed", "error", err)
			return
		}
		afterMonthly, err := usageRepo.LatestByAccount(ctx, "monthly", nil)
		if err != nil {
			s.logger.Warn("usage refresh latest monthly after refresh failed", "error", err)
			return
		}
		recovered, err := ReconcileRecoverableAccountStatuses(ctx, accountRepo, records, afterPrimary, afterSecondary, afterMonthly)
		if err != nil {
			s.logger.Warn("usage refresh reconcile failed", "error", err)
		}
		if err := s.runLimitWarmup(ctx, accountRepo, beforePrimary, beforeSecondary, beforeMonthly, afterPrimary, afterSecondary, afterMonthly); err != nil {
			s.logger.Warn("usage refresh limit warmup failed", "error", err)
		}
		s.logger.Info("usage refresh completed", "written", written, "recovered", recovered, "beforePrimary", len(beforePrimary), "beforeSecondary", len(beforeSecondary))
		s.invalidate()
	}
}

func (s *RefreshScheduler) runLimitWarmup(
	ctx context.Context,
	accountRepo accounts.Repository,
	beforePrimary map[string]usage.Entry,
	beforeSecondary map[string]usage.Entry,
	beforeMonthly map[string]usage.Entry,
	afterPrimary map[string]usage.Entry,
	afterSecondary map[string]usage.Entry,
	afterMonthly map[string]usage.Entry,
) error {
	encryptor := s.encryptor
	if encryptor == nil {
		var err error
		encryptor, err = crypto.NewEncryptor(s.cfg.EncryptionKeyPath)
		if err != nil {
			return err
		}
	}
	settingsRepo := settings.NewRepository(s.store, encryptor)
	dashboardSettings, err := settingsRepo.Get(ctx)
	if err != nil {
		return err
	}
	if !dashboardSettings.LimitWarmupEnabled {
		return nil
	}
	refreshedAccounts, err := accountRepo.List(ctx)
	if err != nil {
		return err
	}
	service := limitwarmup.NewService(
		limitwarmup.NewRepository(s.store),
		requestlogs.NewRepository(s.store),
		limitwarmup.NewStreamingSender(encryptor, s.cfg),
	)
	return service.RunAfterUsageRefresh(ctx, limitwarmup.RefreshInputs{
		Accounts: refreshedAccounts,
		Settings: dashboardSettings,
		Before: limitwarmup.UsageSnapshot{
			Primary:   latestUsageMapFromEntries(beforePrimary, "primary"),
			Secondary: mergeLatestUsageMaps(latestUsageMapFromEntries(beforeSecondary, "secondary"), latestUsageMapFromEntries(beforeMonthly, "monthly")),
		},
		After: limitwarmup.UsageSnapshot{
			Primary:   latestUsageMapFromEntries(afterPrimary, "primary"),
			Secondary: mergeLatestUsageMaps(latestUsageMapFromEntries(afterSecondary, "secondary"), latestUsageMapFromEntries(afterMonthly, "monthly")),
		},
		DefaultModelSlug: defaultLimitWarmupModel(dashboardSettings),
	})
}

func mergeLatestUsageMaps(base map[string]accounts.LatestUsage, overlay map[string]accounts.LatestUsage) map[string]accounts.LatestUsage {
	if len(overlay) == 0 {
		return base
	}
	if base == nil {
		base = map[string]accounts.LatestUsage{}
	}
	for accountID, entry := range overlay {
		base[accountID] = entry
	}
	return base
}

func latestUsageMapFromEntries(entries map[string]usage.Entry, fallbackWindow string) map[string]accounts.LatestUsage {
	out := make(map[string]accounts.LatestUsage, len(entries))
	for accountID, entry := range entries {
		window := entry.Window.String
		if window == "" {
			window = fallbackWindow
		}
		out[accountID] = accounts.LatestUsage{
			AccountID:        entry.AccountID,
			Window:           window,
			UsedPercent:      entry.UsedPercent,
			ResetAt:          entry.ResetAt,
			WindowMinutes:    entry.WindowMinutes,
			CreditsHas:       entry.CreditsHas,
			CreditsUnlimited: entry.CreditsUnlimited,
			CreditsBalance:   entry.CreditsBalance,
			RecordedAt:       sql.NullString{String: entry.RecordedAt, Valid: entry.RecordedAt != ""},
		}
	}
	return out
}

func defaultLimitWarmupModel(settings settings.DashboardSettings) string {
	model := strings.TrimSpace(settings.LimitWarmupModel)
	if model != "" && !strings.EqualFold(model, "auto") {
		return model
	}
	return "gpt-5.4-mini"
}

func ReconcileRecoverableAccountStatuses(
	ctx context.Context,
	repo accounts.Repository,
	records []accounts.ProxyRecord,
	latestPrimary map[string]usage.Entry,
	latestSecondary map[string]usage.Entry,
	latestMonthly map[string]usage.Entry,
) (int, error) {
	recovered := 0
	for _, record := range records {
		if record.Status != proxy.AccountStatusRateLimited && record.Status != proxy.AccountStatusQuotaExceeded {
			continue
		}
		account := selectionAccountFromRecord(record)
		primary := usageEntryFromEntry(latestPrimary[record.ID])
		secondary := usageEntryFromEntry(selectLongWindowEntry(record, latestMonthly[record.ID], latestSecondary[record.ID]))
		state := proxy.BackgroundRecoveryStateFromAccount(&account, primary, secondary)
		if state.Status != proxy.AccountStatusActive {
			continue
		}
		update := accounts.StatusCompareUpdate{
			AccountID:                  record.ID,
			Status:                     proxy.AccountStatusActive,
			ExpectedStatus:             record.Status,
			ExpectedDeactivationReason: record.DeactivationReason,
			ExpectedResetAt:            nullFloatToNullInt(record.ResetAt),
			ExpectedBlockedAt:          nullFloatToNullInt(record.BlockedAt),
		}
		ok, err := repo.UpdateStatusIfCurrent(ctx, update)
		if err != nil {
			return recovered, err
		}
		if ok {
			recovered++
		}
	}
	return recovered, nil
}

type HTTPUsageUpdater struct {
	repo          usage.Repository
	accountsRepo  accounts.Repository
	encryptor     *crypto.Encryptor
	cfg           config.Config
	client        *http.Client
	usageURL      string
	authRefresher interface {
		Refresh(context.Context, accounts.Account) error
	}
	additionalQuotaRegistry *proxy.AdditionalQuotaRegistry
	lastSuccessfulRefresh   map[string]time.Time
}

func NewHTTPUsageUpdater(repo usage.Repository, accountsRepo accounts.Repository, encryptor *crypto.Encryptor, cfg config.Config) *HTTPUsageUpdater {
	return &HTTPUsageUpdater{
		repo:                    repo,
		accountsRepo:            accountsRepo,
		encryptor:               encryptor,
		cfg:                     cfg,
		client:                  &http.Client{Timeout: cfg.UsageFetchTimeout},
		usageURL:                "https://chatgpt.com/backend-api/wham/usage",
		authRefresher:           authguardian.NewOAuthRefresher(accountsRepo, encryptor, cfg, nil),
		additionalQuotaRegistry: proxy.NewAdditionalQuotaRegistry(),
		lastSuccessfulRefresh:   map[string]time.Time{},
	}
}

func (u *HTTPUsageUpdater) RefreshAccounts(ctx context.Context, records []accounts.ProxyRecord, latest map[string]usage.Entry) (bool, error) {
	wroteAny := false
	now := time.Now().UTC()
	for _, record := range records {
		if record.Status == proxy.AccountStatusReauthRequired || record.Status == proxy.AccountStatusDeactivated {
			continue
		}
		if latestEntryIsFresh(latest[record.ID], u.cfg.UsageRefreshInterval) {
			continue
		}
		if u.additionalOnlyUsageIsFresh(ctx, record.ID, now) {
			continue
		}
		wrote, err := u.refreshAccount(ctx, record)
		if err != nil {
			continue
		}
		u.lastSuccessfulRefresh[record.ID] = now
		wroteAny = wroteAny || wrote
	}
	return wroteAny, nil
}

func (u *HTTPUsageUpdater) additionalOnlyUsageIsFresh(ctx context.Context, accountID string, now time.Time) bool {
	interval := u.cfg.UsageRefreshInterval
	if interval <= 0 {
		return false
	}
	if refreshedAt, ok := u.lastSuccessfulRefresh[accountID]; ok && now.Sub(refreshedAt) < interval {
		return true
	}
	recordedAt, err := u.repo.LatestAdditionalRecordedAtForAccount(ctx, accountID)
	if err != nil || !recordedAt.Valid || strings.TrimSpace(recordedAt.String) == "" {
		return false
	}
	parsed, err := parseSQLiteTime(recordedAt.String)
	if err != nil {
		return false
	}
	if now.Sub(parsed) >= interval {
		return false
	}
	u.lastSuccessfulRefresh[accountID] = parsed
	return true
}

func (u *HTTPUsageUpdater) refreshAuthAndFetchUsage(ctx context.Context, record accounts.ProxyRecord) (usagePayload, error) {
	if u.accountsRepo.IsZero() || u.authRefresher == nil {
		return usagePayload{}, fmt.Errorf("usage refresh auth retry unavailable")
	}
	account, err := u.accountsRepo.Get(ctx, record.ID)
	if err != nil {
		return usagePayload{}, err
	}
	if account == nil {
		return usagePayload{}, fmt.Errorf("account %s not found for usage refresh auth retry", record.ID)
	}
	if err := u.authRefresher.Refresh(ctx, *account); err != nil {
		return usagePayload{}, err
	}
	refreshed, err := u.accountsRepo.Get(ctx, record.ID)
	if err != nil {
		return usagePayload{}, err
	}
	if refreshed == nil {
		return usagePayload{}, fmt.Errorf("account %s not found after usage refresh auth retry", record.ID)
	}
	token, err := u.encryptor.Decrypt(refreshed.AccessTokenEncrypted)
	if err != nil {
		return usagePayload{}, err
	}
	accountID := refreshed.ChatGPTAccountID.String
	if !refreshed.ChatGPTAccountID.Valid {
		accountID = record.ChatGPTAccountID.String
	}
	return u.fetchUsage(ctx, token, accountID)
}

func (u *HTTPUsageUpdater) refreshAccount(ctx context.Context, record accounts.ProxyRecord) (bool, error) {
	token, err := u.encryptor.Decrypt(record.AccessTokenEncrypted)
	if err != nil {
		return false, err
	}
	payload, err := u.fetchUsage(ctx, token, record.ChatGPTAccountID.String)
	if err != nil {
		if fetchErr, ok := err.(*usageFetchError); ok {
			if fetchErr.statusCode == http.StatusUnauthorized {
				refreshedPayload, refreshErr := u.refreshAuthAndFetchUsage(ctx, record)
				if refreshErr == nil {
					payload = refreshedPayload
					err = nil
				} else if shouldDeactivateForUsageError(fetchErr) {
					if updateErr := u.deactivateForUsageError(ctx, record, fetchErr); updateErr != nil {
						return false, updateErr
					}
					return false, nil
				} else {
					return false, refreshErr
				}
			} else if shouldDeactivateForUsageError(fetchErr) {
				if updateErr := u.deactivateForUsageError(ctx, record, fetchErr); updateErr != nil {
					return false, updateErr
				}
				return false, nil
			}
		}
		if err != nil {
			return false, err
		}
	}
	if err := u.syncIdentityMetadata(ctx, record, payload); err != nil {
		return false, err
	}
	now := time.Now().Unix()
	wrote := false
	if payload.RateLimit == nil {
		wroteAdditional, err := u.syncAdditionalUsage(ctx, record.ID, payload, now)
		return wroteAdditional, err
	}
	wroteAdditional, err := u.syncAdditionalUsage(ctx, record.ID, payload, now)
	if err != nil {
		return false, err
	}
	for _, candidate := range []struct {
		name   string
		window *usagePayloadWindow
	}{
		{"primary", normalizedPrimaryWindow(payload.RateLimit)},
		{"secondary", payload.RateLimit.SecondaryWindow},
		{"monthly", normalizedMonthlyWindow(payload.RateLimit)},
	} {
		if candidate.window == nil || candidate.window.UsedPercent == nil {
			continue
		}
		entry := usage.Entry{
			AccountID:   record.ID,
			Window:      sql.NullString{String: candidate.name, Valid: true},
			UsedPercent: *candidate.window.UsedPercent,
			ResetAt:     sql.NullInt64{Int64: resetAt(candidate.window, now), Valid: resetAt(candidate.window, now) > 0},
			WindowMinutes: sql.NullInt64{
				Int64: windowMinutes(candidate.window),
				Valid: windowMinutes(candidate.window) > 0,
			},
		}
		if candidate.name == "primary" || candidate.name == "monthly" {
			entry.CreditsHas = sql.NullBool{Bool: payload.Credits != nil && payload.Credits.HasCredits != nil && *payload.Credits.HasCredits, Valid: payload.Credits != nil && payload.Credits.HasCredits != nil}
			entry.CreditsUnlimited = sql.NullBool{Bool: payload.Credits != nil && payload.Credits.Unlimited != nil && *payload.Credits.Unlimited, Valid: payload.Credits != nil && payload.Credits.Unlimited != nil}
			if payload.Credits != nil && payload.Credits.Balance != nil {
				if balance, ok := parseFloatString(*payload.Credits.Balance); ok {
					entry.CreditsBalance = sql.NullFloat64{Float64: balance, Valid: true}
				}
			}
		}
		if _, err := u.repo.AddEntry(ctx, entry); err != nil {
			return wrote, err
		}
		wrote = true
	}
	return wrote || wroteAdditional, nil
}

func normalizedPrimaryWindow(rateLimit *usagePayloadRateLimit) *usagePayloadWindow {
	if normalizedMonthlyWindow(rateLimit) != nil {
		return nil
	}
	if rateLimit == nil {
		return nil
	}
	return rateLimit.PrimaryWindow
}

func normalizedMonthlyWindow(rateLimit *usagePayloadRateLimit) *usagePayloadWindow {
	if rateLimit == nil || rateLimit.PrimaryWindow == nil || rateLimit.SecondaryWindow != nil {
		return nil
	}
	if rateLimit.PrimaryWindow.LimitWindowSeconds == nil {
		return nil
	}
	if *rateLimit.PrimaryWindow.LimitWindowSeconds != int64((30*24*time.Hour)/time.Second) {
		return nil
	}
	return rateLimit.PrimaryWindow
}

func (u *HTTPUsageUpdater) syncAdditionalUsage(ctx context.Context, accountID string, payload usagePayload, now int64) (bool, error) {
	if payload.AdditionalRateLimits == nil {
		return false, nil
	}
	if len(*payload.AdditionalRateLimits) == 0 {
		if err := u.repo.DeleteAdditionalForAccount(ctx, accountID); err != nil {
			return false, err
		}
		return true, nil
	}
	type mergedWindow struct {
		limitName      string
		meteredFeature string
		usedPercent    float64
		resetAt        sql.NullInt64
		windowMinutes  sql.NullInt64
	}
	merged := map[string]map[string]mergedWindow{}
	for _, additional := range *payload.AdditionalRateLimits {
		if additional.RateLimit == nil {
			continue
		}
		quotaKey := u.additionalQuotaRegistry.QuotaKeyForUsage(additional.LimitName, additional.MeteredFeature)
		if quotaKey == "" {
			continue
		}
		for _, candidate := range []struct {
			window string
			value  *usagePayloadWindow
		}{
			{window: "primary", value: additional.RateLimit.PrimaryWindow},
			{window: "secondary", value: additional.RateLimit.SecondaryWindow},
		} {
			if candidate.value == nil || candidate.value.UsedPercent == nil {
				continue
			}
			window := mergedWindow{
				limitName:      additional.LimitName,
				meteredFeature: additional.MeteredFeature,
				usedPercent:    *candidate.value.UsedPercent,
				resetAt:        sql.NullInt64{Int64: resetAt(candidate.value, now), Valid: resetAt(candidate.value, now) > 0},
				windowMinutes:  sql.NullInt64{Int64: windowMinutes(candidate.value), Valid: windowMinutes(candidate.value) > 0},
			}
			windows := merged[quotaKey]
			if windows == nil {
				windows = map[string]mergedWindow{}
				merged[quotaKey] = windows
			}
			if existing, ok := windows[candidate.window]; !ok || window.usedPercent > existing.usedPercent {
				windows[candidate.window] = window
			}
		}
	}

	current := map[string]map[string]struct{}{}
	for quotaKey, windows := range merged {
		for window, item := range windows {
			if _, err := u.repo.AddAdditionalEntry(ctx, usage.AdditionalEntry{
				AccountID:      accountID,
				QuotaKey:       quotaKey,
				LimitName:      item.limitName,
				MeteredFeature: item.meteredFeature,
				Window:         window,
				UsedPercent:    item.usedPercent,
				ResetAt:        item.resetAt,
				WindowMinutes:  item.windowMinutes,
			}); err != nil {
				return false, err
			}
			if current[quotaKey] == nil {
				current[quotaKey] = map[string]struct{}{}
			}
			current[quotaKey][window] = struct{}{}
		}
	}

	existingKeys, err := u.repo.ListAdditionalQuotaKeys(ctx, []string{accountID})
	if err != nil {
		return false, err
	}
	for _, staleKey := range existingKeys {
		windows, ok := current[staleKey]
		if !ok {
			if err := u.repo.DeleteAdditionalForAccountAndQuotaKey(ctx, accountID, staleKey); err != nil {
				return false, err
			}
			continue
		}
		for _, window := range []string{"primary", "secondary"} {
			if _, ok := windows[window]; !ok {
				if err := u.repo.DeleteAdditionalForAccountQuotaKeyWindow(ctx, accountID, staleKey, window); err != nil {
					return false, err
				}
			}
		}
	}
	return true, nil
}

func (u *HTTPUsageUpdater) fetchUsage(ctx context.Context, token string, accountID string) (usagePayload, error) {
	usageURL := u.usageURL
	if usageURL == "" {
		usageURL = "https://chatgpt.com/backend-api/wham/usage"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return usagePayload{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if accountID != "" && !strings.HasPrefix(accountID, "email_") && !strings.HasPrefix(accountID, "local_") {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	var lastErr error
	for attempt := 0; attempt <= u.cfg.UsageFetchMaxRetries; attempt++ {
		resp, err := u.client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			payload, err := decodeUsageResponse(resp)
			if err == nil {
				return payload, nil
			}
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return usagePayload{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
	return usagePayload{}, lastErr
}

type usagePayload struct {
	PlanType             *string                            `json:"plan_type"`
	WorkspaceID          *string                            `json:"workspace_id"`
	WorkspaceLabel       *string                            `json:"workspace_label"`
	SeatType             *string                            `json:"seat_type"`
	RateLimit            *usagePayloadRateLimit             `json:"rate_limit"`
	Credits              *usagePayloadCredits               `json:"credits"`
	AdditionalRateLimits *[]usagePayloadAdditionalRateLimit `json:"additional_rate_limits"`
}

type usagePayloadRateLimit struct {
	PrimaryWindow   *usagePayloadWindow `json:"primary_window"`
	SecondaryWindow *usagePayloadWindow `json:"secondary_window"`
}

type usagePayloadWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	ResetAt            *int64   `json:"reset_at"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds"`
}

type usagePayloadCredits struct {
	HasCredits *bool   `json:"has_credits"`
	Unlimited  *bool   `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type usagePayloadAdditionalRateLimit struct {
	LimitName      string                 `json:"limit_name"`
	MeteredFeature string                 `json:"metered_feature"`
	RateLimit      *usagePayloadRateLimit `json:"rate_limit"`
}

func decodeUsageResponse(resp *http.Response) (usagePayload, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return usagePayload{}, err
	}
	if resp.StatusCode >= 400 {
		return usagePayload{}, usageFetchErrorFromResponse(resp.StatusCode, body)
	}
	var payload usagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return usagePayload{}, err
	}
	return payload, nil
}

type usageFetchError struct {
	statusCode int
	code       string
	message    string
}

func (e *usageFetchError) Error() string {
	return e.message
}

func usageFetchErrorFromResponse(statusCode int, body []byte) *usageFetchError {
	message := strings.TrimSpace(string(body))
	code := ""
	var envelope struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		switch typed := envelope.Error.(type) {
		case map[string]any:
			if value, ok := typed["code"].(string); ok {
				code = value
			}
			if value, ok := typed["message"].(string); ok && strings.TrimSpace(value) != "" {
				message = value
			} else if value, ok := typed["error_description"].(string); ok && strings.TrimSpace(value) != "" {
				message = value
			}
		case string:
			if strings.TrimSpace(typed) != "" {
				message = typed
			}
		}
		if strings.TrimSpace(message) == "" && strings.TrimSpace(envelope.Message) != "" {
			message = envelope.Message
		}
	}
	if message == "" {
		message = fmt.Sprintf("Usage fetch failed (%d)", statusCode)
	}
	return &usageFetchError{statusCode: statusCode, code: code, message: message}
}

func shouldDeactivateForUsageError(err *usageFetchError) bool {
	if err == nil {
		return false
	}
	if err.statusCode == http.StatusPaymentRequired || err.statusCode == http.StatusNotFound {
		return true
	}
	if _, ok := proxy.PermanentFailureCodes[err.code]; ok {
		return true
	}
	lowered := strings.ToLower(err.message)
	return strings.Contains(lowered, "your openai account has been deactivated") ||
		strings.Contains(lowered, "account has been deactivated")
}

func (u *HTTPUsageUpdater) syncIdentityMetadata(ctx context.Context, record accounts.ProxyRecord, payload usagePayload) error {
	if u.accountsRepo.IsZero() {
		return nil
	}
	nextPlanType := coerceAccountPlanType(stringPtrValue(payload.PlanType), record.PlanType)
	nextWorkspaceID := cleanOptionalStringPtr(payload.WorkspaceID, record.WorkspaceID)
	nextWorkspaceLabel := cleanOptionalStringPtr(payload.WorkspaceLabel, record.WorkspaceLabel)
	nextSeatType := cleanOptionalStringPtr(payload.SeatType, record.SeatType)
	if nextPlanType == record.PlanType &&
		nullStringEqual(nextWorkspaceID, record.WorkspaceID) &&
		nullStringEqual(nextWorkspaceLabel, record.WorkspaceLabel) &&
		nullStringEqual(nextSeatType, record.SeatType) {
		return nil
	}
	_, err := u.accountsRepo.UpdateTokens(ctx, accounts.TokenUpdate{
		AccountID:             record.ID,
		AccessTokenEncrypted:  record.AccessTokenEncrypted,
		RefreshTokenEncrypted: record.RefreshTokenEncrypted,
		IDTokenEncrypted:      record.IDTokenEncrypted,
		LastRefresh:           record.LastRefresh,
		PlanType:              nextPlanType,
		Email:                 record.Email,
		ChatGPTAccountID:      record.ChatGPTAccountID,
		WorkspaceID:           nextWorkspaceID,
		WorkspaceLabel:        nextWorkspaceLabel,
		SeatType:              nextSeatType,
	})
	return err
}

func coerceAccountPlanType(value string, fallback string) string {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		if strings.TrimSpace(fallback) == "" {
			return "free"
		}
		return fallback
	}
	return strings.ToLower(cleaned)
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cleanOptionalStringPtr(value *string, fallback sql.NullString) sql.NullString {
	if value == nil {
		return fallback
	}
	cleaned := strings.TrimSpace(*value)
	if cleaned == "" {
		return fallback
	}
	return sql.NullString{String: cleaned, Valid: true}
}

func nullStringEqual(left sql.NullString, right sql.NullString) bool {
	if left.Valid != right.Valid {
		return false
	}
	if !left.Valid {
		return true
	}
	return left.String == right.String
}

func (u *HTTPUsageUpdater) deactivateForUsageError(ctx context.Context, record accounts.ProxyRecord, err *usageFetchError) error {
	if u.accountsRepo.IsZero() {
		return nil
	}
	status := proxy.AccountStatusDeactivated
	if _, ok := proxy.PermanentFailureCodes[err.code]; ok {
		status = proxy.AccountStatusForPermanentFailure(err.code)
	}
	reason := fmt.Sprintf("Usage API error: HTTP %d - %s", err.statusCode, err.message)
	_, updateErr := u.accountsRepo.UpdateStatus(ctx, record.ID, status, sql.NullString{String: reason, Valid: true})
	return updateErr
}

type noopAccountsRefresher struct{}

func (noopAccountsRefresher) RefreshAccounts(context.Context, []accounts.ProxyRecord, map[string]usage.Entry) (bool, error) {
	return false, nil
}

func selectionAccountFromRecord(record accounts.ProxyRecord) proxy.SelectionAccount {
	account := proxy.SelectionAccount{
		ID:                     record.ID,
		Status:                 record.Status,
		PlanType:               record.PlanType,
		RoutingPolicy:          record.RoutingPolicy,
		SecurityWorkAuthorized: record.SecurityWorkAuthorized,
	}
	if record.DeactivationReason.Valid {
		account.DeactivationReason = &record.DeactivationReason.String
	}
	if record.ResetAt.Valid {
		v := record.ResetAt.Float64
		account.ResetAt = &v
	}
	if record.BlockedAt.Valid {
		v := record.BlockedAt.Float64
		account.BlockedAt = &v
	}
	return account
}

func usageEntryFromEntry(entry usage.Entry) *proxy.UsageEntry {
	if entry.AccountID == "" {
		return nil
	}
	out := &proxy.UsageEntry{
		AccountID:   entry.AccountID,
		Window:      entry.Window.String,
		UsedPercent: &entry.UsedPercent,
	}
	if entry.ResetAt.Valid {
		out.ResetAt = &entry.ResetAt.Int64
	}
	if entry.WindowMinutes.Valid {
		out.WindowMinutes = &entry.WindowMinutes.Int64
	}
	if entry.CreditsHas.Valid {
		out.CreditsHas = &entry.CreditsHas.Bool
	}
	if entry.CreditsUnlimited.Valid {
		out.CreditsUnlimited = &entry.CreditsUnlimited.Bool
	}
	if entry.CreditsBalance.Valid {
		out.CreditsBalance = &entry.CreditsBalance.Float64
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", entry.RecordedAt, time.UTC); err == nil {
		out.RecordedAt = &parsed
	}
	return out
}

func selectLongWindowEntry(record accounts.ProxyRecord, monthly usage.Entry, secondary usage.Entry) usage.Entry {
	if monthly.AccountID != "" && usage.CapacityForPlan(record.PlanType, "monthly") != nil {
		return monthly
	}
	return secondary
}

func nullFloatToNullInt(value sql.NullFloat64) sql.NullInt64 {
	if !value.Valid {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(value.Float64), Valid: true}
}

func latestEntryIsFresh(entry usage.Entry, interval time.Duration) bool {
	if entry.AccountID == "" || entry.RecordedAt == "" {
		return false
	}
	recordedAt, err := parseSQLiteTime(entry.RecordedAt)
	if err != nil {
		return false
	}
	return time.Since(recordedAt) < interval
}

func resetAt(window *usagePayloadWindow, now int64) int64 {
	if window.ResetAt != nil {
		return *window.ResetAt
	}
	if window.ResetAfterSeconds != nil {
		return now + *window.ResetAfterSeconds
	}
	return 0
}

func windowMinutes(window *usagePayloadWindow) int64 {
	if window.LimitWindowSeconds == nil {
		return 0
	}
	return *window.LimitWindowSeconds / 60
}

func parseSQLiteTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty sqlite timestamp")
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid sqlite timestamp %q", value)
}

func parseFloatString(raw string) (float64, bool) {
	var value float64
	if _, err := fmt.Sscanf(raw, "%f", &value); err != nil {
		return 0, false
	}
	return value, true
}
