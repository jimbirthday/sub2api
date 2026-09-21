package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type sharedAllocation struct {
	Pool   service.SharedPoolAuthorization `json:"pool"`
	Amount float64                         `json:"amount"`
}

// Lock subscriptions in ID order regardless of consumption order to avoid
// deadlocks between overlapping memberships and renewals changing expiry order.
func lockSharedFunding(ctx context.Context, tx *sql.Tx, f *service.SharedFunding) error {
	if f == nil || f.UserID <= 0 || f.GroupID <= 0 || f.BillingAt.IsZero() {
		return errors.New("invalid shared funding authorization")
	}
	ids := make([]int64, 0, len(f.Pools))
	seen := map[int64]bool{}
	generations := map[int64]int{}
	for _, p := range f.Pools {
		if p.ID <= 0 || p.Generation < 0 || seen[p.ID] {
			return errors.New("invalid shared funding pool")
		}
		ids = append(ids, p.ID)
		seen[p.ID] = true
		generations[p.ID] = p.Generation
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		var owner int64
		var generation int
		// Membership and plan changes after authorization do not rewrite accepted
		// requests. The immutable authorization still has to belong to the same
		// subscription owner and may not claim a future generation.
		err := tx.QueryRowContext(ctx, "SELECT user_id, generation FROM shared_subscriptions WHERE id=$1 FOR UPDATE", id).Scan(&owner, &generation)
		if err != nil {
			return err
		}
		// Older generations retain their immutable authorization after plan changes.
		if owner != f.UserID || generations[id] > generation {
			return errors.New("shared funding ownership mismatch")
		}
	}
	return nil
}
func sharedPoolCapacity(ctx context.Context, tx *sql.Tx, p service.SharedPoolAuthorization) (float64, error) {
	if len(p.Windows) != 3 {
		return 0, errors.New("shared funding must contain three windows")
	}
	remaining := math.Inf(1)
	seen := map[string]bool{}
	for _, w := range p.Windows {
		if (w.Kind != "daily" && w.Kind != "weekly" && w.Kind != "monthly") || seen[w.Kind] || w.StartsAt.IsZero() {
			return 0, errors.New("invalid shared quota window")
		}
		seen[w.Kind] = true
		if _, err := tx.ExecContext(ctx, "INSERT INTO shared_subscription_windows(subscription_id,generation,kind,starts_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", p.ID, p.Generation, w.Kind, w.StartsAt); err != nil {
			return 0, err
		}
		var used, reserved float64
		if err := tx.QueryRowContext(ctx, "SELECT used,reserved FROM shared_subscription_windows WHERE subscription_id=$1 AND generation=$2 AND kind=$3 AND starts_at=$4 FOR UPDATE", p.ID, p.Generation, w.Kind, w.StartsAt).Scan(&used, &reserved); err != nil {
			return 0, err
		}
		if w.Limit != nil {
			remaining = math.Min(remaining, service.QuantizeUsageBillingAmount(*w.Limit-used-reserved))
		}
	}
	return math.Max(0, remaining), nil
}
func updateSharedWindows(ctx context.Context, tx *sql.Tx, p service.SharedPoolAuthorization, used, reserved float64) error {
	for _, w := range p.Windows {
		if _, err := tx.ExecContext(ctx, "UPDATE shared_subscription_windows SET used=used+$5,reserved=reserved+$6 WHERE subscription_id=$1 AND generation=$2 AND kind=$3 AND starts_at=$4", p.ID, p.Generation, w.Kind, w.StartsAt, used, reserved); err != nil {
			return err
		}
	}
	return nil
}
func allocateSharedFunding(ctx context.Context, tx *sql.Tx, f *service.SharedFunding, cost float64, reserve bool) ([]sharedAllocation, float64, error) {
	if err := lockSharedFunding(ctx, tx, f); err != nil {
		return nil, 0, err
	}
	left := cost
	allocations := []sharedAllocation{}
	for _, p := range f.Pools {
		if left <= 0 {
			break
		}
		capacity, err := sharedPoolCapacity(ctx, tx, p)
		if err != nil {
			return nil, 0, err
		}
		amount := service.QuantizeUsageBillingAmount(math.Min(left, capacity))
		if amount <= 0 {
			continue
		}
		used, held := amount, 0.0
		if reserve {
			used, held = 0, amount
		}
		if err = updateSharedWindows(ctx, tx, p, used, held); err != nil {
			return nil, 0, err
		}
		allocations = append(allocations, sharedAllocation{Pool: p, Amount: amount})
		left = service.QuantizeUsageBillingAmount(left - amount)
	}
	return allocations, left, nil
}
func recordSharedSettlement(ctx context.Context, tx *sql.Tx, requestID string, keyID int64, f *service.SharedFunding, cost, wallet float64, a []sharedAllocation) error {
	subCost := service.QuantizeUsageBillingAmount(cost - wallet)
	_, err := tx.ExecContext(ctx, "INSERT INTO shared_subscription_settlements(request_id,api_key_id,user_id,group_id,cost,subscription_cost,balance_cost,billing_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", requestID, keyID, f.UserID, f.GroupID, cost, subCost, wallet, f.BillingAt)
	if err != nil {
		return err
	}
	for _, v := range a {
		if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_debits(request_id,api_key_id,subscription_id,amount) VALUES($1,$2,$3,$4)", requestID, keyID, v.Pool.ID, v.Amount); err != nil {
			return err
		}
	}
	return nil
}
func (r *usageBillingRepository) persistSharedJob(ctx context.Context, cmd *service.UsageBillingCommand) error {
	raw, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "INSERT INTO shared_subscription_billing_jobs(request_id,api_key_id,fingerprint,command) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint, raw)
	if err != nil {
		return err
	}
	var fp string
	var stored []byte
	if err = r.db.QueryRowContext(ctx, "SELECT fingerprint,command FROM shared_subscription_billing_jobs WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID).Scan(&fp, &stored); err != nil {
		return err
	}
	if fp != cmd.RequestFingerprint {
		return service.ErrUsageBillingRequestConflict
	}
	return json.Unmarshal(stored, cmd)
}

// RecoverSharedBilling retries durable commands, not pricing calculations.
func (r *usageBillingRepository) RecoverSharedBilling(ctx context.Context) ([]service.SharedBillingRecovered, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT command FROM shared_subscription_billing_jobs WHERE completed_at IS NULL AND created_at < NOW()-interval '10 seconds' ORDER BY created_at LIMIT 100")
	if err != nil {
		return nil, err
	}
	commands := []service.UsageBillingCommand{}
	for rows.Next() {
		var raw []byte
		var c service.UsageBillingCommand
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			rows.Close()
			return nil, err
		}
		commands = append(commands, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	users := []service.SharedBillingRecovered{}
	for i := range commands {
		c := &commands[i]
		result, applyErr := r.Apply(ctx, c)
		err = applyErr
		if err != nil {
			_, _ = r.db.ExecContext(ctx, "UPDATE shared_subscription_billing_jobs SET attempts=attempts+1,last_error=$3 WHERE request_id=$1 AND api_key_id=$2", c.RequestID, c.APIKeyID, fmt.Sprintf("%.1000s", err))
			continue
		}
		users = append(users, service.SharedBillingRecovered{UserID: c.UserID, APIKeyID: c.APIKeyID, Platform: c.SharedPlatform, Cost: c.SharedCost, Applied: result != nil && result.Applied})
	}
	return users, nil
}
