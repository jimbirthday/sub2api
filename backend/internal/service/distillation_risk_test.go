package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type distillationRepoTestStub struct {
	DistillationRiskRepository
	config      *DistillationRiskConfig
	restriction *DistillationRestriction
	restrictErr error
}

func (s *distillationRepoTestStub) GetConfig(context.Context) (*DistillationRiskConfig, error) {
	return s.config, nil
}
func (s *distillationRepoTestStub) UpdateConfig(_ context.Context, mode string, scoring json.RawMessage, expected, actor int64, now time.Time) (*DistillationRiskConfig, error) {
	c := *s.config
	c.Mode, c.ScoringConfig, c.Revision, c.UpdatedAt = mode, scoring, expected+1, now
	return &c, nil
}
func (s *distillationRepoTestStub) GetActiveRestriction(context.Context, int64, time.Time) (*DistillationRestriction, error) {
	return s.restriction, s.restrictErr
}

type distillationConcurrencyCacheStub struct {
	ConcurrencyCache
	seenLimit int
}

func (s *distillationConcurrencyCacheStub) AcquireUserSlot(_ context.Context, _ int64, limit int, _ string) (bool, error) {
	s.seenLimit = limit
	return true, nil
}
func (s *distillationConcurrencyCacheStub) ReleaseUserSlot(context.Context, int64, string) error {
	return nil
}

type distillationRPMCacheStub struct {
	UserRPMCache
	count int
}

func (s *distillationRPMCacheStub) IncrementUserRPM(context.Context, int64) (int, error) {
	s.count++
	return s.count, nil
}

func TestDistillationWindowAlignmentUTC(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 44, 59, 0, time.FixedZone("CST", 8*60*60))
	got := DistillationWindowStart(at)
	want := time.Date(2026, 10, 21, 4, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("window start = %s, want %s", got, want)
	}
	if got := DistillationWindowSealAt(got); !got.Equal(want.Add(40 * time.Minute)) {
		t.Fatalf("seal = %s", got)
	}
}

func TestDistillationInsufficientDataNeverScores(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	e, err := svc.EvaluateWindow(DistillationWindowAggregate{UserID: 1, WindowStart: start, WorkloadUnits: 199, TotalTokens: DistillationMinTokens * 2}, 1, start.Add(41*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != "insufficient_data" || e.Score != nil {
		t.Fatalf("got status=%s score=%v", e.Status, e.Score)
	}
	if e.Signals.PromptRepeatRatio.Value != nil || e.Signals.PromptRepeatRatio.Points != 0 {
		t.Fatal("missing signal must stay null and score zero")
	}
}

func TestDistillationFixedWeightsDoNotRenormalizeMissingSignals(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	cv := 0.05
	e, err := svc.EvaluateWindow(DistillationWindowAggregate{UserID: 1, WindowStart: start, WorkloadUnits: 200, RequestCount: 1800, TotalTokens: 1_000_000, InputTokens: 100_000, OutputTokens: 300_000, DistinctModels: 5, InterarrivalCV: &cv}, 1, start.Add(41*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if e.Score == nil || *e.Score != 0 {
		t.Fatalf("score=%v, want unavailable proxy signals to contribute zero", e.Score)
	}
	if e.Signals.ConcurrencySaturation.Value != nil || e.Signals.PromptRepeatRatio.Value != nil {
		t.Fatal("unavailable telemetry must remain null")
	}
	if DistillationEvidenceComplete(e.Signals) {
		t.Fatal("missing telemetry must not be treated as complete")
	}
}

func TestDistillationAcceptedSignalThresholds(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	concurrencyDuration, rateBuckets, repeat, p99, cv := 0.5, 0.2, 0.9, 1_499_999.0, 0.15
	e, err := svc.EvaluateWindow(DistillationWindowAggregate{
		UserID: 1, WindowStart: start, WorkloadUnits: 200, RequestCount: 200,
		TotalTokens: 1_500_000, InputTokens: 125_000, OutputTokens: 500_000,
		DistinctModels: 4, DistinctProviders: 2, InterarrivalCV: &cv, InterarrivalCount: 100,
		ConcurrencySaturation: &concurrencyDuration, ConcurrencyDurationEligible: true,
		RequestRateSaturation: &rateBuckets, TokenCohortP99: &p99, TokenBaselineSamples: 1,
		PromptRepeatRatio: &repeat, PromptRepeatEligible: true, StrongSignal: true,
		TelemetryComplete: true,
	}, 1, start.Add(41*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if e.Score == nil || *e.Score != 100 || !DistillationEvidenceComplete(e.Signals) {
		t.Fatalf("evaluation=%+v", e)
	}
}

func TestDistillationRepeatRequiresStrongSignal(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	repeat := 1.0
	e, err := svc.EvaluateWindow(DistillationWindowAggregate{
		UserID: 1, WindowStart: start, WorkloadUnits: 200, TotalTokens: 100_000,
		InputTokens: 50_000, OutputTokens: 50_000, PromptRepeatRatio: &repeat,
		PromptRepeatEligible: true, TelemetryComplete: true,
	}, 1, start.Add(41*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !e.Signals.PromptRepeatRatio.Available || e.Signals.PromptRepeatRatio.Points != 0 {
		t.Fatalf("measured repeat signal must contribute zero without a strong signal: %+v", e.Signals.PromptRepeatRatio)
	}
}

func TestDistillationEnforceRequiresSevenObservationDays(t *testing.T) {
	now := time.Now().UTC()
	repo := &distillationRepoTestStub{config: &DistillationRiskConfig{Mode: DistillationModeObserve, ObservationStartedAt: now.Add(-6 * 24 * time.Hour), Revision: 3, ScoringConfig: json.RawMessage(`{}`)}}
	svc := NewDistillationRiskService(repo)
	_, err := svc.UpdateConfig(context.Background(), DistillationModeEnforce, nil, 3, 9, now)
	if !errors.Is(err, ErrDistillationObservationShort) {
		t.Fatalf("got %v", err)
	}
	repo.config.ObservationStartedAt = now.Add(-7 * 24 * time.Hour)
	if _, err := svc.UpdateConfig(context.Background(), DistillationModeEnforce, nil, 3, 9, now); err != nil {
		t.Fatal(err)
	}
}

func TestDistillationStateThresholdsAndRecovery(t *testing.T) {
	now := time.Now().UTC()
	c := DistillationCase{State: DistillationStateClear}
	c = AdvanceDistillationCase(c, 85, DistillationModeObserve, now, now)
	c = AdvanceDistillationCase(c, 85, DistillationModeObserve, now.Add(30*time.Minute), now)
	c = AdvanceDistillationCase(c, 85, DistillationModeObserve, now.Add(time.Hour), now)
	if c.State == DistillationStateLimited {
		t.Fatal("observe mode must never limit")
	}
	c = DistillationCase{State: DistillationStateClear}
	for i := 0; i < 3; i++ {
		c = AdvanceDistillationCase(c, 85, DistillationModeEnforce, now.Add(time.Duration(i+1)*30*time.Minute), now)
	}
	if c.State != DistillationStateLimited {
		t.Fatalf("state=%s", c.State)
	}
	for i := 0; i < 4; i++ {
		c = AdvanceDistillationCase(c, 40, DistillationModeEnforce, now.Add(time.Duration(i+4)*30*time.Minute), now)
	}
	if c.State != DistillationStateClear {
		t.Fatalf("four eligible low windows must recover, state=%s", c.State)
	}
}

func TestDistillationEffectiveLimitsTreatZeroAsUnlimited(t *testing.T) {
	if got := distillationMinLimit(0, 2); got != 2 {
		t.Fatalf("zero configured limit should be capped to restriction: %d", got)
	}
	if got := distillationMinLimit(10, 30); got != 10 {
		t.Fatalf("existing lower limit should win: %d", got)
	}
}

func TestDistillationEffectiveLimitsFailOpenContract(t *testing.T) {
	repo := &distillationRepoTestStub{restriction: &DistillationRestriction{ConcurrencyCap: 2, RPMCap: 30}}
	svc := NewDistillationRiskService(repo)
	concurrency, rpm, err := svc.EffectiveLimits(context.Background(), 7, 0, 0, time.Now())
	if err != nil || concurrency != 2 || rpm != 30 {
		t.Fatalf("limits=(%d,%d) err=%v", concurrency, rpm, err)
	}
	repo.restrictErr = errors.New("db down")
	concurrency, rpm, err = svc.EffectiveLimits(context.Background(), 7, 9, 80, time.Now())
	if err == nil || concurrency != 9 || rpm != 80 {
		t.Fatalf("hard limits must be preserved on lookup failure: (%d,%d) %v", concurrency, rpm, err)
	}
}

func TestDistillationRestrictionAppliesToConcurrencyAndRPMAdmission(t *testing.T) {
	repo := &distillationRepoTestStub{restriction: &DistillationRestriction{ConcurrencyCap: 2, RPMCap: 30}}
	risk := NewDistillationRiskService(repo)
	concurrencyCache := &distillationConcurrencyCacheStub{}
	concurrency := NewConcurrencyService(concurrencyCache)
	concurrency.SetDistillationRiskService(risk)
	result, err := concurrency.AcquireUserSlot(context.Background(), 7, 0)
	if err != nil || !result.Acquired || concurrencyCache.seenLimit != 2 {
		t.Fatalf("concurrency cap not applied: result=%+v limit=%d err=%v", result, concurrencyCache.seenLimit, err)
	}
	result.ReleaseFunc()

	rpmCache := &distillationRPMCacheStub{count: 30}
	billing := &BillingCacheService{userRPMCache: rpmCache, distillationRisk: risk}
	if err := billing.checkRPM(context.Background(), &User{ID: 7, RPMLimit: 0}, nil); !errors.Is(err, ErrUserRPMExceeded) {
		t.Fatalf("rpm cap not applied: %v", err)
	}
}

func TestDistillationAsyncCohortCanScoreWithReliableTokens(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	e, err := svc.EvaluateWindow(DistillationWindowAggregate{UserID: 1, Cohort: "async_batch", WindowStart: start, WorkloadUnits: 200, TotalTokens: 100_000, InputTokens: 50_000, OutputTokens: 50_000}, 1, start.Add(41*time.Minute))
	if err != nil || e.Status != "scored" {
		t.Fatalf("evaluation=%+v error=%v", e, err)
	}
}

func TestDistillationRegularityNeedsHitNotJustConcurrencyTelemetry(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	saturation, cv := 0.1, 0.01
	e, err := svc.EvaluateWindow(DistillationWindowAggregate{UserID: 1, WindowStart: start, WorkloadUnits: 200, TotalTokens: 100_000, InputTokens: 50_000, OutputTokens: 50_000, TelemetryComplete: true, ConcurrencyDurationEligible: true, ConcurrencySaturation: &saturation, InterarrivalCV: &cv, InterarrivalCount: 100}, 1, start.Add(41*time.Minute))
	if err != nil || e.Signals.InterarrivalRegularity.Points != 0 {
		t.Fatalf("evaluation=%+v error=%v", e, err)
	}
}

func TestDistillationManualRestrictionRequiresExplicitBoundedExpiry(t *testing.T) {
	svc := NewDistillationRiskService(nil)
	now := time.Now().UTC()
	for _, expiry := range []time.Time{{}, now, now.Add(24*time.Hour + time.Second)} {
		_, err := svc.ApplyAction(context.Background(), DistillationAction{UserID: 1, Type: "restrict", ExpectedRevision: 1, ActorID: 1, IdempotencyKey: "test", Reason: "reviewed", ExpiresAt: expiry}, now)
		if !errors.Is(err, ErrDistillationInvalid) {
			t.Fatalf("expiry=%s err=%v", expiry, err)
		}
	}
}

func TestDistillationCompleteLowEvidenceCanRecover(t *testing.T) {
	start := DistillationWindowStart(time.Now().Add(-time.Hour))
	low, repeat, p99, cv := 0.0, 1.0, 2000000.0, 0.01
	a := DistillationWindowAggregate{UserID: 1, WindowStart: start, Cohort: "interactive", WorkloadUnits: 200, TotalTokens: 100000, InputTokens: 50000, OutputTokens: 50000, DistinctModels: 4, DistinctProviders: 2, InterarrivalCV: &cv, InterarrivalCount: 199, ConcurrencySaturation: &low, ConcurrencyDurationEligible: true, RequestRateSaturation: &low, TokenCohortP99: &p99, TokenBaselineSamples: 100, PromptRepeatRatio: &repeat, PromptRepeatEligible: true, TelemetryComplete: true}
	e, err := NewDistillationRiskService(nil).EvaluateWindow(a, 1, start.Add(time.Hour))
	if err != nil || e.Score == nil || *e.Score != 0 || !DistillationEvidenceComplete(e.Signals) {
		t.Fatalf("low risk evidence must permit recovery: %+v err=%v", e, err)
	}
	a.PromptRepeatRatio = nil
	e, err = NewDistillationRiskService(nil).EvaluateWindow(a, 1, start.Add(time.Hour))
	if err != nil || DistillationEvidenceComplete(e.Signals) {
		t.Fatalf("missing fingerprint cannot recover: %+v err=%v", e, err)
	}
}

func TestDistillationEnforceAllowsConfigEdits(t *testing.T) {
	now := time.Now().UTC()
	repo := &distillationRepoTestStub{config: &DistillationRiskConfig{Mode: DistillationModeEnforce, ObservationStartedAt: now.Add(-8 * 24 * time.Hour), Revision: 3, ScoringConfig: json.RawMessage(`{}`)}}
	got, err := NewDistillationRiskService(repo).UpdateConfig(context.Background(), DistillationModeEnforce, json.RawMessage(`{"prompt_repeat_ratio":0.8}`), 3, 9, now)
	if err != nil || got.Mode != DistillationModeEnforce {
		t.Fatalf("active enforcement configuration edit rejected: %+v %v", got, err)
	}
}
