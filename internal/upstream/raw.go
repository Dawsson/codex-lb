package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type RawResponse struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

func ForwardRaw(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	method string,
	path string,
	query url.Values,
	payload []byte,
	inboundHeaders http.Header,
	accessToken string,
	accountID string,
) (RawResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	targetURL := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		targetURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(payload))
	if err != nil {
		return RawResponse{}, fmt.Errorf("build upstream raw request: %w", err)
	}
	for key, values := range BuildUpstreamHeaders(inboundHeaders, accessToken, accountID) {
		req.Header[key] = values
	}
	if contentType := inboundHeaders.Get("Content-Type"); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if len(payload) == 0 {
		req.Header.Del("Content-Type")
	}
	accept := inboundHeaders.Get("Accept")
	if accept == "" {
		accept = "*/*"
	}
	req.Header.Set("Accept", accept)

	resp, err := client.Do(req)
	if err != nil {
		return RawResponse{}, fmt.Errorf("upstream raw request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return RawResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone()}, fmt.Errorf("read upstream raw response: %w", err)
	}
	return RawResponse{StatusCode: resp.StatusCode, Body: body, Header: resp.Header.Clone()}, nil
}
