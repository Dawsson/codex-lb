package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/skip2/go-qrcode"
)

const totpIssuer = "codex-lb"
const totpAccount = "dashboard"

type totpSetupConfirmRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

type totpVerifyRequest struct {
	Code string `json:"code"`
}

type totpSetupStartResponse struct {
	Secret       string `json:"secret"`
	OtpauthURI   string `json:"otpauthUri"`
	QRSvgDataURI string `json:"qrSvgDataUri"`
}

func (h Handler) StartTOTPSetup(w http.ResponseWriter, r *http.Request) {
	if err := h.requirePasswordManagementSession(w, r); err != nil {
		return
	}
	settings, err := h.repo.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if settings.TOTPConfigured {
		writeError(w, http.StatusConflict, "totp_already_configured", "TOTP is already configured")
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: totpAccount,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to generate TOTP secret")
		return
	}
	writeJSON(w, http.StatusOK, totpSetupStartResponse{
		Secret:       key.Secret(),
		OtpauthURI:   key.URL(),
		QRSvgDataURI: qrDataURI(key.URL()),
	})
}

func (h Handler) ConfirmTOTPSetup(w http.ResponseWriter, r *http.Request) {
	if err := h.requirePasswordManagementSession(w, r); err != nil {
		return
	}
	var payload totpSetupConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	settings, err := h.repo.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if settings.TOTPConfigured {
		writeError(w, http.StatusConflict, "totp_already_configured", "TOTP is already configured")
		return
	}
	if payload.Secret == "" || !totp.Validate(payload.Code, payload.Secret) {
		writeError(w, http.StatusBadRequest, "invalid_totp_code", "Invalid TOTP code")
		return
	}
	if h.encryptor == nil {
		writeError(w, http.StatusInternalServerError, "server_error", "Encryption is not configured")
		return
	}
	encrypted, err := h.encryptor.Encrypt(payload.Secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if err := h.repo.SetTOTPSecret(r.Context(), encrypted); err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

func (h Handler) VerifyTOTP(w http.ResponseWriter, r *http.Request) {
	if h.authDisabled {
		response, err := h.sessionResponse(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	var payload totpVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	secret, err := h.decryptTOTPSecret(r)
	if err != nil || secret == "" {
		writeError(w, http.StatusBadRequest, "totp_not_configured", "TOTP is not configured")
		return
	}
	if !h.isPasswordVerified(r) {
		writeError(w, http.StatusUnauthorized, "authentication_required", "Password-authenticated session is required")
		return
	}
	if !totp.Validate(payload.Code, secret) {
		writeError(w, http.StatusUnauthorized, "invalid_totp_code", "Invalid TOTP code")
		return
	}
	h.markTOTPVerified(r)
	response, err := h.sessionResponse(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h Handler) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	if err := h.requirePasswordManagementSession(w, r); err != nil {
		return
	}
	var payload totpVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	secret, err := h.decryptTOTPSecret(r)
	if err != nil || secret == "" {
		writeError(w, http.StatusBadRequest, "totp_not_configured", "TOTP is not configured")
		return
	}
	if !totp.Validate(payload.Code, secret) {
		writeError(w, http.StatusUnauthorized, "invalid_totp_code", "Invalid TOTP code")
		return
	}
	if err := h.repo.ClearTOTP(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

func (h Handler) decryptTOTPSecret(r *http.Request) (string, error) {
	encrypted, err := h.repo.TOTPSecretEncrypted(r.Context())
	if err != nil || len(encrypted) == 0 {
		return "", err
	}
	if h.encryptor == nil {
		return "", err
	}
	return h.encryptor.Decrypt(encrypted)
}

func qrDataURI(payload string) string {
	png, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		return "data:image/svg+xml;base64,PHN2Zy8+"
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}
