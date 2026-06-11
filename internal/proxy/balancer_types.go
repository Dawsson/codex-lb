package proxy

// This file ports the shared types from app/core/balancer/types.py and the
// type-level constants from app/core/balancer/logic.py used by the
// account-selection logic in balancer_logic.go and load_balancer.go.

// Health tier constants, mirroring HEALTH_TIER_* in app/core/balancer/logic.py.
const (
	HealthTierHealthy  = 0
	HealthTierDraining = 1
	HealthTierProbing  = 2
)

// Routing policy constants, mirroring ROUTING_POLICY_* in
// app/core/balancer/logic.py.
const (
	RoutingPolicyNormal    = "normal"
	RoutingPolicyBurnFirst = "burn_first"
	RoutingPolicyPreserve  = "preserve"
)

// Traffic class constants, mirroring TRAFFIC_CLASS_* in
// app/core/balancer/logic.py.
const (
	TrafficClassForeground    = "foreground"
	TrafficClassOpportunistic = "opportunistic"
)

// RoutingStrategy mirrors app.core.balancer.logic.RoutingStrategy.
type RoutingStrategy string

const (
	RoutingStrategyUsageWeighted        RoutingStrategy = "usage_weighted"
	RoutingStrategyRoundRobin           RoutingStrategy = "round_robin"
	RoutingStrategyCapacityWeighted     RoutingStrategy = "capacity_weighted"
	RoutingStrategyRelativeAvailability RoutingStrategy = "relative_availability"
	RoutingStrategyFillFirst            RoutingStrategy = "fill_first"
	RoutingStrategySequentialDrain      RoutingStrategy = "sequential_drain"
	RoutingStrategyResetDrain           RoutingStrategy = "reset_drain"
	RoutingStrategySingleAccount        RoutingStrategy = "single_account"
)

// ResetPreferenceWindow mirrors app.core.balancer.logic.ResetPreferenceWindow.
type ResetPreferenceWindow string

const (
	ResetPreferenceWindowPrimary   ResetPreferenceWindow = "primary"
	ResetPreferenceWindowSecondary ResetPreferenceWindow = "secondary"
)

// UsageWeightedOrder mirrors app.core.balancer.logic.UsageWeightedOrder.
type UsageWeightedOrder string

const (
	UsageWeightedOrderSecondaryFirst UsageWeightedOrder = "secondary_first"
	UsageWeightedOrderPrimaryFirst   UsageWeightedOrder = "primary_first"
)

// AccountState mirrors app.core.balancer.logic.AccountState: the balancer's
// view of a single account's eligibility and usage for one selection pass.
type AccountState struct {
	AccountID      string
	Status         string
	UsedPercent    *float64
	ResetAt        *float64
	PrimaryResetAt *int64
	BlockedAt      *float64
	CooldownUntil  *float64

	SecondaryUsedPercent *float64
	SecondaryResetAt     *int64

	LastErrorAt    *float64
	LastSelectedAt *float64
	ErrorCount     int

	DeactivationReason *string
	PlanType           string
	CapacityCredits    *float64
	HealthTier         int

	PriorityUsedPercent          *float64
	PrioritySecondaryUsedPercent *float64
	PriorityResetAt              *int64
	PriorityCapacityCredits      *float64
	LimitScopedUsage             bool

	InflightResponseCreates int
	InflightStreams         int
	LeasedTokens            float64

	RoutingPolicy       string
	IgnoreStandardQuota bool
}

// SelectionResult mirrors app.core.balancer.logic.SelectionResult.
type SelectionResult struct {
	Account      *AccountState
	ErrorMessage string
}

// RoutingCost mirrors app.core.balancer.logic.RoutingCost: a request-scoped
// planner cost applied after hard eligibility filters.
type RoutingCost struct {
	Total  float64
	Reason string
}

// RoutingCostsByAccount mirrors app.core.balancer.logic.RoutingCostsByAccount.
type RoutingCostsByAccount map[string]RoutingCost

// AccountStatus value constants, mirroring app.db.models.AccountStatus.
const (
	AccountStatusActive         = "active"
	AccountStatusRateLimited    = "rate_limited"
	AccountStatusQuotaExceeded  = "quota_exceeded"
	AccountStatusPaused         = "paused"
	AccountStatusReauthRequired = "reauth_required"
	AccountStatusDeactivated    = "deactivated"
)

// QuotaExceededCooldownSeconds mirrors QUOTA_EXCEEDED_COOLDOWN_SECONDS.
const QuotaExceededCooldownSeconds = 120.0

// SelectorRetryHintMaxSeconds mirrors SELECTOR_RETRY_HINT_MAX_SECONDS.
const SelectorRetryHintMaxSeconds = 300

// Default relative-availability tuning constants, mirroring
// DEFAULT_RELATIVE_AVAILABILITY_POWER / DEFAULT_RELATIVE_AVAILABILITY_TOP_K.
const (
	DefaultRelativeAvailabilityPower = 2.0
	DefaultRelativeAvailabilityTopK  = 5
)

// AccountLeaseKind mirrors app.modules.proxy.load_balancer.AccountLeaseKind.
type AccountLeaseKind string

const (
	AccountLeaseKindResponseCreate AccountLeaseKind = "response_create"
	AccountLeaseKindStream         AccountLeaseKind = "stream"
)

// AccountLease mirrors app.modules.proxy.load_balancer.AccountLease.
type AccountLease struct {
	LeaseID          string
	AccountID        string
	Kind             AccountLeaseKind
	AcquiredAt       float64
	EstimatedTokens  float64
}

// AccountSelection mirrors app.modules.proxy.load_balancer.AccountSelection.
type AccountSelection struct {
	Account      *ProxyAccount
	ErrorMessage string
	ErrorCode    string
	Lease        *AccountLease
}
