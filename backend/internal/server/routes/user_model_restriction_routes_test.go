package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUserModelRestrictionRoutesAdminAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{Admin: &handler.AdminHandlers{UserModelRestriction: adminhandler.NewUserModelRestrictionHandler(nil, nil)}}
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		switch c.GetHeader("Authorization") {
		case "admin":
			c.Next()
		case "user":
			c.AbortWithStatus(http.StatusForbidden)
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	audited := 0
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { audited++; c.Next() })
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), h, auth, audit, stepUp, nil, nil)
	for _, route := range []struct{ method, path string }{{"GET", "/model-restrictions"}, {"PUT", "/model-restrictions"}, {"DELETE", "/model-restrictions"}, {"GET", "/model-restrictions/history"}, {"GET", "/model-restrictions/history/1"}} {
		for _, tc := range []struct {
			auth   string
			status int
		}{{"", 401}, {"user", 403}, {"admin", 503}} {
			before := audited
			req := httptest.NewRequest(route.method, "/api/v1/admin/users/1"+route.path, nil)
			req.Header.Set("Authorization", tc.auth)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, route.path)
			if tc.auth == "admin" {
				require.Equal(t, before+1, audited)
			} else {
				require.Equal(t, before, audited)
			}
		}
	}
}

func TestModelIdentityCatalogRoutesRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{Admin: &handler.AdminHandlers{UserModelRestriction: adminhandler.NewUserModelRestrictionHandler(nil, nil)}}
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "admin" {
			c.Next()
			return
		}
		c.AbortWithStatus(http.StatusForbidden)
	})
	audited := 0
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { audited++; c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), h, auth, audit, middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }), nil, nil)
	for _, method := range []string{"GET", "PUT"} {
		for _, token := range []string{"user", "admin"} {
			before := audited
			req := httptest.NewRequest(method, "/api/v1/admin/model-restrictions/catalog", nil)
			req.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if token == "user" {
				require.Equal(t, 403, w.Code)
				require.Equal(t, before, audited)
			} else {
				require.Equal(t, 503, w.Code)
				require.Equal(t, before+1, audited)
			}
		}
	}
}
