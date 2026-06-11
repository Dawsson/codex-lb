package proxy

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	SSEKeepaliveFrame  = ": keepalive\n\n"
	CodexKeepaliveFrame = "event: codex.keepalive\ndata: {\"type\":\"codex.keepalive\"}\n\n"
)

var responseStreamTerminalEventTypes = map[string]struct{}{
	"response.completed":  {},
	"response.failed":     {},
	"response.incomplete": {},
}

// FormatSSEEvent ports format_sse_event.
func FormatSSEEvent(payload map[string]any) string {
	data, _ := json.Marshal(payload)
	if eventType, ok := payload["type"].(string); ok && eventType != "" {
		return "event: " + eventType + "\ndata: " + string(data) + "\n\n"
	}
	return "data: " + string(data) + "\n\n"
}

// FormatSSEData ports format_sse_data.
func FormatSSEData(payload map[string]any) string {
	data, _ := json.Marshal(payload)
	return "data: " + string(data) + "\n\n"
}

// ParseSSEDataJSON ports parse_sse_data_json.
func ParseSSEDataJSON(eventBlock string) map[string]any {
	data := ExtractSSEData(eventBlock)
	if data == "" || data == "[DONE]" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return nil
	}
	return payload
}

// ExtractSSEData ports extract_sse_data.
func ExtractSSEData(eventBlock string) string {
	lines := extractSSEDataLines(eventBlock)
	if len(lines) == 0 {
		return ""
	}
	data := strings.Join(lines, "\n")
	if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
		return ""
	}
	return data
}

func extractSSEDataLines(eventBlock string) []string {
	var dataLines []string
	for _, rawLine := range strings.Split(eventBlock, "\n") {
		if rawLine == "" || strings.HasPrefix(rawLine, ":") {
			continue
		}
		field, value := parseSSEField(rawLine)
		if field == "data" {
			dataLines = append(dataLines, value)
		}
	}
	return dataLines
}

func parseSSEField(line string) (string, string) {
	idx := strings.Index(line, ":")
	if idx == -1 {
		return line, ""
	}
	field := line[:idx]
	value := line[idx+1:]
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value
}

// IsTerminalResponseEvent reports whether an SSE payload ends the stream.
func IsTerminalResponseEvent(payload map[string]any) bool {
	eventType, _ := payload["type"].(string)
	_, ok := responseStreamTerminalEventTypes[eventType]
	return ok
}

// InjectSSEKeepalives wraps an event channel with periodic keepalive frames.
func InjectSSEKeepalives(events <-chan string, interval time.Duration, keepaliveFrame string) <-chan string {
	out := make(chan string, 32)
	if interval <= 0 {
		go func() {
			defer close(out)
			for event := range events {
				out <- event
			}
		}()
		return out
	}
	if keepaliveFrame == "" {
		keepaliveFrame = SSEKeepaliveFrame
	}
	go func() {
		defer close(out)
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					return
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				out <- event
				if payload := ParseSSEDataJSON(event); payload != nil {
					eventType, _ := payload["type"].(string)
					if eventType == "error" || IsTerminalResponseEvent(payload) {
						return
					}
				}
				timer.Reset(interval)
			case <-timer.C:
				out <- keepaliveFrame
				timer.Reset(interval)
			}
		}
	}()
	return out
}
