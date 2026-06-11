package requestlogs

import (
	"math"
	"strings"
)

type modelPrice struct {
	inputPer1M                  float64
	outputPer1M                 float64
	cachedInputPer1M            *float64
	priorityMultiplier          *float64
	priorityInputPer1M          *float64
	priorityOutputPer1M         *float64
	priorityCachedInputPer1M    *float64
	flexInputPer1M              *float64
	flexOutputPer1M             *float64
	flexCachedInputPer1M        *float64
	longContextThresholdTokens  *float64
	longContextInputPer1M       *float64
	longContextOutputPer1M      *float64
	longContextCachedInputPer1M *float64
}

type usageTokens struct {
	inputTokens       float64
	outputTokens      float64
	cachedInputTokens float64
}

type costBreakdown struct {
	inputUSD       *float64
	cachedInputUSD *float64
	outputUSD      *float64
	totalUSD       *float64
	rawTotalUSD    *float64
}

var defaultModelPrices = map[string]modelPrice{
	"gpt-5.5": {
		inputPer1M: 5.0, cachedInputPer1M: f64ptr(0.5), outputPer1M: 30.0,
		flexInputPer1M: f64ptr(2.5), flexCachedInputPer1M: f64ptr(0.25), flexOutputPer1M: f64ptr(15.0),
		priorityInputPer1M: f64ptr(12.5), priorityCachedInputPer1M: f64ptr(1.25), priorityOutputPer1M: f64ptr(75.0),
	},
	"gpt-5.5-pro": {inputPer1M: 30.0, outputPer1M: 180.0, flexInputPer1M: f64ptr(15.0), flexOutputPer1M: f64ptr(90.0)},
	"gpt-5.4": {
		inputPer1M: 2.5, cachedInputPer1M: f64ptr(0.25), outputPer1M: 15.0,
		priorityInputPer1M: f64ptr(5.0), priorityCachedInputPer1M: f64ptr(0.5), priorityOutputPer1M: f64ptr(30.0),
		flexInputPer1M: f64ptr(1.25), flexCachedInputPer1M: f64ptr(0.125), flexOutputPer1M: f64ptr(7.5),
		longContextThresholdTokens: f64ptr(272000), longContextInputPer1M: f64ptr(5.0), longContextCachedInputPer1M: f64ptr(0.5), longContextOutputPer1M: f64ptr(22.5),
	},
	"gpt-5.4-mini": {inputPer1M: 0.75, cachedInputPer1M: f64ptr(0.075), outputPer1M: 4.5, flexInputPer1M: f64ptr(0.375), flexCachedInputPer1M: f64ptr(0.0375), flexOutputPer1M: f64ptr(2.25)},
	"gpt-5.4-nano": {inputPer1M: 0.20, cachedInputPer1M: f64ptr(0.02), outputPer1M: 1.25, flexInputPer1M: f64ptr(0.10), flexCachedInputPer1M: f64ptr(0.01), flexOutputPer1M: f64ptr(0.625)},
	"gpt-5.4-pro": {
		inputPer1M: 30.0, outputPer1M: 180.0, flexInputPer1M: f64ptr(15.0), flexOutputPer1M: f64ptr(90.0),
		longContextThresholdTokens: f64ptr(272000), longContextInputPer1M: f64ptr(60.0), longContextOutputPer1M: f64ptr(270.0),
	},
	"gpt-5.3-codex":       {inputPer1M: 1.75, cachedInputPer1M: f64ptr(0.175), outputPer1M: 14.0, priorityInputPer1M: f64ptr(3.5), priorityCachedInputPer1M: f64ptr(0.35), priorityOutputPer1M: f64ptr(28.0)},
	"gpt-5.3":             {inputPer1M: 1.75, cachedInputPer1M: f64ptr(0.175), outputPer1M: 14.0},
	"gpt-5.3-chat-latest": {inputPer1M: 1.75, cachedInputPer1M: f64ptr(0.175), outputPer1M: 14.0},
	"gpt-5.2":             {inputPer1M: 1.75, cachedInputPer1M: f64ptr(0.175), outputPer1M: 14.0, priorityMultiplier: f64ptr(2.0), flexInputPer1M: f64ptr(0.875), flexCachedInputPer1M: f64ptr(0.0875), flexOutputPer1M: f64ptr(7.0)},
	"gpt-5.2-chat-latest": {inputPer1M: 1.75, cachedInputPer1M: f64ptr(0.175), outputPer1M: 14.0},
	"gpt-5.2-codex":       {inputPer1M: 1.75, cachedInputPer1M: f64ptr(0.175), outputPer1M: 14.0, priorityInputPer1M: f64ptr(3.5), priorityCachedInputPer1M: f64ptr(0.35), priorityOutputPer1M: f64ptr(28.0)},
	"gpt-5.1":             {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0, priorityMultiplier: f64ptr(2.0), flexInputPer1M: f64ptr(0.625), flexCachedInputPer1M: f64ptr(0.0625), flexOutputPer1M: f64ptr(5.0)},
	"gpt-5.1-chat-latest": {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0},
	"gpt-5.1-codex-max":   {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0, priorityInputPer1M: f64ptr(2.5), priorityCachedInputPer1M: f64ptr(0.25), priorityOutputPer1M: f64ptr(20.0)},
	"gpt-5.1-codex-mini":  {inputPer1M: 0.25, cachedInputPer1M: f64ptr(0.025), outputPer1M: 2.0},
	"gpt-5.1-codex":       {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0, priorityInputPer1M: f64ptr(2.5), priorityCachedInputPer1M: f64ptr(0.25), priorityOutputPer1M: f64ptr(20.0)},
	"gpt-5":               {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0, priorityMultiplier: f64ptr(2.0), flexInputPer1M: f64ptr(0.625), flexCachedInputPer1M: f64ptr(0.0625), flexOutputPer1M: f64ptr(5.0)},
	"gpt-5-chat-latest":   {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0},
	"gpt-5-codex":         {inputPer1M: 1.25, cachedInputPer1M: f64ptr(0.125), outputPer1M: 10.0, priorityInputPer1M: f64ptr(2.5), priorityCachedInputPer1M: f64ptr(0.25), priorityOutputPer1M: f64ptr(20.0)},
	"gpt-image-2":         {inputPer1M: 5.0, cachedInputPer1M: f64ptr(2.0), outputPer1M: 30.0},
	"gpt-image-1.5":       {inputPer1M: 5.0, cachedInputPer1M: f64ptr(2.0), outputPer1M: 30.0},
	"gpt-image-1":         {inputPer1M: 5.0, cachedInputPer1M: f64ptr(2.0), outputPer1M: 30.0},
	"gpt-image-1-mini":    {inputPer1M: 5.0, cachedInputPer1M: f64ptr(2.0), outputPer1M: 30.0},
}

var defaultModelAliases = map[string]string{
	"gpt-5.5-pro*":         "gpt-5.5-pro",
	"gpt-5.5*":             "gpt-5.5",
	"gpt-5.4-pro*":         "gpt-5.4-pro",
	"gpt-5.4-mini*":        "gpt-5.4-mini",
	"gpt-5.4-nano*":        "gpt-5.4-nano",
	"gpt-5.4*":             "gpt-5.4",
	"gpt-5.3-codex*":       "gpt-5.3-codex",
	"gpt-5.3-chat-latest*": "gpt-5.3-chat-latest",
	"gpt-5.3*":             "gpt-5.3",
	"gpt-5.2-codex*":       "gpt-5.2-codex",
	"gpt-5.2-chat-latest*": "gpt-5.2-chat-latest",
	"gpt-5.2*":             "gpt-5.2",
	"gpt-5.1-chat-latest*": "gpt-5.1-chat-latest",
	"gpt-5.1-codex-max*":   "gpt-5.1-codex-max",
	"gpt-5.1-codex-mini*":  "gpt-5.1-codex-mini",
	"gpt-5.1-codex*":       "gpt-5.1-codex",
	"gpt-5.1*":             "gpt-5.1",
	"gpt-5-chat-latest*":   "gpt-5-chat-latest",
	"gpt-5-codex*":         "gpt-5-codex",
	"gpt-5*":               "gpt-5",
	"gpt-image-2*":         "gpt-image-2",
	"gpt-image-1.5*":       "gpt-image-1.5",
	"gpt-image-1-mini*":    "gpt-image-1-mini",
	"gpt-image-1*":         "gpt-image-1",
}

func f64ptr(value float64) *float64 {
	return &value
}

func priceForModel(model string) (modelPrice, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return modelPrice{}, false
	}
	for key, price := range defaultModelPrices {
		if strings.EqualFold(key, normalized) {
			return price, true
		}
	}
	alias := resolvePricingAlias(normalized)
	if alias == "" {
		return modelPrice{}, false
	}
	price, ok := defaultModelPrices[alias]
	return price, ok
}

func resolvePricingAlias(model string) string {
	bestPatternLen := -1
	bestAlias := ""
	for pattern, alias := range defaultModelAliases {
		prefix := strings.TrimSuffix(strings.ToLower(pattern), "*")
		if strings.HasPrefix(model, prefix) && len(pattern) > bestPatternLen {
			bestPatternLen = len(pattern)
			bestAlias = alias
		}
	}
	return bestAlias
}

func calculateCostBreakdown(usage usageTokens, price modelPrice, serviceTier string, precision *int) costBreakdown {
	if usage.inputTokens < 0 {
		usage.inputTokens = 0
	}
	if usage.outputTokens < 0 {
		usage.outputTokens = 0
	}
	usage.cachedInputTokens = math.Max(0, math.Min(usage.cachedInputTokens, usage.inputTokens))
	inputRate, cachedRate, outputRate := effectiveRates(usage, price, serviceTier)
	inputUSD := ((usage.inputTokens - usage.cachedInputTokens) / 1_000_000) * inputRate
	cachedInputUSD := (usage.cachedInputTokens / 1_000_000) * cachedRate
	outputUSD := (usage.outputTokens / 1_000_000) * outputRate
	rawTotal := inputUSD + cachedInputUSD + outputUSD
	if precision != nil {
		inputUSD = roundTo(inputUSD, *precision)
		cachedInputUSD = roundTo(cachedInputUSD, *precision)
		outputUSD = roundTo(outputUSD, *precision)
	}
	totalUSD := inputUSD + cachedInputUSD + outputUSD
	if precision != nil {
		totalUSD = roundTo(totalUSD, *precision)
	}
	return costBreakdown{
		inputUSD:       &inputUSD,
		cachedInputUSD: &cachedInputUSD,
		outputUSD:      &outputUSD,
		totalUSD:       &totalUSD,
		rawTotalUSD:    &rawTotal,
	}
}

func effectiveRates(usage usageTokens, price modelPrice, serviceTier string) (float64, float64, float64) {
	isLongContext := price.longContextThresholdTokens != nil &&
		usage.inputTokens > *price.longContextThresholdTokens &&
		price.longContextInputPer1M != nil &&
		price.longContextOutputPer1M != nil
	inputRate := price.inputPer1M
	cachedRate := price.inputPer1M
	if price.cachedInputPer1M != nil {
		cachedRate = *price.cachedInputPer1M
	}
	outputRate := price.outputPer1M

	switch normalizedServiceTier(serviceTier) {
	case "priority", "fast":
		if price.priorityInputPer1M != nil && price.priorityOutputPer1M != nil {
			priorityCached := *price.priorityInputPer1M
			if price.priorityCachedInputPer1M != nil {
				priorityCached = *price.priorityCachedInputPer1M
			}
			return *price.priorityInputPer1M, priorityCached, *price.priorityOutputPer1M
		}
		if price.priorityMultiplier != nil {
			return inputRate * *price.priorityMultiplier, cachedRate * *price.priorityMultiplier, outputRate * *price.priorityMultiplier
		}
	case "flex":
		if price.flexInputPer1M != nil && price.flexOutputPer1M != nil {
			inputRate = *price.flexInputPer1M
			cachedRate = inputRate
			if price.flexCachedInputPer1M != nil {
				cachedRate = *price.flexCachedInputPer1M
			}
			outputRate = *price.flexOutputPer1M
			if isLongContext {
				return inputRate * 2.0, cachedRate * 2.0, outputRate * 1.5
			}
			return inputRate, cachedRate, outputRate
		}
	}
	if isLongContext {
		inputRate = *price.longContextInputPer1M
		cachedRate = inputRate
		if price.longContextCachedInputPer1M != nil {
			cachedRate = *price.longContextCachedInputPer1M
		}
		outputRate = *price.longContextOutputPer1M
	}
	return inputRate, cachedRate, outputRate
}

func normalizedServiceTier(serviceTier string) string {
	return strings.ToLower(strings.TrimSpace(serviceTier))
}

func roundTo(value float64, precision int) float64 {
	factor := math.Pow10(precision)
	return math.Round(value*factor) / factor
}

func totalsMatch(left *float64, right *float64, precision int) bool {
	if left == nil || right == nil {
		return false
	}
	return math.Abs(*left-*right) < math.Pow10(-precision)/2
}
