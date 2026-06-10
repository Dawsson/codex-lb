package oauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
)

const accountIdentityConflictMessage = "Multiple accounts match the authenticated identity. Remove duplicate accounts and retry OAuth."

// StartResponse mirrors app.modules.oauth.schemas.OauthStartResponse.
type StartResponse struct {
	FlowID           *string `json:"flowId,omitempty"`
	Method           string  `json:"method"`
	AuthorizationURL *string `json:"authorizationUrl,omitempty"`
	CallbackURL      *string `json:"callbackUrl,omitempty"`
	VerificationURL  *string `json:"verificationUrl,omitempty"`
	UserCode         *string `json:"userCode,omitempty"`
	DeviceAuthID     *string `json:"deviceAuthId,omitempty"`
	IntervalSeconds  *int    `json:"intervalSeconds,omitempty"`
	ExpiresInSeconds *int    `json:"expiresInSeconds,omitempty"`
}

// StatusResponse mirrors app.modules.oauth.schemas.OauthStatusResponse.
type StatusResponse struct {
	Status       string  `json:"status"`
	ErrorMessage *string `json:"errorMessage,omitempty"`
}

// CompleteResponse mirrors app.modules.oauth.schemas.OauthCompleteResponse.
type CompleteResponse struct {
	Status string `json:"status"`
}

// ManualCallbackResponse mirrors
// app.modules.oauth.schemas.ManualCallbackResponse.
type ManualCallbackResponse struct {
	Status       string  `json:"status"`
	ErrorMessage *string `json:"errorMessage,omitempty"`
}

// Service ports app.modules.oauth.service.OauthService. The asyncio-based
// flow orchestration is replaced with a sync.Mutex-protected stateStore and
// goroutines for the device-poll loop and the local OAuth callback server.
type Service struct {
	cfg          config.Config
	accountsRepo accounts.Repository
	encryptor    *crypto.Encryptor
	httpClient   *http.Client
	store        *stateStore
	logger       *slog.Logger

	invalidateCache func()

	callbackMu     sync.Mutex
	callbackServer *http.Server
}

func NewService(cfg config.Config, accountsRepo accounts.Repository, encryptor *crypto.Encryptor, invalidateCache func(), logger *slog.Logger) *Service {
	return &Service{
		cfg:             cfg,
		accountsRepo:    accountsRepo,
		encryptor:       encryptor,
		httpClient:      &http.Client{},
		store:           newStateStore(),
		logger:          logger,
		invalidateCache: invalidateCache,
	}
}

func (s *Service) timeout() time.Duration {
	return time.Duration(s.cfg.OAuthTimeoutSeconds * float64(time.Second))
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// StartOAuth ports OauthService.start_oauth.
func (s *Service) StartOAuth(ctx context.Context, forceMethod string) (StartResponse, error) {
	forceMethod = strings.ToLower(strings.TrimSpace(forceMethod))

	if forceMethod == "" {
		existing, err := s.accountsRepo.List(ctx)
		if err != nil {
			return StartResponse{}, err
		}
		if len(existing) > 0 {
			s.store.mu.Lock()
			s.store.flows = make(map[string]*flow)
			s.store.stateTokenIdx = make(map[string]string)
			s.store.latest = flow{Status: statusSuccess}
			s.store.mu.Unlock()
			return StartResponse{Method: methodBrowser}, nil
		}
	}

	if forceMethod == methodDevice {
		return s.startDeviceFlow(ctx)
	}

	resp, err := s.startBrowserFlow(ctx)
	if err != nil {
		var netErr *net.OpError
		if errors.As(err, &netErr) {
			return s.startDeviceFlow(ctx)
		}
		return StartResponse{}, err
	}
	return resp, nil
}

// startBrowserFlow ports OauthService._start_browser_flow.
func (s *Service) startBrowserFlow(ctx context.Context) (StartResponse, error) {
	flowID, err := randomToken(12)
	if err != nil {
		return StartResponse{}, err
	}
	verifier, challenge, err := generatePKCEPair()
	if err != nil {
		return StartResponse{}, err
	}
	stateToken, err := randomToken(16)
	if err != nil {
		return StartResponse{}, err
	}

	authorizationURL := buildAuthorizationURL(authorizationURLParams{
		State:         stateToken,
		CodeChallenge: challenge,
		BaseURL:       s.cfg.OAuthAuthBaseURL,
		ClientID:      s.cfg.OAuthClientID,
		Originator:    s.cfg.OAuthOriginator,
		RedirectURI:   s.cfg.OAuthRedirectURI,
		Scope:         s.cfg.OAuthScope,
	})

	f := &flow{
		FlowID:       flowID,
		Status:       statusPending,
		Method:       methodBrowser,
		StateToken:   stateToken,
		CodeVerifier: verifier,
		ExpiresAt:    time.Now().Add(pendingBrowserFlowTTL),
	}

	s.store.mu.Lock()
	s.store.rememberFlowLocked(f)
	s.store.mu.Unlock()

	if err := s.ensureCallbackServer(); err != nil {
		return StartResponse{}, err
	}

	callbackURL := s.cfg.OAuthRedirectURI
	return StartResponse{
		FlowID:           &flowID,
		Method:           methodBrowser,
		AuthorizationURL: &authorizationURL,
		CallbackURL:      &callbackURL,
	}, nil
}

// startDeviceFlow ports OauthService._start_device_flow.
func (s *Service) startDeviceFlow(ctx context.Context) (StartResponse, error) {
	flowID, err := randomToken(12)
	if err != nil {
		return StartResponse{}, err
	}

	device, err := requestDeviceCode(ctx, s.httpClient, requestDeviceCodeParams{
		BaseURL:  s.cfg.OAuthAuthBaseURL,
		ClientID: s.cfg.OAuthClientID,
		Timeout:  s.timeout(),
	})
	if err != nil {
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) {
			s.setError(oauthErr.Message, "")
		}
		return StartResponse{}, err
	}

	f := &flow{
		FlowID:          flowID,
		Status:          statusPending,
		Method:          methodDevice,
		DeviceAuthID:    device.DeviceAuthID,
		UserCode:        device.UserCode,
		IntervalSeconds: device.IntervalSeconds,
		ExpiresAt:       time.Now().Add(time.Duration(device.ExpiresInSeconds) * time.Second),
	}

	s.store.mu.Lock()
	s.store.removePendingDeviceFlowsLocked()
	s.store.rememberFlowLocked(f)
	s.ensureDevicePollLocked(f)
	s.store.mu.Unlock()

	return StartResponse{
		FlowID:           &flowID,
		Method:           methodDevice,
		VerificationURL:  &device.VerificationURL,
		UserCode:         &device.UserCode,
		DeviceAuthID:     &device.DeviceAuthID,
		IntervalSeconds:  &device.IntervalSeconds,
		ExpiresInSeconds: &device.ExpiresInSeconds,
	}, nil
}

// OAuthStatus ports OauthService.oauth_status.
func (s *Service) OAuthStatus(flowID string) StatusResponse {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	f := s.store.getFlowLocked(flowID)
	var status, errorMessage string
	if f == nil {
		if flowID == "" {
			status = s.store.latest.Status
			errorMessage = s.store.latest.ErrorMessage
		} else {
			status = statusPending
		}
	} else {
		status = f.Status
		errorMessage = f.ErrorMessage
	}
	if status == statusIdle {
		status = statusPending
	}
	resp := StatusResponse{Status: status}
	if errorMessage != "" {
		resp.ErrorMessage = &errorMessage
	}
	return resp
}

// CompleteOAuth ports OauthService.complete_oauth.
func (s *Service) CompleteOAuth(flowID, deviceAuthID, userCode string) CompleteResponse {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	f := s.store.getFlowLocked(flowID)
	var state flow
	if f == nil {
		if flowID == "" {
			state = s.store.latest
		} else {
			state = flow{Status: statusPending}
		}
	} else {
		if deviceAuthID != "" {
			f.DeviceAuthID = deviceAuthID
		}
		if userCode != "" {
			f.UserCode = userCode
		}
		s.store.setLatestFlowLocked(f)
		state = *f
	}

	if state.Status == statusSuccess {
		return CompleteResponse{Status: statusSuccess}
	}
	if state.Method != methodDevice {
		return CompleteResponse{Status: statusPending}
	}
	if !s.ensureDevicePollLocked(f) {
		message := "Device code flow is not initialized."
		if f != nil {
			s.store.setFlowStatusLocked(f, statusError, message)
		}
		return CompleteResponse{Status: statusError}
	}
	return CompleteResponse{Status: statusPending}
}

// ensureDevicePollLocked ports OauthService._ensure_device_poll_task_locked.
// Caller must hold s.store.mu.
func (s *Service) ensureDevicePollLocked(f *flow) bool {
	if f == nil {
		return false
	}
	if f.isPollActive() {
		return true
	}
	if f.DeviceAuthID == "" || f.UserCode == "" || f.ExpiresAt.IsZero() {
		return false
	}

	pollCtx, cancel := context.WithCancel(context.Background())
	f.pollCancel = cancel
	f.pollDone = false
	go s.pollDeviceTokens(pollCtx, f.FlowID, f.DeviceAuthID, f.UserCode, f.IntervalSeconds, f.ExpiresAt)
	return true
}

// pollDeviceTokens ports OauthService._poll_device_tokens.
func (s *Service) pollDeviceTokens(ctx context.Context, flowID, deviceAuthID, userCode string, intervalSeconds int, expiresAt time.Time) {
	defer func() {
		s.store.mu.Lock()
		if f := s.store.getFlowLocked(flowID); f != nil {
			f.pollDone = true
			f.pollCancel = nil
			s.store.setLatestFlowLocked(f)
		}
		s.store.mu.Unlock()
	}()

	interval := time.Duration(max(intervalSeconds, 0)) * time.Second
	for time.Now().Before(expiresAt) {
		select {
		case <-ctx.Done():
			return
		default:
		}

		tokens, err := exchangeDeviceToken(ctx, s.httpClient, exchangeDeviceTokenParams{
			DeviceAuthID: deviceAuthID,
			UserCode:     userCode,
			BaseURL:      s.cfg.OAuthAuthBaseURL,
			ClientID:     s.cfg.OAuthClientID,
			Timeout:      s.timeout(),
		})
		if err != nil {
			var oauthErr *OAuthError
			if errors.As(err, &oauthErr) {
				s.setError(oauthErr.Message, flowID)
				return
			}
			s.setError(err.Error(), flowID)
			return
		}
		if tokens != nil {
			if err := s.persistTokens(ctx, *tokens); err != nil {
				if errors.Is(err, accounts.ErrAccountIdentityConflict) {
					s.setError(accountIdentityConflictMessage, flowID)
				} else {
					s.setError(err.Error(), flowID)
				}
				return
			}
			s.setSuccess(flowID)
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
	s.setError("Device code expired.", flowID)
}

// ManualCallback ports OauthService.manual_callback.
func (s *Service) ManualCallback(ctx context.Context, callbackURL, flowID string) ManualCallbackResponse {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		message := "Invalid OAuth callback: state mismatch or missing code."
		return ManualCallbackResponse{Status: statusError, ErrorMessage: &message}
	}
	query := parsed.Query()
	oauthError := query.Get("error")
	code := query.Get("code")
	state := query.Get("state")

	s.store.mu.Lock()
	f := s.store.getFlowByStateTokenLocked(state)
	verifier := ""
	targetFlowID := ""
	canUpdateError := false
	if f != nil {
		verifier = f.CodeVerifier
		targetFlowID = f.FlowID
		canUpdateError = true
	} else if flowID != "" {
		targetFlowID = flowID
		canUpdateError = true
	}
	if flowID != "" && (f == nil || f.FlowID != flowID) {
		f = nil
		verifier = ""
		targetFlowID = ""
		canUpdateError = false
	}
	if f != nil && f.Status == statusSuccess && state == f.StateToken {
		s.store.mu.Unlock()
		return ManualCallbackResponse{Status: statusSuccess}
	}
	s.store.mu.Unlock()

	if oauthError != "" {
		message := "OAuth error: " + oauthError
		if canUpdateError {
			s.setError(message, targetFlowID)
		}
		return ManualCallbackResponse{Status: statusError, ErrorMessage: &message}
	}

	if code == "" || state == "" || f == nil || verifier == "" {
		message := "Invalid OAuth callback: state mismatch or missing code."
		if canUpdateError {
			s.setError(message, targetFlowID)
		}
		return ManualCallbackResponse{Status: statusError, ErrorMessage: &message}
	}

	tokens, err := exchangeAuthorizationCode(ctx, s.httpClient, exchangeAuthorizationCodeParams{
		Code:         code,
		CodeVerifier: verifier,
		RedirectURI:  s.cfg.OAuthRedirectURI,
		BaseURL:      s.cfg.OAuthAuthBaseURL,
		ClientID:     s.cfg.OAuthClientID,
		Timeout:      s.timeout(),
	})
	if err != nil {
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) {
			s.setError(oauthErr.Message, f.FlowID)
			return ManualCallbackResponse{Status: statusError, ErrorMessage: &oauthErr.Message}
		}
		message := "An internal error occurred."
		s.setError(message, f.FlowID)
		return ManualCallbackResponse{Status: statusError, ErrorMessage: &message}
	}

	if err := s.persistTokens(ctx, tokens); err != nil {
		var message string
		if errors.Is(err, accounts.ErrAccountIdentityConflict) {
			message = accountIdentityConflictMessage
		} else {
			message = "An internal error occurred."
		}
		s.setError(message, f.FlowID)
		return ManualCallbackResponse{Status: statusError, ErrorMessage: &message}
	}

	s.setSuccess(f.FlowID)
	return ManualCallbackResponse{Status: statusSuccess}
}

// ensureCallbackServer starts the local OAuth callback server the first
// time it's needed. Per app.core.config.settings, the port (default 1455)
// must remain stable -- OpenAI's OAuth client allowlists it -- so once
// bound the server is kept running for the process lifetime rather than
// stopped/restarted between flows.
func (s *Service) ensureCallbackServer() error {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	if s.callbackServer != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", s.handleCallback)

	addr := fmt.Sprintf("%s:%d", s.cfg.OAuthCallbackHost, s.cfg.OAuthCallbackPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	server := &http.Server{Handler: mux}
	s.callbackServer = server
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("oauth callback server failed", "error", err)
		}
	}()
	return nil
}

// handleCallback ports OauthService._handle_callback.
func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	oauthError := query.Get("error")
	code := query.Get("code")
	state := query.Get("state")

	s.store.mu.Lock()
	f := s.store.getFlowByStateTokenLocked(state)
	verifier := ""
	flowID := ""
	if f != nil {
		verifier = f.CodeVerifier
		flowID = f.FlowID
	}
	s.store.mu.Unlock()

	if oauthError != "" {
		s.setError("OAuth error: "+oauthError, flowID)
		writeCallbackHTML(w, errorHTML("Authorization failed."))
		return
	}
	if code == "" || state == "" || f == nil || verifier == "" {
		s.setError("Invalid OAuth callback state.", flowID)
		writeCallbackHTML(w, errorHTML("Invalid OAuth callback."))
		return
	}

	tokens, err := exchangeAuthorizationCode(r.Context(), s.httpClient, exchangeAuthorizationCodeParams{
		Code:         code,
		CodeVerifier: verifier,
		RedirectURI:  s.cfg.OAuthRedirectURI,
		BaseURL:      s.cfg.OAuthAuthBaseURL,
		ClientID:     s.cfg.OAuthClientID,
		Timeout:      s.timeout(),
	})
	if err != nil {
		var oauthErr *OAuthError
		message := "An internal error occurred."
		if errors.As(err, &oauthErr) {
			message = oauthErr.Message
		}
		s.setError(message, flowID)
		writeCallbackHTML(w, errorHTML(message))
		return
	}

	if err := s.persistTokens(r.Context(), tokens); err != nil {
		message := "An internal error occurred."
		if errors.Is(err, accounts.ErrAccountIdentityConflict) {
			message = accountIdentityConflictMessage
		}
		s.setError(message, flowID)
		writeCallbackHTML(w, errorHTML(message))
		return
	}

	s.setSuccess(flowID)
	writeCallbackHTML(w, successHTML())
}

func writeCallbackHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func successHTML() string {
	return "<html><body><h1>Login complete</h1><p>Return to the dashboard.</p></body></html>"
}

func errorHTML(message string) string {
	return fmt.Sprintf("<html><body><h1>Login failed</h1><p>%s</p></body></html>", html.EscapeString(message))
}

// persistTokens ports OauthService._persist_tokens.
func (s *Service) persistTokens(ctx context.Context, tokens OAuthTokens) error {
	claims := extractIDTokenClaims(tokens.IDToken)

	rawAccountID := claims.ChatGPTAccountID
	planTypeRaw := claims.ChatGPTPlanType
	workspaceID := claims.WorkspaceID
	workspaceLabel := claims.WorkspaceLabel
	seatType := claims.SeatType
	if claims.Auth != nil {
		rawAccountID = firstNonEmpty(claims.Auth.ChatGPTAccountID, claims.ChatGPTAccountID)
		planTypeRaw = firstNonEmpty(claims.Auth.ChatGPTPlanType, claims.ChatGPTPlanType)
		workspaceID = firstNonEmpty(claims.Auth.WorkspaceID, claims.WorkspaceID)
		workspaceLabel = firstNonEmpty(claims.Auth.WorkspaceLabel, claims.WorkspaceLabel)
		seatType = firstNonEmpty(claims.Auth.SeatType, claims.SeatType)
	}

	email := claims.Email
	if email == "" {
		email = defaultEmail
	}
	workspaceID = cleanAccountIdentityPart(workspaceID)
	workspaceLabel = cleanAccountIdentityPart(workspaceLabel)
	seatType = normalizeSeatType(seatType)
	accountID := generateUniqueAccountID(rawAccountID, email, workspaceID)
	planType := coerceAccountPlanType(planTypeRaw, defaultPlan)

	accessEncrypted, err := s.encryptor.Encrypt(tokens.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	refreshEncrypted, err := s.encryptor.Encrypt(tokens.RefreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}
	idEncrypted, err := s.encryptor.Encrypt(tokens.IDToken)
	if err != nil {
		return fmt.Errorf("encrypt id token: %w", err)
	}

	account := accounts.OAuthAccount{
		ID:                    accountID,
		ChatGPTAccountID:      nullableString(rawAccountID),
		Email:                 email,
		WorkspaceID:           nullableString(workspaceID),
		WorkspaceLabel:        nullableString(workspaceLabel),
		SeatType:              nullableString(seatType),
		PlanType:              planType,
		AccessTokenEncrypted:  accessEncrypted,
		RefreshTokenEncrypted: refreshEncrypted,
		IDTokenEncrypted:      idEncrypted,
		LastRefresh:           time.Now().UTC().Format("2006-01-02 15:04:05"),
		Status:                "active",
	}

	if _, err := s.accountsRepo.UpsertOAuthAccount(ctx, account); err != nil {
		return err
	}

	if s.invalidateCache != nil {
		s.invalidateCache()
	}
	return nil
}

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

// setSuccess ports OauthService._set_success.
func (s *Service) setSuccess(flowID string) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	f := s.store.getFlowLocked(flowID)
	if flowID != "" && f == nil {
		return
	}
	if f == nil {
		s.store.latest.Status = statusSuccess
		s.store.latest.ErrorMessage = ""
		return
	}
	s.store.setFlowStatusLocked(f, statusSuccess, "")
}

// setError ports OauthService._set_error.
func (s *Service) setError(message, flowID string) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	if flowID == "" && s.store.latest.FlowID != "" {
		return
	}
	f := s.store.getFlowLocked(flowID)
	if flowID != "" && f == nil {
		return
	}
	if f == nil {
		s.store.latest.Status = statusError
		s.store.latest.ErrorMessage = message
		return
	}
	s.store.setFlowStatusLocked(f, statusError, message)
}
