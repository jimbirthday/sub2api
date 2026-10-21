package service

import (
	"context"
	"github.com/google/uuid"
	"sync/atomic"
	"time"
)

type distillationTelemetryContextKey struct{}
type distillationTelemetryRequestContextKey struct{}

type DistillationTelemetryRequest struct {
	UserID                int64
	APIKeyID              *int64
	OperationID           string
	Provider              string
	Model                 string
	Cohort                string
	ConfiguredConcurrency int
	ConfiguredRPM         int
	ObservedConcurrency   int
	PromptMaterial        []byte
}

func WithDistillationTelemetry(ctx context.Context, telemetry *DistillationTelemetryService) context.Context {
	if telemetry == nil {
		return ctx
	}
	return context.WithValue(ctx, distillationTelemetryContextKey{}, telemetry)
}

func PrepareDistillationTelemetry(ctx context.Context, request DistillationTelemetryRequest) context.Context {
	return context.WithValue(ctx, distillationTelemetryRequestContextKey{}, request)
}
func BeginPreparedDistillationAttempt(ctx context.Context, observed int, at time.Time) (context.Context, error) {
	request, ok := ctx.Value(distillationTelemetryRequestContextKey{}).(DistillationTelemetryRequest)
	if !ok {
		return ctx, nil
	}
	if observed <= 0 {
		observed = 1
	}
	request.ObservedConcurrency = observed
	return BeginDistillationAttempt(ctx, request, at)
}

func BeginDistillationAttempt(ctx context.Context, request DistillationTelemetryRequest, at time.Time) (context.Context, error) {
	telemetry, _ := ctx.Value(distillationTelemetryContextKey{}).(*DistillationTelemetryService)
	if telemetry == nil {
		return ctx, nil
	}
	attempt, err := telemetry.BeginAttempt(ctx, request.UserID, request.APIKeyID, request.OperationID, request.Provider, request.Model, request.Cohort, request.ConfiguredConcurrency, request.ConfiguredRPM, request.ObservedConcurrency, request.PromptMaterial, at)
	if err != nil {
		_ = telemetry.MarkGap(request.UserID, at, "attempt_start_failed")
		return ctx, err
	}
	return context.WithValue(ctx, distillationAttemptContextKey{}, &distillationAttemptState{attempt: attempt}), nil
}

type distillationAttemptContextKey struct{}

type distillationAttemptState struct {
	attempt   DistillationAttempt
	completed atomic.Bool
}

func CarryDistillationTelemetry(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent == nil {
		return base
	}
	if telemetry, ok := parent.Value(distillationTelemetryContextKey{}).(*DistillationTelemetryService); ok && telemetry != nil {
		base = context.WithValue(base, distillationTelemetryContextKey{}, telemetry)
	}
	if state, ok := parent.Value(distillationAttemptContextKey{}).(*distillationAttemptState); ok && state != nil && state.attempt.AttemptID != uuid.Nil {
		base = context.WithValue(base, distillationAttemptContextKey{}, state)
	}
	if request, ok := parent.Value(distillationTelemetryRequestContextKey{}).(DistillationTelemetryRequest); ok {
		base = context.WithValue(base, distillationTelemetryRequestContextKey{}, request)
	}
	if admission, ok := parent.Value(userModelRestrictionAdmissionKey{}).(userModelRestrictionAdmission); ok {
		base = context.WithValue(base, userModelRestrictionAdmissionKey{}, admission)
	}
	if identity, ok := parent.Value(modelIdentityContextKey{}).(ModelIdentityContext); ok {
		base = context.WithValue(base, modelIdentityContextKey{}, identity)
	}
	return CarryModelExecutionBudget(parent, base)
}

func CompleteDistillationAttempt(ctx context.Context, inputTokens, outputTokens *int64, status string, complete bool, at time.Time) error {
	telemetry, _ := ctx.Value(distillationTelemetryContextKey{}).(*DistillationTelemetryService)
	state, _ := ctx.Value(distillationAttemptContextKey{}).(*distillationAttemptState)
	if telemetry == nil || state == nil || state.attempt.AttemptID == uuid.Nil {
		return nil
	}
	if !state.completed.CompareAndSwap(false, true) {
		return nil
	}
	if err := telemetry.CompleteAttempt(state.attempt, inputTokens, outputTokens, status, complete, at); err != nil {
		return err
	}
	return nil
}
