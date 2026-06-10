package proxy

import (
	"net/http"
	"sort"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

// v1MaxOutputTokenOverrides mirrors _V1_MAX_OUTPUT_TOKEN_OVERRIDES.
var v1MaxOutputTokenOverrides = map[string]int{
	"gpt-5.4":       128_000,
	"gpt-5.5":       128_000,
	"gpt-5.4-mini":  128_000,
	"gpt-5.3-codex": 128_000,
}

// ModelsHandler serves the proxy model-listing endpoints.
type ModelsHandler struct {
	apiKeys  apikeys.Repository
	settings settings.Repository
	registry *ModelRegistry
}

// NewModelsHandler constructs a ModelsHandler.
func NewModelsHandler(apiKeysRepo apikeys.Repository, settingsRepo settings.Repository, registry *ModelRegistry) ModelsHandler {
	return ModelsHandler{apiKeys: apiKeysRepo, settings: settingsRepo, registry: registry}
}

// CodexModels handles GET /backend-api/codex/models, porting
// _build_codex_models_response.
//
// Simplification: request-limit reservation/release
// (_enforce_request_limits / _release_reservation) is not yet ported; it
// lands with the API-key rate-limit enforcement work.
func (h ModelsHandler) CodexModels(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeys, h.settings, r)
	if err != nil {
		writeProxyError(w, err)
		return
	}

	allowedModels := allowedModelsForAPIKey(apiKey)
	visibilityAllowedModels := codexModelVisibilityAllowedModels(apiKey)

	models := h.registry.GetModelsWithFallback()
	entries := make([]map[string]any, 0, len(models))
	for _, slug := range sortedModelSlugs(models) {
		model := models[slug]
		if visibilityAllowedModels == nil {
			if !IsPublicModel(model, allowedModels) {
				continue
			}
			entries = append(entries, toCodexModelEntry(model, ""))
			continue
		}
		visibility := "hide"
		if _, ok := visibilityAllowedModels[slug]; ok {
			visibility = "list"
		}
		entries = append(entries, toCodexModelEntry(model, visibility))
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"models": entries})
}

// V1Models handles GET /v1/models, porting _build_models_response.
//
// Simplification: see CodexModels.
func (h ModelsHandler) V1Models(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeys, h.settings, r)
	if err != nil {
		writeProxyError(w, err)
		return
	}

	allowedModels := allowedModelsForAPIKey(apiKey)
	created := time.Now().Unix()

	models := h.registry.GetModelsWithFallback()
	items := make([]map[string]any, 0, len(models))
	for _, slug := range sortedModelSlugs(models) {
		model := models[slug]
		if !IsPublicModel(model, allowedModels) {
			continue
		}
		maxOutputTokens := v1MaxOutputTokens(model)
		item := map[string]any{
			"id":                 slug,
			"object":             "model",
			"created":            created,
			"owned_by":           "codex-lb",
			"metadata":           toModelMetadata(model),
			"api_types":          []string{"chat_completions"},
			"capabilities":       v1ModelCapabilities(model),
			"context_length":     model.ContextWindow,
			"contextLength":      model.ContextWindow,
			"max_output_tokens":  maxOutputTokens,
			"maxOutputTokens":    maxOutputTokens,
			"supports_reasoning": v1SupportsReasoning(model),
			"supportsReasoning":  v1SupportsReasoning(model),
			"supports_images":    v1SupportsVision(model),
			"supportsImages":     v1SupportsVision(model),
			"supports_vision":    v1SupportsVision(model),
			"supportsVision":     v1SupportsVision(model),
		}
		items = append(items, item)
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": items})
}

func writeProxyError(w http.ResponseWriter, err error) {
	if appErr, ok := err.(*AppError); ok {
		WriteError(w, appErr)
		return
	}
	httputil.WriteServerError(w, err)
}

func sortedModelSlugs(models map[string]UpstreamModel) []string {
	slugs := make([]string, 0, len(models))
	for slug := range models {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// allowedModelsForAPIKey ports api._allowed_models_for_api_key. A nil result
// means "no restriction".
func allowedModelsForAPIKey(apiKey *ApiKeyData) map[string]struct{} {
	var allowed map[string]struct{}
	if apiKey != nil && len(apiKey.AllowedModels) > 0 {
		allowed = stringSet(canonicalModelSlugs(apiKey.AllowedModels)...)
	}
	if apiKey != nil && apiKey.EnforcedModel != nil && *apiKey.EnforcedModel != "" {
		forced := stringSet(CanonicalModelSlug(*apiKey.EnforcedModel))
		if allowed == nil {
			return forced
		}
		return intersectSets(allowed, forced)
	}
	return allowed
}

// codexModelVisibilityAllowedModels ports api._codex_model_visibility_allowed_models.
func codexModelVisibilityAllowedModels(apiKey *ApiKeyData) map[string]struct{} {
	if apiKey == nil || !apiKey.ApplyToCodexModel || len(apiKey.AllowedModels) == 0 {
		return nil
	}
	return allowedModelsForAPIKey(apiKey)
}

func canonicalModelSlugs(models []string) []string {
	result := make([]string, len(models))
	for i, model := range models {
		result[i] = CanonicalModelSlug(model)
	}
	return result
}

func intersectSets(a, b map[string]struct{}) map[string]struct{} {
	result := map[string]struct{}{}
	for key := range a {
		if _, ok := b[key]; ok {
			result[key] = struct{}{}
		}
	}
	return result
}

// toCodexModelEntry ports api._to_codex_model_entry. visibility, if
// non-empty, overrides the model's own visibility.
func toCodexModelEntry(model UpstreamModel, visibility string) map[string]any {
	skipKeys := stringSet(
		"slug", "display_name", "description", "base_instructions",
		"default_reasoning_level", "supported_reasoning_levels", "supported_in_api",
		"priority", "minimal_client_version", "supports_reasoning_summaries",
		"support_verbosity", "default_verbosity", "supports_parallel_tool_calls",
		"context_window", "input_modalities", "available_in_plans",
		"prefer_websockets", "visibility",
	)

	entry := map[string]any{
		"slug":                         model.Slug,
		"display_name":                 model.DisplayName,
		"description":                  model.Description,
		"base_instructions":            model.BaseInstructions,
		"default_reasoning_level":      model.DefaultReasoningLevel,
		"supported_reasoning_levels":   reasoningLevelEntries(model.SupportedReasoningLevels),
		"supported_in_api":             model.SupportedInAPI,
		"priority":                     model.Priority,
		"minimal_client_version":       model.MinimalClientVersion,
		"supports_reasoning_summaries": model.SupportsReasoningSummaries,
		"support_verbosity":            model.SupportVerbosity,
		"default_verbosity":            model.DefaultVerbosity,
		"supports_parallel_tool_calls": model.SupportsParallelToolCalls,
		"context_window":               model.ContextWindow,
		"input_modalities":             model.InputModalities,
		"available_in_plans":           sortedKeys(model.AvailableInPlans),
		"prefer_websockets":            model.PreferWebsockets,
	}
	if visibility != "" {
		entry["visibility"] = visibility
	} else {
		entry["visibility"] = modelVisibility(model)
	}

	for key, value := range model.Raw {
		if _, skip := skipKeys[key]; !skip {
			entry[key] = value
		}
	}
	return entry
}

func modelVisibility(model UpstreamModel) string {
	if visibility, ok := model.Raw["visibility"].(string); ok {
		return visibility
	}
	return "list"
}

func toModelMetadata(model UpstreamModel) map[string]any {
	return map[string]any{
		"display_name":                 model.DisplayName,
		"description":                  model.Description,
		"context_window":               model.ContextWindow,
		"input_context_window":         model.ContextWindow,
		"max_output_tokens":            v1MaxOutputTokens(model),
		"input_modalities":             model.InputModalities,
		"supported_reasoning_levels":   reasoningLevelEntries(model.SupportedReasoningLevels),
		"default_reasoning_level":      model.DefaultReasoningLevel,
		"supports_reasoning_summaries": model.SupportsReasoningSummaries,
		"support_verbosity":            model.SupportVerbosity,
		"default_verbosity":            model.DefaultVerbosity,
		"prefer_websockets":            model.PreferWebsockets,
		"supports_parallel_tool_calls": model.SupportsParallelToolCalls,
		"supported_in_api":             model.SupportedInAPI,
		"minimal_client_version":       model.MinimalClientVersion,
		"priority":                     model.Priority,
	}
}

func v1ModelCapabilities(model UpstreamModel) map[string]any {
	return map[string]any{
		"context_length":     model.ContextWindow,
		"max_output_tokens":  v1MaxOutputTokens(model),
		"supports_reasoning": v1SupportsReasoning(model),
		"supports_images":    v1SupportsVision(model),
		"supportsImages":     v1SupportsVision(model),
		"supports_vision":    v1SupportsVision(model),
		"supports_tool_use":  true,
		"supports_streaming": true,
		"input_modalities":   model.InputModalities,
		"output_modalities":  []string{"text"},
	}
}

func v1MaxOutputTokens(model UpstreamModel) *int {
	if value, ok := v1MaxOutputTokenOverrides[model.Slug]; ok {
		return &value
	}
	return nil
}

func v1SupportsReasoning(model UpstreamModel) bool {
	return len(model.SupportedReasoningLevels) > 0 || model.SupportsReasoningSummaries
}

func v1SupportsVision(model UpstreamModel) bool {
	for _, modality := range model.InputModalities {
		if modality == "image" {
			return true
		}
	}
	return false
}

func reasoningLevelEntries(levels []ReasoningLevel) []map[string]string {
	entries := make([]map[string]string, len(levels))
	for i, level := range levels {
		entries[i] = map[string]string{"effort": level.Effort, "description": level.Description}
	}
	return entries
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
