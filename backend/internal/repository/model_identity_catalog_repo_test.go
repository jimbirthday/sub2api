package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelIdentityAdmissionUsesOnePrimaryStatement(t *testing.T) {
	repo, mock := restrictionMockRepo(t)
	now := time.Now()
	// 只允许一条查询；若代码再取head或逐模型读取，不会有匹配的sqlmock expectation。
	mock.ExpectQuery("WITH heads AS").WithArgs(int64(42), `[{"namespace":"","transport":"antigravity","model":"claude-sonnet-4-5"}]`).WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "catalog_revision", "checked_at", "rules", "candidates"}).AddRow(7, 3, now, `[{"Namespace":"anthropic","CanonicalModel":"claude-sonnet-4-5-20250929","Enabled":true}]`, `[[{"namespace":"anthropic","canonical_model":"claude-sonnet-4-5-20250929"}]]`))
	identityRepo, ok := repo.(service.ModelIdentityRepository)
	require.True(t, ok)
	result, err := identityRepo.GetModelIdentityAdmission(context.Background(), 42, []service.ModelIdentityInput{{Transport: "antigravity", Model: "claude-sonnet-4-5"}})
	require.NoError(t, err)
	require.Equal(t, int64(7), result.PolicyRevision)
	require.Equal(t, int64(3), result.CatalogRevision)
	require.Equal(t, now, result.CheckedAt)
	require.Len(t, result.Candidates, 1)
	require.Len(t, result.ActiveRules, 1)
}
