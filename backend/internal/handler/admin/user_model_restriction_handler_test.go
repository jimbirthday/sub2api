package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type restrictionAdminUsersStub struct {
	service.AdminService
	err error
}

func (s restrictionAdminUsersStub) GetUser(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 1}, s.err
}

type restrictionAdminRepoStub struct {
	service.UserModelRestrictionRepository
	err      error
	writes   int
	actor    int64
	rules    []service.UserModelRestrictionRule
	before   int64
	limit    int
	revision int64
}

func (s *restrictionAdminRepoStub) GetSnapshot(_ context.Context, id int64) (*service.UserModelRestrictionSnapshot, error) {
	return &service.UserModelRestrictionSnapshot{UserID: id, Rules: []service.UserModelRestrictionRule{}}, s.err
}
func (s *restrictionAdminRepoStub) ReplaceSnapshot(_ context.Context, id, revision int64, by *int64, rules []service.UserModelRestrictionRule) (*service.UserModelRestrictionSnapshot, error) {
	s.writes++
	s.actor = *by
	s.rules = rules
	return &service.UserModelRestrictionSnapshot{UserID: id, Revision: revision + 1, UpdatedBy: by, Rules: rules}, s.err
}
func (s *restrictionAdminRepoStub) GetVersion(_ context.Context, id, revision int64) (*service.UserModelRestrictionSnapshot, error) {
	s.revision = revision
	return &service.UserModelRestrictionSnapshot{UserID: id, Revision: revision, Rules: []service.UserModelRestrictionRule{}}, s.err
}
func (s *restrictionAdminRepoStub) ListVersions(_ context.Context, id, before int64, limit int) (*service.UserModelRestrictionHistory, error) {
	s.before = before
	s.limit = limit
	return &service.UserModelRestrictionHistory{Items: []service.UserModelRestrictionVersion{}}, s.err
}

func restrictionAdminTestRouter(repo *restrictionAdminRepoStub, userErr error, actor int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if actor > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actor})
		}
		c.Next()
	})
	h := NewUserModelRestrictionHandler(service.NewUserModelRestrictionService(repo), restrictionAdminUsersStub{err: userErr})
	r.GET("/users/:id/policy", h.Get)
	r.PUT("/users/:id/policy", h.Replace)
	r.DELETE("/users/:id/policy", h.Clear)
	r.GET("/users/:id/policy/history", h.History)
	r.GET("/users/:id/policy/history/:revision", h.Version)
	return r
}
func restrictionAdminRequest(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-key")
	r.ServeHTTP(w, req)
	return w
}
func TestUserModelRestrictionHandlerLifecycle(t *testing.T) {
	repo := &restrictionAdminRepoStub{}
	r := restrictionAdminTestRouter(repo, nil, 42)
	w := restrictionAdminRequest(r, "GET", "/users/1/policy", "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"rules":[]`)
	w = restrictionAdminRequest(r, "PUT", "/users/1/policy", `{"expected_revision":0,"rules":[{"namespace":"openai","canonical_model":"gpt-x","enabled":true,"expires_at":null,"reason":"review"}]}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"revision":1`)
	require.Equal(t, int64(42), repo.actor)
	require.Equal(t, int64(42), *repo.rules[0].CreatedBy)
	w = restrictionAdminRequest(r, "DELETE", "/users/1/policy", `{"expected_revision":1,"reason":"clear reviewed policy"}`)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"revision":2`)
	require.Empty(t, repo.rules)
	w = restrictionAdminRequest(r, "GET", "/users/1/policy/history?limit=7&before_revision=20", "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, 7, repo.limit)
	require.Equal(t, int64(20), repo.before)
	w = restrictionAdminRequest(r, "GET", "/users/1/policy/history/1", "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, int64(1), repo.revision)
}
func TestUserModelRestrictionHandlerValidation(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"expected_revision":null,"rules":[]}`, `{"expected_revision":0,"rules":null}`, `{"expected_revision":-1,"rules":[]}`,
		`{"expected_revision":0,"rules":[],"updated_by":6}`, `{"expected_revision":0,"rules":[]} {}`,
		`{"expected_revision":0,"rules":[{"namespace":"openai","canonical_model":"x"}]}`,
		`{"expected_revision":0,"rules":[{"namespace":"openai","canonical_model":"x","enabled":true,"created_by":6}]}`,
		`{"expected_revision":0,"rules":[{"namespace":"openai","canonical_model":"x","enabled":true,"expires_at":"bad"}]}`,
		`{"expected_revision":0,"rules":[{"namespace":"openai","canonical_model":"x","enabled":true},{"namespace":"OPENAI","canonical_model":" X ","enabled":false}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			repo := &restrictionAdminRepoStub{}
			w := restrictionAdminRequest(restrictionAdminTestRouter(repo, nil, 42), "PUT", "/users/1/policy", body)
			require.Equal(t, 400, w.Code, w.Body.String())
			require.Zero(t, repo.writes)
		})
	}
	for _, path := range []string{"/users/0/policy", "/users/a/policy", "/users/1/policy/history/0", "/users/1/policy/history?limit=101", "/users/1/policy/history?limit=", "/users/1/policy/history?before_revision=", "/users/1/policy/history?before_revision=0"} {
		w := restrictionAdminRequest(restrictionAdminTestRouter(&restrictionAdminRepoStub{}, nil, 42), "GET", path, "")
		require.Equal(t, 400, w.Code, path)
	}
	w := restrictionAdminRequest(restrictionAdminTestRouter(&restrictionAdminRepoStub{}, nil, 42), "DELETE", "/users/1/policy", `{}`)
	require.Equal(t, 400, w.Code)
}
func TestUserModelRestrictionHandlerErrors(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		repoErr, userErr         error
		actor                    int64
		status                   int
	}{
		{"conflict", "PUT", "/users/1/policy", `{"expected_revision":0,"rules":[],"reason":"review"}`, service.ErrUserModelRestrictionRevisionConflict, nil, 42, 409},
		{"db", "GET", "/users/1/policy", "", errors.New("db unavailable"), nil, 42, 503},
		{"user missing", "GET", "/users/1/policy", "", nil, service.ErrUserNotFound, 42, 404},
		{"user db", "GET", "/users/1/policy", "", nil, errors.New("db unavailable"), 42, 503},
		{"version missing", "GET", "/users/1/policy/history/2", "", service.ErrUserModelRestrictionNotFound, nil, 42, 404},
		{"actor missing", "DELETE", "/users/1/policy", `{"expected_revision":0}`, nil, nil, 0, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := restrictionAdminRequest(restrictionAdminTestRouter(&restrictionAdminRepoStub{err: tc.repoErr}, tc.userErr, tc.actor), tc.method, tc.path, tc.body)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}

type modelIdentityAdminRepoStub struct {
	restrictionAdminRepoStub
	catalog *service.ModelIdentityCatalog
	writes  int
	actor   int64
}

func (r *modelIdentityAdminRepoStub) GetModelIdentityCatalog(context.Context) (*service.ModelIdentityCatalog, error) {
	return r.catalog, r.err
}
func (r *modelIdentityAdminRepoStub) ReplaceModelIdentityCatalog(_ context.Context, rev, actor int64, models []service.ModelIdentityCatalogEntry, aliases []service.ModelIdentityAlias) (*service.ModelIdentityCatalog, error) {
	r.writes++
	r.actor = actor
	if r.err != nil {
		return nil, r.err
	}
	return &service.ModelIdentityCatalog{Revision: rev + 1, Models: models, Aliases: aliases}, nil
}
func (r *modelIdentityAdminRepoStub) GetModelIdentityAdmission(context.Context, int64, []service.ModelIdentityInput) (*service.ModelIdentityAdmissionSnapshot, error) {
	return nil, r.err
}

func TestModelIdentityCatalogAdminContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &modelIdentityAdminRepoStub{catalog: &service.ModelIdentityCatalog{Revision: 1, Models: []service.ModelIdentityCatalogEntry{}, Aliases: []service.ModelIdentityAlias{}}}
	h := NewUserModelRestrictionHandler(service.NewUserModelRestrictionService(repo), restrictionAdminUsersStub{})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	r.GET("/catalog", h.ModelIdentityCatalog)
	r.PUT("/catalog", h.ReplaceModelIdentityCatalog)
	w := restrictionAdminRequest(r, "GET", "/catalog", "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"models":[]`)
	valid := `{"reason":"register verified model","expected_revision":1,"models":[{"namespace":"custom:acme","canonical_model":"private-model","display_name":"Private","source":"operator:verified"}],"aliases":[{"namespace":"custom:acme","transport":"openai","alias":"private-alias","canonical_model":"private-model"}]}`
	w = restrictionAdminRequest(r, "PUT", "/catalog", valid)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 1, repo.writes)
	require.Equal(t, int64(9), repo.actor)
	require.Contains(t, w.Body.String(), `"revision":2`)
	for _, body := range []string{`{}`, `{"expected_revision":1,"models":null,"aliases":[]}`, `{"expected_revision":1,"models":[],"aliases":[],"created_by":3}`, `{"expected_revision":1,"models":[{"namespace":"antigravity","canonical_model":"x","source":"proxy"}],"aliases":[]}`} {
		w = restrictionAdminRequest(r, "PUT", "/catalog", body)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	repo.err = service.ErrUserModelRestrictionRevisionConflict
	w = restrictionAdminRequest(r, "PUT", "/catalog", valid)
	require.Equal(t, 409, w.Code)
	repo.err = errors.New("database offline")
	w = restrictionAdminRequest(r, "GET", "/catalog", "")
	require.Equal(t, 503, w.Code)
}

func (s *restrictionAdminRepoStub) ExecuteModelManagementMutation(ctx context.Context, m service.ModelManagementMutation, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return fn(ctx)
}
