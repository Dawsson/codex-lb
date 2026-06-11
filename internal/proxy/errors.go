package proxy

import (
	"net/http"

	"github.com/soju06/codex-lb/internal/httputil"
)

// OpenAIErrorDetail mirrors app.core.errors.OpenAIErrorDetail.
type OpenAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Param   string `json:"param,omitempty"`
}

// OpenAIErrorEnvelope mirrors app.core.errors.OpenAIErrorEnvelope.
type OpenAIErrorEnvelope struct {
	Error OpenAIErrorDetail `json:"error"`
}

// OpenAIError ports app.core.errors.openai_error.
func OpenAIError(code, message, errorType string) OpenAIErrorEnvelope {
	if errorType == "" {
		errorType = "server_error"
	}
	return OpenAIErrorEnvelope{Error: OpenAIErrorDetail{Message: message, Type: errorType, Code: code}}
}

// AppError mirrors app.core.exceptions.AppError and its subclasses, carrying
// the HTTP status/code/type used to render an OpenAI-style error envelope.
type AppError struct {
	StatusCode int
	Code       string
	ErrorType  string
	Message    string
	Param      string
}

func (e *AppError) Error() string {
	return e.Message
}

// NewProxyAuthError ports app.core.exceptions.ProxyAuthError.
func NewProxyAuthError(message string) *AppError {
	return &AppError{StatusCode: http.StatusUnauthorized, Code: "invalid_api_key", ErrorType: "authentication_error", Message: message}
}

// NewProxyModelNotAllowedError ports app.core.exceptions.ProxyModelNotAllowed.
func NewProxyModelNotAllowedError(message string) *AppError {
	return &AppError{StatusCode: http.StatusForbidden, Code: "model_not_allowed", ErrorType: "permission_error", Message: message}
}

// NewProxyRateLimitError ports app.core.exceptions.ProxyRateLimitError.
func NewProxyRateLimitError(message string) *AppError {
	return &AppError{StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded", ErrorType: "rate_limit_error", Message: message}
}

func NewClientPayloadError(message, param, code, errorType string) *AppError {
	if code == "" {
		code = "invalid_request_error"
	}
	if errorType == "" {
		errorType = "invalid_request_error"
	}
	return &AppError{StatusCode: http.StatusBadRequest, Code: code, ErrorType: errorType, Message: message, Param: param}
}

// WriteError writes an AppError as an OpenAI-style JSON error envelope.
func WriteError(w http.ResponseWriter, err *AppError) {
	envelope := OpenAIError(err.Code, err.Message, err.ErrorType)
	envelope.Error.Param = err.Param
	httputil.WriteJSON(w, err.StatusCode, envelope)
}
