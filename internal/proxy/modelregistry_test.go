package proxy

import "testing"

func TestGetModelsWithFallbackReturnsBootstrap(t *testing.T) {
	registry := NewModelRegistry(0)
	models := registry.GetModelsWithFallback()
	if len(models) == 0 {
		t.Fatalf("expected bootstrap models")
	}
	if _, ok := models["gpt-5.5"]; !ok {
		t.Fatalf("expected gpt-5.5 in bootstrap models, got %v", models)
	}
}

func TestPrefersWebsocketsBootstrap(t *testing.T) {
	registry := NewModelRegistry(0)
	if !registry.PrefersWebsockets("gpt-5.5") {
		t.Fatalf("expected gpt-5.5 to prefer websockets")
	}
	if !registry.PrefersWebsockets("gpt-5.3-codex-spark") {
		t.Fatalf("expected gpt-5.3-codex-spark to prefer websockets")
	}
	// Unknown model matching the gpt-5.5 wildcard pattern.
	if !registry.PrefersWebsockets("gpt-5.5-preview") {
		t.Fatalf("expected gpt-5.5-preview to match websocket-preferred pattern")
	}
	if registry.PrefersWebsockets("gpt-4o") {
		t.Fatalf("did not expect gpt-4o to prefer websockets")
	}
	if registry.PrefersWebsockets("") {
		t.Fatalf("did not expect empty slug to prefer websockets")
	}
}

func TestIsPublicModel(t *testing.T) {
	model := UpstreamModel{Slug: "gpt-5.5"}
	if !IsPublicModel(model, nil) {
		t.Fatalf("expected nil allowlist to permit all models")
	}
	if IsPublicModel(model, map[string]struct{}{"other": {}}) {
		t.Fatalf("expected model not in allowlist to be excluded")
	}
	if !IsPublicModel(model, map[string]struct{}{"gpt-5.5": {}}) {
		t.Fatalf("expected model in allowlist to be included")
	}
}

func TestFnmatchCase(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"gpt-5.5", "gpt-5.5", true},
		{"gpt-5.5-preview", "gpt-5.5-*", true},
		{"gpt-5.5", "gpt-5.5-*", false},
		{"gpt-5.4-mini", "gpt-5.4", false},
		{"gpt-5.4", "gpt-5.4-*", false},
	}
	for _, tc := range cases {
		if got := fnmatchCase(tc.name, tc.pattern); got != tc.want {
			t.Fatalf("fnmatchCase(%q, %q) = %v, want %v", tc.name, tc.pattern, got, tc.want)
		}
	}
}
