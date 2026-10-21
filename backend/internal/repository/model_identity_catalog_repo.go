package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *userModelRestrictionRepository) GetModelIdentityCatalog(ctx context.Context) (*service.ModelIdentityCatalog, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	var revision int64
	var updatedAt time.Time
	var by sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT revision,updated_at,updated_by FROM model_identity_catalog_heads WHERE id=1`).Scan(&revision, &updatedAt, &by)
	if err == sql.ErrNoRows {
		return &service.ModelIdentityCatalog{Revision: 0, Models: []service.ModelIdentityCatalogEntry{}, Aliases: []service.ModelIdentityAlias{}}, nil
	}
	if err != nil {
		return nil, err
	}
	return r.loadCatalog(ctx, r.db, revision, updatedAt, by)
}
func (r *userModelRestrictionRepository) loadCatalog(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, revision int64, updatedAt time.Time, by sql.NullInt64) (*service.ModelIdentityCatalog, error) {
	out := &service.ModelIdentityCatalog{Revision: revision, UpdatedAt: updatedAt, Models: []service.ModelIdentityCatalogEntry{}, Aliases: []service.ModelIdentityAlias{}}
	if by.Valid {
		v := by.Int64
		out.UpdatedBy = &v
	}
	rows, err := q.QueryContext(ctx, `SELECT namespace,canonical_model,display_name,source FROM model_identity_catalog_models WHERE revision=$1 ORDER BY namespace,canonical_model`, revision)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v service.ModelIdentityCatalogEntry
		if err := rows.Scan(&v.Namespace, &v.CanonicalModel, &v.DisplayName, &v.Source); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out.Models = append(out.Models, v)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT namespace,transport,alias,canonical_model,alias_id,protocol,role,provider_context FROM model_identity_catalog_aliases WHERE revision=$1 ORDER BY namespace,transport,alias`, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v service.ModelIdentityAlias
		if err := rows.Scan(&v.Namespace, &v.Transport, &v.Alias, &v.CanonicalModel, &v.AliasID, &v.Protocol, &v.Role, &v.ProviderContext); err != nil {
			return nil, err
		}
		out.Aliases = append(out.Aliases, v)
	}
	return out, rows.Err()
}
func (r *userModelRestrictionRepository) ReplaceModelIdentityCatalog(ctx context.Context, expectedRevision, actor int64, models []service.ModelIdentityCatalogEntry, aliases []service.ModelIdentityAlias) (out *service.ModelIdentityCatalog, err error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	tx, owned, err := beginModelManagementTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if owned && !committed {
			_ = tx.Rollback()
		}
	}()
	var current int64
	var updatedAt time.Time
	var by sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT revision,updated_at,updated_by FROM model_identity_catalog_heads WHERE id=1 FOR UPDATE`).Scan(&current, &updatedAt, &by)

	if err != nil {
		return nil, err
	}
	if current != expectedRevision {
		return nil, service.ErrUserModelRestrictionRevisionConflict
	}
	previous, loadErr := r.loadCatalog(ctx, tx, current, updatedAt, by)
	if loadErr != nil {
		return nil, loadErr
	}
	scopeKey := func(v service.ModelIdentityAlias) string {
		return v.Namespace + "\x00" + v.Transport + "\x00" + v.Protocol + "\x00" + v.Role + "\x00" + v.ProviderContext + "\x00" + v.Alias
	}
	previousIDs := make(map[string]bool, len(previous.Aliases))
	previousScopes := make(map[string]string, len(previous.Aliases))
	for _, v := range previous.Aliases {
		previousIDs[v.AliasID] = true
		previousScopes[scopeKey(v)] = v.AliasID
	}
	for i := range aliases {
		if aliases[i].AliasID != "" && !previousIDs[aliases[i].AliasID] {
			return nil, service.ErrUserModelRestrictionInvalid
		}
		if aliases[i].AliasID == "" {
			aliases[i].AliasID = previousScopes[scopeKey(aliases[i])]
		}
		if aliases[i].AliasID == "" {
			aliases[i].AliasID = uuid.NewString()
		}
	}
	next := current + 1
	var createdAt time.Time
	if err = tx.QueryRowContext(ctx, `INSERT INTO model_identity_catalog_versions(revision,created_at,created_by) VALUES($1,NOW(),$2) RETURNING created_at`, next, actor).Scan(&createdAt); err != nil {
		return nil, err
	}
	for _, v := range models {
		if _, err = tx.ExecContext(ctx, `INSERT INTO model_identity_catalog_models(revision,namespace,canonical_model,display_name,source) VALUES($1,$2,$3,$4,$5)`, next, v.Namespace, v.CanonicalModel, v.DisplayName, v.Source); err != nil {
			return nil, err
		}
	}
	for _, v := range aliases {
		if v.AliasID == "" {
			v.AliasID = uuid.NewString()
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO model_identity_catalog_aliases(revision,namespace,transport,alias,canonical_model,alias_id,protocol,role,provider_context) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, next, v.Namespace, v.Transport, v.Alias, v.CanonicalModel, v.AliasID, v.Protocol, v.Role, v.ProviderContext); err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE model_identity_catalog_heads SET revision=$1,updated_at=NOW(),updated_by=$2 WHERE id=1 AND revision=$3`, next, actor, current)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, service.ErrUserModelRestrictionRevisionConflict
	}
	out, err = r.loadCatalog(ctx, tx, next, createdAt, sql.NullInt64{Int64: actor, Valid: true})
	if err != nil {
		return nil, err
	}
	if owned {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	committed = true
	return out, nil
}

// GetModelIdentityAdmission uses one primary statement snapshot for both heads,
// active rules and every input. No cache or second head read can mix revisions.
func (r *userModelRestrictionRepository) GetModelIdentityAdmission(ctx context.Context, userID int64, inputs []service.ModelIdentityInput) (*service.ModelIdentityAdmissionSnapshot, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrUserModelRestrictionUnavailable
	}
	if inputs == nil {
		inputs = []service.ModelIdentityInput{}
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return nil, err
	}
	const query = `
 WITH heads AS (
 SELECT COALESCE((SELECT current_revision FROM user_model_restriction_heads WHERE user_id=$1),0) AS policy_revision,
 (SELECT revision FROM model_identity_catalog_heads WHERE id=1) AS catalog_revision,
 statement_timestamp() AS checked_at
 ), active_rules AS (
 SELECT r.namespace,r.canonical_model,r.action,r.limit_value FROM user_model_restriction_rules r CROSS JOIN heads h
 WHERE r.user_id=$1 AND r.revision=h.policy_revision AND r.enabled
 AND (r.expires_at IS NULL OR r.expires_at>h.checked_at) AND (r.starts_at IS NULL OR r.starts_at<=h.checked_at)
 ), resolved_rules AS (
 SELECT r.action,r.limit_value,r.namespace,COALESCE((SELECT CASE WHEN COUNT(DISTINCT candidates.canonical_model)=1 THEN MIN(candidates.canonical_model) END FROM (
 SELECT m.canonical_model FROM model_identity_catalog_models m CROSS JOIN heads h
 WHERE m.revision=h.catalog_revision AND m.namespace=r.namespace AND m.canonical_model=r.canonical_model
 UNION
 SELECT a.canonical_model FROM model_identity_catalog_aliases a CROSS JOIN heads h
 WHERE a.revision=h.catalog_revision AND a.namespace=r.namespace AND a.alias=r.canonical_model AND a.transport='' AND a.protocol='' AND a.role='' AND a.provider_context=''
 ) candidates),'') AS canonical_model FROM active_rules r
 ), input AS (
 SELECT ordinality AS idx, value->>'namespace' AS namespace,
 value->>'transport' AS transport, value->>'model' AS model, COALESCE(value->>'protocol','') AS protocol, COALESCE(value->>'role','') AS role, COALESCE(value->>'provider_context','') AS provider_context
 FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY
 ), matches AS (
 SELECT i.idx,m.namespace,m.canonical_model
 FROM input i CROSS JOIN heads h JOIN model_identity_catalog_models m
 ON m.revision=h.catalog_revision AND m.canonical_model=i.model
 AND (i.namespace='' OR m.namespace=i.namespace)
 UNION
 SELECT i.idx,a.namespace,a.canonical_model
 FROM input i CROSS JOIN heads h JOIN model_identity_catalog_aliases a
 ON a.revision=h.catalog_revision AND a.alias=i.model
 AND (a.transport='' OR a.transport=i.transport) AND (a.protocol='' OR a.protocol=i.protocol) AND (a.role='' OR a.role=i.role) AND (a.provider_context='' OR a.provider_context=i.provider_context)
 AND (i.namespace='' OR a.namespace=i.namespace)
 ), candidates AS (
 SELECT i.idx,COALESCE((SELECT jsonb_agg(jsonb_build_object('namespace',m.namespace,'canonical_model',m.canonical_model)
 ORDER BY m.namespace,m.canonical_model) FROM matches m WHERE m.idx=i.idx),'[]'::jsonb) AS identities FROM input i
 )
 SELECT h.policy_revision,h.catalog_revision,h.checked_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('Namespace',r.namespace,'CanonicalModel',r.canonical_model,'Enabled',true,'Action',r.action,'LimitValue',r.limit_value))
 FROM resolved_rules r),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(c.identities ORDER BY c.idx) FROM candidates c),'[]'::jsonb)
 FROM heads h`
	snapshot := &service.ModelIdentityAdmissionSnapshot{}
	var rawRules, rawCandidates []byte
	err = r.db.QueryRowContext(ctx, query, userID, string(encoded)).Scan(&snapshot.PolicyRevision, &snapshot.CatalogRevision, &snapshot.CheckedAt, &rawRules, &rawCandidates)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(rawRules, &snapshot.ActiveRules); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(rawCandidates, &snapshot.Candidates); err != nil {
		return nil, err
	}
	return snapshot, nil
}
