//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAITransportFailureImmediatelyFencesDurableExecution(t *testing.T) {
	cache := &executionBudgetCacheStub{}
	repo := &executionBudgetTelemetryStub{}
	ctx := context.WithValue(context.Background(), modelExecutionBudgetContextKey{}, &modelExecutionBudgetContext{
		cache: cache, telemetry: &DistillationTelemetryService{repo: repo}, userID: 42, operationID: "op",
		lease: &ModelExecutionLease{Revision: 2, Epoch: 1}, attempts: []string{"sent"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test/responses", nil)
	require.NoError(t, err)
	transportErr := errors.New("connection reset after write")
	upstream := &failingOpenAIHTTPUpstream{err: transportErr}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	_, err = svc.doOpenAIUpstream(req, "", &Account{ID: 1})
	require.ErrorIs(t, err, transportErr)
	require.Equal(t, []string{"unknown"}, repo.states)
	require.Equal(t, 1, cache.unknown)
	CompleteModelExecution(ctx, true)
	require.Zero(t, cache.released)
}
