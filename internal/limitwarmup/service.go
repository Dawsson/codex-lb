package limitwarmup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/settings"
)

const (
	Source      = "limit_warmup"
	RequestKind = "warmup"
	Header      = "x-codex-lb-limit-warmup"

	defaultWarmupInstructions = "Reply with OK only."
	maxConcurrentWarmupSends  = 4
)

var quotaErrorCodes = map[string]struct{}{
	"insufficient_quota":  {},
	"quota_exceeded":      {},
	"rate_limit_exceeded": {},
	"usage_limit_reached": {},
}

type AttemptsRepository interface {
	LatestByAccount(ctx context.Context, accountIDs []string) (map[string]Attempt, error)
	TryCreateAttempt(ctx context.Context, accountID, window string, resetAt int64, model, attemptedAt string) (Attempt, bool, error)
	CompleteAttempt(ctx context.Context, attemptID int64, status, completedAt string, errorCode, errorMessage sql.NullString) (Attempt, bool, error)
}

type RequestLogRepository interface {
	Insert(ctx context.Context, params requestlogs.InsertParams) error
}

type Sender interface {
	Send(ctx context.Context, account accounts.Account, params SendParams) (SendResult, error)
}

type SendParams struct {
	Model        string
	Prompt       string
	Instructions string
	RequestID    string
}

type SendResult struct {
	RequestID     string
	Success       bool
	LatencyMS     int64
	InputTokens   *int64
	OutputTokens  *int64
	ErrorCode     string
	ErrorMessage  string
	Transport     string
	ServiceTier   *string
	RequestedTier *string
}

type Service struct {
	attempts AttemptsRepository
	logs     RequestLogRepository
	sender   Sender
	now      func() time.Time
}

func NewService(attempts AttemptsRepository, logs RequestLogRepository, sender Sender) Service {
	return Service{
		attempts: attempts,
		logs:     logs,
		sender:   sender,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s Service) WithClock(now func() time.Time) Service {
	s.now = now
	return s
}

type UsageSnapshot struct {
	Primary   map[string]accounts.LatestUsage
	Secondary map[string]accounts.LatestUsage
}

type RefreshInputs struct {
	Accounts         []accounts.Account
	Settings         settings.DashboardSettings
	Before           UsageSnapshot
	After            UsageSnapshot
	DefaultModelSlug string
}

func (s Service) RunAfterUsageRefresh(ctx context.Context, inputs RefreshInputs) error {
	if !inputs.Settings.LimitWarmupEnabled {
		return nil
	}
	if s.sender == nil {
		return fmt.Errorf("limit warmup sender is required")
	}
	windows := selectedWindows(inputs.Settings.LimitWarmupWindows)
	if len(windows) == 0 {
		return nil
	}

	accountIDs := make([]string, 0, len(inputs.Accounts))
	for _, account := range inputs.Accounts {
		accountIDs = append(accountIDs, account.ID)
	}
	latestAttempts, err := s.attempts.LatestByAccount(ctx, accountIDs)
	if err != nil {
		return err
	}

	type queued struct {
		attempt Attempt
		account accounts.Account
		model   string
	}
	var queue []queued
	for _, account := range inputs.Accounts {
		if strings.ToLower(account.Status) != "active" || !account.LimitWarmupEnabled {
			continue
		}
		if inCooldown(latestAttempts[account.ID], inputs.Settings.LimitWarmupCooldownSeconds, s.now()) {
			continue
		}
		for _, window := range windows {
			candidate, ok := buildCandidate(account.ID, window, inputs.Before, inputs.After, inputs.Settings.LimitWarmupMinAvailablePercent)
			if !ok {
				continue
			}
			model := resolveModel(inputs.Settings.LimitWarmupModel, inputs.DefaultModelSlug)
			if model == "" {
				attempt, created, err := s.attempts.TryCreateAttempt(ctx, account.ID, window, candidate.resetAt, "auto", formatDBTime(s.now()))
				if err != nil {
					return err
				}
				if created {
					_, _, err = s.attempts.CompleteAttempt(ctx, attempt.ID, "skipped", formatDBTime(s.now()), sqlString("model_unavailable"), sqlString("No eligible priced text model was available for warm-up"))
					if err != nil {
						return err
					}
				}
				continue
			}
			attempt, created, err := s.attempts.TryCreateAttempt(ctx, account.ID, window, candidate.resetAt, model, formatDBTime(s.now()))
			if err != nil {
				return err
			}
			if !created {
				continue
			}
			queue = append(queue, queued{attempt: attempt, account: account, model: model})
		}
	}

	sem := make(chan struct{}, maxConcurrentWarmupSends)
	errs := make(chan error, len(queue))
	var wg sync.WaitGroup
	for _, item := range queue {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
			if err := s.sendAndComplete(ctx, item.account, item.attempt, item.model, inputs.Settings.LimitWarmupPrompt); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Service) sendAndComplete(ctx context.Context, account accounts.Account, attempt Attempt, model, prompt string) error {
	requestID := fmt.Sprintf("limit-warmup-%d-%d", attempt.ID, s.now().UnixNano())
	result, err := s.sender.Send(ctx, account, SendParams{
		Model:        model,
		Prompt:       prompt,
		Instructions: defaultWarmupInstructions,
		RequestID:    requestID,
	})
	if err != nil {
		_, _, completeErr := s.attempts.CompleteAttempt(ctx, attempt.ID, "failed", formatDBTime(s.now()), sqlString("warmup_send_failed"), sqlString(truncate(err.Error(), 1000)))
		return completeErr
	}
	if result.RequestID == "" {
		result.RequestID = requestID
	}
	status := "failed"
	if result.Success {
		status = "succeeded"
	}
	errorCode := result.ErrorCode
	if _, ok := quotaErrorCodes[errorCode]; ok {
		errorCode = "quota_still_exhausted"
	}
	if err := s.insertRequestLog(ctx, account, model, result, errorCode); err != nil {
		_, _, _ = s.attempts.CompleteAttempt(ctx, attempt.ID, "failed", formatDBTime(s.now()), sqlString("warmup_completion_failed"), sqlString("Limit warm-up completion failed"))
		return err
	}
	_, _, err = s.attempts.CompleteAttempt(ctx, attempt.ID, status, formatDBTime(s.now()), nullString(errorCode), nullString(truncate(result.ErrorMessage, 1000)))
	return err
}

func (s Service) insertRequestLog(ctx context.Context, account accounts.Account, model string, result SendResult, errorCode string) error {
	status := "error"
	if result.Success {
		status = "success"
	}
	transport := result.Transport
	if transport == "" {
		transport = "http"
	}
	return s.logs.Insert(ctx, requestlogs.InsertParams{
		RequestID:            result.RequestID,
		RequestKind:          RequestKind,
		Model:                model,
		AccountID:            &account.ID,
		PlanType:             &account.PlanType,
		Status:               status,
		ErrorCode:            nullStringPtr(errorCode),
		ErrorMessage:         nullStringPtr(truncate(result.ErrorMessage, 1000)),
		InputTokens:          result.InputTokens,
		OutputTokens:         result.OutputTokens,
		LatencyMS:            &result.LatencyMS,
		UserAgent:            stringPtr("codex-lb-limit-warmup"),
		Transport:            &transport,
		ServiceTier:          result.ServiceTier,
		RequestedServiceTier: result.RequestedTier,
		Source:               stringPtr(Source),
	})
}

type warmupCandidate struct {
	resetAt int64
}

func selectedWindows(value string) []string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "both":
		return []string{"primary", "secondary"}
	case "primary":
		return []string{"primary"}
	case "secondary":
		return []string{"secondary"}
	default:
		return nil
	}
}

func buildCandidate(accountID, window string, before, after UsageSnapshot, minAvailablePercent float64) (warmupCandidate, bool) {
	beforeEntry, ok := effectiveUsageEntry(accountID, window, before)
	if !ok || !beforeEntry.ResetAt.Valid {
		return warmupCandidate{}, false
	}
	afterEntry, ok := effectiveUsageEntry(accountID, window, after)
	if !ok || !afterEntry.ResetAt.Valid {
		return warmupCandidate{}, false
	}
	if beforeEntry.UsedPercent < 100.0 || afterEntry.UsedPercent >= 100.0 {
		return warmupCandidate{}, false
	}
	availablePercent := 100.0 - afterEntry.UsedPercent
	if minAvailablePercent < 100.0 && availablePercent < minAvailablePercent {
		return warmupCandidate{}, false
	}
	if afterEntry.ResetAt.Int64 <= beforeEntry.ResetAt.Int64 {
		return warmupCandidate{}, false
	}
	return warmupCandidate{resetAt: afterEntry.ResetAt.Int64}, true
}

func effectiveUsageEntry(accountID, window string, snapshot UsageSnapshot) (accounts.LatestUsage, bool) {
	if window == "primary" {
		entry, ok := snapshot.Primary[accountID]
		if !ok || isWeeklyWindow(entry.WindowMinutes) {
			return accounts.LatestUsage{}, false
		}
		return entry, true
	}
	primary, hasPrimary := snapshot.Primary[accountID]
	secondary, hasSecondary := snapshot.Secondary[accountID]
	if hasPrimary && isWeeklyWindow(primary.WindowMinutes) {
		return primary, true
	}
	return secondary, hasSecondary
}

func isWeeklyWindow(value sql.NullInt64) bool {
	return value.Valid && value.Int64 >= 10080
}

func inCooldown(attempt Attempt, cooldownSeconds int, now time.Time) bool {
	if attempt.ID == 0 || cooldownSeconds <= 0 {
		return false
	}
	attemptedAt, err := parseDBTime(attempt.AttemptedAt)
	if err != nil {
		return false
	}
	return now.Sub(attemptedAt) < time.Duration(cooldownSeconds)*time.Second
}

func resolveModel(configured, fallback string) string {
	normalized := strings.TrimSpace(configured)
	if normalized != "" && !strings.EqualFold(normalized, "auto") {
		return normalized
	}
	return strings.TrimSpace(fallback)
}

func formatDBTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05")
}

func parseDBTime(value string) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.UTC); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, value)
}

func sqlString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sqlString(value)
}

func nullStringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	return value[:limit-1] + "..."
}
