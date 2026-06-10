package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const maxPasswordBytes = 72

type passwordSetupRequest struct {
	Password       string `json:"password"`
	BootstrapToken string `json:"bootstrapToken"`
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type passwordRemoveRequest struct {
	Password string `json:"password"`
}

func (h Handler) SetupPassword(w http.ResponseWriter, r *http.Request) {
	if h.authDisabled {
		writeError(w, http.StatusConflict, "password_management_disabled", "Password management is disabled while dashboard auth is bypassed")
		return
	}
	var payload passwordSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	settings, err := h.repo.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if settings.PasswordHash.Valid {
		writeError(w, http.StatusConflict, "password_already_configured", "Password is already configured")
		return
	}
	password := strings.TrimSpace(payload.Password)
	if err := validatePassword(password); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to hash password")
		return
	}
	ok, err := h.repo.TrySetPasswordHash(r.Context(), string(hash))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "password_already_configured", "Password is already configured")
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

func (h Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if err := h.requirePasswordManagementSession(w, r); err != nil {
		return
	}
	var payload passwordChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	newPassword := strings.TrimSpace(payload.NewPassword)
	if err := validatePassword(newPassword); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	if err := h.verifyPassword(r.Context(), payload.CurrentPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid password")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to hash password")
		return
	}
	if err := h.repo.SetPasswordHash(r.Context(), string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

func (h Handler) RemovePassword(w http.ResponseWriter, r *http.Request) {
	if err := h.requirePasswordManagementSession(w, r); err != nil {
		return
	}
	var payload passwordRemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	if err := h.verifyPassword(r.Context(), payload.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid password")
		return
	}
	if err := h.repo.ClearPasswordAndTOTP(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	_ = h.sessions.Destroy(r.Context())
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

func (h Handler) verifyPassword(ctx context.Context, password string) error {
	settings, err := h.repo.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.PasswordHash.Valid {
		return errPasswordNotConfigured
	}
	if err := bcrypt.CompareHashAndPassword([]byte(settings.PasswordHash.String), []byte(password)); err != nil {
		return errInvalidPassword
	}
	return nil
}

func (h Handler) requirePasswordManagementSession(w http.ResponseWriter, r *http.Request) error {
	if h.authDisabled {
		writeError(w, http.StatusConflict, "password_management_disabled", "Password management is disabled while dashboard auth is bypassed")
		return errPasswordManagementDisabled
	}
	settings, err := h.repo.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return err
	}
	if !settings.PasswordHash.Valid {
		writeError(w, http.StatusUnauthorized, "authentication_required", "Password-authenticated session is required")
		return errPasswordSessionRequired
	}
	if !h.sessions.GetBool(r.Context(), sessionAuthenticatedKey) {
		writeError(w, http.StatusUnauthorized, "authentication_required", "Password-authenticated session is required")
		return errPasswordSessionRequired
	}
	return nil
}

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < 8 {
		return errPasswordTooShort
	}
	if len([]byte(password)) > maxPasswordBytes {
		return errPasswordTooLong
	}
	return nil
}
