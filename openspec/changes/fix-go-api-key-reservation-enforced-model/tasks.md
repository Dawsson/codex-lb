## 1. Spec

- [x] Add API-key reservation parity requirement for post-policy model enforcement.

## 2. Implementation

- [x] Use the post-enforcement model for HTTP streaming Responses API-key reservations.
- [x] Use the post-enforcement model for websocket Responses API-key reservations.
- [x] Leave bridge forwarding and quota-planner warm-now explicitly out of scope.

## 3. Validation

- [x] Add focused Go regression coverage for model-filtered reservation enforcement after API-key model rewrite.
- [ ] Run focused Go tests.
