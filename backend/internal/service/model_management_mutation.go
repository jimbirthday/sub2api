package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

var ErrModelManagementIdempotencyConflict = errors.New("model management idempotency key reused with different payload")

type ModelManagementMutation struct {
	ActorID  int64
	Key      string
	Resource string
	Reason   string
	Payload  json.RawMessage
}

type ModelManagementMutationRepository interface {
	ExecuteModelManagementMutation(context.Context, ModelManagementMutation, func(context.Context) (json.RawMessage, error)) (json.RawMessage, error)
}

func (s *UserModelRestrictionService) MutateManagement(ctx context.Context, m ModelManagementMutation, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	if s == nil {
		return nil, ErrUserModelRestrictionUnavailable
	}
	if m.ActorID <= 0 || strings.TrimSpace(m.Key) == "" || len(m.Key) > 200 || strings.TrimSpace(m.Reason) == "" || len(m.Reason) > 2000 {
		return nil, ErrUserModelRestrictionInvalid
	}
	repo, ok := s.repo.(ModelManagementMutationRepository)
	if !ok {
		return nil, ErrUserModelRestrictionUnavailable
	}
	return repo.ExecuteModelManagementMutation(ctx, m, fn)
}
