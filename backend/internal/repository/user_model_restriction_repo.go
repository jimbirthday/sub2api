package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type userModelRestrictionRepository struct{ db *sql.DB }

func NewUserModelRestrictionRepository(db *sql.DB) service.UserModelRestrictionRepository {
	return &userModelRestrictionRepository{db: db}
}

func (r *userModelRestrictionRepository) GetSnapshot(ctx context.Context, userID int64) (*service.UserModelRestrictionSnapshot, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	var revision int64
	var updatedAt time.Time
	var updatedBy sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT current_revision, updated_at, updated_by FROM user_model_restriction_heads WHERE user_id=$1`, userID).Scan(&revision, &updatedAt, &updatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return &service.UserModelRestrictionSnapshot{UserID: userID, Revision: 0, Rules: []service.UserModelRestrictionRule{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user model restriction head: %w", err)
	}
	s := &service.UserModelRestrictionSnapshot{UserID: userID, Revision: revision, UpdatedAt: updatedAt, Rules: []service.UserModelRestrictionRule{}}
	if updatedBy.Valid {
		v := updatedBy.Int64
		s.UpdatedBy = &v
	}
	if revision == 0 {
		return s, nil
	}
	return loadRestrictionRules(ctx, r.db, s)
}

type restrictionQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadRestrictionRules(ctx context.Context, db restrictionQueryer, s *service.UserModelRestrictionSnapshot) (*service.UserModelRestrictionSnapshot, error) {
	rows, err := db.QueryContext(ctx, `SELECT namespace, canonical_model, enabled, expires_at, reason, created_by, created_at, updated_at, rule_id, action, limit_value, starts_at, source FROM user_model_restriction_rules WHERE user_id=$1 AND revision=$2 ORDER BY namespace, canonical_model`, s.UserID, s.Revision)
	if err != nil {
		return nil, fmt.Errorf("read user model restriction rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rule service.UserModelRestrictionRule
		var expires sql.NullTime
		var by sql.NullInt64
		var starts sql.NullTime
		var limit sql.NullInt64
		if err := rows.Scan(&rule.Namespace, &rule.CanonicalModel, &rule.Enabled, &expires, &rule.Reason, &by, &rule.CreatedAt, &rule.UpdatedAt, &rule.RuleID, &rule.Action, &limit, &starts, &rule.Source); err != nil {
			return nil, fmt.Errorf("scan user model restriction rule: %w", err)
		}
		if expires.Valid {
			v := expires.Time
			rule.ExpiresAt = &v
		}
		if by.Valid {
			v := by.Int64
			rule.CreatedBy = &v
		}
		if starts.Valid {
			rule.StartsAt = &starts.Time
		}
		if limit.Valid {
			v := int(limit.Int64)
			rule.LimitValue = &v
		}
		s.Rules = append(s.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user model restriction rules: %w", err)
	}
	return s, nil
}

func (r *userModelRestrictionRepository) ReplaceSnapshot(ctx context.Context, userID, expectedRevision int64, updatedBy *int64, rules []service.UserModelRestrictionRule) (snapshot *service.UserModelRestrictionSnapshot, err error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	if userID <= 0 || expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return nil, service.ErrUserModelRestrictionInvalid
	}
	rules = append([]service.UserModelRestrictionRule(nil), rules...)
	for i := range rules {
		if rules[i].Action == "" {
			rules[i].Action = "deny_model"
		}
		if rules[i].Source == "" {
			rules[i].Source = "manual"
		}
	}
	tx, owned, err := beginModelManagementTx(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("begin restriction transaction: %w", err)
	}
	committed := false
	defer func() {
		if owned && !committed {
			_ = tx.Rollback()
		}
	}()
	var current int64
	rowErr := tx.QueryRowContext(ctx, `SELECT current_revision FROM user_model_restriction_heads WHERE user_id=$1 FOR UPDATE`, userID).Scan(&current)
	if errors.Is(rowErr, sql.ErrNoRows) {
		if expectedRevision != 0 {
			err = service.ErrUserModelRestrictionRevisionConflict
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_model_restriction_heads(user_id,current_revision,updated_at,updated_by) VALUES($1,0,NOW(),$2) ON CONFLICT (user_id) DO NOTHING`, userID, updatedBy); err != nil {
			return nil, fmt.Errorf("create restriction head: %w", err)
		}
		if err = tx.QueryRowContext(ctx, `SELECT current_revision FROM user_model_restriction_heads WHERE user_id=$1 FOR UPDATE`, userID).Scan(&current); err != nil {
			return nil, fmt.Errorf("lock restriction head: %w", err)
		}
	} else if rowErr != nil {
		return nil, fmt.Errorf("lock restriction head: %w", rowErr)
	}
	if current != expectedRevision {
		err = service.ErrUserModelRestrictionRevisionConflict
		return nil, err
	}
	// 规则必须引用当前目录中明确登记的原厂身份；未知custom须先登记。
	if len(rules) > 0 {
		identities := make([]service.CanonicalModelIdentity, 0, len(rules))
		for _, rule := range rules {
			identities = append(identities, service.CanonicalModelIdentity{Namespace: rule.Namespace, CanonicalModel: rule.CanonicalModel})
		}
		encoded, marshalErr := json.Marshal(identities)
		if marshalErr != nil {
			return nil, marshalErr
		}
		var registered bool
		err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS (
   SELECT 1 FROM jsonb_array_elements($1::jsonb) i WHERE NOT EXISTS (
    SELECT 1 FROM model_identity_catalog_heads h JOIN model_identity_catalog_models m ON m.revision=h.revision
    WHERE h.id=1 AND m.namespace=i->>'namespace' AND m.canonical_model=i->>'canonical_model'
   ))`, string(encoded)).Scan(&registered)
		if err != nil {
			return nil, err
		}
		if !registered {
			return nil, service.ErrUserModelRestrictionInvalid
		}
	}
	// A manual edit preserves the latest behavior-owned subset. Stable IDs are
	// reused by identity/action and cannot be supplied from another user's policy.
	previous, loadErr := loadRestrictionRules(ctx, tx, &service.UserModelRestrictionSnapshot{UserID: userID, Revision: current, Rules: []service.UserModelRestrictionRule{}})
	if loadErr != nil {
		return nil, loadErr
	}
	stable := make(map[string]string)
	ownedIDs := make(map[string]bool)
	for _, rule := range previous.Rules {
		if rule.Source != "behavior" {
			ownedIDs[rule.RuleID] = true
		}
		if rule.Source != "behavior" {
			stable[rule.Namespace+"\x00"+rule.CanonicalModel+"\x00"+rule.Action] = rule.RuleID
		}
		if rule.Source == "behavior" {
			rules = append(rules, rule)
		}
	}
	for i := range rules {
		key := rules[i].Namespace + "\x00" + rules[i].CanonicalModel + "\x00" + rules[i].Action
		if rules[i].RuleID == "" {
			rules[i].RuleID = stable[key]
			if rules[i].RuleID == "" {
				rules[i].RuleID = uuid.NewString()
			}
		} else if rules[i].Source != "behavior" && !ownedIDs[rules[i].RuleID] {
			return nil, service.ErrUserModelRestrictionInvalid
		}
	}
	next := current + 1
	var createdAt time.Time
	if err = tx.QueryRowContext(ctx, `INSERT INTO user_model_restriction_versions(user_id,revision,created_at,updated_at,created_by) VALUES($1,$2,NOW(),NOW(),$3) RETURNING created_at`, userID, next, updatedBy).Scan(&createdAt); err != nil {
		return nil, fmt.Errorf("insert restriction version: %w", err)
	}
	for _, rule := range rules {
		by := updatedBy
		if rule.Source == "behavior" {
			by = rule.CreatedBy
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_model_restriction_rules(user_id,revision,namespace,canonical_model,enabled,expires_at,reason,created_by,created_at,updated_at,rule_id,action,limit_value,starts_at,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NOW(),NOW(),$9,$10,$11,$12,$13)`, userID, next, rule.Namespace, rule.CanonicalModel, rule.Enabled, rule.ExpiresAt, rule.Reason, by, rule.RuleID, rule.Action, rule.LimitValue, rule.StartsAt, rule.Source); err != nil {
			return nil, fmt.Errorf("insert restriction rule: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE user_model_restriction_heads SET current_revision=$2,updated_at=NOW(),updated_by=$3 WHERE user_id=$1 AND current_revision=$4`, userID, next, updatedBy, current)
	if err != nil {
		return nil, fmt.Errorf("update restriction head: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, service.ErrUserModelRestrictionRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE user_model_restriction_versions SET parent_revision=$3,catalog_revision=(SELECT revision FROM model_identity_catalog_heads WHERE id=1) WHERE user_id=$1 AND revision=$2`, userID, next, current); err != nil {
		return nil, err
	}
	// 在事务内读取刚写入的确切版本，避免提交后另一个写入覆盖响应。
	snapshot, err = loadRestrictionRules(ctx, tx, &service.UserModelRestrictionSnapshot{
		UserID: userID, Revision: next, UpdatedAt: createdAt, UpdatedBy: updatedBy, Rules: []service.UserModelRestrictionRule{},
	})
	if err != nil {
		return nil, err
	}
	if owned {
		if err = tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit restriction transaction: %w", err)
		}
	}
	committed = true
	return snapshot, nil
}

func (r *userModelRestrictionRepository) GetVersion(ctx context.Context, userID, revision int64) (*service.UserModelRestrictionSnapshot, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	s := &service.UserModelRestrictionSnapshot{UserID: userID, Revision: revision, Rules: []service.UserModelRestrictionRule{}}
	var by sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT updated_at, created_by FROM user_model_restriction_versions WHERE user_id=$1 AND revision=$2`, userID, revision).Scan(&s.UpdatedAt, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUserModelRestrictionNotFound
	}
	if err != nil {
		return nil, err
	}
	if by.Valid {
		s.UpdatedBy = &by.Int64
	}
	return loadRestrictionRules(ctx, r.db, s)
}

func (r *userModelRestrictionRepository) ListVersions(ctx context.Context, userID, beforeRevision int64, limit int) (*service.UserModelRestrictionHistory, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	if userID <= 0 || beforeRevision < 0 || limit < 1 || limit > 100 {
		return nil, service.ErrUserModelRestrictionInvalid
	}
	// 从版本表分页，空规则版本也必须出现在历史中。
	rows, err := r.db.QueryContext(ctx, `SELECT v.revision, v.created_at, v.created_by,
 (SELECT COUNT(*) FROM user_model_restriction_rules r WHERE r.user_id=v.user_id AND r.revision=v.revision)
 FROM user_model_restriction_versions v WHERE v.user_id=$1 AND ($2::bigint=0 OR v.revision<$2)
 ORDER BY v.revision DESC LIMIT $3`, userID, beforeRevision, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := &service.UserModelRestrictionHistory{Items: []service.UserModelRestrictionVersion{}}
	for rows.Next() {
		var item service.UserModelRestrictionVersion
		var by sql.NullInt64
		if err := rows.Scan(&item.Revision, &item.CreatedAt, &by, &item.RuleCount); err != nil {
			return nil, err
		}
		if by.Valid {
			item.CreatedBy = &by.Int64
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		before := result.Items[limit-1].Revision
		result.NextBeforeRevision = &before
	}
	return result, nil
}
