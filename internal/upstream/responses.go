package upstream

import (
	"context"
	"net/http"
	"strings"
)

// Transport selects how responses are streamed upstream.
type Transport string

const (
	TransportHTTP      Transport = "http"
	TransportWebSocket Transport = "websocket"
	TransportAuto      Transport = "auto"
)

// StreamOptions configures upstream response streaming.
type StreamOptions struct {
	BaseURL         string
	Payload         map[string]any
	InboundHeaders  http.Header
	AccessToken     string
	AccountID       string
	Transport       Transport
	PrefersWebSocket bool
	Client          *http.Client
}

// OpenResponseStream streams upstream Codex responses as SSE event blocks.
func OpenResponseStream(ctx context.Context, opts StreamOptions) (<-chan string, <-chan error) {
	transport := resolveTransport(opts)
	if transport == TransportWebSocket {
		return StreamResponsesWebSocket(ctx, opts.BaseURL, opts.Payload, opts.InboundHeaders, opts.AccessToken, opts.AccountID)
	}
	return StreamResponses(ctx, opts.Client, opts.BaseURL, opts.Payload, opts.InboundHeaders, opts.AccessToken, opts.AccountID)
}

func resolveTransport(opts StreamOptions) Transport {
	switch strings.ToLower(string(opts.Transport)) {
	case "websocket":
		return TransportWebSocket
	case "http":
		return TransportHTTP
	default:
		if opts.PrefersWebSocket {
			return TransportWebSocket
		}
		return TransportHTTP
	}
}
