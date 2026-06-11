package proxy

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

type MediaHandler struct {
	service      *Service
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
}

func NewMediaHandler(service *Service, apiKeysRepo apikeys.Repository, settingsRepo settings.Repository) MediaHandler {
	return MediaHandler{service: service, apiKeysRepo: apiKeysRepo, settingsRepo: settingsRepo}
}

func (h MediaHandler) CreateFile(w http.ResponseWriter, r *http.Request) {
	h.serveBackendJSON(w, r, "files", "files-create", "")
}

func (h MediaHandler) FinalizeFile(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "fileID")
	if fileID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "fileID is required")
		return
	}
	h.serveBackendJSON(w, r, "files/"+fileID+"/uploaded", "files-finalize", fileID)
}

func (h MediaHandler) WhamJWKS(w http.ResponseWriter, r *http.Request) {
	h.serveBackendJSON(w, r, "wham/agent-identities/jwks", "codex-control", "")
}

func (h MediaHandler) CodexJWKS(w http.ResponseWriter, r *http.Request) {
	h.serveBackendJSON(w, r, "agent-identities/jwks", "codex-control", "")
}

func (h MediaHandler) BackendTranscribe(w http.ResponseWriter, r *http.Request) {
	h.serveTranscribe(w, r, false)
}

func (h MediaHandler) V1AudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	h.serveTranscribe(w, r, true)
}

func (h MediaHandler) V1ImagesVariations(w http.ResponseWriter, r *http.Request) {
	if _, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	envelope := OpenAIError("not_found", "/v1/images/variations is not supported by codex-lb. Use /v1/images/edits with an explicit prompt instead.", "invalid_request_error")
	httputil.WriteJSON(w, http.StatusNotFound, envelope)
}

func (h MediaHandler) V1ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		envelope := OpenAIError("invalid_json", "Invalid JSON body", "invalid_request_error")
		httputil.WriteJSON(w, http.StatusBadRequest, envelope)
		return
	}
	result, status, envelope, err := h.service.GenerateImage(r.Context(), r, apiKey, payload)
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

func (h MediaHandler) V1ImagesEdits(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid multipart form")
		return
	}
	body := map[string]any{
		"model":              r.FormValue("model"),
		"prompt":             r.FormValue("prompt"),
		"size":               formValueDefault(r, "size", "auto"),
		"quality":            formValueDefault(r, "quality", "auto"),
		"background":         formValueDefault(r, "background", "auto"),
		"output_format":      formValueDefault(r, "output_format", "png"),
		"moderation":         formValueDefault(r, "moderation", "auto"),
		"input_fidelity":     r.FormValue("input_fidelity"),
		"n":                  parseFormIntDefault(r, "n", 1),
		"output_compression": parseFormIntDefault(r, "output_compression", 100),
	}
	if stream := r.FormValue("stream"); stream == "true" || stream == "1" {
		body["stream"] = true
	}
	images, err := readImageParts(r, "image")
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	bracketImages, err := readImageParts(r, "image[]")
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	images = append(images, bracketImages...)
	var mask *imagePart
	if parts := r.MultipartForm.File["mask"]; len(parts) > 0 {
		part, err := readImagePart(parts[0])
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		mask = &part
	}
	result, status, envelope, err := h.service.EditImage(r.Context(), r, apiKey, body, images, mask)
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

func (h MediaHandler) serveTranscribe(w http.ResponseWriter, r *http.Request, requireModel bool) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid multipart form")
		return
	}
	if requireModel && r.FormValue("model") != transcriptionModel {
		envelope := OpenAIError("invalid_request_error", "Invalid transcription model. Supported value: gpt-4o-transcribe.", "invalid_request_error")
		httputil.WriteJSON(w, http.StatusBadRequest, envelope)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "file is required")
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(io.LimitReader(file, 128<<20))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	contentType := header.Header.Get("Content-Type")
	result, status, envelope, err := h.service.TranscribeAudio(r.Context(), r, apiKey, audio, header.Filename, contentType, r.FormValue("prompt"))
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

func (h MediaHandler) serveBackendJSON(w http.ResponseWriter, r *http.Request, upstreamPath, model, fileID string) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if body == nil {
		body = []byte("{}")
	}
	response, status, envelope, err := h.service.ProxyBackendJSON(r.Context(), r, apiKey, upstreamPath, model, fileID, body)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if envelope != nil {
		httputil.WriteJSON(w, status, envelope)
		return
	}
	httputil.WriteJSON(w, status, response)
}

func formValueDefault(r *http.Request, key, fallback string) string {
	if value := r.FormValue(key); value != "" {
		return value
	}
	return fallback
}

func parseFormIntDefault(r *http.Request, key string, fallback int64) int64 {
	value := r.FormValue(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func readImageParts(r *http.Request, key string) ([]imagePart, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	headers := r.MultipartForm.File[key]
	parts := make([]imagePart, 0, len(headers))
	for _, header := range headers {
		part, err := readImagePart(header)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func readImagePart(header *multipart.FileHeader) (imagePart, error) {
	file, err := header.Open()
	if err != nil {
		return imagePart{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		return imagePart{}, err
	}
	return imagePart{Data: data, ContentType: header.Header.Get("Content-Type")}, nil
}
