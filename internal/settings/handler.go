package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/cacheinvalidation"
	"github.com/soju06/codex-lb/internal/httputil"
)

var loopbackHosts = map[string]struct{}{
	"localhost": {},
	"127.0.0.1": {},
	"::1":       {},
	"[::1]":     {},
}

type Handler struct {
	repo      Repository
	bumper    cacheBumper
	auditRepo *audit.Repository
}

type cacheBumper interface {
	Bump(ctx context.Context, namespace string) error
}

func NewHandler(repo Repository, bumpers ...cacheBumper) Handler {
	var bumper cacheBumper
	if len(bumpers) > 0 {
		bumper = bumpers[0]
	}
	return Handler{repo: repo, bumper: bumper}
}

func (h Handler) WithAudit(repo audit.Repository) Handler {
	h.auditRepo = &repo
	return h
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	settings, err := h.repo.Get(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, settings)
}

func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	current, err := h.repo.Get(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var payload UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	updated, err := h.repo.Update(r.Context(), current, payload)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "settings_changed", map[string]any{"changed_fields": changedSettingsFields(current, updated)})
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (h Handler) ConnectAddress(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, RuntimeConnectAddressResponse{
		ConnectAddress: resolveRuntimeConnectAddress(r),
	})
}

func (h Handler) UpstreamProxyAdmin(w http.ResponseWriter, r *http.Request) {
	admin, err := h.repo.UpstreamProxyAdmin(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, admin)
}

func (h Handler) CreateProxyEndpoint(w http.ResponseWriter, r *http.Request) {
	var payload UpstreamProxyEndpointCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	endpoint, err := h.repo.CreateProxyEndpoint(r.Context(), payload)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "proxy_endpoint_created", map[string]any{"endpoint_id": endpoint.ID})
	httputil.WriteJSON(w, http.StatusOK, endpoint)
}

func (h Handler) CreateProxyPool(w http.ResponseWriter, r *http.Request) {
	var payload UpstreamProxyPoolCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	pool, err := h.repo.CreateProxyPool(r.Context(), payload)
	if err != nil {
		if errors.Is(err, ErrProxyEndpointNotFound) {
			httputil.WriteError(w, http.StatusBadRequest, "proxy_endpoint_not_found", "Proxy endpoint not found")
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "proxy_pool_created", map[string]any{"pool_id": pool.ID})
	httputil.WriteJSON(w, http.StatusOK, pool)
}

func (h Handler) AddProxyPoolMember(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolID")
	var payload UpstreamProxyPoolMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	pool, err := h.repo.AddProxyPoolMember(r.Context(), poolID, payload)
	if err != nil {
		switch {
		case errors.Is(err, ErrProxyPoolNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "proxy_pool_not_found", "Proxy pool not found")
		case errors.Is(err, ErrProxyEndpointNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "proxy_endpoint_not_found", "Proxy endpoint not found")
		default:
			httputil.WriteServerError(w, err)
		}
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "proxy_pool_member_added", map[string]any{"pool_id": poolID, "endpoint_id": payload.EndpointID})
	httputil.WriteJSON(w, http.StatusOK, pool)
}

func (h Handler) PutAccountProxyBinding(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	var payload AccountProxyBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	binding, err := h.repo.PutAccountProxyBinding(r.Context(), accountID, payload)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "account_not_found", "Account not found")
		case errors.Is(err, ErrProxyPoolNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "proxy_pool_not_found", "Proxy pool not found")
		default:
			httputil.WriteServerError(w, err)
		}
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "account_proxy_binding_changed", map[string]any{"account_id": accountID, "pool_id": binding.PoolID, "is_active": binding.IsActive})
	httputil.WriteJSON(w, http.StatusOK, binding)
}

func (h Handler) bumpInvalidation(ctx context.Context) {
	if h.bumper != nil {
		_ = h.bumper.Bump(ctx, cacheinvalidation.NamespaceSettings)
	}
}

func (h Handler) audit(r *http.Request, action string, details map[string]any) {
	audit.LogRequest(h.auditRepo, r, action, details)
}

func changedSettingsFields(before, after DashboardSettings) []string {
	fields := make([]string, 0)
	if before.StickyThreadsEnabled != after.StickyThreadsEnabled {
		fields = append(fields, "sticky_threads_enabled")
	}
	if before.UpstreamStreamTransport != after.UpstreamStreamTransport {
		fields = append(fields, "upstream_stream_transport")
	}
	if before.UpstreamProxyRoutingEnabled != after.UpstreamProxyRoutingEnabled {
		fields = append(fields, "upstream_proxy_routing_enabled")
	}
	if stringValue(before.UpstreamProxyDefaultPoolID) != stringValue(after.UpstreamProxyDefaultPoolID) {
		fields = append(fields, "upstream_proxy_default_pool_id")
	}
	if before.PreferEarlierResetAccounts != after.PreferEarlierResetAccounts {
		fields = append(fields, "prefer_earlier_reset_accounts")
	}
	if before.PreferEarlierResetWindow != after.PreferEarlierResetWindow {
		fields = append(fields, "prefer_earlier_reset_window")
	}
	if before.RoutingStrategy != after.RoutingStrategy {
		fields = append(fields, "routing_strategy")
	}
	if before.RelativeAvailabilityPower != after.RelativeAvailabilityPower {
		fields = append(fields, "relative_availability_power")
	}
	if before.RelativeAvailabilityTopK != after.RelativeAvailabilityTopK {
		fields = append(fields, "relative_availability_top_k")
	}
	if stringValue(before.SingleAccountID) != stringValue(after.SingleAccountID) {
		fields = append(fields, "single_account_id")
	}
	if before.OpenAICacheAffinityMaxAgeSeconds != after.OpenAICacheAffinityMaxAgeSeconds {
		fields = append(fields, "openai_cache_affinity_max_age_seconds")
	}
	if before.DashboardSessionTTLSeconds != after.DashboardSessionTTLSeconds {
		fields = append(fields, "dashboard_session_ttl_seconds")
	}
	if before.StickyReallocationBudgetThresholdPct != after.StickyReallocationBudgetThresholdPct {
		fields = append(fields, "sticky_reallocation_budget_threshold_pct")
	}
	if before.StickyReallocationPrimaryBudgetThresholdPct != after.StickyReallocationPrimaryBudgetThresholdPct {
		fields = append(fields, "sticky_reallocation_primary_budget_threshold_pct")
	}
	if before.StickyReallocationSecondaryBudgetThresholdPct != after.StickyReallocationSecondaryBudgetThresholdPct {
		fields = append(fields, "sticky_reallocation_secondary_budget_threshold_pct")
	}
	if !reflect.DeepEqual(before.AdditionalQuotaRoutingPolicies, after.AdditionalQuotaRoutingPolicies) {
		fields = append(fields, "additional_quota_routing_policies")
	}
	if before.WarmupModel != after.WarmupModel {
		fields = append(fields, "warmup_model")
	}
	if before.ImportWithoutOverwrite != after.ImportWithoutOverwrite {
		fields = append(fields, "import_without_overwrite")
	}
	if before.TOTPRequiredOnLogin != after.TOTPRequiredOnLogin {
		fields = append(fields, "totp_required_on_login")
	}
	if before.APIKeyAuthEnabled != after.APIKeyAuthEnabled {
		fields = append(fields, "api_key_auth_enabled")
	}
	if before.LimitWarmupEnabled != after.LimitWarmupEnabled {
		fields = append(fields, "limit_warmup_enabled")
	}
	if before.LimitWarmupWindows != after.LimitWarmupWindows {
		fields = append(fields, "limit_warmup_windows")
	}
	if before.LimitWarmupModel != after.LimitWarmupModel {
		fields = append(fields, "limit_warmup_model")
	}
	if before.LimitWarmupPrompt != after.LimitWarmupPrompt {
		fields = append(fields, "limit_warmup_prompt")
	}
	if before.LimitWarmupCooldownSeconds != after.LimitWarmupCooldownSeconds {
		fields = append(fields, "limit_warmup_cooldown_seconds")
	}
	if before.LimitWarmupMinAvailablePercent != after.LimitWarmupMinAvailablePercent {
		fields = append(fields, "limit_warmup_min_available_percent")
	}
	if before.WeeklyPaceWorkingDays != after.WeeklyPaceWorkingDays {
		fields = append(fields, "weekly_pace_working_days")
	}
	return fields
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func resolveRuntimeConnectAddress(r *http.Request) string {
	if override := strings.TrimSpace(os.Getenv("CODEX_LB_CONNECT_ADDRESS")); override != "" {
		return override
	}
	host := r.URL.Hostname()
	if isNonLoopbackIPv4(host) {
		return host
	}
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized != "" {
		if _, isLoopback := loopbackHosts[normalized]; !isLoopback {
			if resolved := resolveHostnameIPv4(host); resolved != "" {
				return resolved
			}
			return host
		}
	}
	return "<codex-lb-ip-or-dns>"
}

func isNonLoopbackIPv4(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsUnspecified()
}

func resolveHostnameIPv4(hostname string) string {
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			return ip.String()
		}
	}
	return ""
}
