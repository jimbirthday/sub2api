//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type sharedAuthRepo struct {
	service.SharedSubscriptionRepository
	funding *service.SharedFunding
	err     error
}

func (s *sharedAuthRepo) Authorize(context.Context, int64, int64, time.Time) (*service.SharedFunding, error) {
	return s.funding, s.err
}
func TestSharedSubscriptionAuthAcrossProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, google := range []bool{false, true} {
		for _, typ := range []string{service.SubscriptionTypeStandard, service.SubscriptionTypeSubscription} {
			for _, tc := range []struct {
				name               string
				covered, available bool
				balance            float64
				failed             bool
				status             int
			}{
				{name: "zero-wallet-credit", covered: true, available: true, status: 200},
				{name: "exhausted-credit-wallet", covered: true, balance: 5, status: 200},
				{name: "exhausted-both", covered: true, status: 403},
				{name: "uncovered-exclusive", balance: 5, status: 403},
				{name: "database-failure", failed: true, balance: 5, status: 503},
			} {
				t.Run(typ+"/"+tc.name+map[bool]string{false: "/standard", true: "/google"}[google], func(t *testing.T) {
					g := &service.Group{ID: 42, Name: "covered", Platform: service.PlatformGemini, Status: service.StatusActive, Hydrated: true, IsExclusive: true, SubscriptionType: typ}
					user := &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: tc.balance, Concurrency: 3}
					key := &service.APIKey{ID: 100, UserID: 7, Key: "shared-auth-test-key", Status: service.StatusActive, User: user, Group: g, GroupID: &g.ID}
					keyRepo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
					sharedRepo := &sharedAuthRepo{}
					if tc.covered {
						sharedRepo.funding = &service.SharedFunding{UserID: 7, GroupID: 42, Available: tc.available}
					}
					if tc.failed {
						sharedRepo.err = errors.New("offline")
					}
					cfg := &config.Config{}
					shared := service.NewSharedSubscriptionService(sharedRepo, nil, nil)
					svc := service.ProvideAPIKeyService(keyRepo, nil, nil, nil, nil, nil, cfg, &service.BillingCacheService{}, nil, shared)
					legacy := service.NewSubscriptionService(nil, &stubUserSubscriptionRepo{}, nil, nil, cfg)
					r := gin.New()
					if google {
						r.Use(gin.HandlerFunc(APIKeyAuthWithSubscriptionGoogle(svc, legacy, cfg)))
					} else {
						r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, legacy, cfg)))
					}
					r.GET("/t", func(c *gin.Context) {
						requestKey, _ := GetAPIKeyFromContext(c)
						require.NotNil(t, requestKey.SharedFunding)
						c.Status(200)
					})
					req := httptest.NewRequest(http.MethodGet, "/t", nil)
					req.Header.Set("x-api-key", key.Key)
					req.Header.Set("x-goog-api-key", key.Key)
					w := httptest.NewRecorder()
					r.ServeHTTP(w, req)
					require.Equal(t, tc.status, w.Code, w.Body.String())
					require.Nil(t, key.SharedFunding)
				})
			}
		}
	}
}
