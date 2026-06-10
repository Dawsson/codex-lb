package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/crypto/bcrypt"
)

const sessionAuthenticatedKey = "authenticated"

type Handler struct {
	repo         Repository
	sessions     *scs.SessionManager
	authDisabled bool
}

type SessionResponse struct {
	Authenticated             bool   `json:"authenticated"`
	PasswordRequired          bool   `json:"passwordRequired"`
	TOTPRequiredOnLogin       bool   `json:"totpRequiredOnLogin"`
	TOTPConfigured            bool   `json:"totpConfigured"`
	BootstrapRequired         bool   `json:"bootstrapRequired"`
	BootstrapTokenConfigured  bool   `json:"bootstrapTokenConfigured"`
	AuthMode                  string `json:"authMode"`
	PasswordManagementEnabled bool   `json:"passwordManagementEnabled"`
	PasswordSessionActive     bool   `json:"passwordSessionActive"`
}

type loginRequest struct {
	Password string `json:"password"`
}

type statusResponse struct {
	Status string `json:"status"`
}

func NewHandler(repo Repository, sessions *scs.SessionManager, authDisabled bool) Handler {
	return Handler{repo: repo, sessions: sessions, authDisabled: authDisabled}
}

func (h Handler) Session(w http.ResponseWriter, r *http.Request) {
	response, err := h.sessionResponse(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h Handler) Login(w http.ResponseWriter, r *http.Request) {
	var payload loginRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	settings, err := h.repo.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if !settings.PasswordHash.Valid {
		writeError(w, http.StatusConflict, "password_not_configured", "Password login is not configured")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(settings.PasswordHash.String), []byte(payload.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid password")
		return
	}
	if err := h.sessions.RenewToken(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to renew session")
		return
	}
	h.sessions.Put(r.Context(), sessionAuthenticatedKey, true)
	response, err := h.sessionResponse(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.sessions.Destroy(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to destroy session")
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

func (h Handler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := h.repo.Settings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		if !h.authDisabled && settings.PasswordHash.Valid && !h.sessions.GetBool(r.Context(), sessionAuthenticatedKey) {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h Handler) sessionResponse(r *http.Request) (SessionResponse, error) {
	settings, err := h.repo.Settings(r.Context())
	if err != nil {
		return SessionResponse{}, err
	}
	passwordRequired := settings.PasswordHash.Valid
	authenticated := h.authDisabled || !passwordRequired || h.sessions.GetBool(r.Context(), sessionAuthenticatedKey)
	totpRequired := false
	if settings.TOTPRequiredOnLogin && !authenticated {
		return SessionResponse{}, errors.New("totp cannot be required before password authentication")
	}
	return SessionResponse{
		Authenticated:             authenticated,
		PasswordRequired:          passwordRequired,
		TOTPRequiredOnLogin:       totpRequired,
		TOTPConfigured:            settings.TOTPConfigured,
		BootstrapRequired:         false,
		BootstrapTokenConfigured:  false,
		AuthMode:                  authMode(h.authDisabled),
		PasswordManagementEnabled: !h.authDisabled,
		PasswordSessionActive:     authenticated && passwordRequired && !h.authDisabled,
	}, nil
}

func authMode(disabled bool) string {
	if disabled {
		return "disabled"
	}
	return "standard"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
