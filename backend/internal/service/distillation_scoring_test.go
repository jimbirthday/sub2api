package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestDistillationScoringValidationAndConsumption(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"unknown":1}`, `{"concurrency_saturation":0}`, `{"output_input_ratio":101}`} {
		_, err := ParseDistillationScoring(json.RawMessage(raw))
		require.ErrorIs(t, err, ErrDistillationInvalid)
	}
	cfg, err := ParseDistillationScoring(json.RawMessage(`{"concurrency_saturation":0.8}`))
	require.NoError(t, err)
	saturation := 0.6
	at := DistillationWindowStart(time.Now().UTC()).Add(-time.Hour)
	a := DistillationWindowAggregate{WindowStart: at, WorkloadUnits: 200, TotalTokens: 100000, TelemetryComplete: true, ConcurrencyDurationEligible: true, ConcurrencySaturation: &saturation}
	svc := NewDistillationRiskService(nil)
	initial, err := svc.EvaluateWindow(a, 1, time.Now())
	require.NoError(t, err)
	require.Equal(t, 20, initial.Signals.ConcurrencySaturation.Points)
	tuned, err := svc.evaluateWindow(a, 2, time.Now(), cfg)
	require.NoError(t, err)
	require.Zero(t, tuned.Signals.ConcurrencySaturation.Points)
	require.Equal(t, 0.8, *tuned.Signals.ConcurrencySaturation.Threshold)
}
