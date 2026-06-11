package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordLoginLeavesTOTPRequiredWhenEnabled(t *testing.T) {
	handler, repo, _, sessions := newAuthTestHandler(t, "")
	configurePasswordAndTOTP(t, repo, handler, true)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"password123"}`))
	rec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Login)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Authenticated || !body.TOTPRequiredOnLogin || body.PasswordSessionActive {
		t.Fatalf("expected password-only session with TOTP pending, got %#v", body)
	}
}

func TestTOTPVerifyCompletesLogin(t *testing.T) {
	handler, repo, _, sessions := newAuthTestHandler(t, "")
	secret := configurePasswordAndTOTP(t, repo, handler, true)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"password123"}`))
	loginRec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Login)).ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginRec.Code, loginRec.Body.String())
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	verifyReq := httptest.NewRequest(http.MethodPost, "/api/auth/totp/verify", strings.NewReader(`{"code":"`+code+`"}`))
	for _, cookie := range loginRec.Result().Cookies() {
		verifyReq.AddCookie(cookie)
	}
	verifyRec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.VerifyTOTP)).ServeHTTP(verifyRec, verifyReq)

	if verifyRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", verifyRec.Code, verifyRec.Body.String())
	}
	var body SessionResponse
	if err := json.NewDecoder(verifyRec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Authenticated || body.TOTPRequiredOnLogin || !body.PasswordSessionActive {
		t.Fatalf("expected fully authenticated session, got %#v", body)
	}
}

func TestRequireSessionRejectsPasswordOnlySessionWhenTOTPRequired(t *testing.T) {
	handler, repo, _, sessions := newAuthTestHandler(t, "")
	configurePasswordAndTOTP(t, repo, handler, true)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"password123"}`))
	loginRec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Login)).ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginRec.Code, loginRec.Body.String())
	}

	protected := sessions.LoadAndSave(handler.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error.Code != "totp_required" {
		t.Fatalf("expected totp_required, got %#v", payload)
	}
}

func configurePasswordAndTOTP(t *testing.T, repo Repository, handler Handler, required bool) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := repo.SetPasswordHash(context.Background(), string(hash)); err != nil {
		t.Fatalf("set password: %v", err)
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: totpAccount})
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	encrypted, err := handler.encryptor.Encrypt(key.Secret())
	if err != nil {
		t.Fatalf("encrypt totp: %v", err)
	}
	if err := repo.SetTOTPSecret(context.Background(), encrypted); err != nil {
		t.Fatalf("set totp secret: %v", err)
	}
	if _, err := repo.store.DB().ExecContext(context.Background(), `UPDATE dashboard_settings SET totp_required_on_login = ?`, boolToInt(required)); err != nil {
		t.Fatalf("set totp required: %v", err)
	}
	return key.Secret()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
