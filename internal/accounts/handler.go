package accounts

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/soju06/codex-lb/internal/cache"
	"github.com/soju06/codex-lb/internal/platform"
)

type Handler struct {
	repo    Repository
	summary *cache.TTL[[]AccountSummary]
}

type UsageSummary struct {
	PrimaryRemainingPercent   *float64 `json:"primaryRemainingPercent"`
	SecondaryRemainingPercent *float64 `json:"secondaryRemainingPercent"`
	MonthlyRemainingPercent   *float64 `json:"monthlyRemainingPercent,omitempty"`
}

type AccountSummary struct {
	AccountID                 string        `json:"accountId"`
	Email                     string        `json:"email"`
	Alias                     *string       `json:"alias"`
	DisplayName               string        `json:"displayName"`
	WorkspaceID               *string       `json:"workspaceId,omitempty"`
	WorkspaceLabel            *string       `json:"workspaceLabel,omitempty"`
	SeatType                  *string       `json:"seatType,omitempty"`
	PlanType                  string        `json:"planType"`
	RoutingPolicy             string        `json:"routingPolicy"`
	Status                    string        `json:"status"`
	SecurityWorkAuthorized    bool          `json:"securityWorkAuthorized"`
	Usage                     *UsageSummary `json:"usage"`
	ResetAtPrimary            *string       `json:"resetAtPrimary"`
	ResetAtSecondary          *string       `json:"resetAtSecondary"`
	ResetAtMonthly            *string       `json:"resetAtMonthly,omitempty"`
	WindowMinutesPrimary      *int64        `json:"windowMinutesPrimary"`
	WindowMinutesSecondary    *int64        `json:"windowMinutesSecondary"`
	WindowMinutesMonthly      *int64        `json:"windowMinutesMonthly,omitempty"`
	CapacityCreditsPrimary    *float64      `json:"capacityCreditsPrimary"`
	RemainingCreditsPrimary   *float64      `json:"remainingCreditsPrimary"`
	CapacityCreditsSecondary  *float64      `json:"capacityCreditsSecondary"`
	RemainingCreditsSecondary *float64      `json:"remainingCreditsSecondary"`
	CreditsHas                *bool         `json:"creditsHas"`
	CreditsUnlimited          *bool         `json:"creditsUnlimited"`
	CreditsBalance            *float64      `json:"creditsBalance"`
	AdditionalQuotas          []any         `json:"additionalQuotas"`
	LimitWarmupEnabled        bool          `json:"limitWarmupEnabled"`
	LimitWarmup               any           `json:"limitWarmup"`
}

type ListResponse struct {
	Accounts []AccountSummary `json:"accounts"`
}

func NewHandler(repo Repository) Handler {
	return Handler{
		repo:    repo,
		summary: cache.NewTTL[[]AccountSummary](2 * time.Second),
	}
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.Summaries(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ListResponse{Accounts: summaries})
}

func (h Handler) Summaries(r *http.Request) ([]AccountSummary, error) {
	if cached, ok := h.summary.Get("accounts"); ok {
		return cached, nil
	}
	ctx := r.Context()
	accountRows, err := h.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	primary, err := h.repo.LatestUsageByWindow(ctx, "primary")
	if err != nil {
		return nil, err
	}
	secondary, err := h.repo.LatestUsageByWindow(ctx, "secondary")
	if err != nil {
		return nil, err
	}
	monthly, err := h.repo.LatestUsageByWindow(ctx, "monthly")
	if err != nil {
		return nil, err
	}

	summaries := make([]AccountSummary, 0, len(accountRows))
	for _, account := range accountRows {
		p := primary[account.ID]
		s := secondary[account.ID]
		m := monthly[account.ID]
		displayName := account.Email
		if account.Alias.Valid && account.Alias.String != "" {
			displayName = account.Alias.String
		}
		summary := AccountSummary{
			AccountID:              account.ID,
			Email:                  account.Email,
			Alias:                  nullStringPtr(account.Alias),
			DisplayName:            displayName,
			WorkspaceID:            nullStringPtr(account.WorkspaceID),
			WorkspaceLabel:         nullStringPtr(account.WorkspaceLabel),
			SeatType:               nullStringPtr(account.SeatType),
			PlanType:               account.PlanType,
			RoutingPolicy:          account.RoutingPolicy,
			Status:                 account.Status,
			SecurityWorkAuthorized: account.SecurityWorkAuthorized,
			Usage: &UsageSummary{
				PrimaryRemainingPercent:   remainingPercentPtr(p),
				SecondaryRemainingPercent: remainingPercentPtr(s),
				MonthlyRemainingPercent:   remainingPercentPtr(m),
			},
			ResetAtPrimary:         platform.UnixSecondsToISO(p.ResetAt),
			ResetAtSecondary:       platform.UnixSecondsToISO(s.ResetAt),
			ResetAtMonthly:         platform.UnixSecondsToISO(m.ResetAt),
			WindowMinutesPrimary:   nullInt64Ptr(p.WindowMinutes),
			WindowMinutesSecondary: nullInt64Ptr(s.WindowMinutes),
			WindowMinutesMonthly:   nullInt64Ptr(m.WindowMinutes),
			CreditsHas:             nullBoolPtr(p.CreditsHas),
			CreditsBalance:         nullFloat64Ptr(p.CreditsBalance),
			CreditsUnlimited:       boolValuePtr(false),
			AdditionalQuotas:       []any{},
			LimitWarmupEnabled:     account.LimitWarmupEnabled,
			LimitWarmup:            nil,
		}
		summary.CapacityCreditsPrimary, summary.RemainingCreditsPrimary = credits(p)
		summary.CapacityCreditsSecondary, summary.RemainingCreditsSecondary = credits(s)
		summaries = append(summaries, summary)
	}
	h.summary.Set("accounts", summaries)
	return summaries, nil
}

func remainingPercentPtr(usage LatestUsage) *float64 {
	if usage.AccountID == "" {
		return nil
	}
	value := max(0, 100-usage.UsedPercent)
	return &value
}

func credits(usage LatestUsage) (*float64, *float64) {
	if usage.AccountID == "" || !usage.CreditsBalance.Valid {
		return nil, nil
	}
	remaining := usage.CreditsBalance.Float64
	remainingPct := max(0.01, 100-usage.UsedPercent)
	capacity := remaining / (remainingPct / 100)
	return &capacity, &remaining
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	return &value.String
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullBoolPtr(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	return &value.Bool
}

func boolValuePtr(value bool) *bool {
	return &value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": map[string]string{
			"code":    "server_error",
			"message": err.Error(),
		},
	})
}
