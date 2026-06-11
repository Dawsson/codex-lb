package audit

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
)

func LogRequest(repo *Repository, r *http.Request, action string, details map[string]any) {
	if repo == nil || action == "" {
		return
	}
	rawDetails, _ := json.Marshal(details)
	actorIP := sql.NullString{}
	if r != nil && r.RemoteAddr != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil && host != "" {
			actorIP = sql.NullString{String: host, Valid: true}
		} else {
			actorIP = sql.NullString{String: r.RemoteAddr, Valid: true}
		}
	}
	requestID := sql.NullString{}
	if r != nil {
		if value := r.Header.Get("X-Request-Id"); value != "" {
			requestID = sql.NullString{String: value, Valid: true}
		}
	}
	_, _ = repo.Insert(r.Context(), Entry{
		Action:    action,
		ActorIP:   actorIP,
		Details:   sql.NullString{String: string(rawDetails), Valid: len(rawDetails) > 0},
		RequestID: requestID,
	})
}
