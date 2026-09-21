package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	app "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type sharedSubscriptionRepository struct {
	db     *sql.DB
	client *dbent.Client
}

func NewSharedSubscriptionRepository(client *dbent.Client, db *sql.DB) service.SharedSubscriptionRepository {
	return &sharedSubscriptionRepository{db: db, client: client}
}

type sharedSQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (r *sharedSubscriptionRepository) executor(ctx context.Context) sharedSQL {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.db
}
func sharedOne(ctx context.Context, q sharedSQL, query string, args []any, dest ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}
func readSharedPlan(ctx context.Context, q sharedSQL, id int64) (*service.SharedSubscriptionPlan, error) {
	p := &service.SharedSubscriptionPlan{}
	var raw []byte
	err := sharedOne(ctx, q, "SELECT id, definition, version, for_sale FROM shared_subscription_plans WHERE id=$1 AND deleted_at IS NULL", []any{id}, &p.ID, &raw, &p.Version, &p.ForSale)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSharedPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	var def service.SharedSubscriptionPlan
	if err = json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	def.ID = p.ID
	def.Version = p.Version
	def.ForSale = p.ForSale
	return &def, nil
}
func (r *sharedSubscriptionRepository) GetPlan(ctx context.Context, id int64) (*service.SharedSubscriptionPlan, error) {
	return readSharedPlan(ctx, r.executor(ctx), id)
}
func (r *sharedSubscriptionRepository) ListPlans(ctx context.Context, sale bool) ([]service.SharedSubscriptionPlan, error) {
	query := "SELECT id, definition, version, for_sale FROM shared_subscription_plans WHERE deleted_at IS NULL"
	if sale {
		query += " AND for_sale"
	}
	query += " ORDER BY id DESC LIMIT 1000"
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.SharedSubscriptionPlan{}
	for rows.Next() {
		var p service.SharedSubscriptionPlan
		var id int64
		var v int
		var active bool
		var raw []byte
		if err = rows.Scan(&id, &raw, &v, &active); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		p.ID = id
		p.Version = v
		p.ForSale = active
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *sharedSubscriptionRepository) SavePlan(ctx context.Context, p *service.SharedSubscriptionPlan) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if p.ID == 0 {
		err = tx.QueryRowContext(ctx, "INSERT INTO shared_subscription_plans(definition,for_sale) VALUES($1,$2) RETURNING id,version", raw, p.ForSale).Scan(&p.ID, &p.Version)
	} else {
		err = tx.QueryRowContext(ctx, "UPDATE shared_subscription_plans SET definition=$1,for_sale=$2,version=version+1,updated_at=NOW() WHERE id=$3 AND version=$4 AND deleted_at IS NULL RETURNING version", raw, p.ForSale, p.ID, p.Version).Scan(&p.Version)
		if errors.Is(err, sql.ErrNoRows) {
			return app.Conflict("SHARED_PLAN_CHANGED", "plan changed; reload before saving")
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM shared_subscription_plan_groups WHERE plan_id=$1", p.ID); err != nil {
		return err
	}
	for _, id := range p.GroupIDs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_plan_groups(plan_id,group_id) VALUES($1,$2)", p.ID, id); err != nil {
			return err
		}
	}
	// Existing subscriptions follow the live plan immediately. Their current
	// generation and quota windows are deliberately preserved.
	liveRaw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE shared_subscriptions SET snapshot=$2,updated_at=NOW() WHERE plan_id=$1 AND status<>'revoked'", p.ID, liveRaw); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM shared_subscription_groups g USING shared_subscriptions s WHERE g.subscription_id=s.id AND s.plan_id=$1 AND s.status<>'revoked'", p.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_groups(subscription_id,group_id) SELECT s.id,pg.group_id FROM shared_subscriptions s JOIN shared_subscription_plan_groups pg ON pg.plan_id=s.plan_id WHERE s.plan_id=$1 AND s.status<>'revoked' ON CONFLICT DO NOTHING", p.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *sharedSubscriptionRepository) DeletePlan(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "UPDATE shared_subscription_plans SET deleted_at=NOW(),for_sale=FALSE WHERE id=$1", id)
	return err
}

const sharedSubColumns = "id,user_id,plan_id,snapshot,starts_at,expires_at,status,generation,quota_overrides"

func scanSharedSub(scan func(...any) error) (*service.SharedSubscription, error) {
	s := &service.SharedSubscription{}
	var raw, overrides []byte
	err := scan(&s.ID, &s.UserID, &s.PlanID, &raw, &s.StartsAt, &s.ExpiresAt, &s.Status, &s.Generation, &overrides)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSharedSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &s.Plan); err != nil {
		return nil, err
	}
	s.QuotaOverrides = map[string]*float64{}
	if len(overrides) > 0 {
		if err = json.Unmarshal(overrides, &s.QuotaOverrides); err != nil {
			return nil, err
		}
	}
	s.Plan = service.ApplySharedQuotaOverrides(s.Plan, s.QuotaOverrides)
	return s, nil
}
func loadSharedSub(ctx context.Context, q sharedSQL, id int64, lock bool) (*service.SharedSubscription, error) {
	query := "SELECT " + sharedSubColumns + " FROM shared_subscriptions WHERE id=$1"
	if lock {
		query += " FOR UPDATE"
	}
	return scanSharedSub(func(dest ...any) error { return sharedOne(ctx, q, query, []any{id}, dest...) })
}
func readSharedWindows(ctx context.Context, q sharedSQL, s *service.SharedSubscription, at time.Time) error {
	s.Windows = service.SharedWindows(s.Plan, s.StartsAt, at)
	for i := range s.Windows {
		w := &s.Windows[i]
		err := sharedOne(ctx, q, "SELECT used,reserved FROM shared_subscription_windows WHERE subscription_id=$1 AND generation=$2 AND kind=$3 AND starts_at=$4", []any{s.ID, s.Generation, w.Kind, w.StartsAt}, &w.Used, &w.Reserved)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}
func (r *sharedSubscriptionRepository) Get(ctx context.Context, id int64) (*service.SharedSubscription, error) {
	s, err := loadSharedSub(ctx, r.executor(ctx), id, false)
	if err != nil {
		return nil, err
	}
	err = readSharedWindows(ctx, r.executor(ctx), s, time.Now())
	return s, err
}
func (r *sharedSubscriptionRepository) List(ctx context.Context, userID int64, limit int) ([]service.SharedSubscription, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, "SELECT "+sharedSubColumns+" FROM shared_subscriptions WHERE ($1::bigint=0 OR user_id=$1) AND status NOT IN ('revoked','expired') AND expires_at>NOW() ORDER BY id DESC LIMIT $2", userID, limit)
	if err != nil {
		return nil, err
	}
	out := []service.SharedSubscription{}
	for rows.Next() {
		s, err := scanSharedSub(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err = readSharedWindows(ctx, r.db, &out[i], time.Now()); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (r *sharedSubscriptionRepository) Authorize(ctx context.Context, userID, groupID int64, at time.Time) (*service.SharedFunding, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+sharedSubColumns+" FROM shared_subscriptions WHERE user_id=$1 AND status='active' AND starts_at<=$3 AND expires_at>$3 AND EXISTS (SELECT 1 FROM shared_subscription_groups g WHERE g.subscription_id=shared_subscriptions.id AND g.group_id=$2) ORDER BY created_at,id", userID, groupID, at)
	if err != nil {
		return nil, err
	}
	subs := []service.SharedSubscription{}
	for rows.Next() {
		s, err := scanSharedSub(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		subs = append(subs, *s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, nil
	}
	f := &service.SharedFunding{UserID: userID, GroupID: groupID, BillingAt: at, Pools: []service.SharedPoolAuthorization{}}
	for i := range subs {
		s := &subs[i]
		if err = readSharedWindows(ctx, r.db, s, at); err != nil {
			return nil, err
		}
		if service.SharedAvailable(s.Windows) > 0 {
			f.Available = true
		}
		f.Pools = append(f.Pools, service.SharedPoolAuthorization{ID: s.ID, Generation: s.Generation, Windows: s.Windows})
	}
	return f, nil
}
func createSharedSub(ctx context.Context, q sharedSQL, userID int64, p *service.SharedSubscriptionPlan) (*service.SharedSubscription, error) {
	return createSharedSubWithDuration(ctx, q, userID, p, p.ValidityDays)
}

func createSharedSubWithDuration(ctx context.Context, q sharedSQL, userID int64, p *service.SharedSubscriptionPlan, durationDays int) (*service.SharedSubscription, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	s := &service.SharedSubscription{UserID: userID, PlanID: p.ID, Plan: *p, Status: "active"}
	err = sharedOne(ctx, q, "INSERT INTO shared_subscriptions(user_id,plan_id,snapshot,starts_at,expires_at) VALUES($1,$2,$3,NOW(),NOW()+$4 * interval '1 day') RETURNING id,starts_at,expires_at", []any{userID, p.ID, raw, durationDays}, &s.ID, &s.StartsAt, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	for _, id := range p.GroupIDs {
		if _, err = q.ExecContext(ctx, "INSERT INTO shared_subscription_groups(subscription_id,group_id) VALUES($1,$2)", s.ID, id); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (r *sharedSubscriptionRepository) Assign(ctx context.Context, userID int64, p *service.SharedSubscriptionPlan, key string) (*service.SharedSubscription, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_grants(user_id,request_id,plan_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", userID, key, p.ID); err != nil {
		return nil, err
	}
	var subID sql.NullInt64
	var planID int64
	if err = tx.QueryRowContext(ctx, "SELECT plan_id,subscription_id FROM shared_subscription_grants WHERE user_id=$1 AND request_id=$2 FOR UPDATE", userID, key).Scan(&planID, &subID); err != nil {
		return nil, err
	}
	if planID != p.ID {
		return nil, app.Conflict("GRANT_CONFLICT", "request_id reused for another plan")
	}
	if subID.Valid {
		return loadSharedSub(ctx, tx, subID.Int64, false)
	}
	s, err := createSharedSub(ctx, tx, userID, p)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE shared_subscription_grants SET subscription_id=$1 WHERE user_id=$2 AND request_id=$3", s.ID, userID, key); err != nil {
		return nil, err
	}
	return s, tx.Commit()
}
func (r *sharedSubscriptionRepository) Action(ctx context.Context, id int64, action string, days int) error {
	var query string
	switch action {
	case "pause":
		query = "status='paused'"
	case "resume":
		query = "status='active'"
	case "revoke":
		query = "status='revoked'"
	case "reset":
		query = "generation=generation+1"
	case "extend":
		query = "expires_at=GREATEST(expires_at,NOW())+$2 * interval '1 day'"
	default:
		return fmt.Errorf("unknown action")
	}
	args := []any{id}
	if action == "extend" {
		args = append(args, days)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sub, err := loadSharedSub(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if sub.Status == "revoked" {
		if action == "revoke" {
			return nil
		}
		return app.Conflict("SHARED_SUBSCRIPTION_REVOKED", "revoked subscriptions cannot be modified")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE shared_subscriptions SET "+query+",updated_at=NOW() WHERE id=$1", args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sharedSubscriptionRepository) UpdateQuota(ctx context.Context, id int64, update service.SharedQuotaUpdate) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sub, err := loadSharedSub(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if sub.Status == "revoked" {
		return app.Conflict("SHARED_SUBSCRIPTION_REVOKED", "revoked subscriptions cannot be modified")
	}
	if sub.Generation != update.Generation {
		return app.Conflict("SHARED_SUBSCRIPTION_CHANGED", "subscription changed; reload before adjusting quota")
	}
	if err = readSharedWindows(ctx, tx, sub, time.Now()); err != nil {
		return err
	}
	before, err := json.Marshal(map[string]any{"quota_overrides": sub.QuotaOverrides, "windows": sub.Windows})
	if err != nil {
		return err
	}
	overrides := map[string]*float64{}
	for _, kind := range []string{"daily", "weekly", "monthly"} {
		value := update.Limits[kind]
		switch value.Mode {
		case "custom":
			overrides[kind] = value.Value
		case "unlimited":
			overrides[kind] = nil
		}
	}
	raw, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE shared_subscriptions SET quota_overrides=$2,updated_at=NOW() WHERE id=$1", id, raw); err != nil {
		return err
	}
	for _, window := range sub.Windows {
		if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_windows(subscription_id,generation,kind,starts_at,used) VALUES($1,$2,$3,$4,$5) ON CONFLICT(subscription_id,generation,kind,starts_at) DO UPDATE SET used=EXCLUDED.used", id, sub.Generation, window.Kind, window.StartsAt, update.Used[window.Kind]); err != nil {
			return err
		}
	}
	sub.QuotaOverrides = overrides
	basePlan, planErr := readSharedPlan(ctx, tx, sub.PlanID)
	if planErr == nil {
		sub.Plan = *basePlan
	}
	sub.Plan = service.ApplySharedQuotaOverrides(sub.Plan, overrides)
	if err = readSharedWindows(ctx, tx, sub, time.Now()); err != nil {
		return err
	}
	after, err := json.Marshal(map[string]any{"quota_overrides": overrides, "windows": sub.Windows})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_quota_adjustments(subscription_id,operator_user_id,reason,before_state,after_state) VALUES($1,$2,$3,$4,$5)", id, update.OperatorID, update.Reason, before, after); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *sharedSubscriptionRepository) History(ctx context.Context, userID int64, limit int) ([]service.SharedSettlement, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, "SELECT request_id,api_key_id,user_id,group_id,cost,subscription_cost,balance_cost,billing_at FROM shared_subscription_settlements WHERE ($1::bigint=0 OR user_id=$1) ORDER BY created_at DESC LIMIT $2", userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.SharedSettlement{}
	for rows.Next() {
		var s service.SharedSettlement
		if err = rows.Scan(&s.RequestID, &s.APIKeyID, &s.UserID, &s.GroupID, &s.Cost, &s.SubscriptionCost, &s.BalanceCost, &s.BillingAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *sharedSubscriptionRepository) GroupIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT g.group_id FROM shared_subscription_groups g JOIN shared_subscriptions s ON s.id=g.subscription_id WHERE s.user_id=$1 AND s.status='active' AND s.starts_at<=NOW() AND s.expires_at>NOW()", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *sharedSubscriptionRepository) ListPage(ctx context.Context, f service.SharedSubscriptionFilter) (*service.SharedSubscriptionPage, error) {
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Page < 1 {
		f.Page = 1
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	const statusExpr = "CASE WHEN s.status='active' AND s.expires_at<=NOW() THEN 'expired' ELSE s.status END"
	where := " FROM shared_subscriptions s JOIN users u ON u.id=s.user_id WHERE ($1::bigint=0 OR s.user_id=$1) AND ($2::bigint=0 OR s.plan_id=$2) AND ($3='' OR " + statusExpr + "=$3) AND ($4::bigint=0 OR s.id<$4) AND ($5='' OR u.email ILIKE $6 OR u.username ILIKE $6 OR u.notes ILIKE $6 OR s.snapshot->>'name' ILIKE $6 OR s.id::text=$5 OR s.user_id::text=$5)"
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(f.Search) + "%"
	args := []any{f.UserID, f.PlanID, f.Status, f.BeforeID, f.Search, pattern}
	out := &service.SharedSubscriptionPage{Items: []service.SharedSubscription{}, Page: f.Page, PageSize: f.Limit}
	if err := tx.QueryRowContext(ctx, "SELECT count(*)"+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	maxPage := (out.Total + f.Limit - 1) / f.Limit
	if maxPage < 1 {
		maxPage = 1
	}
	if out.Page > maxPage {
		out.Page = maxPage
	}
	sortColumns := map[string]string{"id": "s.id", "user_email": "u.email", "plan": "s.snapshot->>'name'", "status": statusExpr, "starts_at": "s.starts_at", "expires_at": "s.expires_at"}
	sortColumn, ok := sortColumns[f.SortBy]
	if !ok {
		sortColumn = "s.id"
	}
	direction := " DESC"
	if f.SortOrder == "asc" {
		direction = " ASC"
	}
	columns := "s." + strings.ReplaceAll(sharedSubColumns, ",", ",s.")
	args = append(args, f.Limit, (out.Page-1)*f.Limit)
	rows, err := tx.QueryContext(ctx, "SELECT "+columns+",u.email,u.username"+where+" ORDER BY "+sortColumn+direction+",s.id"+direction+" LIMIT $7 OFFSET $8", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var email, username sql.NullString
		sub, scanErr := scanSharedSub(func(dest ...any) error { return rows.Scan(append(dest, &email, &username)...) })
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		sub.UserEmail, sub.Username = email.String, username.String
		out.Items = append(out.Items, *sub)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out.HasMore = out.Page*f.Limit < out.Total
	at := time.Now()
	for i := range out.Items {
		if err = readSharedWindows(ctx, tx, &out.Items[i], at); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

func (r *sharedSubscriptionRepository) ChangePlan(ctx context.Context, id, planID int64, generation int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sub, err := loadSharedSub(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if sub.Status == "revoked" {
		return app.Conflict("SHARED_SUBSCRIPTION_REVOKED", "revoked subscriptions cannot be modified")
	}
	if sub.Generation != generation {
		return app.Conflict("SHARED_SUBSCRIPTION_CHANGED", "subscription changed; reload before switching plans")
	}
	// Hold the definition stable until its snapshot and membership are committed.
	var lockedID int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM shared_subscription_plans WHERE id=$1 AND deleted_at IS NULL FOR SHARE", planID).Scan(&lockedID); errors.Is(err, sql.ErrNoRows) {
		return service.ErrSharedPlanNotFound
	} else if err != nil {
		return err
	}
	plan, err := readSharedPlan(ctx, tx, planID)
	if err != nil {
		return err
	}
	if sub.PlanID == plan.ID && sub.Plan.Version == plan.Version {
		return app.BadRequest("SHARED_PLAN_UNCHANGED", "subscription already uses this plan version")
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE shared_subscriptions SET plan_id=$2,snapshot=$3,generation=generation+1,updated_at=NOW() WHERE id=$1", id, plan.ID, raw); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM shared_subscription_groups WHERE subscription_id=$1", id); err != nil {
		return err
	}
	for _, groupID := range plan.GroupIDs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_groups(subscription_id,group_id) VALUES($1,$2)", id, groupID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
