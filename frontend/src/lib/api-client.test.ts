import { describe, expect, it } from "vitest";

import { ApiKeyListSchema } from "@/features/api-keys/schemas";
import { ApiKeyTrendsResponseSchema } from "@/features/apis/schemas";

describe("dashboard API response schemas", () => {
  it("accepts api key list arrays returned by GET /api/api-keys/", () => {
    const payload = [
      {
        id: "key-1",
        name: "test",
        keyPrefix: "sk-clb-example",
        allowedModels: null,
        applyToCodexModel: false,
        enforcedModel: null,
        enforcedReasoningEffort: null,
        enforcedServiceTier: null,
        trafficClass: "foreground",
        expiresAt: null,
        isActive: true,
        accountAssignmentScopeEnabled: false,
        assignedAccountIds: [],
        createdAt: "2026-06-10T21:18:09.947373Z",
        lastUsedAt: null,
        limits: [],
        usageSummary: null,
        pooledRemainingPercentPrimary: null,
        pooledRemainingPercentSecondary: null,
        pooledCapacityCreditsPrimary: 0,
      },
    ];

    expect(ApiKeyListSchema.safeParse(payload).success).toBe(true);
  });

  it("accepts api key trend arrays with Z-suffixed timestamps", () => {
    const payload = {
      keyId: "key-1",
      cost: [{ t: "2026-06-03T22:00:00Z", v: 0 }],
      tokens: [{ t: "2026-06-03T22:00:00Z", v: 12 }],
    };

    expect(ApiKeyTrendsResponseSchema.safeParse(payload).success).toBe(true);
  });
});
