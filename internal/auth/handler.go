package auth

import (
	"encoding/json"
	"net/http"
)

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

func Session(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SessionResponse{
		Authenticated:             true,
		PasswordRequired:          false,
		TOTPRequiredOnLogin:       false,
		TOTPConfigured:            false,
		BootstrapRequired:         false,
		BootstrapTokenConfigured:  false,
		AuthMode:                  "disabled",
		PasswordManagementEnabled: false,
		PasswordSessionActive:     true,
	})
}
