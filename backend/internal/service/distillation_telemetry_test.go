package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type telemetryRepoStub struct {
	DistillationTelemetryRepository
	mu          sync.Mutex
	attempts    []DistillationAttempt
	completions []DistillationAttemptCompletion
	gaps        int
	completeErr error
	intent      *DistillationOperationIntent
}

func (s *telemetryRepoStub) CreateAttempt(_ context.Context, a DistillationAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
	return nil
}
func (s *telemetryRepoStub) CompleteAttempt(_ context.Context, c DistillationAttemptCompletion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completions = append(s.completions, c)
	return s.completeErr
}
func (s *telemetryRepoStub) MarkTelemetryGap(context.Context, int64, time.Time, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gaps++
	return nil
}
func (s *telemetryRepoStub) CreateOperationIntent(_ context.Context, i DistillationOperationIntent) (*DistillationOperationIntent, error) {
	i.Revision = 1
	s.intent = &i
	return &i, nil
}

func (s *telemetryRepoStub) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.attempts), len(s.completions), s.gaps
}

func TestDistillationPromptFingerprintIsUserAndDayScoped(t *testing.T) {
	s := NewDistillationTelemetryService(&telemetryRepoStub{}, "secret", 1)
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	a := s.PromptFingerprintHex(1, []byte("private prompt"), day)
	if a == s.PromptFingerprintHex(2, []byte("private prompt"), day) {
		t.Fatal("fingerprint must be user scoped")
	}
	if a == s.PromptFingerprintHex(1, []byte("private prompt"), day.Add(24*time.Hour)) {
		t.Fatal("fingerprint must rotate daily")
	}
	if len(a) != 64 {
		t.Fatalf("digest length=%d", len(a))
	}
}

func TestDistillationPromptMaterialIgnoresTransportMetadata(t *testing.T) {
	a := DistillationPromptMaterial([]byte(`{"model":"gpt","stream":true,"metadata":{"retry":1},"input":"hello"}`))
	b := DistillationPromptMaterial([]byte(`{"input":"hello","stream":false,"model":"gpt","metadata":{"retry":2}}`))
	if string(a) != string(b) || string(a) != `{"input":"hello"}` {
		t.Fatalf("a=%s b=%s", a, b)
	}
	if got := DistillationPromptMaterial([]byte(`{"model":"gpt"}`)); got != nil {
		t.Fatalf("non-prompt body produced material: %s", got)
	}
}

func TestDistillationBeginAttemptStoresDigestOnly(t *testing.T) {
	repo := &telemetryRepoStub{}
	s := NewDistillationTelemetryService(repo, "secret", 1)
	attempt, err := s.BeginAttempt(context.Background(), 7, nil, "", "openai", "gpt", "interactive", 10, 60, 4, []byte("raw secret"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	attempts, _, _ := repo.counts()
	if attempts != 1 || len(attempt.PromptHMAC) != 32 {
		t.Fatal("expected keyed digest")
	}
	if string(attempt.PromptHMAC) == "raw secret" {
		t.Fatal("raw prompt was retained")
	}
}

func TestDistillationCompletionQueueOverflowMarksGap(t *testing.T) {
	repo := &telemetryRepoStub{}
	s := NewDistillationTelemetryService(repo, "secret", 1)
	a := DistillationAttempt{AttemptID: [16]byte{1}, UserID: 7, StartedAt: time.Now()}
	if err := s.CompleteAttempt(a, nil, nil, "failed", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(a, nil, nil, "failed", false, time.Now()); !errors.Is(err, ErrDistillationTelemetryDropped) {
		t.Fatalf("got %v", err)
	}
	_, _, gaps := repo.counts()
	if gaps != 1 {
		t.Fatalf("gaps=%d", gaps)
	}
}

func TestDistillationTelemetryStopDrainsCompletions(t *testing.T) {
	repo := &telemetryRepoStub{}
	s := NewDistillationTelemetryService(repo, "secret", 4)
	s.Start(1)
	a := DistillationAttempt{AttemptID: [16]byte{1}, UserID: 7, StartedAt: time.Now()}
	if err := s.CompleteAttempt(a, nil, nil, "failed", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	_, completions, gaps := repo.counts()
	if completions != 1 || gaps != 0 {
		t.Fatalf("completions=%d gaps=%d", completions, gaps)
	}
}

func TestDistillationCompletionFailureMarksGap(t *testing.T) {
	repo := &telemetryRepoStub{completeErr: errors.New("write failed")}
	s := NewDistillationTelemetryService(repo, "secret", 4)
	s.Start(1)
	a := DistillationAttempt{AttemptID: [16]byte{1}, UserID: 7, StartedAt: time.Now()}
	if err := s.CompleteAttempt(a, nil, nil, "failed", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	_, completions, gaps := repo.counts()
	if completions != 1 || gaps != 1 {
		t.Fatalf("completions=%d gaps=%d", completions, gaps)
	}
}

func TestDistillationContextCompletionIsIdempotent(t *testing.T) {
	repo := &telemetryRepoStub{}
	s := NewDistillationTelemetryService(repo, "secret", 4)
	s.Start(1)
	ctx := WithDistillationTelemetry(context.Background(), s)
	ctx, err := BeginDistillationAttempt(ctx, DistillationTelemetryRequest{
		UserID: 7, Provider: "openai", Model: "gpt", ObservedConcurrency: 1,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteDistillationAttempt(ctx, nil, nil, "failed", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := CompleteDistillationAttempt(ctx, nil, nil, "failed", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	_, completions, _ := repo.counts()
	if completions != 1 {
		t.Fatalf("completions=%d", completions)
	}
}

func TestDistillationCompletionAfterStopMarksGap(t *testing.T) {
	repo := &telemetryRepoStub{}
	s := NewDistillationTelemetryService(repo, "secret", 1)
	s.Start(1)
	s.Stop()
	a := DistillationAttempt{AttemptID: [16]byte{1}, UserID: 7, StartedAt: time.Now()}
	if err := s.CompleteAttempt(a, nil, nil, "failed", false, time.Now()); !errors.Is(err, ErrDistillationTelemetryDropped) {
		t.Fatalf("got %v", err)
	}
	_, _, gaps := repo.counts()
	if gaps != 1 {
		t.Fatalf("gaps=%d", gaps)
	}
}

func TestDistillationOperationIntentValidation(t *testing.T) {
	repo := &telemetryRepoStub{}
	s := NewDistillationTelemetryService(repo, "secret", 1)
	got, err := s.CreateOperationIntent(context.Background(), "batch-1", 3, "batch", []string{"model-a", "model-b", "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 2 || got.Revision != 1 {
		t.Fatalf("%+v", got)
	}
}
