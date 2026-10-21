package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Embedded nil interfaces deliberately fail if observing ordinary traffic
// touches durable reservations, a recovery DB read, or a provider attempt.
func TestObservedRequestsDoNotConsumeExecutionBudget(t *testing.T) {
	for _, limit := range []int{5, 50, 100, 500} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cache := &executionBudgetCacheStub{}
			telemetry := &DistillationTelemetryService{}
			ctx := WithModelExecutionBudget(context.Background(), cache, telemetry, 42, limit, 60, "", nil)
			require.NotEmpty(t, ModelExecutionOperationID(ctx), "observation still has a correlation ID")
			for range 2 {
				require.NoError(t, ReserveAndConfirmModelExecution(ctx, &UserModelRestrictionDecision{
					Identities: []CanonicalModelIdentity{{Namespace: "openai", CanonicalModel: "gpt-5"}},
				}))
				require.NoError(t, PersistModelExecutionAttempt(ctx, &Account{ID: 1}))
			}
			require.False(t, MarkCurrentModelExecutionAttemptUnknown(ctx), "an observed failure cannot pin capacity")
			CompleteModelExecution(ctx, true)
			require.Zero(t, cache.unknown)
			require.Zero(t, cache.released)
		})
	}
}

type explicitCapBudgetCache struct {
	ModelExecutionBudgetCache
	reservation ModelExecutionReservation
}

func (c *explicitCapBudgetCache) ReserveModelExecution(_ context.Context, reservation ModelExecutionReservation) (*ModelExecutionLease, error) {
	c.reservation = reservation
	return nil, ErrModelExecutionBudgetExceeded
}

type capBudgetTelemetryRepo struct{ executionBudgetTelemetryStub }

func (*capBudgetTelemetryRepo) CreateOperationIntent(_ context.Context, intent DistillationOperationIntent) (*DistillationOperationIntent, error) {
	intent.Revision = 1
	return &intent, nil
}

func TestExplicitModelCapsStillEnforceWithoutDuplicatingUserLimits(t *testing.T) {
	for _, caps := range []ModelRestrictionCaps{{Concurrency: 2}, {RPM: 30}} {
		cache := &explicitCapBudgetCache{}
		telemetry := &DistillationTelemetryService{repo: &capBudgetTelemetryRepo{}, recoveryStarted: true}
		ctx := WithModelExecutionBudget(context.Background(), cache, telemetry, 42, 50, 100, "", nil)
		decision := &UserModelRestrictionDecision{
			Identities: []CanonicalModelIdentity{{Namespace: "openai", CanonicalModel: "gpt-5"}},
			ModelCaps:  map[string]ModelRestrictionCaps{"openai:gpt-5": caps},
		}
		require.ErrorIs(t, ReserveAndConfirmModelExecution(ctx, decision), ErrModelExecutionBudgetExceeded)
		require.Zero(t, cache.reservation.UserLimit)
		require.Zero(t, cache.reservation.RPMLimit)
		require.Equal(t, caps.Concurrency, cache.reservation.TargetLimits["openai:gpt-5"])
		require.Equal(t, caps.RPM, cache.reservation.TargetRPMLimits["openai:gpt-5"])
	}
}

func TestAsyncRemoteWorkRetainsDurableExecution(t *testing.T) {
	cache := &explicitCapBudgetCache{}
	telemetry := &DistillationTelemetryService{repo: &capBudgetTelemetryRepo{}, recoveryStarted: true}
	ctx := WithModelExecutionBudget(context.Background(), cache, telemetry, 42, 50, 100, "", nil)
	RequireDurableModelExecution(ctx)
	require.ErrorIs(t, ReserveAndConfirmModelExecution(ctx, &UserModelRestrictionDecision{}), ErrModelExecutionBudgetExceeded)
	require.NotEmpty(t, cache.reservation.OperationID)
}

func TestBillingFailureDoesNotMakeFinishedProviderExecutionUnknown(t *testing.T) {
	cache := &executionBudgetCacheStub{}
	repo := &executionBudgetTelemetryStub{}
	ctx := context.WithValue(context.Background(), modelExecutionBudgetContextKey{}, &modelExecutionBudgetContext{
		cache: cache, telemetry: &DistillationTelemetryService{repo: repo}, userID: 42, operationID: "op",
		lease: &ModelExecutionLease{Revision: 2, Epoch: 1}, attempts: []string{"sent"},
	})
	parentID := int64(1)
	svc := &OpenAIGatewayService{accountRepo: &openAIRecordUsageAccountRepoStub{}}
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{}, APIKey: &APIKey{}, Account: &Account{ParentAccountID: &parentID},
	})
	require.ErrorContains(t, err, "not found")
	require.Equal(t, []string{"completed"}, repo.states)
	require.Equal(t, 1, cache.released)
	require.Zero(t, cache.unknown)
}

type observationRecoveryRepo struct {
	DistillationTelemetryRepository
	recovered chan struct{}
}

func (r *observationRecoveryRepo) RecoverExecutionBudgets(context.Context, ModelExecutionBudgetCache) error {
	r.recovered <- struct{}{}
	return nil
}

func TestObservationCapacityRecoveryStartsWithoutEnforcedTraffic(t *testing.T) {
	repo := &observationRecoveryRepo{recovered: make(chan struct{}, 1)}
	telemetry := NewDistillationTelemetryService(repo, "", 1)
	defer telemetry.Stop()
	telemetry.StartExecutionRecovery(&executionBudgetCacheStub{})
	select {
	case <-repo.recovered:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy observation capacity must be reconciled without waiting for an enforced request")
	}
}
