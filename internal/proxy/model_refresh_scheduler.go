package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/scheduling"
)

const modelRefreshLeaderTTL = 90 * time.Second

type ModelRefreshScheduler struct {
	store    *db.Store
	logger   *slog.Logger
	cfg      config.Config
	registry *ModelRegistry
	fetcher  ModelFetcher
	leaderID string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type ModelFetchAccount struct {
	ID               string
	PlanType         string
	AccessToken      string
	ChatGPTAccountID *string
}

type ModelFetcher interface {
	FetchModelsForPlan(ctx context.Context, account ModelFetchAccount) ([]UpstreamModel, error)
}

func NewModelRefreshScheduler(
	store *db.Store,
	logger *slog.Logger,
	cfg config.Config,
	registry *ModelRegistry,
	fetcher ModelFetcher,
	leaderID string,
) *ModelRefreshScheduler {
	if leaderID == "" {
		leaderID = uuid.NewString()
	}
	if fetcher == nil {
		fetcher = NewHTTPModelFetcher(cfg, nil)
	}
	return &ModelRefreshScheduler{
		store:    store,
		logger:   logger,
		cfg:      cfg,
		registry: registry,
		fetcher:  fetcher,
		leaderID: leaderID,
	}
}

func (s *ModelRefreshScheduler) Start(ctx context.Context) {
	if !s.cfg.ModelRefreshEnabled || s.registry == nil {
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

func (s *ModelRefreshScheduler) Stop(ctx context.Context) error {
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

func (s *ModelRefreshScheduler) run(ctx context.Context) {
	defer close(s.done)
	s.refreshOnce(ctx)
	ticker := time.NewTicker(s.cfg.ModelRefreshInterval)
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

func (s *ModelRefreshScheduler) refreshOnce(ctx context.Context) {
	leaderRepo := scheduling.NewRepository(s.store)
	acquired, err := leaderRepo.TryAcquireLeader(ctx, s.leaderID, modelRefreshLeaderTTL)
	if err != nil {
		s.logger.Warn("model refresh leader acquire failed", "error", err)
		return
	}
	if !acquired {
		return
	}

	accountRepo := accounts.NewRepository(s.store)
	records, err := accountRepo.ListProxyRecords(ctx)
	if err != nil {
		s.logger.Warn("model refresh account list failed", "error", err)
		return
	}
	encryptor, err := crypto.NewEncryptor(s.cfg.EncryptionKeyPath)
	if err != nil {
		s.logger.Warn("model refresh encryptor init failed", "error", err)
		return
	}
	grouped := groupActiveModelAccounts(records, accountRepo, encryptor, s.logger)
	if len(grouped) == 0 {
		s.logger.Debug("no active accounts for model registry refresh")
		return
	}

	perPlanResults := map[string][]UpstreamModel{}
	for planType, candidates := range grouped {
		models, ok := s.fetchWithFailover(ctx, candidates)
		if ok {
			perPlanResults[planType] = models
		}
	}
	if len(perPlanResults) == 0 {
		s.logger.Warn("model registry refresh failed for all plans")
		return
	}
	s.registry.Update(perPlanResults)
	s.logger.Info("model registry refreshed", "plans", len(perPlanResults), "models", len(s.registry.GetModelsWithFallback()))
}

func groupActiveModelAccounts(records []accounts.ProxyRecord, repo accounts.Repository, encryptor *crypto.Encryptor, logger *slog.Logger) map[string][]ModelFetchAccount {
	grouped := map[string][]ModelFetchAccount{}
	for _, record := range records {
		if record.Status != AccountStatusActive || strings.TrimSpace(record.PlanType) == "" {
			continue
		}
		token, err := repo.DecryptAccessToken(encryptor, record)
		if err != nil {
			logger.Warn("model refresh skipped account without decryptable access token", "account", record.ID, "error", err)
			continue
		}
		account := ModelFetchAccount{
			ID:          record.ID,
			PlanType:    record.PlanType,
			AccessToken: token,
		}
		if record.ChatGPTAccountID.Valid && strings.TrimSpace(record.ChatGPTAccountID.String) != "" {
			value := record.ChatGPTAccountID.String
			account.ChatGPTAccountID = &value
		}
		grouped[record.PlanType] = append(grouped[record.PlanType], account)
	}
	return grouped
}

func (s *ModelRefreshScheduler) fetchWithFailover(ctx context.Context, candidates []ModelFetchAccount) ([]UpstreamModel, bool) {
	for _, account := range candidates {
		models, err := s.fetcher.FetchModelsForPlan(ctx, account)
		if err != nil {
			s.logger.Warn("model fetch failed", "account", account.ID, "plan", account.PlanType, "error", err)
			continue
		}
		return models, true
	}
	return nil, false
}

type HTTPModelFetcher struct {
	cfg    config.Config
	client *http.Client
}

func NewHTTPModelFetcher(cfg config.Config, client *http.Client) *HTTPModelFetcher {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPModelFetcher{cfg: cfg, client: client}
}

func (f *HTTPModelFetcher) FetchModelsForPlan(ctx context.Context, account ModelFetchAccount) ([]UpstreamModel, error) {
	baseURL := strings.TrimRight(f.cfg.UpstreamBaseURL, "/")
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api"
	}
	endpoint, err := url.Parse(baseURL + "/codex/models")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	clientVersion := strings.TrimSpace(f.cfg.ModelRegistryClientVersion)
	if clientVersion == "" {
		clientVersion = "0.101.0"
	}
	query.Set("client_version", clientVersion)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+account.AccessToken)
	req.Header.Set("Accept", "application/json")
	if account.ChatGPTAccountID != nil && strings.TrimSpace(*account.ChatGPTAccountID) != "" {
		req.Header.Set("chatgpt-account-id", *account.ChatGPTAccountID)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model fetch transport error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream models API returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]UpstreamModel, 0, len(payload.Models))
	for _, entry := range payload.Models {
		model := parseUpstreamModel(entry)
		if model.Slug != "" {
			models = append(models, model)
		}
	}
	return models, nil
}

func parseUpstreamModel(data map[string]any) UpstreamModel {
	raw := map[string]any{}
	for key, value := range data {
		if key != "model_messages" {
			raw[key] = value
		}
	}
	return UpstreamModel{
		Slug:                       modelStringField(data, "slug"),
		DisplayName:                modelStringField(data, "display_name"),
		Description:                modelStringField(data, "description"),
		BaseInstructions:           modelStringField(data, "base_instructions"),
		ContextWindow:              modelIntField(data, "context_window"),
		InputModalities:            modelStringListField(data, "input_modalities"),
		SupportedReasoningLevels:   modelReasoningLevelsField(data, "supported_reasoning_levels"),
		DefaultReasoningLevel:      modelOptionalStringField(data, "default_reasoning_level"),
		SupportsReasoningSummaries: modelBoolField(data, "supports_reasoning_summaries", false),
		SupportVerbosity:           modelBoolField(data, "support_verbosity", false),
		DefaultVerbosity:           modelOptionalStringField(data, "default_verbosity"),
		PreferWebsockets:           modelBoolField(data, "prefer_websockets", false),
		SupportsParallelToolCalls:  modelBoolField(data, "supports_parallel_tool_calls", false),
		SupportedInAPI:             modelBoolField(data, "supported_in_api", true),
		MinimalClientVersion:       modelOptionalStringField(data, "minimal_client_version"),
		Priority:                   modelIntField(data, "priority"),
		AvailableInPlans:           stringSetFromList(modelStringListField(data, "available_in_plans")),
		Raw:                        raw,
	}
}

func modelStringField(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func modelOptionalStringField(data map[string]any, key string) *string {
	value, ok := data[key].(string)
	if !ok {
		return nil
	}
	return &value
}

func modelIntField(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func modelBoolField(data map[string]any, key string, fallback bool) bool {
	value, ok := data[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func modelStringListField(data map[string]any, key string) []string {
	values, ok := data[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func stringSetFromList(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func modelReasoningLevelsField(data map[string]any, key string) []ReasoningLevel {
	values, ok := data[key].([]any)
	if !ok {
		return nil
	}
	result := make([]ReasoningLevel, 0, len(values))
	for _, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		effort, effortOK := entry["effort"].(string)
		description, descriptionOK := entry["description"].(string)
		if effortOK && descriptionOK {
			result = append(result, ReasoningLevel{Effort: effort, Description: description})
		}
	}
	return result
}
