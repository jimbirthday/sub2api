package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrDistillationAttemptNotFound   = errors.New("distillation telemetry attempt not found")
	ErrDistillationAttemptConflict   = errors.New("distillation telemetry attempt conflict")
	ErrDistillationOperationConflict = errors.New("distillation operation intent conflict")
	ErrDistillationTelemetryDropped  = errors.New("distillation telemetry event dropped")
)

type DistillationAttempt struct {
	AttemptID             uuid.UUID
	UserID                int64
	APIKeyID              *int64
	OperationID           string
	Provider              string
	Model                 string
	PromptHMAC            []byte
	HMACDay               *time.Time
	ConfiguredConcurrency int
	ConfiguredRPM         int
	ObservedConcurrency   int
	Cohort                string
	StartedAt             time.Time
}

type DistillationAttemptCompletion struct {
	AttemptID         uuid.UUID
	UserID            int64
	WindowStart       time.Time
	CompletedAt       time.Time
	InputTokens       *int64
	OutputTokens      *int64
	Status            string
	TelemetryComplete bool
}

type DistillationOperationIntent struct {
	OperationID string    `json:"operation_id"`
	UserID      int64     `json:"user_id"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
	Targets     []string  `json:"targets"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DistillationTelemetryRepository interface {
	CreateAttempt(context.Context, DistillationAttempt) error
	CompleteAttempt(context.Context, DistillationAttemptCompletion) error
	MarkTelemetryGap(context.Context, int64, time.Time, string) error
	CreateOperationIntent(context.Context, DistillationOperationIntent) (*DistillationOperationIntent, error)
	TransitionOperationIntent(context.Context, string, int64, string, time.Time) (*DistillationOperationIntent, error)
	UpdateOperationIntentTargets(context.Context, string, int64, []string, time.Time) (*DistillationOperationIntent, error)
	GetOperationIntent(context.Context, string) (*DistillationOperationIntent, error)
	ReconcileActiveOperations(context.Context, int) error
}

type DistillationTelemetryService struct {
	repo            DistillationTelemetryRepository
	hmacSecret      []byte
	queue           chan telemetryEvent
	stop            chan struct{}
	workers         sync.WaitGroup
	stopOnce        sync.Once
	startOnce       sync.Once
	stopped         atomic.Bool
	enqueueMu       sync.RWMutex
	recoveryMu      sync.Mutex
	recoveryStarted bool
}

type telemetryEvent struct {
	completion *DistillationAttemptCompletion
	gapUserID  int64
	gapWindow  time.Time
	gapReason  string
}

func NewDistillationTelemetryService(repo DistillationTelemetryRepository, hmacSecret string, queueSize int) *DistillationTelemetryService {
	if queueSize <= 0 {
		queueSize = 4096
	}
	s := &DistillationTelemetryService{repo: repo, hmacSecret: []byte(hmacSecret), queue: make(chan telemetryEvent, queueSize), stop: make(chan struct{})}
	return s
}

func (s *DistillationTelemetryService) Start(workers int) {
	if s == nil || s.repo == nil {
		return
	}
	if workers <= 0 {
		workers = 4
	}
	s.startOnce.Do(func() {
		for i := 0; i < workers; i++ {
			s.workers.Add(1)
			go s.run()
		}
		if maintenance, ok := s.repo.(interface {
			ReconcileActiveOperations(context.Context, int) error
			CleanupDailyHMAC(context.Context, time.Time) (int64, error)
		}); ok {
			s.workers.Add(1)
			go s.runMaintenance(maintenance)
		}
	})
}

func (s *DistillationTelemetryService) runMaintenance(maintenance interface {
	ReconcileActiveOperations(context.Context, int) error
	CleanupDailyHMAC(context.Context, time.Time) (int64, error)
}) {
	defer s.workers.Done()
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := maintenance.ReconcileActiveOperations(ctx, 1000); err != nil {
			slog.Error("distillation operation reconciliation failed", "error", err)
		}
		if _, err := maintenance.CleanupDailyHMAC(ctx, time.Now().UTC()); err != nil {
			slog.Error("distillation HMAC cleanup failed", "error", err)
		}
		if retention, ok := s.repo.(interface {
			CleanupModelAccessHistory(context.Context, time.Time) error
		}); ok {
			if err := retention.CleanupModelAccessHistory(ctx, time.Now().UTC()); err != nil {
				slog.Error("model access history cleanup failed", "error", err)
			}
		}
	}
	run()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			run()
		case <-s.stop:
			return
		}
	}
}

func (s *DistillationTelemetryService) run() {
	defer s.workers.Done()
	for {
		select {
		case e := <-s.queue:
			s.persistEvent(e)
		case <-s.stop:
			for {
				select {
				case e := <-s.queue:
					s.persistEvent(e)
				default:
					return
				}
			}
		}
	}
}

func (s *DistillationTelemetryService) persistEvent(e telemetryEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e.completion != nil {
		if err := s.repo.CompleteAttempt(ctx, *e.completion); err != nil {
			slog.Error("distillation telemetry completion failed", "attempt_id", e.completion.AttemptID, "error", err)
			gapCtx, gapCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.repo.MarkTelemetryGap(gapCtx, e.completion.UserID, e.completion.WindowStart, "completion_write_failed")
			gapCancel()
		}
	} else if e.gapUserID > 0 {
		_ = s.repo.MarkTelemetryGap(ctx, e.gapUserID, e.gapWindow, e.gapReason)
	}
}

func (s *DistillationTelemetryService) Stop() {
	if s != nil {
		s.stopOnce.Do(func() {
			s.enqueueMu.Lock()
			s.stopped.Store(true)
			close(s.stop)
			s.enqueueMu.Unlock()
			s.workers.Wait()
		})
	}
}

func (s *DistillationTelemetryService) BeginAttempt(ctx context.Context, userID int64, apiKeyID *int64, operationID, provider, model, cohort string, configuredConcurrency, configuredRPM, observedConcurrency int, promptMaterial []byte, at time.Time) (DistillationAttempt, error) {
	if s == nil || s.repo == nil || userID <= 0 || observedConcurrency <= 0 || strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return DistillationAttempt{}, ErrDistillationInvalid
	}
	if operationID != "" && (len(operationID) > 128 || strings.ContainsAny(operationID, "\r\n")) {
		return DistillationAttempt{}, ErrDistillationInvalid
	}
	at = at.UTC()
	cohort = strings.TrimSpace(cohort)
	if cohort == "" {
		cohort = "interactive"
	}
	if cohort != "interactive" && cohort != "async_batch" {
		return DistillationAttempt{}, ErrDistillationInvalid
	}
	attempt := DistillationAttempt{AttemptID: uuid.New(), UserID: userID, APIKeyID: apiKeyID, OperationID: operationID, Provider: strings.TrimSpace(provider), Model: strings.TrimSpace(model), Cohort: cohort, ConfiguredConcurrency: configuredConcurrency, ConfiguredRPM: configuredRPM, ObservedConcurrency: observedConcurrency, StartedAt: at}
	if len(promptMaterial) > 0 && len(s.hmacSecret) > 0 {
		day := at.Truncate(24 * time.Hour)
		digest := s.PromptFingerprint(userID, promptMaterial, day)
		attempt.PromptHMAC = digest
		attempt.HMACDay = &day
	}
	if err := s.repo.CreateAttempt(ctx, attempt); err != nil {
		return DistillationAttempt{}, err
	}
	return attempt, nil
}

func (s *DistillationTelemetryService) CompleteAttempt(attempt DistillationAttempt, inputTokens, outputTokens *int64, status string, complete bool, at time.Time) error {
	if attempt.AttemptID == uuid.Nil || status != "completed" && status != "failed" && status != "abandoned" {
		return ErrDistillationInvalid
	}
	s.enqueueMu.RLock()
	if s.stopped.Load() {
		s.enqueueMu.RUnlock()
		s.persistGapBounded(attempt.UserID, attempt.StartedAt, "telemetry_stopped")
		return ErrDistillationTelemetryDropped
	}
	e := telemetryEvent{completion: &DistillationAttemptCompletion{AttemptID: attempt.AttemptID, UserID: attempt.UserID, WindowStart: DistillationWindowStart(attempt.StartedAt), CompletedAt: at.UTC(), InputTokens: inputTokens, OutputTokens: outputTokens, Status: status, TelemetryComplete: complete}}
	select {
	case s.queue <- e:
		s.enqueueMu.RUnlock()
		return nil
	default:
		s.enqueueMu.RUnlock()
		s.persistGapBounded(attempt.UserID, attempt.StartedAt, "telemetry_queue_overflow")
		return ErrDistillationTelemetryDropped
	}
}

func (s *DistillationTelemetryService) MarkGap(userID int64, windowStart time.Time, reason string) error {
	if userID <= 0 || strings.TrimSpace(reason) == "" {
		return ErrDistillationInvalid
	}
	s.enqueueMu.RLock()
	if s.stopped.Load() {
		s.enqueueMu.RUnlock()
		s.persistGapBounded(userID, windowStart, "telemetry_stopped_"+reason)
		return ErrDistillationTelemetryDropped
	}
	e := telemetryEvent{gapUserID: userID, gapWindow: DistillationWindowStart(windowStart), gapReason: reason}
	select {
	case s.queue <- e:
		s.enqueueMu.RUnlock()
		return nil
	default:
		s.enqueueMu.RUnlock()
		s.persistGapBounded(userID, windowStart, "gap_queue_overflow_"+reason)
		return ErrDistillationTelemetryDropped
	}
}

func (s *DistillationTelemetryService) persistGapBounded(userID int64, window time.Time, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.repo.MarkTelemetryGap(ctx, userID, DistillationWindowStart(window), reason); err != nil {
		slog.Error("distillation telemetry gap persistence failed", "user_id", userID, "reason", reason, "error", err)
	}
}

// PromptFingerprint never stores prompt bytes. The day key prevents a digest
// from being used as a stable cross-day identifier.
func (s *DistillationTelemetryService) PromptFingerprint(userID int64, material []byte, day time.Time) []byte {
	dayString := day.UTC().Format("2006-01-02")
	keyMAC := hmac.New(sha256.New, s.hmacSecret)
	_, _ = keyMAC.Write([]byte("sub2api/distillation/" + dayString))
	key := keyMAC.Sum(nil)
	h := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(h, "%d:", userID)
	_, _ = h.Write(material)
	return h.Sum(nil)
}

func (s *DistillationTelemetryService) PromptFingerprintHex(userID int64, material []byte, day time.Time) string {
	return hex.EncodeToString(s.PromptFingerprint(userID, material, day))
}

// DistillationPromptMaterial extracts prompt-bearing JSON fields and encodes
// them deterministically. Transport flags and retry metadata do not affect an
// exact-repeat fingerprint.
func DistillationPromptMaterial(body []byte) []byte {
	var request map[string]any
	if len(body) == 0 || json.Unmarshal(body, &request) != nil {
		return nil
	}
	keys := []string{"input", "messages", "prompt", "contents", "instructions", "system", "tools"}
	prompt := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := request[key]; ok {
			prompt[key] = value
		}
	}
	if len(prompt) == 0 {
		return nil
	}
	encoded, err := json.Marshal(prompt)
	if err != nil {
		return nil
	}
	return encoded
}

func (s *DistillationTelemetryService) CreateOperationIntent(ctx context.Context, operationID string, userID int64, kind string, targets []string) (*DistillationOperationIntent, error) {
	if s == nil || s.repo == nil || userID <= 0 || strings.TrimSpace(operationID) == "" || len(operationID) > 128 || strings.TrimSpace(kind) == "" || len(targets) == 0 {
		return nil, ErrDistillationInvalid
	}
	seen := make(map[string]struct{}, len(targets))
	normalized := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			return nil, ErrDistillationInvalid
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		normalized = append(normalized, target)
	}
	return s.repo.CreateOperationIntent(ctx, DistillationOperationIntent{OperationID: operationID, UserID: userID, Kind: kind, Targets: normalized})
}

func (s *DistillationTelemetryService) TransitionOperationIntent(ctx context.Context, operationID string, expectedRevision int64, state string, now time.Time) (*DistillationOperationIntent, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(operationID) == "" || expectedRevision < 1 {
		return nil, ErrDistillationInvalid
	}
	switch state {
	case "running", "completed", "cancelled", "reconcile_required":
	default:
		return nil, ErrDistillationInvalid
	}
	return s.repo.TransitionOperationIntent(ctx, operationID, expectedRevision, state, now.UTC())
}

func (s *DistillationTelemetryService) GetOperationIntent(ctx context.Context, operationID string) (*DistillationOperationIntent, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(operationID) == "" {
		return nil, ErrDistillationInvalid
	}
	return s.repo.GetOperationIntent(ctx, operationID)
}

func (s *DistillationTelemetryService) UpdateOperationIntentTargets(ctx context.Context, operationID string, expectedRevision int64, targets []string, now time.Time) (*DistillationOperationIntent, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(operationID) == "" || expectedRevision < 1 || len(targets) == 0 {
		return nil, ErrDistillationInvalid
	}
	return s.repo.UpdateOperationIntentTargets(ctx, operationID, expectedRevision, targets, now.UTC())
}

// ExecutionAttempt records durable ownership immediately before a provider write.
type ExecutionAttempt struct {
	OperationID, AttemptID, Provider, PayloadHash, State, FundsState string
	UserID, AccountID, OwnerEpoch                                    int64
	PolicyRevision, CatalogRevision                                  int64
	OwnerID, HoldID                                                  string
	Targets                                                          []string
}
type executionAttemptStore interface {
	CreateExecutionAttempt(context.Context, ExecutionAttempt) error
	TransitionExecutionAttempt(context.Context, string, string, int64) error
	ReconcileExecutionAttempts(context.Context, time.Time) error
}

func (s *DistillationTelemetryService) CreateExecutionAttempt(ctx context.Context, a ExecutionAttempt) error {
	if s == nil || s.repo == nil {
		return ErrModelExecutionBudgetUnavailable
	}
	st, ok := s.repo.(executionAttemptStore)
	if !ok {
		return nil
	}
	return st.CreateExecutionAttempt(ctx, a)
}
func (s *DistillationTelemetryService) TransitionExecutionAttempt(ctx context.Context, id, state string, epoch int64) error {
	if s == nil || s.repo == nil {
		return ErrModelExecutionBudgetUnavailable
	}
	st, ok := s.repo.(executionAttemptStore)
	if !ok {
		return nil
	}
	return st.TransitionExecutionAttempt(ctx, id, state, epoch)
}

func (s *DistillationTelemetryService) RecoverExecutionBudgets(ctx context.Context, cache ModelExecutionBudgetCache) error {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if s.recoveryStarted {
		return nil
	}
	if err := s.recoverExecutionBudgets(ctx, cache); err != nil {
		return err
	}
	s.recoveryStarted = true
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				recoveryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				if err := s.recoverExecutionBudgets(recoveryCtx, cache); err != nil {
					slog.Error("execution budget recovery failed", "error", err)
				}
				cancel()
			}
		}
	}()
	return nil
}

// StartExecutionRecovery also reconciles old observation leases when no new
// request needs an enforced model budget. Recovery cannot gate observation.
func (s *DistillationTelemetryService) StartExecutionRecovery(cache ModelExecutionBudgetCache) {
	if s == nil || cache == nil {
		return
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := s.RecoverExecutionBudgets(ctx, cache)
			cancel()
			if err == nil {
				return
			}
			slog.Error("execution budget startup recovery failed", "error", err)
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *DistillationTelemetryService) recoverExecutionBudgets(ctx context.Context, cache ModelExecutionBudgetCache) error {
	if st, ok := s.repo.(interface {
		RecoverExecutionBudgets(context.Context, ModelExecutionBudgetCache) error
	}); ok {
		return st.RecoverExecutionBudgets(ctx, cache)
	}
	return nil
}

func (s *DistillationTelemetryService) ConfirmExecutionOperationTerminated(ctx context.Context, userID int64, operationID string) error {
	if s == nil {
		return ErrModelExecutionBudgetUnavailable
	}
	st, ok := s.repo.(interface {
		ConfirmExecutionOperationTerminated(context.Context, int64, string) error
	})
	if !ok {
		return ErrModelExecutionBudgetUnavailable
	}
	return st.ConfirmExecutionOperationTerminated(ctx, userID, operationID)
}
