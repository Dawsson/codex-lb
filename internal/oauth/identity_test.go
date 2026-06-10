package oauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func encodeJWTPayload(t *testing.T, payload map[string]any) string {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return "header." + encoded + ".signature"
}

func TestExtractIDTokenClaims(t *testing.T) {
	token := encodeJWTPayload(t, map[string]any{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-123",
			"chatgpt_plan_type":  "Pro",
			"organization_id":    "org-1",
			"organization_name":  "Acme",
			"chatgpt_seat_type":  "Member-Seat",
		},
	})

	claims := extractIDTokenClaims(token)
	if claims.Email != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %q", claims.Email)
	}
	if claims.Auth == nil {
		t.Fatalf("expected auth claims to be present")
	}
	if claims.Auth.ChatGPTAccountID != "acct-123" {
		t.Fatalf("expected chatgpt_account_id acct-123, got %q", claims.Auth.ChatGPTAccountID)
	}
	if claims.Auth.WorkspaceID != "org-1" {
		t.Fatalf("expected workspace id org-1, got %q", claims.Auth.WorkspaceID)
	}
	if claims.Auth.WorkspaceLabel != "Acme" {
		t.Fatalf("expected workspace label Acme, got %q", claims.Auth.WorkspaceLabel)
	}
	if claims.Auth.SeatType != "Member-Seat" {
		t.Fatalf("expected raw seat type Member-Seat, got %q", claims.Auth.SeatType)
	}
}

func TestExtractIDTokenClaimsMalformed(t *testing.T) {
	for _, token := range []string{"", "not-a-jwt", "a.b"} {
		if claims := extractIDTokenClaims(token); claims.Email != "" {
			t.Fatalf("expected empty claims for %q, got %#v", token, claims)
		}
	}
	// "a.b" has 2 parts but "b" isn't valid base64/JSON.
	if claims := extractIDTokenClaims("a.!!!.c"); claims.Email != "" {
		t.Fatalf("expected empty claims for invalid base64 segment, got %#v", claims)
	}
}

func TestNormalizeSeatType(t *testing.T) {
	cases := map[string]string{
		"Member-Seat": "member_seat",
		"  Owner  ":   "owner",
		"":            "",
	}
	for input, expected := range cases {
		if got := normalizeSeatType(input); got != expected {
			t.Fatalf("normalizeSeatType(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestGenerateUniqueAccountID(t *testing.T) {
	if got := generateUniqueAccountID("acct-1", "user@example.com", "ws-1"); got != "acct-1_"+sha8("ws-1") {
		t.Fatalf("workspace-keyed id = %q", got)
	}
	if got := generateUniqueAccountID("acct-1", "user@example.com", ""); got != "acct-1_"+sha8("user@example.com") {
		t.Fatalf("email-keyed id = %q", got)
	}
	if got := generateUniqueAccountID("acct-1", defaultEmail, ""); got != "acct-1" {
		t.Fatalf("plain id = %q, want acct-1", got)
	}
	if got := generateUniqueAccountID("", defaultEmail, ""); len(got) < len("local_") {
		t.Fatalf("fallback id too short: %q", got)
	}
	if got := generateUniqueAccountID("", "user@example.com", ""); got != "email_"+sha12("user@example.com") {
		t.Fatalf("email fallback id = %q", got)
	}
}

func sha8(value string) string {
	return shaHex(value)[:8]
}

func sha12(value string) string {
	return shaHex(value)[:12]
}

func TestCoerceAccountPlanType(t *testing.T) {
	if got := coerceAccountPlanType("", "unknown"); got != "unknown" {
		t.Fatalf("empty value should fall back to default, got %q", got)
	}
	if got := coerceAccountPlanType("Pro", "unknown"); got != "pro" {
		t.Fatalf("recognized plan should be canonicalized, got %q", got)
	}
	if got := coerceAccountPlanType("Mystery-Plan", "unknown"); got != "Mystery-Plan" {
		t.Fatalf("unrecognized plan should be returned as cleaned input, got %q", got)
	}
}
