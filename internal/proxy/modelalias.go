package proxy

import "strings"

// gpt5AliasBaseModels mirrors _GPT5_ALIAS_BASE_MODELS.
var gpt5AliasBaseModels = []string{
	"gpt-5.4-mini",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",
	"gpt-5.1-codex",
	"gpt-5-codex",
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.3",
	"gpt-5.2",
	"gpt-5.1",
	"gpt-5",
}

// modelAliasReasoningTokens mirrors _MODEL_ALIAS_REASONING_TOKENS.
var modelAliasReasoningTokens = map[string]string{
	"minimal": "minimal",
	"low":     "low",
	"medium":  "medium",
	"high":    "high",
	"xhigh":   "high",
	"extra":   "high",
}

// modelAliasReasoningRank mirrors _MODEL_ALIAS_REASONING_RANK.
var modelAliasReasoningRank = map[string]int{"minimal": 0, "low": 1, "medium": 2, "high": 3}

// modelAliasServiceTierTokens mirrors _MODEL_ALIAS_SERVICE_TIER_TOKENS.
var modelAliasServiceTierTokens = map[string]string{
	"fast":     "priority",
	"priority": "priority",
}

// modelAliasIgnoredTokens mirrors _MODEL_ALIAS_IGNORED_TOKENS.
var modelAliasIgnoredTokens = stringSet("reasoning", "thinking")

var modelAliasTokens = func() map[string]struct{} {
	tokens := map[string]struct{}{}
	for token := range modelAliasReasoningTokens {
		tokens[token] = struct{}{}
	}
	for token := range modelAliasServiceTierTokens {
		tokens[token] = struct{}{}
	}
	for token := range modelAliasIgnoredTokens {
		tokens[token] = struct{}{}
	}
	return tokens
}()

// ResolveModelAlias ports request_policy.resolve_model_alias.
func ResolveModelAlias(model string) string {
	canonical, _, _, ok := resolveModelAliasParts(model)
	if !ok {
		return model
	}
	return canonical
}

// ResolveModelAliasParts exposes alias parsing for request policy helpers.
func ResolveModelAliasParts(model string) (canonical, reasoning, tier string) {
	base, effort, serviceTier, ok := resolveModelAliasParts(model)
	if !ok {
		return "", "", ""
	}
	if effort != nil {
		reasoning = *effort
	}
	if serviceTier != nil {
		tier = *serviceTier
	}
	return base, reasoning, tier
}

// resolveModelAliasParts ports request_policy._resolve_model_alias_parts.
func resolveModelAliasParts(model string) (canonical string, effort *string, serviceTier *string, ok bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return "", nil, nil, false
	}

	for _, baseModel := range gpt5AliasBaseModels {
		prefix := baseModel + "-"
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		suffix := normalized[len(prefix):]
		var tokens []string
		for _, token := range strings.Split(suffix, "-") {
			if token != "" {
				tokens = append(tokens, token)
			}
		}
		if len(tokens) == 0 {
			return "", nil, nil, false
		}
		for _, token := range tokens {
			if _, known := modelAliasTokens[token]; !known {
				return "", nil, nil, false
			}
		}
		return baseModel, resolveAliasReasoningEffort(tokens), resolveAliasServiceTier(tokens), true
	}

	return "", nil, nil, false
}

func resolveAliasReasoningEffort(tokens []string) *string {
	selected := ""
	selectedRank := -1
	for _, token := range tokens {
		effort, ok := modelAliasReasoningTokens[token]
		if !ok {
			continue
		}
		rank := modelAliasReasoningRank[effort]
		if rank > selectedRank {
			selected = effort
			selectedRank = rank
		}
	}
	if selected == "" {
		return nil
	}
	return &selected
}

func resolveAliasServiceTier(tokens []string) *string {
	for _, token := range tokens {
		if serviceTier, ok := modelAliasServiceTierTokens[token]; ok {
			return &serviceTier
		}
	}
	return nil
}

// CanonicalModelSlug ports api._canonical_model_slug.
func CanonicalModelSlug(model string) string {
	if alias := ResolveModelAlias(model); alias != "" {
		return alias
	}
	return model
}
