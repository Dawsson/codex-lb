package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

func TranscribeAudio(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	audio []byte,
	filename string,
	contentType string,
	prompt string,
	inboundHeaders http.Header,
	accessToken string,
	accountID string,
) (map[string]any, int, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if filename == "" {
		filename = "audio.wav"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, 0, fmt.Errorf("create transcription file field: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return nil, 0, fmt.Errorf("write transcription file field: %w", err)
	}
	if prompt != "" {
		if err := writer.WriteField("prompt", prompt); err != nil {
			return nil, 0, fmt.Errorf("write transcription prompt field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, 0, fmt.Errorf("close transcription multipart writer: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/transcribe"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, 0, fmt.Errorf("build transcription request: %w", err)
	}
	for key, values := range BuildUpstreamHeaders(inboundHeaders, accessToken, accountID) {
		req.Header[key] = values
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("upstream transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	payloadText, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read transcription response: %w", err)
	}
	var decoded map[string]any
	if len(payloadText) > 0 {
		if err := json.Unmarshal(payloadText, &decoded); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("decode transcription response: %w", err)
		}
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		return decoded, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return decoded, resp.StatusCode, nil
}
