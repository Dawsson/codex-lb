package proxy

import "testing"

func TestResolveModelAlias(t *testing.T) {
	cases := map[string]string{
		"gpt-5.5":                  "gpt-5.5",
		"gpt-5.1-codex-high":       "gpt-5.1-codex",
		"gpt-5.4-mini-low":         "gpt-5.4-mini",
		"gpt-5.3-codex-fast":       "gpt-5.3-codex",
		"gpt-5-codex-extra-high":   "gpt-5-codex",
		"gpt-5.2-codex-reasoning":  "gpt-5.2-codex",
		"unknown-model":            "unknown-model",
		"gpt-5.1-codex-bogustoken": "gpt-5.1-codex-bogustoken",
	}
	for input, want := range cases {
		if got := ResolveModelAlias(input); got != want {
			t.Fatalf("ResolveModelAlias(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveModelAliasParts(t *testing.T) {
	canonical, effort, serviceTier, ok := resolveModelAliasParts("gpt-5.1-codex-high-fast")
	if !ok {
		t.Fatalf("expected alias match")
	}
	if canonical != "gpt-5.1-codex" {
		t.Fatalf("unexpected canonical model: %q", canonical)
	}
	if effort == nil || *effort != "high" {
		t.Fatalf("unexpected effort: %v", effort)
	}
	if serviceTier == nil || *serviceTier != "priority" {
		t.Fatalf("unexpected service tier: %v", serviceTier)
	}
}

func TestCanonicalModelSlug(t *testing.T) {
	if got := CanonicalModelSlug("gpt-5.1-codex-high"); got != "gpt-5.1-codex" {
		t.Fatalf("unexpected canonical slug: %q", got)
	}
	if got := CanonicalModelSlug("custom-model"); got != "custom-model" {
		t.Fatalf("unexpected canonical slug: %q", got)
	}
}
