package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httputil"
)

type Repository struct {
	store *db.Store
}

type LimitRule struct {
	ID           int
	LimitType    string
	LimitWindow  string
	MaxValue     int64
	CurrentValue int64
	ModelFilter  sql.NullString
	ResetAt      sql.NullString
}

type UsageSummary struct {
	RequestCount      int
	TotalTokens       int
	CachedInputTokens int
	TotalCostUSD      float64
}

type KeyRecord struct {
	ID                            string
	Name                          string
	KeyPrefix                     string
	AllowedModels                 sql.NullString
	ApplyToCodexModel             bool
	EnforcedModel                 sql.NullString
	EnforcedReasoningEffort       sql.NullString
	EnforcedServiceTier           sql.NullString
	TrafficClass                  string
	AccountAssignmentScopeEnabled bool
	ExpiresAt                     sql.NullString
	IsActive                      bool
	CreatedAt                     sql.NullString
	LastUsedAt                    sql.NullString
	Limits                        []LimitRule
	AssignedAccountIDs            []string
	UsageSummary                  *UsageSummary
}

type LimitInput struct {
	LimitType    string
	LimitWindow  string
	MaxValue     int64
	ModelFilter  *string
}

type CreateInput struct {
	Name                     string
	AllowedModels            []string
	ApplyToCodexModel        bool
	EnforcedModel            *string
	EnforcedReasoningEffort  *string
	EnforcedServiceTier      *string
	TrafficClass             string
	ExpiresAt                *string
	AssignedAccountIDs       []string
	Limits                   []LimitInput
}

type UpdateInput struct {
	NameSet                    bool
	Name                       string
	AllowedModelsSet           bool
	AllowedModels              []string
	ApplyToCodexModelSet       bool
	ApplyToCodexModel          bool
	EnforcedModelSet           bool
	EnforcedModel              *string
	EnforcedReasoningEffortSet bool
	EnforcedReasoningEffort    *string
	EnforcedServiceTierSet     bool
	EnforcedServiceTier        *string
	TrafficClassSet            bool
	TrafficClass               string
	ExpiresAtSet               bool
	ExpiresAt                  *string
	IsActiveSet                bool
	IsActive                   bool
	AssignedAccountIDsSet      bool
	AssignedAccountIDs         []string
	LimitsSet                  bool
	Limits                     []LimitInput
	ResetUsage                 bool
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) List(ctx context.Context) ([]KeyRecord, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, name, key_prefix, allowed_models, apply_to_codex_model, enforced_model,
		       enforced_reasoning_effort, enforced_service_tier, traffic_class,
		       account_assignment_scope_enabled, expires_at, is_active, created_at, last_used_at
		  FROM api_keys
		 ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []KeyRecord
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return httputil.EmptySlice(keys), nil
	}
	usageByKey, err := r.usageSummaryByKey(ctx)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if summary, ok := usageByKey[keys[i].ID]; ok {
			keys[i].UsageSummary = &summary
		}
		limits, err := r.listLimits(ctx, keys[i].ID)
		if err != nil {
			return nil, err
		}
		keys[i].Limits = limits
		assigned, err := r.listAssignedAccounts(ctx, keys[i].ID)
		if err != nil {
			return nil, err
		}
		keys[i].AssignedAccountIDs = assigned
	}
	return keys, nil
}

func (r Repository) GetByID(ctx context.Context, keyID string) (*KeyRecord, error) {
	row := r.store.DB().QueryRowContext(ctx, `
		SELECT id, name, key_prefix, allowed_models, apply_to_codex_model, enforced_model,
		       enforced_reasoning_effort, enforced_service_tier, traffic_class,
		       account_assignment_scope_enabled, expires_at, is_active, created_at, last_used_at
		  FROM api_keys
		 WHERE id = ?
	`, keyID)
	key, err := scanKeyRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	limits, err := r.listLimits(ctx, key.ID)
	if err != nil {
		return nil, err
	}
	key.Limits = limits
	assigned, err := r.listAssignedAccounts(ctx, key.ID)
	if err != nil {
		return nil, err
	}
	key.AssignedAccountIDs = assigned
	usageByKey, err := r.usageSummaryByKey(ctx)
	if err != nil {
		return nil, err
	}
	if summary, ok := usageByKey[key.ID]; ok {
		key.UsageSummary = &summary
	}
	return &key, nil
}

// GetByHash looks up an API key by its sha256 hash, including limits. It
// returns (nil, nil) if no key with that hash exists.
func (r Repository) GetByHash(ctx context.Context, keyHash string) (*KeyRecord, error) {
	row := r.store.DB().QueryRowContext(ctx, `
		SELECT id, name, key_prefix, allowed_models, apply_to_codex_model, enforced_model,
		       enforced_reasoning_effort, enforced_service_tier, traffic_class,
		       account_assignment_scope_enabled, expires_at, is_active, created_at, last_used_at
		  FROM api_keys
		 WHERE key_hash = ?
	`, keyHash)
	key, err := scanKeyRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	limits, err := r.listLimits(ctx, key.ID)
	if err != nil {
		return nil, err
	}
	key.Limits = limits
	return &key, nil
}

// AllowedModelsList returns the key's allowed model slugs, or nil if the key
// has no model restriction.
func (k KeyRecord) AllowedModelsList() []string {
	return deserializeAllowedModels(k.AllowedModels)
}

// HashKey returns the sha256 hex digest of a plain API key token, matching
// the value stored in api_keys.key_hash.
func HashKey(plainKey string) string {
	return hashKey(plainKey)
}

func (r Repository) Create(ctx context.Context, input CreateInput) (KeyRecord, string, error) {
	plainKey := generatePlainKey()
	keyID := uuid.NewString()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	allowedModels := serializeAllowedModels(input.AllowedModels)
	trafficClass := input.TrafficClass
	if trafficClass == "" {
		trafficClass = "foreground"
	}
	scopeEnabled := len(input.AssignedAccountIDs) > 0
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return KeyRecord{}, "", err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_keys (
			id, name, key_hash, key_prefix, allowed_models, apply_to_codex_model, enforced_model,
			enforced_reasoning_effort, enforced_service_tier, traffic_class,
			account_assignment_scope_enabled, expires_at, is_active, created_at, last_used_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, NULL)
	`, keyID, input.Name, hashKey(plainKey), plainKey[:15], allowedModels, boolToInt(input.ApplyToCodexModel),
		nullStringValue(input.EnforcedModel), nullStringValue(input.EnforcedReasoningEffort),
		nullStringValue(input.EnforcedServiceTier), trafficClass, boolToInt(scopeEnabled),
		nullStringValue(input.ExpiresAt), now)
	if err != nil {
		return KeyRecord{}, "", fmt.Errorf("insert api key: %w", err)
	}
	if err := replaceAssignmentsTx(ctx, tx, keyID, input.AssignedAccountIDs); err != nil {
		return KeyRecord{}, "", err
	}
	if err := replaceLimitsTx(ctx, tx, keyID, input.Limits, now); err != nil {
		return KeyRecord{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return KeyRecord{}, "", err
	}
	created, err := r.GetByID(ctx, keyID)
	if err != nil || created == nil {
		return KeyRecord{}, "", fmt.Errorf("load created api key")
	}
	return *created, plainKey, nil
}

func (r Repository) Update(ctx context.Context, keyID string, input UpdateInput) (*KeyRecord, error) {
	existing, err := r.GetByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if input.NameSet || input.AllowedModelsSet || input.ApplyToCodexModelSet || input.EnforcedModelSet ||
		input.EnforcedReasoningEffortSet || input.EnforcedServiceTierSet || input.TrafficClassSet ||
		input.ExpiresAtSet || input.IsActiveSet || input.AssignedAccountIDsSet {
		name := existing.Name
		if input.NameSet {
			name = input.Name
		}
		allowedModels := existing.AllowedModels
		if input.AllowedModelsSet {
			allowedModels = sql.NullString{String: serializeAllowedModels(input.AllowedModels), Valid: true}
		}
		applyToCodex := existing.ApplyToCodexModel
		if input.ApplyToCodexModelSet {
			applyToCodex = input.ApplyToCodexModel
		}
		enforcedModel := existing.EnforcedModel
		if input.EnforcedModelSet {
			enforcedModel = toNullString(input.EnforcedModel)
		}
		enforcedReasoning := existing.EnforcedReasoningEffort
		if input.EnforcedReasoningEffortSet {
			enforcedReasoning = toNullString(input.EnforcedReasoningEffort)
		}
		enforcedTier := existing.EnforcedServiceTier
		if input.EnforcedServiceTierSet {
			enforcedTier = toNullString(input.EnforcedServiceTier)
		}
		trafficClass := existing.TrafficClass
		if input.TrafficClassSet {
			trafficClass = input.TrafficClass
		}
		expiresAt := existing.ExpiresAt
		if input.ExpiresAtSet {
			expiresAt = toNullString(input.ExpiresAt)
		}
		isActive := existing.IsActive
		if input.IsActiveSet {
			isActive = input.IsActive
		}
		scopeEnabled := existing.AccountAssignmentScopeEnabled
		assigned := existing.AssignedAccountIDs
		if input.AssignedAccountIDsSet {
			assigned = input.AssignedAccountIDs
			scopeEnabled = len(assigned) > 0
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE api_keys SET
				name = ?, allowed_models = ?, apply_to_codex_model = ?, enforced_model = ?,
				enforced_reasoning_effort = ?, enforced_service_tier = ?, traffic_class = ?,
				account_assignment_scope_enabled = ?, expires_at = ?, is_active = ?
			 WHERE id = ?
		`, name, nullStringOrNil(allowedModels), boolToInt(applyToCodex), nullStringOrNil(enforcedModel),
			nullStringOrNil(enforcedReasoning), nullStringOrNil(enforcedTier), trafficClass,
			boolToInt(scopeEnabled), nullStringOrNil(expiresAt), boolToInt(isActive), keyID)
		if err != nil {
			return nil, err
		}
		if input.AssignedAccountIDsSet {
			if err := replaceAssignmentsTx(ctx, tx, keyID, assigned); err != nil {
				return nil, err
			}
		}
	}
	if input.LimitsSet {
		now := time.Now().UTC().Format("2006-01-02 15:04:05")
		if err := replaceLimitsTx(ctx, tx, keyID, input.Limits, now); err != nil {
			return nil, err
		}
	}
	if input.ResetUsage {
		_, err = tx.ExecContext(ctx, `UPDATE api_key_limits SET current_value = 0 WHERE api_key_id = ?`, keyID)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, keyID)
}

func (r Repository) Delete(ctx context.Context, keyID string) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, keyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (r Repository) Regenerate(ctx context.Context, keyID string) (*KeyRecord, string, error) {
	existing, err := r.GetByID(ctx, keyID)
	if err != nil {
		return nil, "", err
	}
	if existing == nil {
		return nil, "", nil
	}
	plainKey := generatePlainKey()
	_, err = r.store.DB().ExecContext(ctx, `
		UPDATE api_keys SET key_hash = ?, key_prefix = ? WHERE id = ?
	`, hashKey(plainKey), plainKey[:15], keyID)
	if err != nil {
		return nil, "", err
	}
	updated, err := r.GetByID(ctx, keyID)
	return updated, plainKey, err
}

func (r Repository) listLimits(ctx context.Context, keyID string) ([]LimitRule, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, limit_type, limit_window, max_value, current_value, model_filter, reset_at
		  FROM api_key_limits
		 WHERE api_key_id = ?
		 ORDER BY id ASC
	`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var limits []LimitRule
	for rows.Next() {
		var limit LimitRule
		if err := rows.Scan(&limit.ID, &limit.LimitType, &limit.LimitWindow, &limit.MaxValue,
			&limit.CurrentValue, &limit.ModelFilter, &limit.ResetAt); err != nil {
			return nil, err
		}
		limits = append(limits, limit)
	}
	return httputil.EmptySlice(limits), rows.Err()
}

func (r Repository) listAssignedAccounts(ctx context.Context, keyID string) ([]string, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id FROM api_key_accounts WHERE api_key_id = ? ORDER BY account_id ASC
	`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return httputil.EmptySlice(ids), rows.Err()
}

func (r Repository) usageSummaryByKey(ctx context.Context) (map[string]UsageSummary, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT api_key_id,
		       COUNT(*) AS request_count,
		       COALESCE(SUM(COALESCE(input_tokens, 0)), 0) AS input_tokens,
		       COALESCE(SUM(COALESCE(output_tokens, reasoning_tokens, 0)), 0) AS output_tokens,
		       COALESCE(SUM(COALESCE(cached_input_tokens, 0)), 0) AS cached_input_tokens,
		       COALESCE(SUM(COALESCE(cost_usd, 0)), 0) AS total_cost_usd
		  FROM request_logs
		 WHERE api_key_id IS NOT NULL
		   AND request_kind NOT IN ('warmup', 'limit_warmup')
		   AND deleted_at IS NULL
		 GROUP BY api_key_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := map[string]UsageSummary{}
	for rows.Next() {
		var keyID string
		var requestCount int
		var inputTokens int
		var outputTokens int
		var cachedInput int
		var totalCost float64
		if err := rows.Scan(&keyID, &requestCount, &inputTokens, &outputTokens, &cachedInput, &totalCost); err != nil {
			return nil, err
		}
		if cachedInput > inputTokens {
			cachedInput = inputTokens
		}
		if cachedInput < 0 {
			cachedInput = 0
		}
		summaries[keyID] = UsageSummary{
			RequestCount:      requestCount,
			TotalTokens:       inputTokens + outputTokens,
			CachedInputTokens: cachedInput,
			TotalCostUSD:      totalCost,
		}
	}
	return summaries, rows.Err()
}

func replaceAssignmentsTx(ctx context.Context, tx *sql.Tx, keyID string, accountIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_key_accounts WHERE api_key_id = ?`, keyID); err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	for _, accountID := range accountIDs {
		if accountID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_key_accounts (api_key_id, account_id, created_at) VALUES (?, ?, ?)
		`, keyID, accountID, now); err != nil {
			return err
		}
	}
	return nil
}

func replaceLimitsTx(ctx context.Context, tx *sql.Tx, keyID string, limits []LimitInput, now string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_key_limits WHERE api_key_id = ?`, keyID); err != nil {
		return err
	}
	for _, limit := range limits {
		resetAt := defaultResetAt(limit.LimitWindow, now)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_key_limits (
				api_key_id, limit_type, limit_window, max_value, current_value, model_filter, reset_at
			) VALUES (?, ?, ?, ?, 0, ?, ?)
		`, keyID, limit.LimitType, limit.LimitWindow, limit.MaxValue, nullStringValue(limit.ModelFilter), resetAt); err != nil {
			return err
		}
	}
	return nil
}

func defaultResetAt(window, now string) string {
	parsed, _ := time.Parse("2006-01-02 15:04:05", now)
	if parsed.IsZero() {
		parsed = time.Now().UTC()
	}
	switch window {
	case "daily":
		return parsed.Add(24 * time.Hour).Format("2006-01-02 15:04:05")
	case "weekly":
		return parsed.Add(7 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	case "monthly":
		return parsed.AddDate(0, 1, 0).Format("2006-01-02 15:04:05")
	case "5h":
		return parsed.Add(5 * time.Hour).Format("2006-01-02 15:04:05")
	case "7d":
		return parsed.Add(7 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	default:
		return parsed.Add(24 * time.Hour).Format("2006-01-02 15:04:05")
	}
}

func scanKey(rows *sql.Rows) (KeyRecord, error) {
	var key KeyRecord
	var applyToCodex int
	var scopeEnabled int
	var isActive int
	err := rows.Scan(
		&key.ID, &key.Name, &key.KeyPrefix, &key.AllowedModels, &applyToCodex, &key.EnforcedModel,
		&key.EnforcedReasoningEffort, &key.EnforcedServiceTier, &key.TrafficClass,
		&scopeEnabled, &key.ExpiresAt, &isActive, &key.CreatedAt, &key.LastUsedAt,
	)
	key.ApplyToCodexModel = applyToCodex != 0
	key.AccountAssignmentScopeEnabled = scopeEnabled != 0
	key.IsActive = isActive != 0
	return key, err
}

func scanKeyRow(row *sql.Row) (KeyRecord, error) {
	var key KeyRecord
	var applyToCodex int
	var scopeEnabled int
	var isActive int
	err := row.Scan(
		&key.ID, &key.Name, &key.KeyPrefix, &key.AllowedModels, &applyToCodex, &key.EnforcedModel,
		&key.EnforcedReasoningEffort, &key.EnforcedServiceTier, &key.TrafficClass,
		&scopeEnabled, &key.ExpiresAt, &isActive, &key.CreatedAt, &key.LastUsedAt,
	)
	key.ApplyToCodexModel = applyToCodex != 0
	key.AccountAssignmentScopeEnabled = scopeEnabled != 0
	key.IsActive = isActive != 0
	return key, err
}

func generatePlainKey() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return "sk-clb-" + base64.RawURLEncoding.EncodeToString(buf)
}

func hashKey(plainKey string) string {
	sum := sha256.Sum256([]byte(plainKey))
	return fmt.Sprintf("%x", sum[:])
}

func serializeAllowedModels(models []string) string {
	if models == nil {
		return ""
	}
	raw, _ := json.Marshal(models)
	return string(raw)
}

func deserializeAllowedModels(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var models []string
	if err := json.Unmarshal([]byte(raw.String), &models); err != nil {
		return nil
	}
	return models
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullStringValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func toNullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func nullStringOrNil(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
