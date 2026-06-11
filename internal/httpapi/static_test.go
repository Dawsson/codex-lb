package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMountDashboardSPAServesStaticFile(t *testing.T) {
	distDir := t.TempDir()
	mustWriteFile(t, filepath.Join(distDir, "index.html"), "<html>index</html>")
	mustWriteFile(t, filepath.Join(distDir, "assets", "app.js"), "console.log('ok')")

	router := chi.NewRouter()
	mountDashboardSPA(router, distDir)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if recorder.Body.String() != "console.log('ok')" {
		t.Fatalf("unexpected body %q", recorder.Body.String())
	}
}

func TestMountDashboardSPAFallsBackToIndexForDashboardRoute(t *testing.T) {
	distDir := t.TempDir()
	mustWriteFile(t, filepath.Join(distDir, "index.html"), "<html>index</html>")

	router := chi.NewRouter()
	mountDashboardSPA(router, distDir)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/accounts", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if recorder.Body.String() != "<html>index</html>" {
		t.Fatalf("unexpected body %q", recorder.Body.String())
	}
}

func TestMountDashboardSPADoesNotMaskAPI404(t *testing.T) {
	distDir := t.TempDir()
	mustWriteFile(t, filepath.Join(distDir, "index.html"), "<html>index</html>")

	router := chi.NewRouter()
	mountDashboardSPA(router, distDir)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/not-found", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() == "<html>index</html>" {
		t.Fatalf("api 404 was masked by index fallback")
	}
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
