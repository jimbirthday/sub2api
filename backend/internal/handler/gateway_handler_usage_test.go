package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageUnrestrictedIncludesWeeklyWindowStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)

	weeklyWindowStart := time.Date(2026, time.July, 13, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{
		WeeklyWindowStart: &weeklyWindowStart,
	})

	handler := &GatewayHandler{}
	handler.usageUnrestricted(
		c,
		context.Background(),
		&service.APIKey{Group: &service.Group{
			Name:             "Weekly plan",
			SubscriptionType: service.SubscriptionTypeSubscription,
		}},
		middleware.AuthSubject{},
		nil,
		nil,
		nil,
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Subscription struct {
			WeeklyWindowStart *time.Time `json:"weekly_window_start"`
		} `json:"subscription"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotNil(t, response.Subscription.WeeklyWindowStart)
	require.True(t, weeklyWindowStart.Equal(*response.Subscription.WeeklyWindowStart))
}

func TestUsageSharedSubscriptionIncludesPoolAndWallet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, unlimited := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		limit := 10.0
		window := service.SharedQuotaWindow{Limit: &limit, Used: 3, Reserved: 2}
		if unlimited {
			window.Limit = nil
		}
		key := &service.APIKey{User: &service.User{Balance: 4}, SharedFunding: &service.SharedFunding{Pools: []service.SharedPoolAuthorization{{Windows: []service.SharedQuotaWindow{window}}}}}
		(&GatewayHandler{}).usageUnrestricted(c, context.Background(), key, middleware.AuthSubject{}, nil, nil, nil)
		require.Equal(t, 200, recorder.Code)
		var response map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		expected := 9.0
		if unlimited {
			expected = -1
		}
		require.Equal(t, expected, response["remaining"])
		require.Equal(t, 4.0, response["balance"])
	}
}
