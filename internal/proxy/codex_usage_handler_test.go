package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/settings"
)

func TestCodexUsageMapsAPIKeyCreditLimits(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	apiKeysRepo := apikeys.NewRepository(store)
	now := time.Now().UTC()
	key, plainKey, err := apiKeysRepo.Create(context.Background(), apikeys.CreateInput{
		Name:         "usage-key",
		TrafficClass: "foreground",
		Limits: []apikeys.LimitInput{
			{LimitType: "credits", LimitWindow: "5h", MaxValue: 100},
			{LimitType: "credits", LimitWindow: "7d", MaxValue: 700},
			{LimitType: "credits", LimitWindow: "monthly", MaxValue: 3000},
			{LimitType: "requests", LimitWindow: "5h", MaxValue: 9},
		},
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if _, err := store.DB().ExecContext(context.Background(), `
		UPDATE api_key_limits
		   SET current_value = CASE limit_window
			 WHEN '5h' THEN 25
			 WHEN '7d' THEN 350
			 WHEN 'monthly' THEN 1000
			 ELSE current_value
		   END,
		       reset_at = ?
		 WHERE api_key_id = ?
	`, now.Add(time.Hour).Format("2006-01-02 15:04:05"), key.ID); err != nil {
		t.Fatalf("update limits: %v", err)
	}

	handler := NewCodexUsageHandler(apiKeysRepo, settings.NewRepository(store, encryptor))
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage", nil)
	req.RemoteAddr = "203.0.113.10:5000"
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload codexUsagePayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.PlanType != "api_key" {
		t.Fatalf("expected api_key plan, got %#v", payload)
	}
	if payload.RateLimit == nil || payload.RateLimit.PrimaryWindow == nil || payload.RateLimit.SecondaryWindow == nil || payload.RateLimit.MonthlyWindow == nil {
		t.Fatalf("expected all windows, got %#v", payload.RateLimit)
	}
	if payload.RateLimit.PrimaryWindow.UsedPercent != 25 || payload.RateLimit.SecondaryWindow.UsedPercent != 50 || payload.RateLimit.MonthlyWindow.UsedPercent != 33 {
		t.Fatalf("unexpected used percentages: %#v", payload.RateLimit)
	}
	if payload.Credits == nil || payload.Credits.Balance == nil || *payload.Credits.Balance != "2000" || !payload.Credits.HasCredits {
		t.Fatalf("expected monthly credit balance, got %#v", payload.Credits)
	}
	if len(payload.AdditionalRateLimits) != 0 {
		t.Fatalf("expected empty additional rate limits, got %#v", payload.AdditionalRateLimits)
	}
}

func TestCodexUsageNoCreditLimitsReturnsNullSnapshots(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	apiKeysRepo := apikeys.NewRepository(store)
	_, plainKey, err := apiKeysRepo.Create(context.Background(), apikeys.CreateInput{
		Name:         "usage-key",
		TrafficClass: "foreground",
		Limits: []apikeys.LimitInput{
			{LimitType: "requests", LimitWindow: "5h", MaxValue: 9},
		},
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	handler := NewCodexUsageHandler(apiKeysRepo, settings.NewRepository(store, encryptor))
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage", nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded["planType"] != "api_key" {
		t.Fatalf("expected api_key plan, got %#v", decoded)
	}
	if decoded["rateLimit"] != nil || decoded["credits"] != nil {
		t.Fatalf("expected null snapshots, got %#v", decoded)
	}
	if _, ok := decoded["additionalRateLimits"].([]any); !ok {
		t.Fatalf("expected additionalRateLimits array, got %#v", decoded["additionalRateLimits"])
	}
}
