package requestlogs

import "strings"

var rateLimitCodes = map[string]struct{}{
	"rate_limit_exceeded": {},
	"usage_limit_reached": {},
}

var quotaCodes = map[string]struct{}{
	"insufficient_quota": {},
	"usage_not_included": {},
	"quota_exceeded":     {},
}

// NormalizeLogStatus maps raw DB status/error_code pairs to dashboard filter values.
func NormalizeLogStatus(status string, errorCode *string) string {
	if status == "success" {
		return "ok"
	}
	if errorCode != nil {
		if _, ok := rateLimitCodes[*errorCode]; ok {
			return "rate_limit"
		}
		if _, ok := quotaCodes[*errorCode]; ok {
			return "quota"
		}
	}
	return "error"
}

// NormalizeRequestKind maps legacy stored values to dashboard request kinds.
func NormalizeRequestKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "normal", "chat_completion", "chat_completions":
		return "normal"
	case "warmup":
		return "warmup"
	case "limit_warmup":
		return "limit_warmup"
	default:
		return "normal"
	}
}

type StatusFilter struct {
	IncludeSuccess      bool
	IncludeErrorOther   bool
	ErrorCodesIn        []string
	ErrorCodesExcluding []string
}

func MapStatusFilter(statuses []string) StatusFilter {
	if len(statuses) == 0 {
		return StatusFilter{
			IncludeSuccess:    true,
			IncludeErrorOther: true,
		}
	}
	normalized := make(map[string]struct{}, len(statuses))
	for _, value := range statuses {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		normalized[value] = struct{}{}
	}
	if len(normalized) == 0 {
		return StatusFilter{
			IncludeSuccess:    true,
			IncludeErrorOther: true,
		}
	}
	if _, ok := normalized["all"]; ok {
		return StatusFilter{
			IncludeSuccess:    true,
			IncludeErrorOther: true,
		}
	}

	includeSuccess := false
	if _, ok := normalized["ok"]; ok {
		includeSuccess = true
	}
	includeRateLimit := false
	if _, ok := normalized["rate_limit"]; ok {
		includeRateLimit = true
	}
	includeQuota := false
	if _, ok := normalized["quota"]; ok {
		includeQuota = true
	}
	includeErrorOther := false
	if _, ok := normalized["error"]; ok {
		includeErrorOther = true
	}

	errorCodesIn := make([]string, 0)
	if includeRateLimit {
		errorCodesIn = append(errorCodesIn, "rate_limit_exceeded", "usage_limit_reached")
	}
	if includeQuota {
		errorCodesIn = append(errorCodesIn, "insufficient_quota", "usage_not_included", "quota_exceeded")
	}

	var errorCodesExcluding []string
	if includeErrorOther {
		errorCodesExcluding = []string{
			"rate_limit_exceeded", "usage_limit_reached",
			"insufficient_quota", "usage_not_included", "quota_exceeded",
		}
	}

	return StatusFilter{
		IncludeSuccess:      includeSuccess,
		IncludeErrorOther:   includeErrorOther,
		ErrorCodesIn:        errorCodesIn,
		ErrorCodesExcluding: errorCodesExcluding,
	}
}

func NormalizeStatusValues(statuses []string, errorCodes []*string) []string {
	seen := make(map[string]struct{}, len(statuses))
	for i, status := range statuses {
		var code *string
		if i < len(errorCodes) {
			code = errorCodes[i]
		}
		seen[NormalizeLogStatus(status, code)] = struct{}{}
	}
	ordered := []string{"ok", "rate_limit", "quota", "error"}
	result := make([]string, 0, len(seen))
	for _, status := range ordered {
		if _, ok := seen[status]; ok {
			result = append(result, status)
		}
	}
	return result
}
