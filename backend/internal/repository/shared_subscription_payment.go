package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *sharedSubscriptionRepository) SaveOrder(ctx context.Context, orderID int64, p *service.SharedSubscriptionPlan, renew, replace int64) error {
	if dbent.TxFromContext(ctx) == nil {
		return errors.New("shared subscription snapshot must be saved in order transaction")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var target any
	if renew > 0 {
		target = renew
	}
	var replaceTarget any
	if replace > 0 {
		replaceTarget = replace
	}
	_, err = r.executor(ctx).ExecContext(ctx, "INSERT INTO shared_subscription_order_items(order_id,snapshot,renew_subscription_id,replace_subscription_id,duration_days) VALUES($1,$2,$3,$4,$5)", orderID, raw, target, replaceTarget, p.ValidityDays)
	return err
}
func (r *sharedSubscriptionRepository) FulfillOrder(ctx context.Context, orderID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID int64
	var typ, status string
	if err = tx.QueryRowContext(ctx, "SELECT user_id,order_type,status FROM payment_orders WHERE id=$1 FOR UPDATE", orderID).Scan(&userID, &typ, &status); err != nil {
		return err
	}
	if typ != service.SharedSubscriptionOrderType || status != service.OrderStatusRecharging {
		return fmt.Errorf("invalid shared order state")
	}
	var raw []byte
	var target, replaceTarget, assigned sql.NullInt64
	var refunded bool
	if err = tx.QueryRowContext(ctx, "SELECT snapshot,renew_subscription_id,replace_subscription_id,subscription_id,refunded FROM shared_subscription_order_items WHERE order_id=$1 FOR UPDATE", orderID).Scan(&raw, &target, &replaceTarget, &assigned, &refunded); err != nil {
		return err
	}
	if assigned.Valid {
		return tx.Commit()
	}
	if refunded {
		return errors.New("cannot fulfill refunded shared subscription")
	}
	var p service.SharedSubscriptionPlan
	if err = json.Unmarshal(raw, &p); err != nil {
		return err
	}
	entitlement := &p
	if live, liveErr := readSharedPlan(ctx, tx, p.ID); liveErr == nil {
		entitlement = live
	}
	var subID int64
	if target.Valid {
		sub, err := loadSharedSub(ctx, tx, target.Int64, true)
		if err != nil {
			return err
		}
		if sub.UserID != userID || sub.Status != "active" || sub.PlanID != p.ID {
			return errors.New("shared renewal ownership or plan mismatch")
		}
		subID = sub.ID
		if _, err = tx.ExecContext(ctx, "UPDATE shared_subscriptions SET expires_at=GREATEST(expires_at,NOW())+$2 * interval '1 day',updated_at=NOW() WHERE id=$1", subID, p.ValidityDays); err != nil {
			return err
		}
	} else if replaceTarget.Valid {
		replaced, err := loadSharedSub(ctx, tx, replaceTarget.Int64, true)
		if err != nil {
			return err
		}
		if replaced.UserID != userID || replaced.Status != "active" {
			return errors.New("shared replacement ownership or state mismatch")
		}
		if _, err = tx.ExecContext(ctx, "UPDATE shared_subscriptions SET status='revoked',updated_at=NOW() WHERE id=$1", replaced.ID); err != nil {
			return err
		}
		sub, err := createSharedSubWithDuration(ctx, tx, userID, entitlement, p.ValidityDays)
		if err != nil {
			return err
		}
		subID = sub.ID
	} else {
		sub, err := createSharedSubWithDuration(ctx, tx, userID, entitlement, p.ValidityDays)
		if err != nil {
			return err
		}
		subID = sub.ID
	}
	if _, err = tx.ExecContext(ctx, "UPDATE shared_subscription_order_items SET subscription_id=$2,fulfilled_at=NOW() WHERE order_id=$1", orderID, subID); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *sharedSubscriptionRepository) RefundOrder(ctx context.Context, orderID int64, refund bool) error {
	// The payment state machine may already own an Ent transaction.
	if dbent.TxFromContext(ctx) != nil {
		return changeSharedRefund(ctx, r.executor(ctx), orderID, refund)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changeSharedRefund(ctx, tx, orderID, refund); err != nil {
		return err
	}
	return tx.Commit()
}
func changeSharedRefund(ctx context.Context, q sharedSQL, orderID int64, refund bool) error {
	var id, replacedID sql.NullInt64
	var days int
	var refunded bool
	if err := sharedOne(ctx, q, "SELECT subscription_id,replace_subscription_id,duration_days,refunded FROM shared_subscription_order_items WHERE order_id=$1 FOR UPDATE", []any{orderID}, &id, &replacedID, &days, &refunded); err != nil {
		return err
	}
	if refund == refunded {
		return nil
	}
	if !id.Valid {
		return errors.New("shared order has not been fulfilled")
	}
	if replacedID.Valid {
		newStatus, oldStatus := "active", "revoked"
		if refund {
			newStatus, oldStatus = "revoked", "active"
		}
		if _, err := q.ExecContext(ctx, "UPDATE shared_subscriptions SET status=$2,updated_at=NOW() WHERE id=$1", id.Int64, newStatus); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "UPDATE shared_subscriptions SET status=$2,updated_at=NOW() WHERE id=$1", replacedID.Int64, oldStatus); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, "UPDATE shared_subscription_order_items SET refunded=$2 WHERE order_id=$1", orderID, refund)
		return err
	}
	if refund {
		days = -days
	}
	if _, err := q.ExecContext(ctx, "UPDATE shared_subscriptions SET expires_at=expires_at+$2 * interval '1 day',updated_at=NOW() WHERE id=$1", id.Int64, days); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "UPDATE shared_subscription_order_items SET refunded=$2 WHERE order_id=$1", orderID, refund)
	return err
}
