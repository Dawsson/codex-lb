package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPKCEPair(t *testing.T) {
	verifier, challenge, err := generatePKCEPair()
	if err != nil {
		t.Fatalf("generate pkce pair: %v", err)
	}
	if verifier == "" || challenge == "" {
		t.Fatalf("expected non-empty verifier/challenge")
	}
	if pkceChallenge(verifier) != challenge {
		t.Fatalf("challenge does not match verifier")
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	url := buildAuthorizationURL(authorizationURLParams{
		State:         "state-token",
		CodeChallenge: "challenge",
		BaseURL:       "https://auth.openai.com/",
		ClientID:      "client-id",
		Originator:    "codex_chatgpt_desktop",
		RedirectURI:   "http://localhost:1455/auth/callback",
		Scope:         "openid profile email",
	})

	if !strings.HasPrefix(url, "https://auth.openai.com/oauth/authorize?") {
		t.Fatalf("unexpected url prefix: %s", url)
	}
	for _, want := range []string{
		"response_type=code",
		"client_id=client-id",
		"code_challenge=challenge",
		"code_challenge_method=S256",
		"state=state-token",
		"id_token_add_organizations=true",
		"codex_cli_simplified_flow=true",
		"scope=openid+profile+email+offline_access",
	} {
		if !strings.Contains(url, want) {
			t.Fatalf("expected url to contain %q, got %s", want, url)
		}
	}
}

func TestExchangeAuthorizationCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Fatalf("unexpected grant_type: %s", r.Form.Get("grant_type"))
		}
		json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "access",
			"refresh_token": "refresh",
			"id_token":      "id",
		})
	}))
	defer server.Close()

	tokens, err := exchangeAuthorizationCode(context.Background(), server.Client(), exchangeAuthorizationCodeParams{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "http://localhost:1455/auth/callback",
		BaseURL:      server.URL,
		ClientID:     "client-id",
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("exchange authorization code: %v", err)
	}
	if tokens.AccessToken != "access" || tokens.RefreshToken != "refresh" || tokens.IDToken != "id" {
		t.Fatalf("unexpected tokens: %#v", tokens)
	}
}

func TestExchangeAuthorizationCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_grant",
			"error_description": "the code is invalid",
		})
	}))
	defer server.Close()

	_, err := exchangeAuthorizationCode(context.Background(), server.Client(), exchangeAuthorizationCodeParams{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "http://localhost:1455/auth/callback",
		BaseURL:      server.URL,
		ClientID:     "client-id",
		Timeout:      5 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var oauthErr *OAuthError
	if !asOAuthError(err, &oauthErr) {
		t.Fatalf("expected OAuthError, got %T: %v", err, err)
	}
	if oauthErr.Code != "invalid_grant" || oauthErr.Message != "the code is invalid" {
		t.Fatalf("unexpected error: %#v", oauthErr)
	}
}

func TestRequestDeviceCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"device_auth_id": "device-1",
			"user_code":      "ABCD-1234",
			"interval":       5,
			"expires_in":     900,
		})
	}))
	defer server.Close()

	device, err := requestDeviceCode(context.Background(), server.Client(), requestDeviceCodeParams{
		BaseURL:  server.URL,
		ClientID: "client-id",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("request device code: %v", err)
	}
	if device.DeviceAuthID != "device-1" || device.UserCode != "ABCD-1234" {
		t.Fatalf("unexpected device code: %#v", device)
	}
	if device.VerificationURL != server.URL+"/codex/device" {
		t.Fatalf("unexpected verification url: %s", device.VerificationURL)
	}
	if device.IntervalSeconds != 5 || device.ExpiresInSeconds != 900 {
		t.Fatalf("unexpected interval/expiry: %#v", device)
	}
}

func TestRequestDeviceCodeUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := requestDeviceCode(context.Background(), server.Client(), requestDeviceCodeParams{
		BaseURL:  server.URL,
		ClientID: "client-id",
		Timeout:  5 * time.Second,
	})
	var oauthErr *OAuthError
	if !asOAuthError(err, &oauthErr) {
		t.Fatalf("expected OAuthError, got %v", err)
	}
	if oauthErr.Code != "device_auth_unavailable" {
		t.Fatalf("unexpected error code: %s", oauthErr.Code)
	}
}

func TestExchangeDeviceTokenPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
	}))
	defer server.Close()

	tokens, err := exchangeDeviceToken(context.Background(), server.Client(), exchangeDeviceTokenParams{
		DeviceAuthID: "device-1",
		UserCode:     "ABCD-1234",
		BaseURL:      server.URL,
		ClientID:     "client-id",
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected nil error for pending, got %v", err)
	}
	if tokens != nil {
		t.Fatalf("expected nil tokens for pending, got %#v", tokens)
	}
}

func TestExchangeDeviceTokenSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "access",
			"refresh_token": "refresh",
			"id_token":      "id",
		})
	}))
	defer server.Close()

	tokens, err := exchangeDeviceToken(context.Background(), server.Client(), exchangeDeviceTokenParams{
		DeviceAuthID: "device-1",
		UserCode:     "ABCD-1234",
		BaseURL:      server.URL,
		ClientID:     "client-id",
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("exchange device token: %v", err)
	}
	if tokens == nil || tokens.AccessToken != "access" {
		t.Fatalf("unexpected tokens: %#v", tokens)
	}
}

func asOAuthError(err error, target **OAuthError) bool {
	oauthErr, ok := err.(*OAuthError)
	if !ok {
		return false
	}
	*target = oauthErr
	return true
}
