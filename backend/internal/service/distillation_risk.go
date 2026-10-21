package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	DistillationModeOff     = "off"
	DistillationModeObserve = "observe"
	DistillationModeEnforce = "enforce"

	DistillationStateClear     = "clear"
	DistillationStateObserving = "observing"
	DistillationStateSuspected = "suspected"
	DistillationStateLimited   = "limited"

	DistillationWindowSize      = 30 * time.Minute
	DistillationAllowedLateness = 10 * time.Minute
	DistillationObservationAge  = 7 * 24 * time.Hour
	DistillationRestrictionTTL  = 2 * time.Hour
	DistillationMaxContinuous   = 6 * time.Hour
	DistillationMaxExemption    = 30 * 24 * time.Hour

	DistillationMinWorkloadUnits = int64(200)
	DistillationMinTokens        = int64(100_000)
)

var (
	ErrDistillationInvalid          = errors.New("invalid distillation risk request")
	ErrDistillationNotFound         = errors.New("distillation risk record not found")
	ErrDistillationRevisionConflict = errors.New("distillation risk revision conflict")
	ErrDistillationIdempotencyReuse = errors.New("distillation risk idempotency key already used")
	ErrDistillationUnavailable      = errors.New("distillation risk storage unavailable")
	ErrDistillationObservationShort = errors.New("enforcement requires seven completed observation days")
)

type DistillationRiskConfig struct {
	Mode                 string          `json:"mode"`
	ObservationStartedAt time.Time       `json:"observation_started_at"`
	ScoringConfig        json.RawMessage `json:"scoring_config"`
	RuleVersion          int64           `json:"rule_version"`
	Revision             int64           `json:"revision"`
	UpdatedBy            *int64          `json:"updated_by"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type DistillationWindowAggregate struct {
	UserID                int64
	WindowStart           time.Time
	Cohort                string
	RequestCount          int64
	WorkloadUnits         int64
	TotalTokens           int64
	InputTokens           int64
	OutputTokens          int64
	DistinctModels        int64
	DistinctAccounts      int64
	InterarrivalCV        *float64
	ConcurrencySaturation *float64
	PromptRepeatRatio     *float64
	// The following fields are populated only by an accepted-design telemetry
	// aggregator. Zero values deliberately mean that the signal is unavailable.
	ConcurrencyDurationEligible bool
	RequestRateSaturation       *float64
	TokenCohortP99              *float64
	TokenFamilyThroughputHit    bool
	TokenBaselineSamples        int64
	PromptRepeatEligible        bool
	StrongSignal                bool
	DistinctProviders           int64
	InterarrivalCount           int64
	TelemetryComplete           bool
}

type DistillationSignalEvidence struct {
	Available bool     `json:"available"`
	Value     *float64 `json:"value"`
	Threshold *float64 `json:"threshold"`
	Points    int      `json:"points"`
	Reason    string   `json:"reason,omitempty"`
}

type DistillationSignals struct {
	ConcurrencySaturation  DistillationSignalEvidence `json:"concurrency_saturation"`
	RequestRateSaturation  DistillationSignalEvidence `json:"request_rate_saturation"`
	TokenThroughput        DistillationSignalEvidence `json:"token_throughput"`
	PromptRepeatRatio      DistillationSignalEvidence `json:"prompt_repeat_ratio"`
	ModelProviderBreadth   DistillationSignalEvidence `json:"model_provider_breadth"`
	InterarrivalRegularity DistillationSignalEvidence `json:"interarrival_regularity"`
	OutputInputRatio       DistillationSignalEvidence `json:"output_input_ratio"`
}

type DistillationWindowEvaluation struct {
	UserID        int64               `json:"user_id"`
	WindowStart   time.Time           `json:"window_start"`
	Cohort        string              `json:"cohort"`
	WindowEnd     time.Time           `json:"window_end"`
	SealAt        time.Time           `json:"seal_at"`
	RuleVersion   int64               `json:"rule_version"`
	WorkloadUnits int64               `json:"workload_units"`
	RequestCount  int64               `json:"request_count"`
	TotalTokens   int64               `json:"total_tokens"`
	InputTokens   int64               `json:"input_tokens"`
	OutputTokens  int64               `json:"output_tokens"`
	Signals       DistillationSignals `json:"signals"`
	Score         *int                `json:"score"`
	Status        string              `json:"status"`
}

type DistillationCase struct {
	UserID                int64      `json:"user_id"`
	State                 string     `json:"state"`
	ReviewStatus          string     `json:"review_status"`
	SuspectedStreak       int        `json:"suspected_streak"`
	LimitedStreak         int        `json:"limited_streak"`
	RecoveryStreak        int        `json:"recovery_streak"`
	LastEligibleWindowEnd *time.Time `json:"last_eligible_window_end"`
	LastScore             *int       `json:"last_score"`
	Revision              int64      `json:"revision"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type DistillationRestriction struct {
	ID                  int64      `json:"id"`
	UserID              int64      `json:"user_id"`
	Source              string     `json:"source"`
	ConcurrencyCap      int        `json:"concurrency_cap"`
	RPMCap              int        `json:"rpm_cap"`
	ContinuousStartedAt time.Time  `json:"continuous_started_at"`
	StartsAt            time.Time  `json:"starts_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	RevokedAt           *time.Time `json:"revoked_at"`
	Reason              string     `json:"reason"`
	Revision            int64      `json:"revision"`
}

type DistillationExemption struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	StartsAt  time.Time  `json:"starts_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	Reason    string     `json:"reason"`
	Revision  int64      `json:"revision"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type DistillationEvent struct {
	ID             int64           `json:"id"`
	UserID         *int64          `json:"user_id"`
	EventType      string          `json:"event_type"`
	Details        json.RawMessage `json:"details"`
	ActorID        *int64          `json:"actor_id"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type DistillationOverview struct {
	Mode               string           `json:"mode"`
	StateCounts        map[string]int64 `json:"state_counts"`
	ActiveRestrictions int64            `json:"active_restrictions"`
	ActiveExemptions   int64            `json:"active_exemptions"`
	PendingReviews     int64            `json:"pending_reviews"`
	LastProcessedAt    *time.Time       `json:"last_processed_at"`
	AsOf               *time.Time       `json:"as_of"`
	TelemetryStatus    string           `json:"telemetry_status"`
	TelemetryGaps      int64            `json:"telemetry_gaps"`
}

type DistillationCaseDetail struct {
	Case              *DistillationCase        `json:"case"`
	ActiveRestriction *DistillationRestriction `json:"active_restriction"`
	ActiveExemption   *DistillationExemption   `json:"active_exemption"`
}

type DistillationListOptions struct {
	BeforeID int64
	Cursor   bool
	Page     int
	PageSize int
	UserID   int64
	State    string
}

type DistillationCaseList struct {
	NextBeforeID *int64             `json:"next_before_id"`
	Items        []DistillationCase `json:"items"`
	Total        int64              `json:"total"`
}

type DistillationEventList struct {
	NextBeforeID *int64              `json:"next_before_id"`
	Items        []DistillationEvent `json:"items"`
	Total        int64               `json:"total"`
}

type DistillationExemptionList struct {
	NextBeforeID *int64                  `json:"next_before_id"`
	Items        []DistillationExemption `json:"items"`
	Total        int64                   `json:"total"`
}

type DistillationAction struct {
	ExpiresAt        time.Time `json:"expires_at"`
	UserID           int64     `json:"user_id"`
	Type             string    `json:"type"`
	ExpectedRevision int64     `json:"expected_revision"`
	Reason           string    `json:"reason"`
	IdempotencyKey   string    `json:"idempotency_key"`
	ActorID          int64     `json:"actor_id"`
}

type DistillationExemptionMutation struct {
	ID               int64
	UserID           int64
	StartsAt         time.Time
	ExpiresAt        time.Time
	Reason           string
	ExpectedRevision int64
	IdempotencyKey   string
	ActorID          int64
}

type DistillationRiskRepository interface {
	GetConfig(context.Context) (*DistillationRiskConfig, error)
	UpdateConfig(context.Context, string, json.RawMessage, int64, int64, time.Time) (*DistillationRiskConfig, error)
	ListSealedAggregates(context.Context, int64, time.Time, int) ([]DistillationWindowAggregate, error)
	RecordWindow(context.Context, DistillationWindowEvaluation, string, time.Time) error
	GetActiveRestriction(context.Context, int64, time.Time) (*DistillationRestriction, error)
	Overview(context.Context, time.Time) (*DistillationOverview, error)
	ListCases(context.Context, DistillationListOptions) (*DistillationCaseList, error)
	GetCase(context.Context, int64, time.Time) (*DistillationCaseDetail, error)
	ListWindows(context.Context, int64, int) ([]DistillationWindowEvaluation, error)
	ListEvents(context.Context, DistillationListOptions) (*DistillationEventList, error)
	ListExemptions(context.Context, DistillationListOptions, time.Time) (*DistillationExemptionList, error)
	CreateExemption(context.Context, DistillationExemptionMutation) (*DistillationExemption, error)
	RevokeExemption(context.Context, DistillationExemptionMutation) (*DistillationExemption, error)
	ApplyAction(context.Context, DistillationAction, time.Time) (*DistillationCaseDetail, error)
}

type DistillationRiskService struct {
	repo   DistillationRiskRepository
	now    func() time.Time
	stop   chan struct{}
	stopMu sync.Once
}

func NewDistillationRiskService(repo DistillationRiskRepository) *DistillationRiskService {
	return &DistillationRiskService{repo: repo, now: time.Now, stop: make(chan struct{})}
}

func DistillationWindowStart(at time.Time) time.Time {
	return at.UTC().Truncate(DistillationWindowSize)
}

func DistillationWindowSealAt(start time.Time) time.Time {
	return DistillationWindowStart(start).Add(DistillationWindowSize + DistillationAllowedLateness)
}

func unavailableDistillationSignal(reason string) DistillationSignalEvidence {
	return DistillationSignalEvidence{Available: false, Value: nil, Threshold: nil, Points: 0, Reason: reason}
}

func availableDistillationSignal(value, threshold float64, points int) DistillationSignalEvidence {
	v, t := value, threshold
	return DistillationSignalEvidence{Available: true, Value: &v, Threshold: &t, Points: points}
}

func (s *DistillationRiskService) EvaluateWindow(a DistillationWindowAggregate, ruleVersion int64, now time.Time) (DistillationWindowEvaluation, error) {
	return s.evaluateWindow(a, ruleVersion, now, DefaultDistillationScoring())
}
func (s *DistillationRiskService) evaluateWindow(a DistillationWindowAggregate, ruleVersion int64, now time.Time, scoring DistillationScoring) (DistillationWindowEvaluation, error) {
	start := DistillationWindowStart(a.WindowStart)
	e := DistillationWindowEvaluation{
		UserID: a.UserID, WindowStart: start, Cohort: a.Cohort, WindowEnd: start.Add(DistillationWindowSize),
		SealAt: DistillationWindowSealAt(start), RuleVersion: ruleVersion, WorkloadUnits: a.WorkloadUnits,
		RequestCount: a.RequestCount, TotalTokens: a.TotalTokens, InputTokens: a.InputTokens, OutputTokens: a.OutputTokens,
	}
	if now.UTC().Before(e.SealAt) {
		return e, fmt.Errorf("%w: window is not sealed", ErrDistillationInvalid)
	}
	// Cohorts are evaluated independently. Async/batch traffic is not merged
	// with interactive traffic; it may still be scored when its own telemetry
	// and token baseline are complete.
	a.StrongSignal = (a.ConcurrencyDurationEligible && a.ConcurrencySaturation != nil && *a.ConcurrencySaturation >= scoring.ConcurrencySaturation) || (a.RequestRateSaturation != nil && *a.RequestRateSaturation >= scoring.RequestRateSaturation) || a.TokenFamilyThroughputHit
	score := 0
	if a.TelemetryComplete && a.ConcurrencyDurationEligible && a.ConcurrencySaturation != nil && finiteDistillationValue(*a.ConcurrencySaturation) {
		points := 0
		if *a.ConcurrencySaturation >= scoring.ConcurrencySaturation {
			points = 20
		}
		e.Signals.ConcurrencySaturation = availableDistillationSignal(*a.ConcurrencySaturation, scoring.ConcurrencySaturation, points)
		score += points
	} else {
		e.Signals.ConcurrencySaturation = unavailableDistillationSignal("concurrency saturation duration telemetry unavailable")
	}
	if a.TelemetryComplete && a.PromptRepeatEligible && a.PromptRepeatRatio != nil && finiteDistillationValue(*a.PromptRepeatRatio) {
		points := 0
		if a.StrongSignal && *a.PromptRepeatRatio >= scoring.PromptRepeatRatio {
			points = 10
		}
		e.Signals.PromptRepeatRatio = availableDistillationSignal(*a.PromptRepeatRatio, scoring.PromptRepeatRatio, points)
		score += points
	} else {
		e.Signals.PromptRepeatRatio = unavailableDistillationSignal("retry-deduplicated prompt telemetry unavailable")
	}
	if a.WorkloadUnits < DistillationMinWorkloadUnits || a.TotalTokens < DistillationMinTokens {
		e.Status = "insufficient_data"
		return e, nil
	}

	if a.TelemetryComplete && a.RequestRateSaturation != nil && finiteDistillationValue(*a.RequestRateSaturation) {
		ratePoints := 0
		if *a.RequestRateSaturation >= scoring.RequestRateSaturation {
			ratePoints = 15
		}
		e.Signals.RequestRateSaturation = availableDistillationSignal(*a.RequestRateSaturation, scoring.RequestRateSaturation, ratePoints)
		score += ratePoints
	} else {
		e.Signals.RequestRateSaturation = unavailableDistillationSignal("active 10-second rate bucket telemetry unavailable")
	}

	if a.TelemetryComplete && a.TokenCohortP99 != nil && a.TokenBaselineSamples > 0 && finiteDistillationValue(*a.TokenCohortP99) {
		value := float64(a.TotalTokens)
		points := 0
		if a.TokenFamilyThroughputHit || (a.Cohort == "" && value >= 1_500_000 && value > *a.TokenCohortP99) {
			points = 20
		}
		e.Signals.TokenThroughput = availableDistillationSignal(value, *a.TokenCohortP99, points)
		score += points
	} else {
		e.Signals.TokenThroughput = unavailableDistillationSignal("cohort token p99 baseline unavailable")
	}

	if a.TelemetryComplete && a.DistinctProviders > 0 && a.RequestRateSaturation != nil && finiteDistillationValue(*a.RequestRateSaturation) {
		value := float64(a.DistinctModels)
		points := 0
		if *a.RequestRateSaturation >= scoring.RequestRateSaturation && a.DistinctModels >= 4 && a.DistinctProviders >= 2 {
			points = 10
		}
		e.Signals.ModelProviderBreadth = availableDistillationSignal(value, 4, points)
		score += points
	} else {
		e.Signals.ModelProviderBreadth = unavailableDistillationSignal("provider breadth or rate telemetry unavailable")
	}

	if a.TelemetryComplete && a.InterarrivalCount >= 100 && a.InterarrivalCV != nil && finiteDistillationValue(*a.InterarrivalCV) {
		points := 0
		if *a.InterarrivalCV <= scoring.InterarrivalCV && ((a.ConcurrencyDurationEligible && a.ConcurrencySaturation != nil && *a.ConcurrencySaturation >= scoring.ConcurrencySaturation) || (a.RequestRateSaturation != nil && *a.RequestRateSaturation >= scoring.RequestRateSaturation)) {
			points = 10
		}
		e.Signals.InterarrivalRegularity = availableDistillationSignal(*a.InterarrivalCV, scoring.InterarrivalCV, points)
		score += points
	} else {
		e.Signals.InterarrivalRegularity = unavailableDistillationSignal("at least 100 interval samples and a strong rate/concurrency signal required")
	}

	if !a.TelemetryComplete || a.InputTokens <= 0 {
		e.Signals.OutputInputRatio = unavailableDistillationSignal("input token denominator is zero")
	} else {
		ratio := float64(a.OutputTokens) / float64(a.InputTokens)
		ratioPoints := 0
		if a.OutputTokens >= 500_000 && ratio >= scoring.OutputInputRatio {
			ratioPoints = 15
		}
		e.Signals.OutputInputRatio = availableDistillationSignal(ratio, scoring.OutputInputRatio, ratioPoints)
		score += ratioPoints
	}

	e.Score = &score
	e.Status = "scored"
	return e, nil
}

func finiteDistillationValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func DistillationEvidenceComplete(signals DistillationSignals) bool {
	return signals.ConcurrencySaturation.Available &&
		signals.RequestRateSaturation.Available &&
		signals.TokenThroughput.Available &&
		signals.PromptRepeatRatio.Available &&
		signals.ModelProviderBreadth.Available &&
		signals.InterarrivalRegularity.Available &&
		signals.OutputInputRatio.Available
}

// AdvanceDistillationCase applies only eligible evidence. Insufficient or failed
// telemetry must never advance sanction or recovery streaks.
func AdvanceDistillationCase(current DistillationCase, score int, mode string, windowEnd, now time.Time) DistillationCase {
	if current.LastEligibleWindowEnd != nil && !windowEnd.Equal(current.LastEligibleWindowEnd.Add(DistillationWindowSize)) {
		current.SuspectedStreak, current.LimitedStreak, current.RecoveryStreak = 0, 0, 0
	}
	if current.State == "" {
		current.State = DistillationStateClear
		current.ReviewStatus = "none"
	}
	if score >= 75 {
		current.SuspectedStreak++
	} else {
		current.SuspectedStreak = 0
	}
	if score >= 85 {
		current.LimitedStreak++
	} else {
		current.LimitedStreak = 0
	}
	if score < 50 {
		current.RecoveryStreak++
	} else {
		current.RecoveryStreak = 0
	}

	switch {
	case current.RecoveryStreak >= 4:
		current.State, current.ReviewStatus = DistillationStateClear, "none"
	case current.LimitedStreak >= 3 && mode == DistillationModeEnforce:
		current.State, current.ReviewStatus = DistillationStateLimited, "pending"
	case current.SuspectedStreak >= 2:
		current.State, current.ReviewStatus = DistillationStateSuspected, "pending"
	case score >= 60 && current.State == DistillationStateClear:
		current.State = DistillationStateObserving
	}
	current.LastEligibleWindowEnd = &windowEnd
	current.LastScore = &score
	current.UpdatedAt = now
	return current
}

func (s *DistillationRiskService) ProcessSealedWindows(ctx context.Context, now time.Time) (resultErr error) {
	if s != nil && s.repo != nil {
		if health, ok := s.repo.(interface {
			RecordWorkerHealth(context.Context, time.Time, error) error
		}); ok {
			defer func() {
				healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = health.RecordWorkerHealth(healthCtx, now, resultErr)
			}()
		}
	}
	if s == nil || s.repo == nil {
		return ErrDistillationUnavailable
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDistillationUnavailable, err)
	}
	if cfg.Mode == DistillationModeOff {
		return nil
	}
	aggregates, err := s.repo.ListSealedAggregates(ctx, cfg.RuleVersion, now.UTC(), 1000)
	if err != nil {
		return fmt.Errorf("%w: aggregate telemetry: %v", ErrDistillationUnavailable, err)
	}
	scoring, err := ParseDistillationScoring(cfg.ScoringConfig)
	if err != nil {
		return err
	}
	for _, aggregate := range aggregates {
		evaluation, evalErr := s.evaluateWindow(aggregate, cfg.RuleVersion, now, scoring)
		if evalErr != nil {
			continue
		}
		if err := s.repo.RecordWindow(ctx, evaluation, cfg.Mode, now.UTC()); err != nil {
			return fmt.Errorf("%w: persist evaluation: %v", ErrDistillationUnavailable, err)
		}
	}
	return nil
}

func (s *DistillationRiskService) Start(interval time.Duration) {
	if s == nil || s.repo == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		_ = s.ProcessSealedWindows(context.Background(), s.now())
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), interval)
				_ = s.ProcessSealedWindows(ctx, s.now())
				cancel()
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *DistillationRiskService) Stop() {
	if s != nil {
		s.stopMu.Do(func() { close(s.stop) })
	}
}

func (s *DistillationRiskService) EffectiveLimits(ctx context.Context, userID int64, configuredConcurrency, configuredRPM int, now time.Time) (int, int, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return configuredConcurrency, configuredRPM, nil
	}
	restriction, err := s.repo.GetActiveRestriction(ctx, userID, now.UTC())
	if err != nil {
		return configuredConcurrency, configuredRPM, err
	}
	if restriction == nil {
		return configuredConcurrency, configuredRPM, nil
	}
	return distillationMinLimit(configuredConcurrency, restriction.ConcurrencyCap), distillationMinLimit(configuredRPM, restriction.RPMCap), nil
}

func distillationMinLimit(configured, cap int) int {
	if cap <= 0 {
		return configured
	}
	if configured <= 0 || cap < configured {
		return cap
	}
	return configured
}

func (s *DistillationRiskService) GetConfig(ctx context.Context) (*DistillationRiskConfig, error) {
	if s == nil || s.repo == nil {
		return nil, ErrDistillationUnavailable
	}
	return s.repo.GetConfig(ctx)
}

func (s *DistillationRiskService) UpdateConfig(ctx context.Context, mode string, scoring json.RawMessage, expectedRevision, actorID int64, now time.Time) (*DistillationRiskConfig, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != DistillationModeOff && mode != DistillationModeObserve && mode != DistillationModeEnforce || expectedRevision < 1 || actorID <= 0 || (len(scoring) > 0 && !json.Valid(scoring)) {
		return nil, ErrDistillationInvalid
	}
	if len(scoring) > 0 {
		if _, err := ParseDistillationScoring(scoring); err != nil {
			return nil, err
		}
	}
	current, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if current.Revision != expectedRevision {
		return nil, ErrDistillationRevisionConflict
	}
	if mode == DistillationModeEnforce && current.Mode != DistillationModeEnforce && current.Mode != DistillationModeObserve {
		return nil, ErrDistillationObservationShort
	}
	if mode == DistillationModeEnforce && now.UTC().Before(current.ObservationStartedAt.UTC().Add(DistillationObservationAge)) {
		return nil, ErrDistillationObservationShort
	}
	if len(scoring) == 0 {
		scoring = current.ScoringConfig
	}
	return s.repo.UpdateConfig(ctx, mode, scoring, expectedRevision, actorID, now.UTC())
}

func (s *DistillationRiskService) Overview(ctx context.Context, now time.Time) (*DistillationOverview, error) {
	return s.repo.Overview(ctx, now.UTC())
}
func (s *DistillationRiskService) ListCases(ctx context.Context, opts DistillationListOptions) (*DistillationCaseList, error) {
	return s.repo.ListCases(ctx, normalizeDistillationList(opts))
}
func (s *DistillationRiskService) GetCase(ctx context.Context, userID int64, now time.Time) (*DistillationCaseDetail, error) {
	if userID <= 0 {
		return nil, ErrDistillationInvalid
	}
	return s.repo.GetCase(ctx, userID, now.UTC())
}
func (s *DistillationRiskService) Trends(ctx context.Context, userID int64, limit int) ([]DistillationWindowEvaluation, error) {
	if userID <= 0 {
		return nil, ErrDistillationInvalid
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.repo.ListWindows(ctx, int64(userID), limit)
}
func (s *DistillationRiskService) ListEvents(ctx context.Context, opts DistillationListOptions) (*DistillationEventList, error) {
	return s.repo.ListEvents(ctx, normalizeDistillationList(opts))
}
func (s *DistillationRiskService) ListExemptions(ctx context.Context, opts DistillationListOptions, now time.Time) (*DistillationExemptionList, error) {
	return s.repo.ListExemptions(ctx, normalizeDistillationList(opts), now.UTC())
}

func normalizeDistillationList(opts DistillationListOptions) DistillationListOptions {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize < 1 || opts.PageSize > 100 {
		opts.PageSize = 20
	}
	return opts
}

func (s *DistillationRiskService) CreateExemption(ctx context.Context, m DistillationExemptionMutation) (*DistillationExemption, error) {
	if m.UserID <= 0 || m.ActorID <= 0 || strings.TrimSpace(m.IdempotencyKey) == "" || strings.TrimSpace(m.Reason) == "" || !m.ExpiresAt.After(m.StartsAt) || m.ExpiresAt.Sub(m.StartsAt) > DistillationMaxExemption {
		return nil, ErrDistillationInvalid
	}
	return s.repo.CreateExemption(ctx, m)
}

func (s *DistillationRiskService) RevokeExemption(ctx context.Context, m DistillationExemptionMutation) (*DistillationExemption, error) {
	if m.ID <= 0 || m.ExpectedRevision < 1 || m.ActorID <= 0 || strings.TrimSpace(m.IdempotencyKey) == "" {
		return nil, ErrDistillationInvalid
	}
	return s.repo.RevokeExemption(ctx, m)
}

func (s *DistillationRiskService) ApplyAction(ctx context.Context, action DistillationAction, now time.Time) (*DistillationCaseDetail, error) {
	action.Type = strings.ToLower(strings.TrimSpace(action.Type))
	if action.UserID <= 0 || action.ExpectedRevision < 1 || action.ActorID <= 0 || strings.TrimSpace(action.IdempotencyKey) == "" || (action.Type != "in_review" && action.Type != "confirm" && action.Type != "dismiss" && action.Type != "restrict" && action.Type != "revoke") {
		return nil, ErrDistillationInvalid
	}
	if action.Type == "restrict" && (strings.TrimSpace(action.Reason) == "" || !action.ExpiresAt.After(now) || action.ExpiresAt.After(now.Add(24*time.Hour))) {
		return nil, ErrDistillationInvalid
	}
	return s.repo.ApplyAction(ctx, action, now.UTC())
}
