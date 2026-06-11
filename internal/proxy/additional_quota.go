package proxy

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/usage"
)

const (
	additionalQuotaDataUnavailable        = "additional_quota_data_unavailable"
	additionalQuotaExhausted              = "additional_quota_exhausted"
	noAdditionalQuotaEligibleAccounts     = "no_additional_quota_eligible_accounts"
	defaultAdditionalUsageFreshnessWindow = 180 * time.Second
)

var additionalQuotaExemptPlanTypes = stringSet("free", "free_workspace", "k12")

type additionalQuotaDefinition struct {
	QuotaKey              string   `json:"quota_key"`
	DisplayLabel          string   `json:"display_label"`
	RoutingPolicy         string   `json:"routing_policy"`
	ModelIDs              []string `json:"model_ids"`
	AppliesToPlans        []string `json:"applies_to_plans"`
	QuotaKeyAliases       []string `json:"quota_key_aliases"`
	LimitNameAliases      []string `json:"limit_name_aliases"`
	MeteredFeatureAliases []string `json:"metered_feature_aliases"`
}

type AdditionalQuotaRegistry struct {
	mu          sync.RWMutex
	definitions map[string]additionalQuotaDefinition
	modelIndex  map[string]string
	aliasIndex  map[string]string
	keyAlias    map[string]string
}

func NewAdditionalQuotaRegistry() *AdditionalQuotaRegistry {
	registry := &AdditionalQuotaRegistry{
		definitions: map[string]additionalQuotaDefinition{},
		modelIndex:  map[string]string{},
		aliasIndex:  map[string]string{},
		keyAlias:    map[string]string{},
	}
	_ = registry.LoadDefault()
	return registry
}

func (r *AdditionalQuotaRegistry) LoadDefault() error {
	candidates := []string{}
	if path := os.Getenv("CODEX_LB_ADDITIONAL_QUOTA_REGISTRY_FILE"); path != "" {
		candidates = append(candidates, path)
	}
	candidates = append(candidates,
		filepath.Join("config", "additional_quota_registry.json"),
		filepath.Join("..", "..", "config", "additional_quota_registry.json"),
	)
	var data []byte
	var err error
	for _, path := range candidates {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	var entries []additionalQuotaDefinition
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.definitions = make(map[string]additionalQuotaDefinition, len(entries))
	r.modelIndex = make(map[string]string, len(entries))
	r.aliasIndex = make(map[string]string, len(entries))
	r.keyAlias = make(map[string]string, len(entries))
	for _, entry := range entries {
		key := normalizeAdditionalQuotaKey(entry.QuotaKey)
		if key == "" {
			continue
		}
		entry.QuotaKey = key
		r.definitions[key] = entry
		r.keyAlias[key] = key
		for _, modelID := range entry.ModelIDs {
			normalized := normalizeAdditionalQuotaKey(modelID)
			if normalized != "" {
				r.modelIndex[normalized] = key
			}
		}
		for _, alias := range entry.QuotaKeyAliases {
			if normalized := normalizeAdditionalQuotaKey(alias); normalized != "" {
				r.keyAlias[normalized] = key
			}
		}
		for _, alias := range append(append([]string{}, entry.LimitNameAliases...), entry.MeteredFeatureAliases...) {
			if normalized := normalizeAdditionalQuotaKey(alias); normalized != "" {
				r.aliasIndex[normalized] = key
			}
		}
	}
	return nil
}

func (r *AdditionalQuotaRegistry) QuotaKeyForModel(model string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if model == "" {
		return ""
	}
	return r.modelIndex[normalizeAdditionalQuotaKey(model)]
}

func (r *AdditionalQuotaRegistry) QuotaKeyForUsage(limitName, meteredFeature string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, candidate := range []string{limitName, meteredFeature} {
		normalized := normalizeAdditionalQuotaKey(candidate)
		if normalized == "" {
			continue
		}
		if key := r.aliasIndex[normalized]; key != "" {
			return key
		}
		if key := r.keyAlias[normalized]; key != "" {
			return key
		}
	}
	if normalized := normalizeAdditionalQuotaKey(limitName); normalized != "" {
		return normalized
	}
	return normalizeAdditionalQuotaKey(meteredFeature)
}

func (r *AdditionalQuotaRegistry) RoutingPolicyOverride(quotaKey string) *string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, ok := r.definitions[quotaKey]
	if !ok {
		return nil
	}
	policy := strings.ToLower(strings.TrimSpace(definition.RoutingPolicy))
	if policy == "" || policy == "inherit" {
		return nil
	}
	return &policy
}

func normalizeAdditionalQuotaKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('_')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "_")
}

type additionalLimitFilterResult struct {
	Accounts        []SelectionAccount
	LatestPrimary   map[string]*UsageEntry
	LatestSecondary map[string]*UsageEntry
	ErrorMessage    string
	ErrorCode       string
}

func filterAccountsForAdditionalLimit(
	registry *AdditionalQuotaRegistry,
	accounts []SelectionAccount,
	model string,
	quotaKey string,
	explicitLimit bool,
	latestPrimary map[string]*UsageEntry,
	latestSecondary map[string]*UsageEntry,
	freshPrimary map[string]*UsageEntry,
	freshSecondary map[string]*UsageEntry,
) additionalLimitFilterResult {
	if len(accounts) == 0 {
		return additionalLimitFilterResult{Accounts: []SelectionAccount{}}
	}
	eligible := make([]SelectionAccount, 0, len(accounts))
	blockedByData := false
	blockedByExhaustion := false
	for _, account := range accounts {
		switch additionalQuotaEligibility(
			account.ID,
			account.PlanType,
			quotaKey,
			explicitLimit,
			registry,
			latestPrimary,
			latestSecondary,
			freshPrimary,
			freshSecondary,
		) {
		case "eligible":
			eligible = append(eligible, account)
		case "data_unavailable":
			blockedByData = true
		case "quota_exhausted":
			blockedByExhaustion = true
		}
	}
	if len(eligible) > 0 {
		eligibleIDs := make(map[string]struct{}, len(eligible))
		for _, account := range eligible {
			eligibleIDs[account.ID] = struct{}{}
		}
		return additionalLimitFilterResult{
			Accounts:        eligible,
			LatestPrimary:   filterUsageMap(latestPrimary, eligibleIDs),
			LatestSecondary: filterUsageMap(latestSecondary, eligibleIDs),
		}
	}
	if blockedByData {
		return additionalLimitFilterResult{
			ErrorCode:    additionalQuotaDataUnavailable,
			ErrorMessage: "No fresh additional quota data available for model '" + model + "'",
		}
	}
	if blockedByExhaustion {
		return additionalLimitFilterResult{
			ErrorCode:    additionalQuotaExhausted,
			ErrorMessage: "Additional quota exhausted for model '" + model + "'",
		}
	}
	return additionalLimitFilterResult{
		ErrorCode:    noAdditionalQuotaEligibleAccounts,
		ErrorMessage: "No accounts with available additional quota for model '" + model + "'",
	}
}

func additionalQuotaEligibility(
	accountID string,
	planType string,
	quotaKey string,
	explicitLimit bool,
	registry *AdditionalQuotaRegistry,
	latestPrimary map[string]*UsageEntry,
	latestSecondary map[string]*UsageEntry,
	freshPrimary map[string]*UsageEntry,
	freshSecondary map[string]*UsageEntry,
) string {
	if !explicitLimit && !additionalQuotaAppliesToPlan(registry, quotaKey, planType) {
		return "eligible"
	}
	latestPrimaryEntry := latestPrimary[accountID]
	latestSecondaryEntry := latestSecondary[accountID]
	primaryEntry := freshPrimary[accountID]
	secondaryEntry := freshSecondary[accountID]

	if latestPrimaryEntry == nil && latestSecondaryEntry == nil {
		return "data_unavailable"
	}
	if latestPrimaryEntry != nil && primaryEntry == nil {
		return "data_unavailable"
	}
	if latestSecondaryEntry != nil && secondaryEntry == nil {
		return "data_unavailable"
	}
	if primaryEntry != nil && additionalUsageIsExhausted(primaryEntry) {
		return "quota_exhausted"
	}
	if secondaryEntry != nil && additionalUsageIsExhausted(secondaryEntry) {
		return "quota_exhausted"
	}
	return "eligible"
}

func additionalQuotaAppliesToPlan(registry *AdditionalQuotaRegistry, quotaKey, planType string) bool {
	registry.mu.RLock()
	definition, ok := registry.definitions[quotaKey]
	registry.mu.RUnlock()
	if !ok || len(definition.AppliesToPlans) == 0 {
		return true
	}
	normalizedPlan := lowerTrimLocal(planType)
	if normalizedPlan == "" {
		return true
	}
	allowed := stringSet(definition.AppliesToPlans...)
	if _, ok := allowed[normalizedPlan]; ok {
		return true
	}
	_, exempt := additionalQuotaExemptPlanTypes[normalizedPlan]
	return !exempt
}

func additionalUsageIsExhausted(entry *UsageEntry) bool {
	if entry == nil || entry.UsedPercent == nil {
		return false
	}
	if entry.ResetAt != nil && *entry.ResetAt <= time.Now().Unix() {
		return false
	}
	return *entry.UsedPercent >= 100.0
}

func filterUsageMap(entries map[string]*UsageEntry, allowed map[string]struct{}) map[string]*UsageEntry {
	out := make(map[string]*UsageEntry, len(allowed))
	for accountID, entry := range entries {
		if _, ok := allowed[accountID]; ok {
			out[accountID] = entry
		}
	}
	return out
}

func additionalUsageFreshSince(now time.Time) time.Time {
	return now.Add(-defaultAdditionalUsageFreshnessWindow)
}

func usageEntriesFromAdditional(rows map[string]usage.AdditionalEntry) map[string]*UsageEntry {
	out := make(map[string]*UsageEntry, len(rows))
	for accountID, row := range rows {
		recordedAt := parseRecordedAtString(sql.NullString{String: row.RecordedAt, Valid: row.RecordedAt != ""})
		out[accountID] = UsageEntryFromRecorded(
			accountID,
			row.Window,
			row.UsedPercent,
			nullInt64ToPtr(row.ResetAt),
			nullInt64ToPtr(row.WindowMinutes),
			recordedAt,
			nil,
			nil,
			nil,
		)
	}
	return out
}

func mergeAdditionalUsageIntoInputs(
	inputs *SelectionInputs,
	accountIDs map[string]struct{},
	additionalPrimary map[string]*UsageEntry,
	additionalSecondary map[string]*UsageEntry,
) {
	for accountID := range accountIDs {
		delete(inputs.LatestPrimary, accountID)
		delete(inputs.LatestSecondary, accountID)
		if entry, ok := additionalPrimary[accountID]; ok {
			inputs.LatestPrimary[accountID] = entry
		}
		if entry, ok := additionalSecondary[accountID]; ok {
			inputs.LatestSecondary[accountID] = entry
		}
		inputs.IgnoreStandardQuotaAccountIDs[accountID] = struct{}{}
	}
}
