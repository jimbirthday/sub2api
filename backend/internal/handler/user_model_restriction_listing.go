package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// filterUserModelRestrictionIDs hides denied or unresolved entries using one
// policy/catalog snapshot. On policy storage failure the list is fail-closed;
// request admission remains responsible for its own protocol error.
func filterUserModelRestrictionIDs(
	ctx context.Context,
	policies *service.UserModelRestrictionService,
	userID int64,
	platform string,
	modelIDs []string,
) []string {
	filtered, err := filterUserModelRestrictionIDsChecked(ctx, policies, userID, platform, modelIDs)
	if err != nil {
		slog.Warn("user_model_restriction_listing_failed", "user_id", userID, "platform", platform, "error", err)
		return []string{}
	}
	return filtered
}

func filterUserModelRestrictionIDsChecked(
	ctx context.Context,
	policies *service.UserModelRestrictionService,
	userID int64,
	platform string,
	modelIDs []string,
) ([]string, error) {
	if policies == nil || userID <= 0 || len(modelIDs) == 0 {
		return modelIDs, nil
	}
	inputs := make([]service.ModelIdentityInput, len(modelIDs))
	for i, model := range modelIDs {
		inputs[i] = service.ModelIdentityInput{
			// A listing platform is a transport, not proof of the original model
			// provider. The catalog must resolve aliases without invented origin.
			Namespace: "",
			Transport: strings.ToLower(strings.TrimSpace(platform)),
			Model:     model,
		}
	}
	result, err := policies.FilterModelsForListing(ctx, userID, inputs)
	if err != nil {
		return nil, fmt.Errorf("evaluate user model listing policy: %w", err)
	}
	if result == nil || len(result.Allowed) != len(modelIDs) {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	filtered := make([]string, 0, len(modelIDs))
	for i, model := range modelIDs {
		if result.Allowed[i] {
			filtered = append(filtered, model)
		}
	}
	return filtered, nil
}
