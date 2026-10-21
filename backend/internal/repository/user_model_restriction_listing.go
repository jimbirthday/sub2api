package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *userModelRestrictionRepository) ListCurrentRestrictions(ctx context.Context, o service.ModelRestrictionListOptions) (*service.ModelRestrictionList, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT r.id,r.user_id,u.email,r.revision,r.namespace,r.canonical_model,r.enabled,r.expires_at,r.reason,r.created_by,r.action,r.limit_value,r.starts_at,r.source,
 r.enabled AND (r.expires_at IS NULL OR r.expires_at>statement_timestamp()) AND (r.starts_at IS NULL OR r.starts_at<=statement_timestamp()) AS active_now
FROM user_model_restriction_rules r
JOIN user_model_restriction_heads h ON h.user_id=r.user_id AND h.current_revision=r.revision
JOIN users u ON u.id=r.user_id AND u.deleted_at IS NULL
WHERE ($1::bigint=0 OR r.id<$1) AND ($2::bigint=0 OR r.user_id=$2)
 AND ($3::text='' OR u.email ILIKE '%'||$3||'%' OR u.username ILIKE '%'||$3||'%')
 AND ($4::text='' OR r.canonical_model ILIKE '%'||$4||'%' OR r.namespace ILIKE '%'||$4||'%')
 AND ($5::text='' OR ($5='active' AND r.enabled AND (r.expires_at IS NULL OR r.expires_at>statement_timestamp()) AND (r.starts_at IS NULL OR r.starts_at<=statement_timestamp()))
 OR ($5='pending' AND r.enabled AND r.starts_at>statement_timestamp() AND (r.expires_at IS NULL OR r.expires_at>statement_timestamp())) OR ($5='disabled' AND NOT r.enabled) OR ($5='expired' AND r.enabled AND r.expires_at<=statement_timestamp()))
 AND ($7::text='' OR r.source=$7)
ORDER BY r.id DESC LIMIT $6`, o.BeforeID, o.UserID, o.Search, o.Model, o.State, o.Limit+1, o.Source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &service.ModelRestrictionList{Items: make([]service.ModelRestrictionListItem, 0, o.Limit)}
	for rows.Next() {
		var item service.ModelRestrictionListItem
		var expires sql.NullTime
		var actor sql.NullInt64
		var starts sql.NullTime
		var cap sql.NullInt64
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserEmail, &item.Revision, &item.Namespace, &item.CanonicalModel, &item.Enabled, &expires, &item.Reason, &actor, &item.Action, &cap, &starts, &item.Source, &item.ActiveNow); err != nil {
			return nil, err
		}
		if expires.Valid {
			item.ExpiresAt = &expires.Time
		}
		if actor.Valid {
			item.CreatedBy = &actor.Int64
		}
		if starts.Valid {
			item.StartsAt = &starts.Time
		}
		if cap.Valid {
			v := int(cap.Int64)
			item.LimitValue = &v
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Items) > o.Limit {
		out.Items = out.Items[:o.Limit]
		cursor := out.Items[len(out.Items)-1].ID
		out.NextBeforeID = &cursor
	}
	return out, nil
}
