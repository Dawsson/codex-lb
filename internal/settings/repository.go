package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httputil"
)

type Repository struct {
	store     *db.Store
	encryptor *crypto.Encryptor
}

type settingsRow struct {
	StickyThreadsEnabled                                bool
	UpstreamStreamTransport                             string
	UpstreamProxyRoutingEnabled                         bool
	UpstreamProxyDefaultPoolID                          sql.NullString
	PreferEarlierResetAccounts                          bool
	PreferEarlierResetWindow                            string
	RoutingStrategy                                     string
	RelativeAvailabilityPower                           float64
	RelativeAvailabilityTopK                            int
	SingleAccountID                                     sql.NullString
	OpenAICacheAffinityMaxAgeSeconds                    int
	DashboardSessionTTLSeconds                          int
	HTTPResponsesSessionBridgePromptCacheIdleTTLSeconds int
	HTTPResponsesSessionBridgeGatewaySafeMode           bool
	StickyReallocationBudgetThresholdPct                float64
	StickyReallocationPrimaryBudgetThresholdPct         float64
	StickyReallocationSecondaryBudgetThresholdPct       float64
	AdditionalQuotaRoutingPoliciesJSON                  string
	WarmupModel                                         string
	ImportWithoutOverwrite                              bool
	TOTPRequiredOnLogin                                 bool
	TOTPSecretEncrypted                                 []byte
	APIKeyAuthEnabled                                   bool
	LimitWarmupEnabled                                  bool
	LimitWarmupWindows                                  string
	LimitWarmupModel                                    string
	LimitWarmupPrompt                                   string
	LimitWarmupCooldownSeconds                          int
	LimitWarmupMinAvailablePercent                      float64
	WeeklyPaceWorkingDays                               string
}

func NewRepository(store *db.Store, encryptor *crypto.Encryptor) Repository {
	return Repository{store: store, encryptor: encryptor}
}

func (r Repository) Get(ctx context.Context) (DashboardSettings, error) {
	row, err := r.loadRow(ctx)
	if err != nil {
		return DashboardSettings{}, err
	}
	return row.toResponse(), nil
}

func (r Repository) Update(ctx context.Context, current DashboardSettings, update UpdateRequest) (DashboardSettings, error) {
	next := current
	if update.StickyThreadsEnabled != nil {
		next.StickyThreadsEnabled = *update.StickyThreadsEnabled
	}
	if update.UpstreamStreamTransport != nil {
		next.UpstreamStreamTransport = *update.UpstreamStreamTransport
	}
	if update.UpstreamProxyRoutingEnabled != nil {
		next.UpstreamProxyRoutingEnabled = *update.UpstreamProxyRoutingEnabled
	}
	if update.UpstreamProxyDefaultPoolID != nil {
		next.UpstreamProxyDefaultPoolID = update.UpstreamProxyDefaultPoolID
	}
	if update.PreferEarlierResetAccounts != nil {
		next.PreferEarlierResetAccounts = *update.PreferEarlierResetAccounts
	}
	if update.PreferEarlierResetWindow != nil {
		next.PreferEarlierResetWindow = *update.PreferEarlierResetWindow
	}
	if update.RoutingStrategy != nil {
		next.RoutingStrategy = *update.RoutingStrategy
	}
	if update.RelativeAvailabilityPower != nil {
		next.RelativeAvailabilityPower = *update.RelativeAvailabilityPower
	}
	if update.RelativeAvailabilityTopK != nil {
		next.RelativeAvailabilityTopK = *update.RelativeAvailabilityTopK
	}
	if update.SingleAccountID != nil {
		next.SingleAccountID = update.SingleAccountID
	}
	if update.OpenAICacheAffinityMaxAgeSeconds != nil {
		next.OpenAICacheAffinityMaxAgeSeconds = *update.OpenAICacheAffinityMaxAgeSeconds
	}
	if update.DashboardSessionTTLSeconds != nil {
		next.DashboardSessionTTLSeconds = *update.DashboardSessionTTLSeconds
	}
	if update.StickyReallocationPrimaryBudgetThresholdPct != nil {
		next.StickyReallocationPrimaryBudgetThresholdPct = *update.StickyReallocationPrimaryBudgetThresholdPct
		next.StickyReallocationBudgetThresholdPct = *update.StickyReallocationPrimaryBudgetThresholdPct
	} else if update.StickyReallocationBudgetThresholdPct != nil {
		next.StickyReallocationBudgetThresholdPct = *update.StickyReallocationBudgetThresholdPct
		next.StickyReallocationPrimaryBudgetThresholdPct = *update.StickyReallocationBudgetThresholdPct
	}
	if update.StickyReallocationSecondaryBudgetThresholdPct != nil {
		next.StickyReallocationSecondaryBudgetThresholdPct = *update.StickyReallocationSecondaryBudgetThresholdPct
	}
	if update.AdditionalQuotaRoutingPolicies != nil {
		next.AdditionalQuotaRoutingPolicies = *update.AdditionalQuotaRoutingPolicies
	}
	if update.WarmupModel != nil {
		next.WarmupModel = *update.WarmupModel
	}
	if update.ImportWithoutOverwrite != nil {
		next.ImportWithoutOverwrite = *update.ImportWithoutOverwrite
	}
	if update.TOTPRequiredOnLogin != nil {
		next.TOTPRequiredOnLogin = *update.TOTPRequiredOnLogin
	}
	if update.APIKeyAuthEnabled != nil {
		next.APIKeyAuthEnabled = *update.APIKeyAuthEnabled
	}
	if update.LimitWarmupEnabled != nil {
		next.LimitWarmupEnabled = *update.LimitWarmupEnabled
	}
	if update.LimitWarmupWindows != nil {
		next.LimitWarmupWindows = *update.LimitWarmupWindows
	}
	if update.LimitWarmupModel != nil {
		next.LimitWarmupModel = *update.LimitWarmupModel
	}
	if update.LimitWarmupPrompt != nil {
		next.LimitWarmupPrompt = *update.LimitWarmupPrompt
	}
	if update.LimitWarmupCooldownSeconds != nil {
		next.LimitWarmupCooldownSeconds = *update.LimitWarmupCooldownSeconds
	}
	if update.LimitWarmupMinAvailablePercent != nil {
		next.LimitWarmupMinAvailablePercent = *update.LimitWarmupMinAvailablePercent
	}
	if update.WeeklyPaceWorkingDays != nil {
		next.WeeklyPaceWorkingDays = *update.WeeklyPaceWorkingDays
	}

	_, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings SET
			sticky_threads_enabled = ?,
			upstream_stream_transport = ?,
			upstream_proxy_routing_enabled = ?,
			upstream_proxy_default_pool_id = ?,
			prefer_earlier_reset_accounts = ?,
			prefer_earlier_reset_window = ?,
			routing_strategy = ?,
			relative_availability_power = ?,
			relative_availability_top_k = ?,
			single_account_id = ?,
			openai_cache_affinity_max_age_seconds = ?,
			dashboard_session_ttl_seconds = ?,
			sticky_reallocation_budget_threshold_pct = ?,
			sticky_reallocation_primary_budget_threshold_pct = ?,
			sticky_reallocation_secondary_budget_threshold_pct = ?,
			additional_quota_routing_policies_json = ?,
			warmup_model = ?,
			import_without_overwrite = ?,
			totp_required_on_login = ?,
			api_key_auth_enabled = ?,
			limit_warmup_enabled = ?,
			limit_warmup_windows = ?,
			limit_warmup_model = ?,
			limit_warmup_prompt = ?,
			limit_warmup_cooldown_seconds = ?,
			limit_warmup_min_available_percent = ?,
			weekly_pace_working_days = ?
	`, next.StickyThreadsEnabled,
		next.UpstreamStreamTransport,
		next.UpstreamProxyRoutingEnabled,
		nullStringValue(next.UpstreamProxyDefaultPoolID),
		next.PreferEarlierResetAccounts,
		next.PreferEarlierResetWindow,
		next.RoutingStrategy,
		next.RelativeAvailabilityPower,
		next.RelativeAvailabilityTopK,
		nullStringValue(next.SingleAccountID),
		next.OpenAICacheAffinityMaxAgeSeconds,
		next.DashboardSessionTTLSeconds,
		next.StickyReallocationBudgetThresholdPct,
		next.StickyReallocationPrimaryBudgetThresholdPct,
		next.StickyReallocationSecondaryBudgetThresholdPct,
		encodeAdditionalQuotaPolicies(next.AdditionalQuotaRoutingPolicies),
		next.WarmupModel,
		next.ImportWithoutOverwrite,
		next.TOTPRequiredOnLogin,
		next.APIKeyAuthEnabled,
		next.LimitWarmupEnabled,
		next.LimitWarmupWindows,
		next.LimitWarmupModel,
		next.LimitWarmupPrompt,
		next.LimitWarmupCooldownSeconds,
		next.LimitWarmupMinAvailablePercent,
		next.WeeklyPaceWorkingDays,
	)
	if err != nil {
		return DashboardSettings{}, fmt.Errorf("update dashboard settings: %w", err)
	}
	return next, nil
}

func (r Repository) UpstreamProxyAdmin(ctx context.Context) (UpstreamProxyAdmin, error) {
	settings, err := r.loadRow(ctx)
	if err != nil {
		return UpstreamProxyAdmin{}, err
	}
	endpoints, err := r.listProxyEndpoints(ctx)
	if err != nil {
		return UpstreamProxyAdmin{}, err
	}
	pools, err := r.listProxyPools(ctx)
	if err != nil {
		return UpstreamProxyAdmin{}, err
	}
	bindings, err := r.listProxyBindings(ctx)
	if err != nil {
		return UpstreamProxyAdmin{}, err
	}
	return UpstreamProxyAdmin{
		RoutingEnabled: settings.UpstreamProxyRoutingEnabled,
		DefaultPoolID:  nullStringPtr(settings.UpstreamProxyDefaultPoolID),
		Endpoints:      httputil.EmptySlice(endpoints),
		Pools:          httputil.EmptySlice(pools),
		Bindings:       httputil.EmptySlice(bindings),
	}, nil
}

func (r Repository) CreateProxyEndpoint(ctx context.Context, req UpstreamProxyEndpointCreateRequest) (UpstreamProxyEndpoint, error) {
	id := uuid.NewString()
	var passwordEncrypted []byte
	if req.Password != nil && *req.Password != "" {
		encrypted, err := r.encryptor.Encrypt(*req.Password)
		if err != nil {
			return UpstreamProxyEndpoint{}, err
		}
		passwordEncrypted = encrypted
	}
	_, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO proxy_endpoints (id, name, scheme, host, port, username, password_encrypted, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, req.Name, req.Scheme, req.Host, req.Port, nullStringValue(req.Username), passwordEncrypted, req.IsActive)
	if err != nil {
		return UpstreamProxyEndpoint{}, fmt.Errorf("create proxy endpoint: %w", err)
	}
	return UpstreamProxyEndpoint{
		ID:       id,
		Name:     req.Name,
		Scheme:   req.Scheme,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		IsActive: req.IsActive,
	}, nil
}

func (r Repository) CreateProxyPool(ctx context.Context, req UpstreamProxyPoolCreateRequest) (UpstreamProxyPool, error) {
	if err := r.validateEndpointIDs(ctx, req.EndpointIDs); err != nil {
		return UpstreamProxyPool{}, err
	}
	poolID := uuid.NewString()
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return UpstreamProxyPool{}, fmt.Errorf("begin proxy pool tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO proxy_pools (id, name, is_active) VALUES (?, ?, ?)
	`, poolID, req.Name, req.IsActive); err != nil {
		return UpstreamProxyPool{}, fmt.Errorf("create proxy pool: %w", err)
	}
	for sortOrder, endpointID := range req.EndpointIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO proxy_pool_members (id, pool_id, endpoint_id, sort_order, weight, is_active)
			VALUES (?, ?, ?, ?, 1, 1)
		`, uuid.NewString(), poolID, endpointID, sortOrder); err != nil {
			return UpstreamProxyPool{}, fmt.Errorf("create proxy pool member: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return UpstreamProxyPool{}, fmt.Errorf("commit proxy pool tx: %w", err)
	}
	return UpstreamProxyPool{
		ID:          poolID,
		Name:        req.Name,
		IsActive:    req.IsActive,
		EndpointIDs: httputil.EmptySlice(req.EndpointIDs),
	}, nil
}

func (r Repository) AddProxyPoolMember(ctx context.Context, poolID string, req UpstreamProxyPoolMemberRequest) (UpstreamProxyPool, error) {
	pool, err := r.getProxyPool(ctx, poolID)
	if err != nil {
		return UpstreamProxyPool{}, err
	}
	if err := r.validateEndpointIDs(ctx, []string{req.EndpointID}); err != nil {
		return UpstreamProxyPool{}, err
	}
	if _, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO proxy_pool_members (id, pool_id, endpoint_id, sort_order, weight, is_active)
		VALUES (?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), poolID, req.EndpointID, req.SortOrder, req.Weight, req.IsActive); err != nil {
		return UpstreamProxyPool{}, fmt.Errorf("add proxy pool member: %w", err)
	}
	endpointIDs, err := r.poolEndpointIDs(ctx, poolID)
	if err != nil {
		return UpstreamProxyPool{}, err
	}
	pool.EndpointIDs = httputil.EmptySlice(endpointIDs)
	return pool, nil
}

func (r Repository) PutAccountProxyBinding(ctx context.Context, accountID string, req AccountProxyBindingRequest) (AccountProxyBinding, error) {
	if err := r.validateAccountID(ctx, accountID); err != nil {
		return AccountProxyBinding{}, err
	}
	if err := r.validatePoolID(ctx, req.PoolID); err != nil {
		return AccountProxyBinding{}, err
	}
	var existingID sql.NullString
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT id FROM account_proxy_bindings WHERE account_id = ? LIMIT 1
	`, accountID).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return AccountProxyBinding{}, fmt.Errorf("load account proxy binding: %w", err)
	}
	if existingID.Valid {
		_, err = r.store.DB().ExecContext(ctx, `
			UPDATE account_proxy_bindings SET pool_id = ?, is_active = ? WHERE account_id = ?
		`, req.PoolID, req.IsActive, accountID)
	} else {
		_, err = r.store.DB().ExecContext(ctx, `
			INSERT INTO account_proxy_bindings (id, account_id, pool_id, is_active)
			VALUES (?, ?, ?, ?)
		`, uuid.NewString(), accountID, req.PoolID, req.IsActive)
	}
	if err != nil {
		return AccountProxyBinding{}, fmt.Errorf("upsert account proxy binding: %w", err)
	}
	return AccountProxyBinding{AccountID: accountID, PoolID: req.PoolID, IsActive: req.IsActive}, nil
}

func (r Repository) loadRow(ctx context.Context) (settingsRow, error) {
	var row settingsRow
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT sticky_threads_enabled,
		       upstream_stream_transport,
		       upstream_proxy_routing_enabled,
		       upstream_proxy_default_pool_id,
		       prefer_earlier_reset_accounts,
		       prefer_earlier_reset_window,
		       routing_strategy,
		       relative_availability_power,
		       relative_availability_top_k,
		       single_account_id,
		       openai_cache_affinity_max_age_seconds,
		       dashboard_session_ttl_seconds,
		       http_responses_session_bridge_prompt_cache_idle_ttl_seconds,
		       http_responses_session_bridge_gateway_safe_mode,
		       sticky_reallocation_budget_threshold_pct,
		       sticky_reallocation_primary_budget_threshold_pct,
		       sticky_reallocation_secondary_budget_threshold_pct,
		       additional_quota_routing_policies_json,
		       warmup_model,
		       import_without_overwrite,
		       totp_required_on_login,
		       totp_secret_encrypted,
		       api_key_auth_enabled,
		       limit_warmup_enabled,
		       limit_warmup_windows,
		       limit_warmup_model,
		       limit_warmup_prompt,
		       limit_warmup_cooldown_seconds,
		       limit_warmup_min_available_percent,
		       weekly_pace_working_days
		  FROM dashboard_settings
		 ORDER BY id
		 LIMIT 1
	`).Scan(
		&row.StickyThreadsEnabled,
		&row.UpstreamStreamTransport,
		&row.UpstreamProxyRoutingEnabled,
		&row.UpstreamProxyDefaultPoolID,
		&row.PreferEarlierResetAccounts,
		&row.PreferEarlierResetWindow,
		&row.RoutingStrategy,
		&row.RelativeAvailabilityPower,
		&row.RelativeAvailabilityTopK,
		&row.SingleAccountID,
		&row.OpenAICacheAffinityMaxAgeSeconds,
		&row.DashboardSessionTTLSeconds,
		&row.HTTPResponsesSessionBridgePromptCacheIdleTTLSeconds,
		&row.HTTPResponsesSessionBridgeGatewaySafeMode,
		&row.StickyReallocationBudgetThresholdPct,
		&row.StickyReallocationPrimaryBudgetThresholdPct,
		&row.StickyReallocationSecondaryBudgetThresholdPct,
		&row.AdditionalQuotaRoutingPoliciesJSON,
		&row.WarmupModel,
		&row.ImportWithoutOverwrite,
		&row.TOTPRequiredOnLogin,
		&row.TOTPSecretEncrypted,
		&row.APIKeyAuthEnabled,
		&row.LimitWarmupEnabled,
		&row.LimitWarmupWindows,
		&row.LimitWarmupModel,
		&row.LimitWarmupPrompt,
		&row.LimitWarmupCooldownSeconds,
		&row.LimitWarmupMinAvailablePercent,
		&row.WeeklyPaceWorkingDays,
	)
	if err == sql.ErrNoRows {
		return settingsRow{}, fmt.Errorf("dashboard settings row missing")
	}
	if err != nil {
		return settingsRow{}, fmt.Errorf("load dashboard settings: %w", err)
	}
	return row, nil
}

func (row settingsRow) toResponse() DashboardSettings {
	return DashboardSettings{
		StickyThreadsEnabled:                                row.StickyThreadsEnabled,
		UpstreamStreamTransport:                             row.UpstreamStreamTransport,
		UpstreamProxyRoutingEnabled:                         row.UpstreamProxyRoutingEnabled,
		UpstreamProxyDefaultPoolID:                          nullStringPtr(row.UpstreamProxyDefaultPoolID),
		PreferEarlierResetAccounts:                          row.PreferEarlierResetAccounts,
		PreferEarlierResetWindow:                            row.PreferEarlierResetWindow,
		RoutingStrategy:                                     row.RoutingStrategy,
		RelativeAvailabilityPower:                           row.RelativeAvailabilityPower,
		RelativeAvailabilityTopK:                            row.RelativeAvailabilityTopK,
		SingleAccountID:                                     nullStringPtr(row.SingleAccountID),
		OpenAICacheAffinityMaxAgeSeconds:                    row.OpenAICacheAffinityMaxAgeSeconds,
		DashboardSessionTTLSeconds:                          row.DashboardSessionTTLSeconds,
		HTTPResponsesSessionBridgePromptCacheIdleTTLSeconds: row.HTTPResponsesSessionBridgePromptCacheIdleTTLSeconds,
		HTTPResponsesSessionBridgeGatewaySafeMode:           row.HTTPResponsesSessionBridgeGatewaySafeMode,
		StickyReallocationBudgetThresholdPct:                row.StickyReallocationBudgetThresholdPct,
		StickyReallocationPrimaryBudgetThresholdPct:         row.StickyReallocationPrimaryBudgetThresholdPct,
		StickyReallocationSecondaryBudgetThresholdPct:       row.StickyReallocationSecondaryBudgetThresholdPct,
		AdditionalQuotaRoutingPolicies:                      decodeAdditionalQuotaPolicies(row.AdditionalQuotaRoutingPoliciesJSON),
		AdditionalQuotaPolicies:                             []any{},
		WarmupModel:                                         row.WarmupModel,
		ImportWithoutOverwrite:                              row.ImportWithoutOverwrite,
		TOTPRequiredOnLogin:                                 row.TOTPRequiredOnLogin,
		TOTPConfigured:                                      len(row.TOTPSecretEncrypted) > 0,
		APIKeyAuthEnabled:                                   row.APIKeyAuthEnabled,
		LimitWarmupEnabled:                                  row.LimitWarmupEnabled,
		LimitWarmupWindows:                                  row.LimitWarmupWindows,
		LimitWarmupModel:                                    row.LimitWarmupModel,
		LimitWarmupPrompt:                                   row.LimitWarmupPrompt,
		LimitWarmupCooldownSeconds:                          row.LimitWarmupCooldownSeconds,
		LimitWarmupMinAvailablePercent:                      row.LimitWarmupMinAvailablePercent,
		WeeklyPaceWorkingDays:                               row.WeeklyPaceWorkingDays,
	}
}

func (r Repository) listProxyEndpoints(ctx context.Context) ([]UpstreamProxyEndpoint, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, name, scheme, host, port, username, is_active
		  FROM proxy_endpoints
		 ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list proxy endpoints: %w", err)
	}
	defer rows.Close()
	var endpoints []UpstreamProxyEndpoint
	for rows.Next() {
		var endpoint UpstreamProxyEndpoint
		var username sql.NullString
		if err := rows.Scan(&endpoint.ID, &endpoint.Name, &endpoint.Scheme, &endpoint.Host, &endpoint.Port, &username, &endpoint.IsActive); err != nil {
			return nil, fmt.Errorf("scan proxy endpoint: %w", err)
		}
		endpoint.Username = nullStringPtr(username)
		endpoints = append(endpoints, endpoint)
	}
	return httputil.EmptySlice(endpoints), rows.Err()
}

func (r Repository) listProxyPools(ctx context.Context) ([]UpstreamProxyPool, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, name, is_active FROM proxy_pools ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list proxy pools: %w", err)
	}
	defer rows.Close()
	var pools []UpstreamProxyPool
	for rows.Next() {
		var pool UpstreamProxyPool
		if err := rows.Scan(&pool.ID, &pool.Name, &pool.IsActive); err != nil {
			return nil, fmt.Errorf("scan proxy pool: %w", err)
		}
		endpointIDs, err := r.poolEndpointIDs(ctx, pool.ID)
		if err != nil {
			return nil, err
		}
		pool.EndpointIDs = httputil.EmptySlice(endpointIDs)
		pools = append(pools, pool)
	}
	return httputil.EmptySlice(pools), rows.Err()
}

func (r Repository) listProxyBindings(ctx context.Context) ([]AccountProxyBinding, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id, pool_id, is_active
		  FROM account_proxy_bindings
		 ORDER BY account_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list proxy bindings: %w", err)
	}
	defer rows.Close()
	var bindings []AccountProxyBinding
	for rows.Next() {
		var binding AccountProxyBinding
		if err := rows.Scan(&binding.AccountID, &binding.PoolID, &binding.IsActive); err != nil {
			return nil, fmt.Errorf("scan proxy binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	return httputil.EmptySlice(bindings), rows.Err()
}

func (r Repository) poolEndpointIDs(ctx context.Context, poolID string) ([]string, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT endpoint_id FROM proxy_pool_members
		 WHERE pool_id = ?
		 ORDER BY sort_order ASC
	`, poolID)
	if err != nil {
		return nil, fmt.Errorf("list pool endpoint ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan pool endpoint id: %w", err)
		}
		ids = append(ids, id)
	}
	return httputil.EmptySlice(ids), rows.Err()
}

func (r Repository) getProxyPool(ctx context.Context, poolID string) (UpstreamProxyPool, error) {
	var pool UpstreamProxyPool
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT id, name, is_active FROM proxy_pools WHERE id = ?
	`, poolID).Scan(&pool.ID, &pool.Name, &pool.IsActive)
	if err == sql.ErrNoRows {
		return UpstreamProxyPool{}, ErrProxyPoolNotFound
	}
	if err != nil {
		return UpstreamProxyPool{}, fmt.Errorf("get proxy pool: %w", err)
	}
	pool.EndpointIDs, err = r.poolEndpointIDs(ctx, poolID)
	return pool, err
}

func (r Repository) validateEndpointIDs(ctx context.Context, endpointIDs []string) error {
	if len(endpointIDs) == 0 {
		return nil
	}
	for _, endpointID := range endpointIDs {
		var exists int
		err := r.store.DB().QueryRowContext(ctx, `SELECT 1 FROM proxy_endpoints WHERE id = ?`, endpointID).Scan(&exists)
		if err == sql.ErrNoRows {
			return ErrProxyEndpointNotFound
		}
		if err != nil {
			return fmt.Errorf("validate proxy endpoint: %w", err)
		}
	}
	return nil
}

func (r Repository) validatePoolID(ctx context.Context, poolID string) error {
	var exists int
	err := r.store.DB().QueryRowContext(ctx, `SELECT 1 FROM proxy_pools WHERE id = ?`, poolID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrProxyPoolNotFound
	}
	if err != nil {
		return fmt.Errorf("validate proxy pool: %w", err)
	}
	return nil
}

func (r Repository) validateAccountID(ctx context.Context, accountID string) error {
	var exists int
	err := r.store.DB().QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = ?`, accountID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("validate account: %w", err)
	}
	return nil
}

var (
	ErrProxyEndpointNotFound = errors.New("proxy endpoint not found")
	ErrProxyPoolNotFound     = errors.New("proxy pool not found")
	ErrAccountNotFound       = errors.New("account not found")
)

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	return &value.String
}

func nullStringValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
