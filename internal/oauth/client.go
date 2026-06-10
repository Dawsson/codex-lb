package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthError ports app.core.clients.oauth.OAuthError.
type OAuthError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *OAuthError) Error() string {
	return e.Message
}

// OAuthTokens ports app.core.clients.oauth.OAuthTokens.
type OAuthTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
}

// DeviceCode ports app.core.clients.oauth.DeviceCode.
type DeviceCode struct {
	VerificationURL  string
	UserCode         string
	DeviceAuthID     string
	IntervalSeconds  int
	ExpiresInSeconds int
}

// oauthTokenPayload mirrors app.core.auth.models.OAuthTokenPayload.
type oauthTokenPayload struct {
	AccessToken       *string         `json:"access_token"`
	RefreshToken      *string         `json:"refresh_token"`
	IDToken           *string         `json:"id_token"`
	AuthorizationCode *string         `json:"authorization_code"`
	CodeVerifier      *string         `json:"code_verifier"`
	Error             json.RawMessage `json:"error"`
	ErrorDescription  *string         `json:"error_description"`
	Message           *string         `json:"message"`
	ErrorCode         *string         `json:"error_code"`
	Code              *string         `json:"code"`
	Status            *string         `json:"status"`
}

// deviceCodePayload mirrors app.core.auth.models.DeviceCodePayload.
type deviceCodePayload struct {
	DeviceAuthID *string `json:"device_auth_id"`
	UserCode     *string `json:"user_code"`
	UserCodeAlt  *string `json:"usercode"`
	Interval     *int    `json:"interval"`
	ExpiresIn    *int    `json:"expires_in"`
	ExpiresAt    *string `json:"expires_at"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// pkceChallenge ports app.core.clients.oauth.pkce_challenge.
func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// generatePKCEPair ports app.core.clients.oauth.generate_pkce_pair.
func generatePKCEPair() (verifier string, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	return verifier, pkceChallenge(verifier), nil
}

func ensureOfflineAccess(scope string) string {
	for _, part := range strings.Fields(scope) {
		if part == "offline_access" {
			return scope
		}
	}
	return scope + " offline_access"
}

// authorizationURLParams configures buildAuthorizationURL.
type authorizationURLParams struct {
	State         string
	CodeChallenge string
	BaseURL       string
	ClientID      string
	Originator    string
	RedirectURI   string
	Scope         string
}

// buildAuthorizationURL ports app.core.clients.oauth.build_authorization_url.
func buildAuthorizationURL(p authorizationURLParams) string {
	authBase := strings.TrimSuffix(p.BaseURL, "/")
	scope := ensureOfflineAccess(p.Scope)
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", p.ClientID)
	values.Set("redirect_uri", p.RedirectURI)
	values.Set("scope", scope)
	values.Set("code_challenge", p.CodeChallenge)
	values.Set("code_challenge_method", "S256")
	values.Set("state", p.State)
	values.Set("id_token_add_organizations", "true")
	values.Set("codex_cli_simplified_flow", "true")
	values.Set("originator", p.Originator)
	return fmt.Sprintf("%s/oauth/authorize?%s", authBase, values.Encode())
}

// exchangeAuthorizationCodeParams configures exchangeAuthorizationCode.
type exchangeAuthorizationCodeParams struct {
	Code         string
	CodeVerifier string
	RedirectURI  string
	BaseURL      string
	ClientID     string
	Timeout      time.Duration
}

// exchangeAuthorizationCode ports the direct-egress branch of
// app.core.clients.oauth.exchange_authorization_code.
func exchangeAuthorizationCode(ctx context.Context, client *http.Client, p exchangeAuthorizationCodeParams) (OAuthTokens, error) {
	authBase := strings.TrimSuffix(p.BaseURL, "/")
	tokenURL := authBase + "/oauth/token"

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", p.ClientID)
	form.Set("code", p.Code)
	form.Set("code_verifier", p.CodeVerifier)
	form.Set("redirect_uri", p.RedirectURI)

	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthTokens{}, &OAuthError{Code: "transport_error", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return OAuthTokens{}, &OAuthError{Code: "transport_error", Message: err.Error()}
	}
	defer resp.Body.Close()

	payload, err := decodeOAuthTokenPayload(resp.Body)
	if err != nil {
		return OAuthTokens{}, &OAuthError{Code: "invalid_response", Message: "OAuth response invalid"}
	}
	if resp.StatusCode >= 400 {
		return OAuthTokens{}, oauthErrorFromPayload(payload, resp.StatusCode)
	}
	return parseTokens(payload)
}

// requestDeviceCodeParams configures requestDeviceCode.
type requestDeviceCodeParams struct {
	BaseURL  string
	ClientID string
	Timeout  time.Duration
}

// requestDeviceCode ports the direct-egress branch of
// app.core.clients.oauth.request_device_code.
func requestDeviceCode(ctx context.Context, client *http.Client, p requestDeviceCodeParams) (DeviceCode, error) {
	authBase := strings.TrimSuffix(p.BaseURL, "/")
	deviceURL := authBase + "/api/accounts/deviceauth/usercode"

	body, err := json.Marshal(map[string]string{"client_id": p.ClientID})
	if err != nil {
		return DeviceCode{}, &OAuthError{Code: "transport_error", Message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceURL, strings.NewReader(string(body)))
	if err != nil {
		return DeviceCode{}, &OAuthError{Code: "transport_error", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return DeviceCode{}, &OAuthError{Code: "transport_error", Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusNotFound {
			return DeviceCode{}, &OAuthError{
				Code:       "device_auth_unavailable",
				Message:    "Device code login is not enabled for this Codex server. Use the browser login or verify the server URL.",
				StatusCode: resp.StatusCode,
			}
		}
		return DeviceCode{}, &OAuthError{
			Code:       "device_auth_failed",
			Message:    fmt.Sprintf("Device code request failed with status %d", resp.StatusCode),
			StatusCode: resp.StatusCode,
		}
	}

	var payload deviceCodePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DeviceCode{}, &OAuthError{Code: "invalid_response", Message: "Device auth response invalid"}
	}

	userCode := deref(payload.UserCode)
	if userCode == "" {
		userCode = deref(payload.UserCodeAlt)
	}
	deviceAuthID := deref(payload.DeviceAuthID)

	interval := 0
	if payload.Interval != nil {
		interval = *payload.Interval
	}
	expiresIn := 0
	if payload.ExpiresIn != nil {
		expiresIn = *payload.ExpiresIn
	}
	if expiresIn <= 0 {
		if seconds := expiresInSeconds(payload.ExpiresAt); seconds != nil {
			expiresIn = *seconds
		} else {
			expiresIn = 900
		}
	}

	if userCode == "" || deviceAuthID == "" {
		return DeviceCode{}, &OAuthError{Code: "invalid_response", Message: "Device auth response missing fields"}
	}

	return DeviceCode{
		VerificationURL:  authBase + "/codex/device",
		UserCode:         userCode,
		DeviceAuthID:     deviceAuthID,
		IntervalSeconds:  interval,
		ExpiresInSeconds: expiresIn,
	}, nil
}

// exchangeDeviceTokenParams configures exchangeDeviceToken.
type exchangeDeviceTokenParams struct {
	DeviceAuthID string
	UserCode     string
	BaseURL      string
	ClientID     string
	Timeout      time.Duration
}

// exchangeDeviceToken ports the direct-egress branch of
// app.core.clients.oauth.exchange_device_token. A nil result with nil error
// means the authorization is still pending.
func exchangeDeviceToken(ctx context.Context, client *http.Client, p exchangeDeviceTokenParams) (*OAuthTokens, error) {
	authBase := strings.TrimSuffix(p.BaseURL, "/")
	tokenURL := authBase + "/api/accounts/deviceauth/token"

	body, err := json.Marshal(map[string]string{
		"device_auth_id": p.DeviceAuthID,
		"user_code":      p.UserCode,
	})
	if err != nil {
		return nil, &OAuthError{Code: "transport_error", Message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, &OAuthError{Code: "transport_error", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, &OAuthError{Code: "transport_error", Message: err.Error()}
	}
	defer resp.Body.Close()

	payload, err := decodeOAuthTokenPayload(resp.Body)
	if err != nil {
		return nil, &OAuthError{Code: "invalid_response", Message: "Device auth response invalid"}
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		if isPendingError(payload) {
			return nil, nil
		}
		return nil, oauthErrorFromPayload(payload, resp.StatusCode)
	}
	if isPendingError(payload) {
		return nil, nil
	}

	if authCode := deref(payload.AuthorizationCode); authCode != "" {
		codeVerifier := deref(payload.CodeVerifier)
		if codeVerifier == "" {
			return nil, &OAuthError{Code: "invalid_response", Message: "Device auth response missing code verifier"}
		}
		tokens, err := exchangeAuthorizationCode(ctx, client, exchangeAuthorizationCodeParams{
			Code:         authCode,
			CodeVerifier: codeVerifier,
			RedirectURI:  authBase + "/deviceauth/callback",
			BaseURL:      p.BaseURL,
			ClientID:     p.ClientID,
			Timeout:      p.Timeout,
		})
		if err != nil {
			return nil, err
		}
		return &tokens, nil
	}

	tokens, err := parseTokens(payload)
	if err != nil {
		return nil, err
	}
	return &tokens, nil
}

func decodeOAuthTokenPayload(body io.Reader) (oauthTokenPayload, error) {
	var payload oauthTokenPayload
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return oauthTokenPayload{}, err
	}
	return payload, nil
}

func parseTokens(payload oauthTokenPayload) (OAuthTokens, error) {
	access := deref(payload.AccessToken)
	refresh := deref(payload.RefreshToken)
	id := deref(payload.IDToken)
	if access == "" || refresh == "" || id == "" {
		return OAuthTokens{}, &OAuthError{Code: "invalid_response", Message: "OAuth response missing tokens"}
	}
	return OAuthTokens{AccessToken: access, RefreshToken: refresh, IDToken: id}, nil
}

func oauthErrorFromPayload(payload oauthTokenPayload, statusCode int) *OAuthError {
	code := extractErrorCode(payload)
	if code == "" {
		code = fmt.Sprintf("http_%d", statusCode)
	}
	message := extractErrorMessage(payload)
	if message == "" {
		message = fmt.Sprintf("OAuth request failed (%d)", statusCode)
	}
	return &OAuthError{Code: code, Message: message, StatusCode: statusCode}
}

// errorObject represents the optional dict-shaped "error" field.
type errorObject struct {
	Code             string `json:"code"`
	Error            string `json:"error"`
	Message          string `json:"message"`
	ErrorDescription string `json:"error_description"`
}

func extractErrorCode(payload oauthTokenPayload) string {
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		var obj errorObject
		if err := json.Unmarshal(payload.Error, &obj); err == nil {
			if obj.Code != "" {
				return obj.Code
			}
			if obj.Error != "" {
				return obj.Error
			}
			return ""
		}
		var str string
		if err := json.Unmarshal(payload.Error, &str); err == nil {
			return str
		}
	}
	if payload.ErrorCode != nil {
		return *payload.ErrorCode
	}
	return deref(payload.Code)
}

func extractErrorMessage(payload oauthTokenPayload) string {
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		var obj errorObject
		if err := json.Unmarshal(payload.Error, &obj); err == nil {
			if obj.Message != "" {
				return obj.Message
			}
			if obj.ErrorDescription != "" {
				return obj.ErrorDescription
			}
			return ""
		}
		var str string
		if err := json.Unmarshal(payload.Error, &str); err == nil {
			if payload.ErrorDescription != nil {
				return *payload.ErrorDescription
			}
			return str
		}
	}
	return deref(payload.Message)
}

func isPendingError(payload oauthTokenPayload) bool {
	switch extractErrorCode(payload) {
	case "authorization_pending", "slow_down":
		return true
	}
	if payload.Status != nil {
		switch strings.ToLower(*payload.Status) {
		case "pending", "authorization_pending":
			return true
		}
	}
	return false
}

func expiresInSeconds(expiresAt *string) *int {
	if expiresAt == nil || *expiresAt == "" {
		return nil
	}
	value := strings.ReplaceAll(*expiresAt, "Z", "+00:00")
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	delta := int(time.Until(parsed).Seconds())
	if delta <= 0 {
		return nil
	}
	return &delta
}
