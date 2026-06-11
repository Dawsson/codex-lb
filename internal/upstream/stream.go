package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultBaseURL = "https://chatgpt.com/backend-api"

// StreamResponses posts a Responses payload to the upstream Codex endpoint and
// yields raw SSE event blocks (including blank-line separators).
func StreamResponses(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	payload map[string]any,
	inboundHeaders http.Header,
	accessToken string,
	accountID string,
) (<-chan string, <-chan error) {
	events := make(chan string, 32)
	errs := make(chan error, 1)

	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	go func() {
		defer close(events)
		defer close(errs)

		body, err := json.Marshal(payload)
		if err != nil {
			errs <- fmt.Errorf("marshal upstream payload: %w", err)
			return
		}

		url := strings.TrimRight(baseURL, "/") + "/codex/responses"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			errs <- fmt.Errorf("build upstream request: %w", err)
			return
		}
		for key, values := range BuildUpstreamHeaders(inboundHeaders, accessToken, accountID) {
			req.Header[key] = values
		}

		resp, err := client.Do(req)
		if err != nil {
			errs <- fmt.Errorf("upstream request failed: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			payloadText, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			errs <- fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(payloadText)))
			return
		}

		reader := bufio.NewReader(resp.Body)
		var block strings.Builder
		for {
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				block.WriteString(line)
				if strings.TrimSpace(line) == "" {
					events <- block.String()
					block.Reset()
				}
			}
			if readErr != nil {
				if block.Len() > 0 {
					events <- block.String()
				}
				if readErr != io.EOF {
					errs <- fmt.Errorf("read upstream stream: %w", readErr)
				}
				return
			}
		}
	}()

	return events, errs
}

// BuildUpstreamHeaders ports _build_upstream_headers.
func BuildUpstreamHeaders(inbound http.Header, accessToken, accountID string) http.Header {
	out := make(http.Header, len(inbound)+4)
	for key, values := range inbound {
		out[key] = append([]string(nil), values...)
	}
	if out.Get("X-Request-Id") == "" && out.Get("Request-Id") == "" {
		out.Set("X-Request-Id", fmt.Sprintf("req_%d", time.Now().UnixNano()))
	}
	out.Set("Authorization", "Bearer "+accessToken)
	out.Set("Accept", "text/event-stream")
	out.Set("Content-Type", "application/json")
	if accountID != "" {
		out.Set("Chatgpt-Account-Id", accountID)
	}
	return out
}
