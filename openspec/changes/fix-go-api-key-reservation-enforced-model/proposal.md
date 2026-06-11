# Change: Fix Go API-key reservation enforced-model matching

## Why

Go proxy streaming paths can apply API-key policy that rewrites the requested model before upstream work, but some reservation call sites still pass the pre-policy model into API-key limit enforcement. Model-filtered API-key limits must be checked against the same effective model that will be sent upstream.

## What Changes

- Reserve API-key request usage on Go HTTP streaming Responses with the post-enforcement model.
- Reserve API-key request usage on Go websocket Responses with the post-enforcement model.
- Keep bridge reservation forwarding and quota-planner warm-now `apiKeyId` enforcement out of scope for this focused proxy-path parity patch.

## Impact

Authenticated proxy requests using an API key with an enforced model now consume and are blocked by model-filtered limits for the enforced upstream model before upstream work begins.
