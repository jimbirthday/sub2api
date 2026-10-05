package handler

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Completion belongs to one connection, not to the worker pool. Closing done
// publishes err to the next request, including when a worker recovers a panic.
type openAIWSSettlement struct {
	done chan struct{}
	err  error
}

type openAIWSSettlementBarrier struct {
	mu   sync.Mutex
	last *openAIWSSettlement
}

func (b *openAIWSSettlementBarrier) begin() *openAIWSSettlement {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &openAIWSSettlement{done: make(chan struct{}), err: service.ErrBillingServiceUnavailable}
	b.last = s
	return s
}

func (b *openAIWSSettlementBarrier) wait(ctx context.Context) error {
	b.mu.Lock()
	s := b.last
	b.mu.Unlock()
	if s == nil {
		return nil
	}
	select {
	case <-s.done:
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *OpenAIGatewayHandler) submitWebSocketUsageRecordTask(parent context.Context, barrier *openAIWSSettlementBarrier, task func(context.Context) error) {
	settlement := barrier.begin()
	h.submitMandatoryUsageRecordTask(parent, func(ctx context.Context) {
		defer close(settlement.done)
		settlement.err = task(ctx)
	})
}

type openAIWSBillingSnapshot struct {
	key          *service.APIKey
	subscription *service.UserSubscription
}
