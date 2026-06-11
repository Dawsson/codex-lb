package proxy

import (
	"strings"
	"sync"
	"time"
)

// ReasoningLevel mirrors app.core.openai.model_registry.ReasoningLevel.
type ReasoningLevel struct {
	Effort      string
	Description string
}

// UpstreamModel mirrors app.core.openai.model_registry.UpstreamModel.
type UpstreamModel struct {
	Slug                       string
	DisplayName                string
	Description                string
	ContextWindow              int
	InputModalities            []string
	SupportedReasoningLevels   []ReasoningLevel
	DefaultReasoningLevel      *string
	SupportsReasoningSummaries bool
	SupportVerbosity           bool
	DefaultVerbosity           *string
	PreferWebsockets           bool
	SupportsParallelToolCalls  bool
	SupportedInAPI             bool
	MinimalClientVersion       *string
	Priority                   int
	AvailableInPlans           map[string]struct{}
	BaseInstructions           string
	Raw                        map[string]any
}

// ModelRegistrySnapshot mirrors app.core.openai.model_registry.ModelRegistrySnapshot.
type ModelRegistrySnapshot struct {
	Models     map[string]UpstreamModel
	ModelPlans map[string]map[string]struct{}
	PlanModels map[string]map[string]struct{}
	FetchedAt  time.Time
}

var bootstrapWebsocketPreferredModelPatterns = []string{"gpt-5.5", "gpt-5.5-*", "gpt-5.4", "gpt-5.4-*"}

var reasoningLevelsExtended = []ReasoningLevel{
	{Effort: "low", Description: "Low reasoning effort"},
	{Effort: "medium", Description: "Medium reasoning effort"},
	{Effort: "high", Description: "High reasoning effort"},
	{Effort: "xhigh", Description: "Extra high reasoning effort"},
}

var bootstrapAvailableInPlans = stringSet(
	"plus", "pro", "prolite", "team", "business", "enterprise", "edu",
	"education", "k12", "go", "hc", "finserv", "free", "free_workspace",
	"quorum", "self_serve_business_usage_based", "enterprise_cbp_usage_based",
)

var bootstrapCoreAvailableInPlans = func() map[string]struct{} {
	excluded := stringSet("free", "free_workspace", "k12")
	result := map[string]struct{}{}
	for plan := range bootstrapAvailableInPlans {
		if _, skip := excluded[plan]; !skip {
			result[plan] = struct{}{}
		}
	}
	return result
}()

func stringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func strPtr(value string) *string {
	return &value
}

type bootstrapModelOptions struct {
	contextWindow         int
	inputModalities       []string
	defaultReasoningLevel *string
	defaultVerbosity      *string
	supportedInAPI        *bool
	availableInPlans      map[string]struct{}
	visibility            string
	rawOverrides          map[string]any
}

func bootstrapModel(slug, displayName string, preferWebsockets bool, minimalClientVersion *string, opts bootstrapModelOptions) UpstreamModel {
	contextWindow := opts.contextWindow
	if contextWindow == 0 {
		contextWindow = 272_000
	}
	inputModalities := opts.inputModalities
	if inputModalities == nil {
		inputModalities = []string{"text", "image"}
	}
	defaultReasoningLevel := opts.defaultReasoningLevel
	if defaultReasoningLevel == nil {
		defaultReasoningLevel = strPtr("medium")
	}
	defaultVerbosity := opts.defaultVerbosity
	if defaultVerbosity == nil {
		defaultVerbosity = strPtr("low")
	}
	supportedInAPI := true
	if opts.supportedInAPI != nil {
		supportedInAPI = *opts.supportedInAPI
	}
	availableInPlans := opts.availableInPlans
	if availableInPlans == nil {
		availableInPlans = bootstrapAvailableInPlans
	}
	visibility := opts.visibility
	if visibility == "" {
		visibility = "list"
	}

	raw := map[string]any{
		"shell_type":         "shell_command",
		"visibility":         visibility,
		"availability_nux":   nil,
		"max_context_window": contextWindow,
	}
	for key, value := range opts.rawOverrides {
		raw[key] = value
	}

	return UpstreamModel{
		Slug:                       slug,
		DisplayName:                displayName,
		Description:                displayName,
		ContextWindow:              contextWindow,
		InputModalities:            inputModalities,
		SupportedReasoningLevels:   reasoningLevelsExtended,
		DefaultReasoningLevel:      defaultReasoningLevel,
		SupportsReasoningSummaries: true,
		SupportVerbosity:           true,
		DefaultVerbosity:           defaultVerbosity,
		PreferWebsockets:           preferWebsockets,
		SupportsParallelToolCalls:  true,
		SupportedInAPI:             supportedInAPI,
		MinimalClientVersion:       minimalClientVersion,
		Priority:                   0,
		AvailableInPlans:           availableInPlans,
		Raw:                        raw,
	}
}

// bootstrapStaticModels mirrors _BOOTSTRAP_STATIC_MODELS: a conservative
// catalog used before the first upstream registry refresh.
var bootstrapStaticModels = []UpstreamModel{
	bootstrapModel("gpt-5.5", "GPT-5.5", true, strPtr("0.124.0"), bootstrapModelOptions{}),
	bootstrapModel("gpt-5.4", "GPT-5.4", true, strPtr("0.98.0"), bootstrapModelOptions{
		availableInPlans: bootstrapCoreAvailableInPlans,
		rawOverrides:     map[string]any{"max_context_window": 1_000_000},
	}),
	bootstrapModel("gpt-5.4-mini", "GPT-5.4 Mini", true, strPtr("0.98.0"), bootstrapModelOptions{
		defaultVerbosity: strPtr("medium"),
	}),
	bootstrapModel("gpt-5.3-codex", "GPT-5.3 Codex", true, strPtr("0.98.0"), bootstrapModelOptions{
		availableInPlans: bootstrapCoreAvailableInPlans,
	}),
	bootstrapModel("gpt-5.3-codex-spark", "GPT-5.3 Codex Spark", true, strPtr("0.100.0"), bootstrapModelOptions{
		contextWindow:         128_000,
		inputModalities:       []string{"text"},
		defaultReasoningLevel: strPtr("high"),
		supportedInAPI:        boolPtr(false),
	}),
	bootstrapModel("gpt-5.2", "GPT-5.2", true, strPtr("0.0.1"), bootstrapModelOptions{}),
	bootstrapModel("codex-auto-review", "Codex Auto Review", true, strPtr("0.98.0"), bootstrapModelOptions{
		availableInPlans: bootstrapCoreAvailableInPlans,
		visibility:       "hide",
		rawOverrides:     map[string]any{"max_context_window": 1_000_000},
	}),
}

func boolPtr(value bool) *bool {
	return &value
}

// ModelRegistry mirrors app.core.openai.model_registry.ModelRegistry.
type ModelRegistry struct {
	ttl             time.Duration
	mu              sync.RWMutex
	snapshot        *ModelRegistrySnapshot
	bootstrapModels map[string]UpstreamModel
}

// NewModelRegistry constructs a ModelRegistry with the given refresh TTL.
func NewModelRegistry(ttl time.Duration) *ModelRegistry {
	if ttl <= 0 {
		ttl = 300 * time.Second
	}
	bootstrap := make(map[string]UpstreamModel, len(bootstrapStaticModels))
	for _, model := range bootstrapStaticModels {
		bootstrap[model.Slug] = model
	}
	return &ModelRegistry{ttl: ttl, bootstrapModels: bootstrap}
}

// GetSnapshot returns the current snapshot, or nil if no refresh has
// completed yet.
func (m *ModelRegistry) GetSnapshot() *ModelRegistrySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// GetModelsWithFallback ports get_models_with_fallback.
func (m *ModelRegistry) GetModelsWithFallback() map[string]UpstreamModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snapshot != nil {
		return m.snapshot.Models
	}
	return m.bootstrapModels
}

func (m *ModelRegistry) NeedsRefresh() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snapshot == nil {
		return true
	}
	return time.Since(m.snapshot.FetchedAt) >= m.ttl
}

func (m *ModelRegistry) Update(perPlanResults map[string][]UpstreamModel) {
	if len(perPlanResults) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	models := map[string]UpstreamModel{}
	modelPlans := map[string]map[string]struct{}{}
	if m.snapshot != nil {
		refreshedPlans := map[string]struct{}{}
		for plan := range perPlanResults {
			refreshedPlans[plan] = struct{}{}
		}
		for plan, slugs := range m.snapshot.PlanModels {
			if _, refreshed := refreshedPlans[plan]; refreshed {
				continue
			}
			for slug := range slugs {
				if model, ok := m.snapshot.Models[slug]; ok {
					models[slug] = model
					if modelPlans[slug] == nil {
						modelPlans[slug] = map[string]struct{}{}
					}
					modelPlans[slug][plan] = struct{}{}
				}
			}
		}
	}
	for plan, planModels := range perPlanResults {
		for _, model := range planModels {
			if model.Slug == "" {
				continue
			}
			models[model.Slug] = model
			if modelPlans[model.Slug] == nil {
				modelPlans[model.Slug] = map[string]struct{}{}
			}
			modelPlans[model.Slug][plan] = struct{}{}
		}
	}
	planModels := map[string]map[string]struct{}{}
	for slug, plans := range modelPlans {
		for plan := range plans {
			if planModels[plan] == nil {
				planModels[plan] = map[string]struct{}{}
			}
			planModels[plan][slug] = struct{}{}
		}
	}
	m.snapshot = &ModelRegistrySnapshot{
		Models:     models,
		ModelPlans: modelPlans,
		PlanModels: planModels,
		FetchedAt:  time.Now().UTC(),
	}
}

// PrefersWebsockets ports prefers_websockets.
func (m *ModelRegistry) PrefersWebsockets(slug string) bool {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.snapshot != nil {
		if model, ok := m.snapshot.Models[slug]; ok {
			return model.PreferWebsockets
		}
		if model, ok := m.snapshot.Models[normalized]; ok {
			return model.PreferWebsockets
		}
		return false
	}

	if model, ok := m.bootstrapModels[slug]; ok {
		return model.PreferWebsockets
	}
	if model, ok := m.bootstrapModels[normalized]; ok {
		return model.PreferWebsockets
	}

	for _, pattern := range bootstrapWebsocketPreferredModelPatterns {
		if fnmatchCase(normalized, pattern) {
			return true
		}
	}
	return false
}

// fnmatchCase ports the subset of Python's fnmatch.fnmatchcase used here:
// "*" matches any sequence of characters, all other characters match
// literally.
func fnmatchCase(name, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return name == pattern
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	rest := name[len(parts[0]):]
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		idx := strings.Index(rest, part)
		if idx == -1 {
			return false
		}
		rest = rest[idx+len(part):]
	}
	return true
}

// PlanTypesForModel ports ModelRegistry.plan_types_for_model.
func (m *ModelRegistry) PlanTypesForModel(slug string) map[string]struct{} {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.snapshot != nil {
		if plans, ok := m.snapshot.ModelPlans[slug]; ok {
			return plans
		}
		if plans, ok := m.snapshot.ModelPlans[normalized]; ok {
			return plans
		}
		return map[string]struct{}{}
	}
	if model, ok := m.bootstrapModels[slug]; ok {
		return model.AvailableInPlans
	}
	if model, ok := m.bootstrapModels[normalized]; ok {
		return model.AvailableInPlans
	}
	return nil
}

// IsPublicModel ports is_public_model.
func IsPublicModel(model UpstreamModel, allowedModels map[string]struct{}) bool {
	if allowedModels == nil {
		return true
	}
	_, ok := allowedModels[model.Slug]
	return ok
}
