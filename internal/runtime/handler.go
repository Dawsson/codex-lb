package runtime

import (
	"encoding/json"
	"net/http"
	"time"
)

type VersionResponse struct {
	CurrentVersion  string  `json:"currentVersion"`
	LatestVersion   *string `json:"latestVersion"`
	UpdateAvailable bool    `json:"updateAvailable"`
	CheckedAt       string  `json:"checkedAt"`
	Source          *string `json:"source"`
	ReleaseURL      string  `json:"releaseUrl"`
}

func Version(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(VersionResponse{
		CurrentVersion:  "go-dev",
		LatestVersion:   nil,
		UpdateAvailable: false,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		Source:          nil,
		ReleaseURL:      "https://github.com/Soju06/codex-lb/releases",
	})
}
