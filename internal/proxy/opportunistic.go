package proxy

import (
	"context"
)

func (s *Service) CheckOpportunisticAdmission(ctx context.Context, apiKey *ApiKeyData, model string) (bool, string, error) {
	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return false, "", err
	}
	effectiveModel := model
	if apiKey != nil {
		effectiveModel = EffectiveModelForAPIKey(apiKey, model)
	}
	params := streamSelectParams(effectiveModel, dashboardSettings, apiKey)
	params.TrafficClass = TrafficClassOpportunistic
	params.LeaseKind = AccountLeaseKindStream
	selection, err := s.loadBalancer.SelectAccount(ctx, params)
	if err != nil {
		return false, "", err
	}
	if selection.Lease != nil {
		s.loadBalancer.ReleaseLease(selection.Lease)
	}
	if selection.ErrorCode != "" || selection.Account == nil {
		message := selection.ErrorMessage
		if message == "" {
			message = "opportunistic burn window closed"
		}
		return false, message, nil
	}
	return true, "", nil
}
