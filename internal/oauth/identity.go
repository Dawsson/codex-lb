package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultEmail = "unknown@example.com"
	defaultPlan  = "unknown"
)

// IDTokenAuthClaims mirrors app.core.auth.OpenAIAuthClaims (the
// "https://api.openai.com/auth" namespace inside an id_token).
type IDTokenAuthClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	WorkspaceID      string `json:"-"`
	WorkspaceLabel   string `json:"-"`
	SeatType         string `json:"-"`
}

// IDTokenClaims mirrors app.core.auth.IdTokenClaims.
type IDTokenClaims struct {
	Email            string             `json:"email"`
	ChatGPTAccountID string             `json:"-"`
	ChatGPTPlanType  string             `json:"-"`
	WorkspaceID      string             `json:"-"`
	WorkspaceLabel   string             `json:"-"`
	SeatType         string             `json:"-"`
	Auth             *IDTokenAuthClaims `json:"-"`
}

type GuardianClaims struct {
	Email            string
	ChatGPTAccountID string
	PlanType         string
	WorkspaceID      string
	WorkspaceLabel   string
	SeatType         string
}

func ExtractIDTokenClaimsForGuardian(idToken string) GuardianClaims {
	claims := extractIDTokenClaims(idToken)
	out := GuardianClaims{
		Email:            claims.Email,
		ChatGPTAccountID: claims.ChatGPTAccountID,
		PlanType:         coerceAccountPlanType(claims.ChatGPTPlanType, defaultPlan),
		WorkspaceID:      cleanAccountIdentityPart(claims.WorkspaceID),
		WorkspaceLabel:   cleanAccountIdentityPart(claims.WorkspaceLabel),
		SeatType:         normalizeSeatType(claims.SeatType),
	}
	if claims.Auth != nil {
		out.ChatGPTAccountID = firstNonEmpty(claims.Auth.ChatGPTAccountID, out.ChatGPTAccountID)
		out.PlanType = coerceAccountPlanType(firstNonEmpty(claims.Auth.ChatGPTPlanType, out.PlanType), defaultPlan)
		out.WorkspaceID = cleanAccountIdentityPart(firstNonEmpty(claims.Auth.WorkspaceID, out.WorkspaceID))
		out.WorkspaceLabel = cleanAccountIdentityPart(firstNonEmpty(claims.Auth.WorkspaceLabel, out.WorkspaceLabel))
		out.SeatType = normalizeSeatType(firstNonEmpty(claims.Auth.SeatType, out.SeatType))
	}
	return out
}

// rawIDTokenClaims maps the raw JSON fields including the alias-choice keys
// used by the Python pydantic models.
type rawIDTokenClaims struct {
	Email              string         `json:"email"`
	ChatGPTAccountID   string         `json:"chatgpt_account_id"`
	ChatGPTPlanType    string         `json:"chatgpt_plan_type"`
	WorkspaceID        string         `json:"workspace_id"`
	ChatGPTWorkspaceID string         `json:"chatgpt_workspace_id"`
	OrganizationID     string         `json:"organization_id"`
	OrgID              string         `json:"org_id"`
	TenantID           string         `json:"tenant_id"`
	WorkspaceLabel     string         `json:"workspace_label"`
	WorkspaceName      string         `json:"workspace_name"`
	OrganizationName   string         `json:"organization_name"`
	OrgName            string         `json:"org_name"`
	TenantName         string         `json:"tenant_name"`
	SeatType           string         `json:"seat_type"`
	ChatGPTSeatType    string         `json:"chatgpt_seat_type"`
	EntitlementType    string         `json:"entitlement_type"`
	Auth               *rawAuthClaims `json:"https://api.openai.com/auth"`
}

type rawAuthClaims struct {
	ChatGPTAccountID   string `json:"chatgpt_account_id"`
	ChatGPTPlanType    string `json:"chatgpt_plan_type"`
	WorkspaceID        string `json:"workspace_id"`
	ChatGPTWorkspaceID string `json:"chatgpt_workspace_id"`
	OrganizationID     string `json:"organization_id"`
	OrgID              string `json:"org_id"`
	TenantID           string `json:"tenant_id"`
	WorkspaceLabel     string `json:"workspace_label"`
	WorkspaceName      string `json:"workspace_name"`
	OrganizationName   string `json:"organization_name"`
	OrgName            string `json:"org_name"`
	TenantName         string `json:"tenant_name"`
	SeatType           string `json:"seat_type"`
	ChatGPTSeatType    string `json:"chatgpt_seat_type"`
	EntitlementType    string `json:"entitlement_type"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// extractIDTokenClaims ports app.core.auth.extract_id_token_claims. It never
// fails: malformed tokens yield zero-value claims, matching the Python
// behavior of swallowing all exceptions.
func extractIDTokenClaims(idToken string) IDTokenClaims {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return IDTokenClaims{}
	}
	payload := parts[1]
	if missing := len(payload) % 4; missing != 0 {
		payload += strings.Repeat("=", 4-missing)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return IDTokenClaims{}
	}
	var raw rawIDTokenClaims
	if err := json.Unmarshal(decoded, &raw); err != nil {
		return IDTokenClaims{}
	}

	claims := IDTokenClaims{
		Email:            raw.Email,
		ChatGPTAccountID: raw.ChatGPTAccountID,
		ChatGPTPlanType:  raw.ChatGPTPlanType,
		WorkspaceID:      firstNonEmpty(raw.WorkspaceID, raw.ChatGPTWorkspaceID, raw.OrganizationID, raw.OrgID, raw.TenantID),
		WorkspaceLabel:   firstNonEmpty(raw.WorkspaceLabel, raw.WorkspaceName, raw.OrganizationName, raw.OrgName, raw.TenantName),
		SeatType:         firstNonEmpty(raw.SeatType, raw.ChatGPTSeatType, raw.EntitlementType),
	}
	if raw.Auth != nil {
		claims.Auth = &IDTokenAuthClaims{
			ChatGPTAccountID: raw.Auth.ChatGPTAccountID,
			ChatGPTPlanType:  raw.Auth.ChatGPTPlanType,
			WorkspaceID:      firstNonEmpty(raw.Auth.WorkspaceID, raw.Auth.ChatGPTWorkspaceID, raw.Auth.OrganizationID, raw.Auth.OrgID, raw.Auth.TenantID),
			WorkspaceLabel:   firstNonEmpty(raw.Auth.WorkspaceLabel, raw.Auth.WorkspaceName, raw.Auth.OrganizationName, raw.Auth.OrgName, raw.Auth.TenantName),
			SeatType:         firstNonEmpty(raw.Auth.SeatType, raw.Auth.ChatGPTSeatType, raw.Auth.EntitlementType),
		}
	}
	return claims
}

// cleanAccountIdentityPart ports app.core.auth.clean_account_identity_part.
func cleanAccountIdentityPart(value string) string {
	return strings.TrimSpace(value)
}

// normalizeSeatType ports app.core.auth.normalize_seat_type.
func normalizeSeatType(value string) string {
	cleaned := cleanAccountIdentityPart(value)
	if cleaned == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(cleaned), "-", "_")
}

// shaHex returns the hex-encoded sha256 digest of value.
func shaHex(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

// generateUniqueAccountID ports app.core.auth.generate_unique_account_id.
func generateUniqueAccountID(accountID, email, workspaceID string) string {
	workspaceKey := cleanAccountIdentityPart(workspaceID)
	if accountID != "" && workspaceKey != "" {
		return accountID + "_" + shaHex(workspaceKey)[:8]
	}
	if accountID != "" && email != "" && email != defaultEmail {
		return accountID + "_" + shaHex(email)[:8]
	}
	if accountID != "" {
		return accountID
	}
	return fallbackAccountID(email)
}

// fallbackAccountID ports app.core.auth.fallback_account_id.
func fallbackAccountID(email string) string {
	if email != "" && email != defaultEmail {
		return "email_" + shaHex(email)[:12]
	}
	return "local_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

var accountPlanTypes = map[string]bool{
	"free":       true,
	"plus":       true,
	"pro":        true,
	"prolite":    true,
	"team":       true,
	"business":   true,
	"enterprise": true,
	"edu":        true,
}

// coerceAccountPlanType ports app.core.plan_types.coerce_account_plan_type.
func coerceAccountPlanType(value, fallback string) string {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return fallback
	}
	normalized := strings.ToLower(cleaned)
	if accountPlanTypes[normalized] {
		return normalized
	}
	return cleaned
}
