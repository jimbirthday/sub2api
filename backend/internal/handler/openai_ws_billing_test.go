package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type wsAuthoritativeKeyRepo struct {
	service.APIKeyRepository
	mu  sync.Mutex
	key *service.APIKey
	err error
}

type wsUsageBillingRepo struct {
	service.UsageBillingRepository
}

func (*wsUsageBillingRepo) Apply(context.Context, *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func (r *wsAuthoritativeKeyRepo) GetByKeyForAuth(context.Context, string) (*service.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	key := *r.key
	user := *key.User
	key.User = &user
	if key.Group != nil {
		group := *key.Group
		key.Group = &group
	}
	return &key, nil
}

func configureWSAuthoritativeKey(h *OpenAIGatewayHandler, key *service.APIKey, cfg *config.Config) *wsAuthoritativeKeyRepo {
	key.Status = service.StatusActive
	key.UserID = key.User.ID
	key.Key = "sk-ws-authoritative-test"
	key.User.Concurrency = 1
	if key.Group == nil && key.GroupID != nil {
		key.Group = &service.Group{ID: *key.GroupID, Status: service.StatusActive, Platform: service.PlatformOpenAI, RateMultiplier: 1}
	}
	repo := &wsAuthoritativeKeyRepo{key: key}
	h.apiKeyService = service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	return repo
}

func TestOpenAIWSSettlementBarrier(t *testing.T) {
	var b openAIWSSettlementBarrier
	require.NoError(t, b.wait(context.Background()))
	s := b.begin()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, b.wait(ctx), context.DeadlineExceeded)
	s.err = nil
	close(s.done)
	require.NoError(t, b.wait(context.Background()))
	s = b.begin()
	close(s.done) // panic/failure before setting success must fail closed
	require.ErrorIs(t, b.wait(context.Background()), service.ErrBillingServiceUnavailable)
}

func TestSubmitWebSocketUsageRecordTask(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			var b openAIWSSettlementBarrier
			h := &OpenAIGatewayHandler{}
			parent, cancel := context.WithCancel(context.Background())
			cancel()
			h.submitWebSocketUsageRecordTask(parent, &b, func(ctx context.Context) error {
				require.NoError(t, ctx.Err(), "accepted usage must survive client disconnect")
				switch outcome {
				case "failure":
					return service.ErrBillingServiceUnavailable
				case "panic":
					panic("billing failed")
				}
				return nil
			})
			if outcome == "success" {
				require.NoError(t, b.wait(context.Background()))
			} else {
				require.ErrorIs(t, b.wait(context.Background()), service.ErrBillingServiceUnavailable)
			}
		})
	}
	for _, overflow := range []bool{false, true} {
		t.Run(map[bool]string{true: "overflow-fallback", false: "queued-settlement"}[overflow], func(t *testing.T) {
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, TaskTimeout: time.Second, OverflowPolicy: "drop"})
			h := &OpenAIGatewayHandler{usageRecordWorkerPool: pool}
			blocked, release := make(chan struct{}), make(chan struct{})
			pool.Submit(func(context.Context) { close(blocked); <-release })
			<-blocked
			defer pool.Stop()
			defer close(release)
			if overflow {
				pool.Submit(func(context.Context) {})
			}
			var b openAIWSSettlementBarrier
			h.submitWebSocketUsageRecordTask(context.Background(), &b, func(context.Context) error { return nil })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if overflow {
				require.NoError(t, b.wait(ctx))
			} else {
				require.ErrorIs(t, b.wait(ctx), context.DeadlineExceeded)
			}
		})
	}
}

func TestOpenAIResponsesWebSocketRechecksAuthority(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeDedicated} {
		for _, mutation := range []string{"deleted", "disabled", "user-disabled", "group-disabled", "database-failure"} {
			t.Run(mode+"/"+mutation, func(t *testing.T) {
				status := 1008
				if mutation == "database-failure" {
					status = 1013
				}
				runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload:  `{"type":"response.create","model":"gpt-5.1"}`,
					secondPayload: `{"type":"response.create","model":"gpt-5.1"}`,
					ingressMode:   mode, secondTurnCloseExpected: true,
					closeStatus: coderws.StatusCode(status), closeReason: "authorization",
					afterFirstClientTurn: func(repo *wsAuthoritativeKeyRepo) {
						repo.mu.Lock()
						defer repo.mu.Unlock()
						// Mutate a DB copy; the opening cached identity stays active.
						key := *repo.key
						user := *key.User
						key.User = &user
						group := *key.Group
						key.Group = &group
						repo.key = &key
						switch mutation {
						case "deleted":
							repo.err = service.ErrAPIKeyNotFound
						case "disabled":
							key.Status = service.StatusDisabled
						case "user-disabled":
							user.Status = service.StatusDisabled
						case "group-disabled":
							group.Status = service.StatusDisabled
						case "database-failure":
							repo.err = errors.New("database unavailable")
						}
					},
				})
			})
		}
	}
}
