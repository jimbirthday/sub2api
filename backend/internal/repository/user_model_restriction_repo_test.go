package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func restrictionMockRepo(t *testing.T) (service.UserModelRestrictionRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	return NewUserModelRestrictionRepository(db), mock
}
func restrictionRuleRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"namespace", "canonical_model", "enabled", "expires_at", "reason", "created_by", "created_at", "updated_at", "rule_id", "action", "limit_value", "starts_at", "source"})
}

func TestUserModelRestrictionRepositoryReplaceExactVersion(t *testing.T) {
	repo, mock := restrictionMockRepo(t)
	actor := int64(42)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision .* FOR UPDATE").WithArgs(int64(5)).WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(8))
	mock.ExpectQuery("SELECT namespace").WillReturnRows(restrictionRuleRows())
	mock.ExpectQuery("INSERT INTO user_model_restriction_versions.*RETURNING created_at").WithArgs(int64(5), int64(9), actor).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectExec("UPDATE user_model_restriction_heads").WithArgs(int64(5), int64(9), actor, int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE user_model_restriction_versions SET parent_revision").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT namespace.*revision=\\$2").WithArgs(int64(5), int64(9)).WillReturnRows(restrictionRuleRows())
	mock.ExpectCommit()
	// 提交后不允许读取 head；否则新写入可能把本次响应替换成 revision 10。
	snapshot, err := repo.ReplaceSnapshot(context.Background(), 5, 8, &actor, []service.UserModelRestrictionRule{})
	require.NoError(t, err)
	require.Equal(t, int64(9), snapshot.Revision)
	require.Equal(t, now, snapshot.UpdatedAt)
	require.NotNil(t, snapshot.Rules)
	require.Empty(t, snapshot.Rules)
}

func TestUserModelRestrictionRepositoryCASAndRollback(t *testing.T) {
	actor := int64(42)
	t.Run("stale revision", func(t *testing.T) {
		repo, mock := restrictionMockRepo(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT current_revision .* FOR UPDATE").WithArgs(int64(5)).WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(2))
		mock.ExpectRollback()
		_, err := repo.ReplaceSnapshot(context.Background(), 5, 1, &actor, []service.UserModelRestrictionRule{})
		require.ErrorIs(t, err, service.ErrUserModelRestrictionRevisionConflict)
	})
	t.Run("failed rule insert", func(t *testing.T) {
		repo, mock := restrictionMockRepo(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT current_revision .* FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(0))
		mock.ExpectQuery("SELECT NOT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"registered"}).AddRow(true))
		mock.ExpectQuery("SELECT namespace").WillReturnRows(restrictionRuleRows())
		mock.ExpectQuery("INSERT INTO user_model_restriction_versions").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
		mock.ExpectExec("INSERT INTO user_model_restriction_rules").WillReturnError(errors.New("write failure"))
		mock.ExpectRollback()
		_, err := repo.ReplaceSnapshot(context.Background(), 5, 0, &actor, []service.UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "x"}})
		require.Error(t, err)
	})
	t.Run("zero affected head", func(t *testing.T) {
		repo, mock := restrictionMockRepo(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT current_revision .* FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(0))
		mock.ExpectQuery("SELECT namespace").WillReturnRows(restrictionRuleRows())
		mock.ExpectQuery("INSERT INTO user_model_restriction_versions").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
		mock.ExpectExec("UPDATE user_model_restriction_heads").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()
		_, err := repo.ReplaceSnapshot(context.Background(), 5, 0, &actor, []service.UserModelRestrictionRule{})
		require.ErrorIs(t, err, service.ErrUserModelRestrictionRevisionConflict)
	})
	t.Run("first creation raced", func(t *testing.T) {
		repo, mock := restrictionMockRepo(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT current_revision .* FOR UPDATE").WillReturnError(sql.ErrNoRows)
		mock.ExpectExec("INSERT INTO user_model_restriction_heads.*ON CONFLICT").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT current_revision .* FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(1))
		mock.ExpectRollback()
		_, err := repo.ReplaceSnapshot(context.Background(), 5, 0, &actor, []service.UserModelRestrictionRule{})
		require.ErrorIs(t, err, service.ErrUserModelRestrictionRevisionConflict)
	})
}

func TestUserModelRestrictionRepositoryHistory(t *testing.T) {
	repo, mock := restrictionMockRepo(t)
	now := time.Now()
	mock.ExpectQuery("SELECT v.revision.*FROM user_model_restriction_versions").WithArgs(int64(5), int64(0), 3).WillReturnRows(sqlmock.NewRows([]string{"revision", "created_at", "created_by", "count"}).AddRow(3, now, 42, 0).AddRow(2, now, 42, 1).AddRow(1, now, nil, 2))
	history, err := repo.ListVersions(context.Background(), 5, 0, 2)
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	require.Equal(t, int64(0), history.Items[0].RuleCount)
	require.Equal(t, int64(2), *history.NextBeforeRevision)
	mock.ExpectQuery("SELECT updated_at, created_by FROM user_model_restriction_versions").WithArgs(int64(5), int64(3)).WillReturnRows(sqlmock.NewRows([]string{"updated_at", "created_by"}).AddRow(now, 42))
	mock.ExpectQuery("SELECT namespace").WithArgs(int64(5), int64(3)).WillReturnRows(restrictionRuleRows())
	version, err := repo.GetVersion(context.Background(), 5, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), version.Revision)
	require.NotNil(t, version.Rules)
	require.Empty(t, version.Rules)
	mock.ExpectQuery("SELECT updated_at, created_by").WithArgs(int64(6), int64(3)).WillReturnError(sql.ErrNoRows)
	_, err = repo.GetVersion(context.Background(), 6, 3)
	require.ErrorIs(t, err, service.ErrUserModelRestrictionNotFound)
}
