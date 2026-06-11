package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionServiceReportsUpdateAvailable(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.20.0"}`))
	}))
	defer server.Close()

	service := NewVersionService("1.19.0", server.Client())
	service.apiURL = server.URL
	service.now = func() time.Time { return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) }

	status := service.Status(context.Background())
	if status.CurrentVersion != "1.19.0" {
		t.Fatalf("unexpected current version %q", status.CurrentVersion)
	}
	if status.LatestVersion == nil || *status.LatestVersion != "1.20.0" {
		t.Fatalf("unexpected latest version %#v", status.LatestVersion)
	}
	if !status.UpdateAvailable {
		t.Fatal("expected update available")
	}
	if status.Source == nil || *status.Source != "github" {
		t.Fatalf("unexpected source %#v", status.Source)
	}
	_ = service.Status(context.Background())
	if calls != 1 {
		t.Fatalf("expected cached second call, got %d upstream calls", calls)
	}
}

func TestVersionServiceFailureStillReturnsCurrentVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	service := NewVersionService("1.19.0", server.Client())
	service.apiURL = server.URL

	status := service.Status(context.Background())
	if status.CurrentVersion != "1.19.0" {
		t.Fatalf("unexpected current version %q", status.CurrentVersion)
	}
	if status.LatestVersion != nil {
		t.Fatalf("expected nil latest version, got %#v", status.LatestVersion)
	}
	if status.UpdateAvailable {
		t.Fatal("expected no update on failure")
	}
	if status.Source != nil {
		t.Fatalf("expected nil source, got %#v", status.Source)
	}
}

func TestVersionComparisonPrerelease(t *testing.T) {
	if !isNewerVersion("1.20.0", "1.20.0-beta.1") {
		t.Fatal("stable release should be newer than prerelease")
	}
	if !isNewerVersion("1.20.0-beta.2", "1.20.0-beta.1") {
		t.Fatal("newer prerelease should be newer")
	}
	if isNewerVersion("1.20.0-beta.1", "1.20.0") {
		t.Fatal("prerelease should not be newer than stable release")
	}
}

func TestCurrentVersionReadsPyProjectInDev(t *testing.T) {
	previous, hadPrevious := os.LookupEnv("CODEX_LB_VERSION")
	t.Setenv("CODEX_LB_VERSION", "")
	if hadPrevious {
		t.Cleanup(func() { _ = os.Setenv("CODEX_LB_VERSION", previous) })
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nversion = \"9.8.7\"\n"), 0o600); err != nil {
		t.Fatalf("write pyproject: %v", err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	if got := CurrentVersion(); got != "9.8.7" {
		t.Fatalf("expected pyproject version, got %q", got)
	}
}
