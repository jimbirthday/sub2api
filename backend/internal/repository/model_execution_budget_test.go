package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newModelExecutionBudgetCache(t *testing.T) (*concurrencyCache, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	return cache, client
}

func reservation(operation string) service.ModelExecutionReservation {
	return service.ModelExecutionReservation{UserID: 42, OperationID: operation, Targets: []string{"openai:gpt-5", "openai:gpt-5"}, PolicyRevision: 3, CatalogRevision: 7, PayloadHash: "payload", UserLimit: 1, RPMLimit: 10, TargetLimits: map[string]int{"openai:gpt-5": 1}}
}

func TestModelExecutionBudgetReserveIsIdempotentAndReleaseReclaimsCapacity(t *testing.T) {
	cache, client := newModelExecutionBudgetCache(t)
	ctx := context.Background()
	first, err := cache.ReserveModelExecution(ctx, reservation("op-1"))
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Revision)
	retry, err := cache.ReserveModelExecution(ctx, reservation("op-1"))
	require.NoError(t, err)
	require.Equal(t, first, retry)
	_, err = cache.ReserveModelExecution(ctx, reservation("op-2"))
	require.ErrorIs(t, err, service.ErrModelExecutionBudgetExceeded)

	confirmed, err := cache.ConfirmModelExecution(ctx, service.ModelExecutionConfirmation{UserID: 42, OperationID: "op-1", ExpectedRevision: first.Revision, ExpectedEpoch: first.Epoch, Targets: []string{"openai:gpt-5"}, PolicyRevision: 3, CatalogRevision: 7, PayloadHash: "payload", TargetLimits: map[string]int{"openai:gpt-5": 1}})
	require.NoError(t, err)
	require.Equal(t, "confirmed", confirmed.State)
	require.NoError(t, cache.ReleaseModelExecution(ctx, 42, "op-1", confirmed.Revision, confirmed.Epoch))
	_, err = cache.ReserveModelExecution(ctx, reservation("op-2"))
	require.NoError(t, err)
	require.Equal(t, "1", client.HGet(ctx, modelExecutionStateKey(42), "running").Val())
}

func TestModelExecutionBudgetUnknownRetainsCapacityAndEpochRejectsStaleOwner(t *testing.T) {
	cache, _ := newModelExecutionBudgetCache(t)
	ctx := context.Background()
	lease, err := cache.ReserveModelExecution(ctx, reservation("op-unknown"))
	require.NoError(t, err)
	require.NoError(t, cache.MarkModelExecutionUnknown(ctx, 42, "op-unknown", lease.Revision, lease.Epoch))
	_, err = cache.ReserveModelExecution(ctx, reservation("op-blocked"))
	require.ErrorIs(t, err, service.ErrModelExecutionBudgetExceeded)

	newEpoch, err := cache.AdvanceModelExecutionEpoch(ctx, 42)
	require.NoError(t, err)
	require.Greater(t, newEpoch, lease.Epoch)
	err = cache.ReleaseModelExecution(ctx, 42, "op-unknown", lease.Revision, newEpoch)
	require.True(t, errors.Is(err, service.ErrModelExecutionRevisionConflict))
}

func TestModelExecutionBudgetRetargetCASAndModelLimit(t *testing.T) {
	cache, _ := newModelExecutionBudgetCache(t)
	ctx := context.Background()
	first := reservation("op-a")
	first.UserLimit = 2
	lease, err := cache.ReserveModelExecution(ctx, first)
	require.NoError(t, err)
	second := reservation("op-b")
	second.UserLimit = 2
	second.Targets = []string{"anthropic:claude-4"}
	second.TargetLimits = map[string]int{"anthropic:claude-4": 1}
	_, err = cache.ReserveModelExecution(ctx, second)
	require.NoError(t, err)

	_, err = cache.RetargetModelExecution(ctx, service.ModelExecutionRetarget{UserID: 42, OperationID: "op-a", ExpectedRevision: lease.Revision, ExpectedEpoch: lease.Epoch, Targets: []string{"anthropic:claude-4"}, PolicyRevision: 3, CatalogRevision: 7, PayloadHash: "next", TargetLimits: map[string]int{"anthropic:claude-4": 1}})
	require.ErrorIs(t, err, service.ErrModelExecutionBudgetExceeded)
	_, err = cache.ExtendModelExecution(ctx, service.ModelExecutionExtension{UserID: 42, OperationID: "op-a", ExpectedRevision: lease.Revision, ExpectedEpoch: lease.Epoch, Targets: []string{"openai:gpt-5", "anthropic:claude-4"}, PolicyRevision: 3, CatalogRevision: 7, PayloadHash: "next", TargetLimits: map[string]int{"openai:gpt-5": 1, "anthropic:claude-4": 2}})
	require.NoError(t, err)
}

func TestModelExecutionKeysShareUserHashTag(t *testing.T) {
	keys := []string{modelExecutionStateKey(9), modelExecutionOpsKey(9), modelExecutionTargetsKey(9), modelExecutionRPMKey(9)}
	for _, key := range keys {
		require.Contains(t, key, "{u:9}")
	}
}

func TestModelExecutionBatchOutstandingAtomicAndReleaseIdempotent(t *testing.T) {
	cache, client := newModelExecutionBudgetCache(t)
	ctx := context.Background()
	r := reservation("batch")
	r.UserLimit = 0
	r.BatchOutstanding = 4
	r.BatchLimit = 5
	lease, err := cache.ReserveModelExecution(ctx, r)
	require.NoError(t, err)
	require.Equal(t, "4", client.HGet(ctx, modelExecutionStateKey(42), "batch").Val())
	next := r
	next.OperationID = "batch-next"
	next.Targets = []string{"google:gemini"}
	next.BatchOutstanding = 2
	_, err = cache.ReserveModelExecution(ctx, next)
	require.ErrorIs(t, err, service.ErrModelExecutionBudgetExceeded)
	require.NoError(t, cache.MarkModelExecutionUnknown(ctx, 42, "batch", lease.Revision, lease.Epoch))
	require.Equal(t, "4", client.HGet(ctx, modelExecutionStateKey(42), "batch").Val())
	require.NoError(t, cache.ReleaseModelExecution(ctx, 42, "batch", lease.Revision, lease.Epoch))
	require.NoError(t, cache.ReleaseModelExecution(ctx, 42, "batch", lease.Revision, lease.Epoch))
	require.Equal(t, "0", client.HGet(ctx, modelExecutionStateKey(42), "batch").Val())
}

func TestUnknownProviderAttemptSurvivesLegacySlotTTLRefreshes(t *testing.T) {
	for _, path := range []string{"acquire", "refresh", "track", "cleanup", "startup"} {
		t.Run(path, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
			require.True(t, ok)
			ctx := context.Background()
			key := accountSlotKey(99)
			require.NoError(t, cache.RestoreExecutionProviderAttempt(ctx, 99, "openai", "uncertain"))
			switch path {
			case "acquire", "refresh":
				acquired, err := cache.AcquireAccountSlot(ctx, 99, 3, "ordinary")
				require.NoError(t, err)
				require.True(t, acquired)
				if path == "refresh" {
					acquired, err = cache.AcquireAccountSlot(ctx, 99, 3, "ordinary")
					require.NoError(t, err)
					require.True(t, acquired)
				}
			case "track":
				_, err := trackSlotScript.Run(ctx, client, []string{key}, 900, "ordinary").Result()
				require.NoError(t, err)
			case "cleanup":
				require.NoError(t, cache.CleanupExpiredAccountSlots(ctx, 99))
			case "startup":
				_, _, err := runScriptInt64Pair(ctx, client, startupCleanupSlotScript, []string{key}, 900)
				require.NoError(t, err)
			}
			require.Equal(t, time.Duration(-1), client.TTL(ctx, key).Val())
			server.FastForward(16 * time.Minute)
			require.EqualValues(t, executionProviderAttemptScore, client.ZScore(ctx, key, "intent:uncertain").Val())
			require.NoError(t, cache.ReleaseExecutionProviderAttempt(ctx, 99, "openai", "uncertain"))
			require.ErrorIs(t, client.ZScore(ctx, key, "intent:uncertain").Err(), redis.Nil)
		})
	}
}
