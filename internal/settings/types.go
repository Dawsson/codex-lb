package settings

import (
	"encoding/json"
)

type DashboardSettings struct {
	StickyThreadsEnabled                                    bool              `json:"stickyThreadsEnabled"`
	UpstreamStreamTransport                                 string            `json:"upstreamStreamTransport"`
	UpstreamProxyRoutingEnabled                             bool              `json:"upstreamProxyRoutingEnabled"`
	UpstreamProxyDefaultPoolID                              *string           `json:"upstreamProxyDefaultPoolId"`
	PreferEarlierResetAccounts                              bool              `json:"preferEarlierResetAccounts"`
	PreferEarlierResetWindow                                string            `json:"preferEarlierResetWindow"`
	RoutingStrategy                                         string            `json:"routingStrategy"`
	RelativeAvailabilityPower                               float64           `json:"relativeAvailabilityPower"`
	RelativeAvailabilityTopK                                int               `json:"relativeAvailabilityTopK"`
	SingleAccountID                                         *string           `json:"singleAccountId"`
	OpenAICacheAffinityMaxAgeSeconds                        int               `json:"openaiCacheAffinityMaxAgeSeconds"`
	DashboardSessionTTLSeconds                              int               `json:"dashboardSessionTtlSeconds"`
	HTTPResponsesSessionBridgePromptCacheIdleTTLSeconds     int               `json:"httpResponsesSessionBridgePromptCacheIdleTtlSeconds"`
	HTTPResponsesSessionBridgeGatewaySafeMode               bool              `json:"httpResponsesSessionBridgeGatewaySafeMode"`
	StickyReallocationBudgetThresholdPct                    float64           `json:"stickyReallocationBudgetThresholdPct"`
	StickyReallocationPrimaryBudgetThresholdPct             float64           `json:"stickyReallocationPrimaryBudgetThresholdPct"`
	StickyReallocationSecondaryBudgetThresholdPct             float64           `json:"stickyReallocationSecondaryBudgetThresholdPct"`
	AdditionalQuotaRoutingPolicies                          map[string]string `json:"additionalQuotaRoutingPolicies"`
	AdditionalQuotaPolicies                                 []any             `json:"additionalQuotaPolicies"`
	WarmupModel                                             string            `json:"warmupModel"`
	ImportWithoutOverwrite                                  bool              `json:"importWithoutOverwrite"`
	TOTPRequiredOnLogin                                     bool              `json:"totpRequiredOnLogin"`
	TOTPConfigured                                          bool              `json:"totpConfigured"`
	APIKeyAuthEnabled                                       bool              `json:"apiKeyAuthEnabled"`
	LimitWarmupEnabled                                      bool              `json:"limitWarmupEnabled"`
	LimitWarmupWindows                                      string            `json:"limitWarmupWindows"`
	LimitWarmupModel                                        string            `json:"limitWarmupModel"`
	LimitWarmupPrompt                                       string            `json:"limitWarmupPrompt"`
	LimitWarmupCooldownSeconds                              int               `json:"limitWarmupCooldownSeconds"`
	LimitWarmupMinAvailablePercent                          float64           `json:"limitWarmupMinAvailablePercent"`
	WeeklyPaceWorkingDays                                   string            `json:"weeklyPaceWorkingDays"`
}

type UpdateRequest struct {
	StickyThreadsEnabled                                *bool              `json:"stickyThreadsEnabled"`
	UpstreamStreamTransport                             *string            `json:"upstreamStreamTransport"`
	UpstreamProxyRoutingEnabled                         *bool              `json:"upstreamProxyRoutingEnabled"`
	UpstreamProxyDefaultPoolID                          *string            `json:"upstreamProxyDefaultPoolId"`
	PreferEarlierResetAccounts                          *bool              `json:"preferEarlierResetAccounts"`
	PreferEarlierResetWindow                            *string            `json:"preferEarlierResetWindow"`
	RoutingStrategy                                     *string            `json:"routingStrategy"`
	RelativeAvailabilityPower                           *float64           `json:"relativeAvailabilityPower"`
	RelativeAvailabilityTopK                            *int               `json:"relativeAvailabilityTopK"`
	SingleAccountID                                     *string            `json:"singleAccountId"`
	OpenAICacheAffinityMaxAgeSeconds                    *int               `json:"openaiCacheAffinityMaxAgeSeconds"`
	DashboardSessionTTLSeconds                          *int               `json:"dashboardSessionTtlSeconds"`
	StickyReallocationBudgetThresholdPct                *float64           `json:"stickyReallocationBudgetThresholdPct"`
	StickyReallocationPrimaryBudgetThresholdPct         *float64           `json:"stickyReallocationPrimaryBudgetThresholdPct"`
	StickyReallocationSecondaryBudgetThresholdPct       *float64           `json:"stickyReallocationSecondaryBudgetThresholdPct"`
	AdditionalQuotaRoutingPolicies                      *map[string]string `json:"additionalQuotaRoutingPolicies"`
	WarmupModel                                         *string            `json:"warmupModel"`
	ImportWithoutOverwrite                              *bool              `json:"importWithoutOverwrite"`
	TOTPRequiredOnLogin                                 *bool              `json:"totpRequiredOnLogin"`
	APIKeyAuthEnabled                                   *bool              `json:"apiKeyAuthEnabled"`
	LimitWarmupEnabled                                  *bool              `json:"limitWarmupEnabled"`
	LimitWarmupWindows                                  *string            `json:"limitWarmupWindows"`
	LimitWarmupModel                                    *string            `json:"limitWarmupModel"`
	LimitWarmupPrompt                                   *string            `json:"limitWarmupPrompt"`
	LimitWarmupCooldownSeconds                          *int               `json:"limitWarmupCooldownSeconds"`
	LimitWarmupMinAvailablePercent                      *float64           `json:"limitWarmupMinAvailablePercent"`
	WeeklyPaceWorkingDays                               *string            `json:"weeklyPaceWorkingDays"`
}

type UpstreamProxyEndpoint struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Scheme   string  `json:"scheme"`
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	Username *string `json:"username"`
	IsActive bool    `json:"isActive"`
}

type UpstreamProxyPool struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	IsActive    bool     `json:"isActive"`
	EndpointIDs []string `json:"endpointIds"`
}

type AccountProxyBinding struct {
	AccountID string `json:"accountId"`
	PoolID    string `json:"poolId"`
	IsActive  bool   `json:"isActive"`
}

type UpstreamProxyAdmin struct {
	RoutingEnabled bool                  `json:"routingEnabled"`
	DefaultPoolID  *string               `json:"defaultPoolId"`
	Endpoints      []UpstreamProxyEndpoint `json:"endpoints"`
	Pools          []UpstreamProxyPool     `json:"pools"`
	Bindings       []AccountProxyBinding   `json:"bindings"`
}

type UpstreamProxyEndpointCreateRequest struct {
	Name     string  `json:"name"`
	Scheme   string  `json:"scheme"`
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	IsActive bool    `json:"isActive"`
}

type UpstreamProxyPoolCreateRequest struct {
	Name        string   `json:"name"`
	EndpointIDs []string `json:"endpointIds"`
	IsActive    bool     `json:"isActive"`
}

type UpstreamProxyPoolMemberRequest struct {
	EndpointID string `json:"endpointId"`
	SortOrder  int    `json:"sortOrder"`
	Weight     int    `json:"weight"`
	IsActive   bool   `json:"isActive"`
}

type AccountProxyBindingRequest struct {
	PoolID   string `json:"poolId"`
	IsActive bool   `json:"isActive"`
}

type RuntimeConnectAddressResponse struct {
	ConnectAddress string `json:"connectAddress"`
}

func decodeAdditionalQuotaPolicies(raw string) map[string]string {
	if raw == "" {
		return map[string]string{}
	}
	var policies map[string]string
	if err := json.Unmarshal([]byte(raw), &policies); err != nil {
		return map[string]string{}
	}
	return policies
}

func encodeAdditionalQuotaPolicies(policies map[string]string) string {
	if policies == nil {
		policies = map[string]string{}
	}
	data, err := json.Marshal(policies)
	if err != nil {
		return "{}"
	}
	return string(data)
}
