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

func ForwardJSON(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	method string,
	path string,
	payload []byte,
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
	url := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("build upstream json request: %w", err)
	}
	for key, values := range BuildUpstreamHeaders(inboundHeaders, accessToken, accountID) {
		req.Header[key] = values
	}
	req.Header.Set("Accept", "application/json")
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("upstream json request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read upstream json response: %w", err)
	}
	var decoded map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("decode upstream json response: %w", err)
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
