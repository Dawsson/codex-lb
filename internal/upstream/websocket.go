package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const defaultIdleTimeout = 120 * time.Second

// StreamResponsesWebSocket connects to the upstream Codex responses WebSocket,
// sends a response.create payload, and yields SSE-formatted event blocks.
func StreamResponsesWebSocket(
	ctx context.Context,
	baseURL string,
	payload map[string]any,
	inboundHeaders http.Header,
	accessToken string,
	accountID string,
) (<-chan string, <-chan error) {
	events := make(chan string, 32)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		if baseURL == "" {
			baseURL = DefaultBaseURL
		}
		httpURL := strings.TrimRight(baseURL, "/") + "/codex/responses"
		wsURL := toWebSocketURL(httpURL)

		headers := BuildUpstreamWebSocketHeaders(inboundHeaders, accessToken, accountID)
		dialer := websocket.Dialer{
			HandshakeTimeout: 30 * time.Second,
			Proxy:            http.ProxyFromEnvironment,
		}

		conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			errs <- fmt.Errorf("upstream websocket dial: %w", err)
			return
		}
		defer conn.Close()

		requestPayload := buildWebSocketResponseCreatePayload(payload)
		if err := conn.WriteJSON(requestPayload); err != nil {
			errs <- fmt.Errorf("upstream websocket send: %w", err)
			return
		}

		idleTimer := time.NewTimer(defaultIdleTimeout)
		defer idleTimer.Stop()

		for {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case <-idleTimer.C:
				errs <- fmt.Errorf("upstream websocket idle timeout")
				return
			default:
			}

			if err := conn.SetReadDeadline(time.Now().Add(defaultIdleTimeout)); err != nil {
				errs <- fmt.Errorf("set read deadline: %w", err)
				return
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return
				}
				errs <- fmt.Errorf("upstream websocket read: %w", err)
				return
			}
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(defaultIdleTimeout)

			var eventPayload map[string]any
			if err := json.Unmarshal(message, &eventPayload); err != nil {
				continue
			}
			block := formatSSEFromJSON(eventPayload)
			events <- block
			if isTerminalEvent(eventPayload) {
				return
			}
		}
	}()

	return events, errs
}

func BuildUpstreamWebSocketHeaders(inbound http.Header, accessToken, accountID string) http.Header {
	out := make(http.Header, len(inbound)+4)
	for key, values := range inbound {
		lower := strings.ToLower(key)
		if isHopByHopHeader(lower) {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	if out.Get("X-Request-Id") == "" && out.Get("Request-Id") == "" {
		out.Set("X-Request-Id", fmt.Sprintf("req_%d", time.Now().UnixNano()))
	}
	out.Set("Authorization", "Bearer "+accessToken)
	if accountID != "" {
		out.Set("Chatgpt-Account-Id", accountID)
	}
	return out
}

func buildWebSocketResponseCreatePayload(payload map[string]any) map[string]any {
	if payloadType, _ := payload["type"].(string); payloadType == "response.create" {
		return payload
	}
	out := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		switch key {
		case "stream", "stream_options":
			continue
		default:
			out[key] = value
		}
	}
	out["type"] = "response.create"
	return out
}

func toWebSocketURL(httpURL string) string {
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	if strings.HasPrefix(httpURL, "http://") {
		return "ws://" + strings.TrimPrefix(httpURL, "http://")
	}
	return httpURL
}

func formatSSEFromJSON(payload map[string]any) string {
	data, _ := json.Marshal(payload)
	if eventType, ok := payload["type"].(string); ok && eventType != "" {
		return "event: " + eventType + "\ndata: " + string(data) + "\n\n"
	}
	return "data: " + string(data) + "\n\n"
}

func isTerminalEvent(payload map[string]any) bool {
	eventType, _ := payload["type"].(string)
	switch eventType {
	case "response.completed", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}

func isHopByHopHeader(name string) bool {
	switch name {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailers", "transfer-encoding", "upgrade", "host":
		return true
	default:
		return false
	}
}
