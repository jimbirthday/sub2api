//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDistillationRiskPersistenceAndExpiry(t *testing.T) {
	ctx := context.Background()
	user, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("behavior-risk-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx) })
	repo := NewDistillationRiskRepository(integrationDB)
	cfg, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	now := service.DistillationWindowStart(time.Now().UTC())
	_, err = integrationDB.ExecContext(ctx, `UPDATE distillation_risk_configs SET mode='enforce',observation_started_at=$1 WHERE id=1`, now.Add(-8*24*time.Hour))
	require.NoError(t, err)
	all := service.DistillationSignalEvidence{Available: true, Value: float64Pointer(1), Threshold: float64Pointer(1), Points: 10}
	signals := service.DistillationSignals{ConcurrencySaturation: all, RequestRateSaturation: all, TokenThroughput: all, PromptRepeatRatio: all, ModelProviderBreadth: all, InterarrivalRegularity: all, OutputInputRatio: all}
	record := func(start time.Time, score int, complete bool) {
		t.Helper()
		sig := signals
		if !complete {
			sig.PromptRepeatRatio = service.DistillationSignalEvidence{}
		}
		e := service.DistillationWindowEvaluation{UserID: user.ID, WindowStart: start, WindowEnd: start.Add(30 * time.Minute), SealAt: start.Add(40 * time.Minute), RuleVersion: cfg.RuleVersion, WorkloadUnits: 200, RequestCount: 200, TotalTokens: 100000, InputTokens: 25000, OutputTokens: 75000, Cohort: "interactive", Signals: sig, Score: &score, Status: "scored"}
		require.NoError(t, repo.RecordWindow(ctx, e, service.DistillationModeEnforce, start.Add(41*time.Minute)))
	}
	for i := 0; i < 3; i++ {
		record(now.Add(time.Duration(i-3)*30*time.Minute), 90, true)
	}
	detail, err := repo.GetCase(ctx, user.ID, now.Add(11*time.Minute))
	require.NoError(t, err)
	require.Equal(t, service.DistillationStateLimited, detail.Case.State)
	require.NotNil(t, detail.ActiveRestriction)
	require.Equal(t, 2, detail.ActiveRestriction.ConcurrencyCap)
	firstRevision := detail.Case.Revision
	// Duplicate worker delivery cannot advance the streak or create another limit.
	record(now.Add(-30*time.Minute), 90, true)
	again, err := repo.GetCase(ctx, user.ID, now.Add(11*time.Minute))
	require.NoError(t, err)
	require.Equal(t, firstRevision, again.Case.Revision)
	// A failed/missing signal cannot recover the case.
	record(now, 1, false)
	again, err = repo.GetCase(ctx, user.ID, now.Add(41*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 0, again.Case.RecoveryStreak)
	// Expiry is unconditional even when telemetry stops.
	expired, err := repo.GetActiveRestriction(ctx, user.ID, now.Add(4*time.Hour))
	require.NoError(t, err)
	require.Nil(t, expired)
	// Complete consecutive low windows may revoke before the expiry.
	for i := 1; i <= 4; i++ {
		record(now.Add(time.Duration(i)*30*time.Minute), 20, true)
	}
	recovered, err := repo.GetCase(ctx, user.ID, now.Add(3*time.Hour))
	require.NoError(t, err)
	require.Equal(t, service.DistillationStateClear, recovered.Case.State)
	// Configuration writes are CAS-protected and disabling enforcement removes automatic caps.
	updated, err := repo.UpdateConfig(ctx, service.DistillationModeObserve, json.RawMessage(`{}`), cfg.Revision, user.ID, now.Add(3*time.Hour))
	require.NoError(t, err)
	require.Equal(t, cfg.Revision+1, updated.Revision)
	_, err = repo.UpdateConfig(ctx, service.DistillationModeOff, json.RawMessage(`{}`), cfg.Revision, user.ID, now.Add(3*time.Hour))
	require.ErrorIs(t, err, service.ErrDistillationRevisionConflict)
	events, err := repo.ListEvents(ctx, service.DistillationListOptions{Page: 1, PageSize: 100, UserID: user.ID})
	require.NoError(t, err)
	require.NotEmpty(t, events.Items)
}

func float64Pointer(value float64) *float64 { return &value }

func TestDistillationSealedAggregateSQL(t *testing.T) {
	ctx := context.Background()
	repo := NewDistillationRiskRepository(integrationDB)
	cfg, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	user, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("aggregate-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx) })
	key, err := integrationEntClient.APIKey.Create().SetUserID(user.ID).SetKey(fmt.Sprintf("aggregate-%d", time.Now().UnixNano())).SetName("test").Save(ctx)
	require.NoError(t, err)
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "aggregate-test"})
	t.Cleanup(func() {
		// Usage logs intentionally outlive users; remove this fixture explicitly.
		_, cleanupErr := integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE user_id=$1`, user.ID)
		require.NoError(t, cleanupErr)
		require.NoError(t, integrationEntClient.APIKey.DeleteOneID(key.ID).Exec(ctx))
		require.NoError(t, integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx))
	})
	start := service.DistillationWindowStart(time.Now().UTC().Add(-time.Hour))
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,model,input_tokens,output_tokens,created_at) VALUES($1,$2,$3,'family-a',100000,500000,$4)`, user.ID, key.ID, account.ID, start.Add(time.Minute))
	require.NoError(t, err)
	rows, err := repo.ListSealedAggregates(ctx, cfg.RuleVersion, start.Add(39*time.Minute), 1000)
	require.NoError(t, err)
	for _, row := range rows {
		require.NotEqual(t, user.ID, row.UserID, "window cannot score before seal")
	}
	rows, err = repo.ListSealedAggregates(ctx, cfg.RuleVersion, start.Add(41*time.Minute), 1000)
	require.NoError(t, err)
	found := false
	for _, row := range rows {
		if row.UserID == user.ID {
			found = true
			require.Equal(t, "interactive", row.Cohort)
			require.Equal(t, int64(600000), row.TotalTokens)
			require.False(t, row.TelemetryComplete)
			require.Nil(t, row.TokenCohortP99)
		}
	}
	require.True(t, found)
	// Six requests begun in the preceding bucket remain concurrent in this
	// bucket (saturation requires at least six concurrent operations). A
	// retry's unresolved earlier attempt must still invalidate it.
	telemetryRepo := &distillationTelemetryRepository{db: integrationDB}
	var previous []service.DistillationAttempt
	for i := 0; i < 6; i++ {
		a := service.DistillationAttempt{AttemptID: uuid.New(), UserID: user.ID, OperationID: fmt.Sprintf("carry-%d-%d", user.ID, i), Provider: "openai", Model: "family-a", Cohort: "interactive", ConfiguredConcurrency: 4, ObservedConcurrency: 4, StartedAt: start.Add(-time.Minute), PromptHMAC: make([]byte, 32), HMACDay: &start}
		require.NoError(t, telemetryRepo.CreateAttempt(ctx, a))
		previous = append(previous, a)
	}
	latest := service.DistillationAttempt{AttemptID: uuid.New(), UserID: user.ID, OperationID: previous[0].OperationID, Provider: "openai", Model: "family-a", Cohort: "interactive", ConfiguredConcurrency: 4, ObservedConcurrency: 4, StartedAt: start.Add(time.Minute), PromptHMAC: make([]byte, 32), HMACDay: &start}
	require.NoError(t, telemetryRepo.CreateAttempt(ctx, latest))
	input, output := int64(100000), int64(500000)
	require.NoError(t, telemetryRepo.CompleteAttempt(ctx, service.DistillationAttemptCompletion{AttemptID: latest.AttemptID, CompletedAt: start.Add(2 * time.Minute), InputTokens: &input, OutputTokens: &output, Status: "completed", TelemetryComplete: true}))
	for i, a := range previous {
		if i == 0 {
			continue
		}
		require.NoError(t, telemetryRepo.CompleteAttempt(ctx, service.DistillationAttemptCompletion{AttemptID: a.AttemptID, CompletedAt: start.Add(10 * time.Minute), Status: "failed", TelemetryComplete: true}))
	}
	rows, err = repo.ListSealedAggregates(ctx, cfg.RuleVersion, start.Add(41*time.Minute), 1000)
	require.NoError(t, err)
	for _, row := range rows {
		if row.UserID == user.ID {
			require.False(t, row.TelemetryComplete, "an unresolved earlier retry cannot be hidden by deduplication")
		}
	}
	require.NoError(t, telemetryRepo.CompleteAttempt(ctx, service.DistillationAttemptCompletion{AttemptID: previous[0].AttemptID, CompletedAt: start.Add(10 * time.Minute), Status: "failed", TelemetryComplete: true}))
	rows, err = repo.ListSealedAggregates(ctx, cfg.RuleVersion, start.Add(41*time.Minute), 1000)
	require.NoError(t, err)
	for _, row := range rows {
		if row.UserID == user.ID {
			require.True(t, row.TelemetryComplete)
			require.NotNil(t, row.ConcurrencySaturation)
			require.InDelta(t, 1, *row.ConcurrencySaturation, 0.001, "carryover concurrency must count in the later bucket")
		}
	}
	// Execute every CTE against PostgreSQL, including timestamp operators and
	// percentile_cont. An empty result is valid on a clean fixture database.
	_, err = repo.ListSealedAggregates(ctx, cfg.RuleVersion, time.Now().UTC(), 1000)
	require.NoError(t, err)
}

func TestDistillationCohortSequencesAndRestrictionRestart(t *testing.T) {
	ctx := context.Background()
	u, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("cohort-sequence-%d@example.com", time.Now().UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, integrationEntClient.User.DeleteOneID(u.ID).Exec(ctx)) })
	repo := NewDistillationRiskRepository(integrationDB)
	cfg, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE distillation_risk_configs SET mode='enforce' WHERE id=1`)
	require.NoError(t, err)
	start := service.DistillationWindowStart(time.Now().UTC().Add(-12 * time.Hour))
	all := service.DistillationSignalEvidence{Available: true, Value: float64Pointer(1), Threshold: float64Pointer(1)}
	signals := service.DistillationSignals{ConcurrencySaturation: all, RequestRateSaturation: all, TokenThroughput: all, PromptRepeatRatio: all, ModelProviderBreadth: all, InterarrivalRegularity: all, OutputInputRatio: all}
	record := func(cohort string, i, score int) {
		t.Helper()
		at := start.Add(time.Duration(i) * 30 * time.Minute)
		e := service.DistillationWindowEvaluation{UserID: u.ID, Cohort: cohort, WindowStart: at, WindowEnd: at.Add(30 * time.Minute), SealAt: at.Add(40 * time.Minute), RuleVersion: cfg.RuleVersion, WorkloadUnits: 200, RequestCount: 200, TotalTokens: 100000, InputTokens: 50000, OutputTokens: 50000, Signals: signals, Score: &score, Status: "scored"}
		require.NoError(t, repo.RecordWindow(ctx, e, service.DistillationModeEnforce, at.Add(41*time.Minute)))
	}
	// Alternating cohorts must not manufacture three consecutive windows.
	record("interactive", 0, 90)
	record("async_batch", 1, 90)
	record("interactive", 2, 90)
	cap, err := repo.GetActiveRestriction(ctx, u.ID, start.Add(2*time.Hour))
	require.NoError(t, err)
	require.Nil(t, cap)
	// Both cohorts may deliver the same window, in either order. Batch low
	// evidence cannot consume or recover the interactive sequence.
	record("async_batch", 3, 20)
	record("interactive", 3, 90)
	record("interactive", 4, 90)
	record("async_batch", 4, 20)
	now := start.Add(4*30*time.Minute + 41*time.Minute)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, now)
	require.NoError(t, err)
	require.NotNil(t, cap)
	firstStart := cap.StartsAt
	for i := 5; i <= 15; i++ {
		record("async_batch", i, 20)
		record("interactive", i, 90)
	}
	var expires time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT MAX(expires_at) FROM distillation_risk_restrictions WHERE user_id=$1`, u.ID).Scan(&expires))
	require.Equal(t, firstStart.Add(6*time.Hour), expires, "renewals must stop at six continuous hours")
	// At the cap's expiry the old high streak cannot immediately recreate it.
	record("interactive", 16, 90)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, start.Add(16*30*time.Minute+41*time.Minute))
	require.NoError(t, err)
	require.Nil(t, cap)
	record("interactive", 17, 90)
	record("interactive", 18, 90)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, start.Add(18*30*time.Minute+41*time.Minute))
	require.NoError(t, err)
	require.Nil(t, cap)
	record("interactive", 19, 90)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, start.Add(19*30*time.Minute+41*time.Minute))
	require.NoError(t, err)
	require.Nil(t, cap)
	record("interactive", 20, 90)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, start.Add(20*30*time.Minute+41*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, cap, "three new eligible windows permit a new bounded restriction")
	// Four low windows in the other cohort still cannot recover this case.
	detail, err := repo.GetCase(ctx, u.ID, start.Add(19*30*time.Minute+41*time.Minute))
	require.NoError(t, err)
	require.Equal(t, service.DistillationStateLimited, detail.Case.State)
	for i := 21; i <= 24; i++ {
		record("interactive", i, 20)
	}
	detail, err = repo.GetCase(ctx, u.ID, start.Add(24*30*time.Minute+41*time.Minute))
	require.NoError(t, err)
	require.Equal(t, service.DistillationStateClear, detail.Case.State)
	require.Nil(t, detail.ActiveRestriction)
}

func TestDistillationFutureExemptionDoesNotRevokeCurrentLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	u, err := integrationEntClient.User.Create().SetEmail(fmt.Sprintf("future-exemption-%d@example.com", now.UnixNano())).SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, integrationEntClient.User.DeleteOneID(u.ID).Exec(ctx)) })
	_, err = integrationDB.ExecContext(ctx, `UPDATE distillation_risk_configs SET mode='enforce' WHERE id=1`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO distillation_risk_restrictions(user_id,source,concurrency_cap,rpm_cap,continuous_started_at,starts_at,expires_at,reason) VALUES($1,'automatic',2,30,$2,$2,$3,'test')`, u.ID, now.Add(-time.Minute), now.Add(2*time.Hour))
	require.NoError(t, err)
	repo := NewDistillationRiskRepository(integrationDB)
	_, err = repo.CreateExemption(ctx, service.DistillationExemptionMutation{UserID: u.ID, ActorID: u.ID, StartsAt: now.Add(time.Hour), ExpiresAt: now.Add(24 * time.Hour), Reason: "scheduled", IdempotencyKey: fmt.Sprintf("future-%d", u.ID)})
	require.NoError(t, err)
	cap, err := repo.GetActiveRestriction(ctx, u.ID, now)
	require.NoError(t, err)
	require.NotNil(t, cap)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, now.Add(90*time.Minute))
	require.NoError(t, err)
	require.Nil(t, cap)
	// A manual limit is never bypassed by this exemption.
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO distillation_risk_restrictions(user_id,source,concurrency_cap,rpm_cap,continuous_started_at,starts_at,expires_at,reason) VALUES($1,'manual',1,10,$2,$2,$3,'reviewed')`, u.ID, now, now.Add(3*time.Hour))
	require.NoError(t, err)
	cap, err = repo.GetActiveRestriction(ctx, u.ID, now.Add(90*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, 1, cap.ConcurrencyCap)
}
