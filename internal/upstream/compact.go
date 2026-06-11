package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func CompactResponses(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	payload map[string]any,
	inboundHeaders http.Header,
	accessToken string,
	accountID string,
) (map[string]any, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal compact payload: %w", err)
	}
	url := strings.TrimRight(baseURL, "/") + "/codex/responses/compact"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build compact request: %w", err)
	}
	for key, values := range BuildUpstreamHeaders(inboundHeaders, accessToken, accountID) {
		req.Header[key] = values
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream compact request failed: %w", err)
	}
	defer resp.Body.Close()
	payloadText, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read compact response: %w", err)
	}
	var decoded map[string]any
	if len(payloadText) > 0 {
		if err := json.Unmarshal(payloadText, &decoded); err != nil {
			return nil, fmt.Errorf("decode compact response: %w", err)
		}
	}
	if resp.StatusCode >= 400 {
		return decoded, fmt.Errorf("upstream compact status %d", resp.StatusCode)
	}
	if decoded == nil {
		return nil, fmt.Errorf("empty compact response")
	}
	return decoded, nil
}
