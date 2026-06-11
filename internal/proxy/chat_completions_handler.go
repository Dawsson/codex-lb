package proxy

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

type ChatCompletionsHandler struct {
	service      *Service
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
}

func NewChatCompletionsHandler(service *Service, apiKeysRepo apikeys.Repository, settingsRepo settings.Repository) ChatCompletionsHandler {
	return ChatCompletionsHandler{
		service:      service,
		apiKeysRepo:  apiKeysRepo,
		settingsRepo: settingsRepo,
	}
}

func (h ChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		envelope := OpenAIError("invalid_json", "Invalid JSON body", "invalid_request_error")
		httputil.WriteJSON(w, http.StatusBadRequest, envelope)
		return
	}
	if err := ValidateChatCompletionsRequest(body); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if err := ValidateAndNormalizeChatTools(body); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if err := ApplyChatResponseFormat(body); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if err := EnforceStrictTextFormat(body); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	responsesShapedPayload := isResponsesShapedChatPayload(body)
	if responsesShapedPayload {
		if err := EnforceStrictFunctionToolsFormat(body["tools"], "tools[{index}].parameters", false); err != nil {
			if appErr, ok := err.(*AppError); ok {
				WriteError(w, appErr)
				return
			}
			httputil.WriteServerError(w, err)
			return
		}
	} else if err := EnforceStrictFunctionToolsFormat(body["tools"], "tools[{index}].function.parameters", true); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}

	if stream, ok := body["stream"].(bool); ok && stream {
		events, errs, err := h.service.StreamResponses(r.Context(), r, apiKey, body, StreamResponsesOptions{})
		if err != nil {
			if appErr, ok := err.(*AppError); ok {
				WriteError(w, appErr)
				return
			}
			httputil.WriteServerError(w, err)
			return
		}
		includeUsage := false
		if streamOptions, ok := body["stream_options"].(map[string]any); ok {
			if value, ok := streamOptions["include_usage"].(bool); ok {
				includeUsage = value
			}
		}
		model := EffectiveModelForAPIKey(apiKey, stringField(body, "model"))
		chatChunks := StreamChatChunks(events, model, includeUsage)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		for chunk := range chatChunks {
			_, _ = io.WriteString(w, chunk)
			flusher.Flush()
		}
		if err, ok := <-errs; ok && err != nil {
			return
		}
		return
	}

	result, envelope, status, err := h.service.CompleteChat(r.Context(), r, apiKey, body)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if envelope != nil {
		httputil.WriteJSON(w, status, envelope)
		return
	}
	httputil.WriteJSON(w, status, result)
}
