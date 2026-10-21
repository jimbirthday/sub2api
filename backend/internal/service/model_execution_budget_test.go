package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type executionBudgetCacheStub struct {
	ModelExecutionBudgetCache
	unknown, released int
}

func (s *executionBudgetCacheStub) MarkModelExecutionUnknown(context.Context, int64, string, int64, int64) error {
	s.unknown++
	return nil
}
func (s *executionBudgetCacheStub) ReleaseModelExecution(context.Context, int64, string, int64, int64) error {
	s.released++
	return nil
}

type executionBudgetTelemetryStub struct {
	DistillationTelemetryRepository
	states []string
}

func (s *executionBudgetTelemetryStub) TransitionExecutionAttempt(_ context.Context, _ string, state string, _ int64) error {
	s.states = append(s.states, state)
	return nil
}
func (s *executionBudgetTelemetryStub) CreateExecutionAttempt(context.Context, ExecutionAttempt) error {
	return nil
}
func (s *executionBudgetTelemetryStub) ReconcileExecutionAttempts(context.Context, time.Time) error {
	return nil
}
func (s *executionBudgetTelemetryStub) GetOperationIntent(context.Context, string) (*DistillationOperationIntent, error) {
	return &DistillationOperationIntent{OperationID: "op", Revision: 1}, nil
}
func (s *executionBudgetTelemetryStub) TransitionOperationIntent(_ context.Context, _ string, _ int64, _ string, _ time.Time) (*DistillationOperationIntent, error) {
	return &DistillationOperationIntent{OperationID: "op", Revision: 2}, nil
}

func TestUnknownModelExecutionCannotBeResubmittedOrCompletedByLaterSuccess(t *testing.T) {
	cache := &executionBudgetCacheStub{}
	repo := &executionBudgetTelemetryStub{}
	telemetry := &DistillationTelemetryService{repo: repo, recoveryStarted: true}
	ctx := context.WithValue(context.Background(), modelExecutionBudgetContextKey{}, &modelExecutionBudgetContext{
		cache: cache, telemetry: telemetry, userID: 42, operationID: "op",
		lease: &ModelExecutionLease{Revision: 2, Epoch: 1}, attempts: []string{"sent"},
	})
	require.True(t, MarkCurrentModelExecutionAttemptUnknown(ctx))
	require.Equal(t, []string{"unknown"}, repo.states)
	require.Equal(t, 1, cache.unknown)
	CompleteModelExecution(ctx, true)
	require.Zero(t, cache.released)
	require.Equal(t, []string{"unknown"}, repo.states)
	require.ErrorIs(t, ReserveAndConfirmModelExecution(ctx, &UserModelRestrictionDecision{}), ErrModelExecutionBudgetUnavailable)
	require.ErrorIs(t, PersistModelExecutionAttempt(ctx, &Account{ID: 1}), ErrModelExecutionBudgetUnavailable)
	require.ErrorIs(t, BeginModelExecutionTurn(ctx, 2), ErrModelExecutionBudgetUnavailable)
}
