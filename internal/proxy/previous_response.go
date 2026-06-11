package proxy

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

const previousResponseIndexLimit = 4096

// PreviousResponseIndex maps previous_response_id to the account that created it.
type PreviousResponseIndex struct {
	mu   sync.Mutex
	byID map[string]string
}

func NewPreviousResponseIndex() *PreviousResponseIndex {
	return &PreviousResponseIndex{byID: make(map[string]string)}
}

func previousResponseCacheKey(responseID string, apiKeyID string) string {
	responseID = strings.TrimSpace(responseID)
	if apiKeyID == "" {
		return responseID
	}
	return responseID + "|" + apiKeyID
}

func (idx *PreviousResponseIndex) Remember(responseID, apiKeyID, accountID string) {
	responseID = strings.TrimSpace(responseID)
	accountID = strings.TrimSpace(accountID)
	if responseID == "" || accountID == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.byID) >= previousResponseIndexLimit {
		for key := range idx.byID {
			delete(idx.byID, key)
			break
		}
	}
	idx.byID[previousResponseCacheKey(responseID, apiKeyID)] = accountID
}

func (idx *PreviousResponseIndex) Lookup(responseID, apiKeyID string) string {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return ""
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if accountID, ok := idx.byID[previousResponseCacheKey(responseID, apiKeyID)]; ok {
		return accountID
	}
	if apiKeyID != "" {
		if accountID, ok := idx.byID[previousResponseCacheKey(responseID, "")]; ok {
			return accountID
		}
	}
	return ""
}

func (s *Service) resolvePreviousResponseOwner(ctx context.Context, responseID, apiKeyID, sessionID string) (string, error) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return "", nil
	}
	if ownerID := s.previousResponseIndex.Lookup(responseID, apiKeyID); ownerID != "" {
		return ownerID, nil
	}
	ownerID, err := s.requestLogsRepo.FindLatestAccountIDForResponseID(ctx, responseID, apiKeyID, sessionID)
	if err != nil {
		return "", fmt.Errorf("resolve previous response owner: %w", err)
	}
	if ownerID != "" {
		s.previousResponseIndex.Remember(responseID, apiKeyID, ownerID)
	}
	return ownerID, nil
}

func previousResponseIDFromPayload(payload map[string]any) string {
	if value, ok := payload["previous_response_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func responseIDFromEvent(payload map[string]any) string {
	if response, ok := payload["response"].(map[string]any); ok {
		if id, ok := response["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}
