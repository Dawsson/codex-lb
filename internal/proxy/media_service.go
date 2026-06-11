package proxy

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/upstream"
)

const transcriptionModel = "gpt-4o-transcribe"

func (s *Service) ProxyBackendJSON(ctx context.Context, r *http.Request, apiKey *ApiKeyData, upstreamPath, model, fileID string, body []byte) (map[string]any, int, *OpenAIErrorEnvelope, error) {
	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, model, map[string]any{"model": model})
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, appErr.StatusCode, &envelope, nil
		}
		return nil, 0, nil, err
	}
	preferredAccountID := ""
	if fileID != "" {
		preferredAccountID = s.resolvePinnedFileAccount(fileID)
	}
	selectParams := streamSelectParams("", dashboardSettings, apiKey)
	selectParams.LeaseKind = AccountLeaseKindResponseCreate
	if preferredAccountID != "" {
		selectParams.PreferredAccountID = &preferredAccountID
	}
	selection, err := s.loadBalancer.SelectAccount(ctx, selectParams)
	if err != nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		return nil, 0, nil, err
	}
	defer s.loadBalancer.ReleaseLease(selection.Lease)
	if selection.ErrorCode != "" || selection.Account == nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		envelope := OpenAIError(selectionErrorCode(selection.ErrorCode), message, "server_error")
		return nil, http.StatusServiceUnavailable, &envelope, nil
	}

	started := time.Now()
	result, upstreamStatus, err := upstream.ForwardJSON(ctx, s.upstreamClient, s.upstreamBaseURL, r.Method, upstreamPath, body, r.Header.Clone(), selection.Account.AccessToken, selection.Account.ID)
	latency := time.Since(started).Milliseconds()
	status := "success"
	var envelope *OpenAIErrorEnvelope
	if err != nil {
		status = "error"
		envelopeValue := OpenAIError("upstream_error", err.Error(), "server_error")
		envelope = &envelopeValue
	}
	s.logRequest(ctx, logRequestParams{
		requestID: r.Header.Get("X-Request-Id"),
		model:     model,
		account:   selection.Account,
		apiKey:    apiKey,
		status:    status,
		envelope:  envelope,
		latencyMS: latency,
		userAgent: r.UserAgent(),
	})
	s.releaseAPIKeyReservation(ctx, reservation)
	if err != nil {
		if upstreamStatus == 0 {
			upstreamStatus = http.StatusBadGateway
		}
		return nil, upstreamStatus, envelope, nil
	}
	if createdFileID, _ := result["file_id"].(string); createdFileID != "" {
		s.pinFileAccount(createdFileID, selection.Account.ID)
	}
	if fileID != "" {
		s.pinFileAccount(fileID, selection.Account.ID)
	}
	return result, http.StatusOK, nil, nil
}

func (s *Service) ProxyCodexControlRaw(ctx context.Context, r *http.Request, apiKey *ApiKeyData, upstreamPath string, body []byte) (upstream.RawResponse, *OpenAIErrorEnvelope, error) {
	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return upstream.RawResponse{}, nil, err
	}
	selectParams := streamSelectParams("", dashboardSettings, apiKey)
	selectParams.LeaseKind = AccountLeaseKindResponseCreate
	selection, err := s.loadBalancer.SelectAccount(ctx, selectParams)
	if err != nil {
		return upstream.RawResponse{}, nil, err
	}
	defer s.loadBalancer.ReleaseLease(selection.Lease)
	if selection.ErrorCode != "" || selection.Account == nil {
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		envelope := OpenAIError(selectionErrorCode(selection.ErrorCode), message, "server_error")
		return upstream.RawResponse{StatusCode: http.StatusServiceUnavailable}, &envelope, nil
	}
	started := time.Now()
	response, err := upstream.ForwardRaw(ctx, s.upstreamClient, s.upstreamBaseURL, r.Method, codexControlUpstreamPath(upstreamPath), r.URL.Query(), body, r.Header.Clone(), selection.Account.AccessToken, selection.Account.ID)
	latency := time.Since(started).Milliseconds()
	status := "success"
	var envelope *OpenAIErrorEnvelope
	if err != nil || response.StatusCode >= 400 {
		status = "error"
		envelopeValue := OpenAIError("upstream_error", "Request to upstream failed", "server_error")
		if err != nil {
			envelopeValue = OpenAIError("upstream_error", err.Error(), "server_error")
		}
		envelope = &envelopeValue
	}
	s.logRequest(ctx, logRequestParams{
		requestID: r.Header.Get("X-Request-Id"),
		model:     "codex-control",
		account:   selection.Account,
		apiKey:    apiKey,
		status:    status,
		envelope:  envelope,
		latencyMS: latency,
		userAgent: r.UserAgent(),
	})
	if err != nil {
		return upstream.RawResponse{StatusCode: http.StatusBadGateway}, envelope, nil
	}
	if response.StatusCode >= 400 {
		return response, envelope, nil
	}
	return response, nil, nil
}

func codexControlUpstreamPath(path string) string {
	normalized := strings.Trim(path, "/")
	if strings.HasPrefix(normalized, "wham/") {
		return normalized
	}
	return "codex/" + normalized
}

func (s *Service) TranscribeAudio(ctx context.Context, r *http.Request, apiKey *ApiKeyData, audio []byte, filename, contentType, prompt string) (map[string]any, int, *OpenAIErrorEnvelope, error) {
	if err := ValidateModelAccess(apiKey, transcriptionModel); err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, appErr.StatusCode, &envelope, nil
		}
		return nil, 0, nil, err
	}
	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, transcriptionModel, map[string]any{"model": transcriptionModel})
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, appErr.StatusCode, &envelope, nil
		}
		return nil, 0, nil, err
	}
	selectParams := streamSelectParams("", dashboardSettings, apiKey)
	selectParams.LeaseKind = AccountLeaseKindResponseCreate
	selection, err := s.loadBalancer.SelectAccount(ctx, selectParams)
	if err != nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		return nil, 0, nil, err
	}
	defer s.loadBalancer.ReleaseLease(selection.Lease)
	if selection.ErrorCode != "" || selection.Account == nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		envelope := OpenAIError(selectionErrorCode(selection.ErrorCode), message, "server_error")
		return nil, http.StatusServiceUnavailable, &envelope, nil
	}

	started := time.Now()
	result, upstreamStatus, err := upstream.TranscribeAudio(ctx, s.upstreamClient, s.upstreamBaseURL, audio, filename, contentType, prompt, r.Header.Clone(), selection.Account.AccessToken, selection.Account.ID)
	latency := time.Since(started).Milliseconds()
	status := "success"
	var envelope *OpenAIErrorEnvelope
	if err != nil {
		status = "error"
		envelopeValue := OpenAIError("upstream_error", err.Error(), "server_error")
		envelope = &envelopeValue
	}
	s.logRequest(ctx, logRequestParams{
		requestID: r.Header.Get("X-Request-Id"),
		model:     transcriptionModel,
		account:   selection.Account,
		apiKey:    apiKey,
		status:    status,
		envelope:  envelope,
		latencyMS: latency,
		userAgent: r.UserAgent(),
	})
	s.releaseAPIKeyReservation(ctx, reservation)
	if err != nil {
		if upstreamStatus == 0 {
			upstreamStatus = http.StatusBadGateway
		}
		return nil, upstreamStatus, envelope, nil
	}
	return result, http.StatusOK, nil, nil
}

func (s *Service) resolvePinnedFileAccount(fileID string) string {
	s.filePinsMu.Lock()
	defer s.filePinsMu.Unlock()
	s.evictExpiredFilePinsLocked(time.Now())
	if pin, ok := s.filePins[fileID]; ok {
		return pin.AccountID
	}
	return ""
}
