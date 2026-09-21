package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func reserveSharedHold(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.SharedFunding.UserID != cmd.UserID {
		return nil, errors.New("shared hold owner mismatch")
	}
	allocations, wallet, err := allocateSharedFunding(ctx, tx, cmd.SharedFunding, service.QuantizeUsageBillingAmount(cmd.HoldAmount), true)
	if err != nil {
		return nil, err
	}
	funding, err := json.Marshal(cmd.SharedFunding)
	if err != nil {
		return nil, err
	}
	split, err := json.Marshal(allocations)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO shared_subscription_holds(batch_id,user_id,api_key_id,funding,allocations,wallet_amount,total_amount) VALUES($1,$2,$3,$4,$5,$6,$7)", cmd.BatchID, cmd.UserID, cmd.APIKeyID, funding, split, wallet, cmd.HoldAmount); err != nil {
		return nil, err
	}
	cp := *cmd
	cp.HoldAmount = wallet
	return reserveUsageBillingBatchImageBalance(ctx, tx, &cp)
}
func finishSharedHold(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand, capture bool) (*service.BatchImageBalanceHoldResult, error) {
	var raw, split []byte
	var wallet, total float64
	var status string
	var uid, keyID int64
	err := tx.QueryRowContext(ctx, "SELECT user_id,api_key_id,funding,allocations,wallet_amount,total_amount,status FROM shared_subscription_holds WHERE batch_id=$1 FOR UPDATE", cmd.BatchID).Scan(&uid, &keyID, &raw, &split, &wallet, &total, &status)
	if errors.Is(err, sql.ErrNoRows) && (!capture || (cmd.HoldAmount == 0 && cmd.ActualAmount == 0)) {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	if uid != cmd.UserID || keyID != cmd.APIKeyID {
		return nil, errors.New("shared hold ownership mismatch")
	}
	target := "released"
	actual := 0.0
	if capture {
		target = "captured"
		actual = service.QuantizeUsageBillingAmount(cmd.ActualAmount)
	}
	if status == target {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if status != "held" {
		return nil, errors.New("shared hold already finalized")
	}
	if actual > total || actual < 0 {
		return nil, service.ErrBatchImageSettlementCostExceedsHold
	}
	var funding service.SharedFunding
	var held []sharedAllocation
	if err = json.Unmarshal(raw, &funding); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(split, &held); err != nil {
		return nil, err
	}
	if err = lockSharedFunding(ctx, tx, &funding); err != nil {
		return nil, err
	}
	left := actual
	used := []sharedAllocation{}
	for _, a := range held {
		consume := service.QuantizeUsageBillingAmount(math.Min(left, a.Amount))
		if err = updateSharedWindows(ctx, tx, a.Pool, consume, -a.Amount); err != nil {
			return nil, err
		}
		if consume > 0 {
			used = append(used, sharedAllocation{Pool: a.Pool, Amount: consume})
		}
		left = service.QuantizeUsageBillingAmount(left - consume)
	}
	cp := *cmd
	cp.HoldAmount = wallet
	cp.ActualAmount = left
	var result *service.BatchImageBalanceHoldResult
	if capture {
		result, err = captureUsageBillingBatchImageBalance(ctx, tx, &cp)
	} else {
		// The locked independent hold proves this wallet amount was reserved.
		// Capturing zero releases it without relying on the legacy request-id format.
		cp.ActualAmount = 0
		result, err = captureUsageBillingBatchImageBalance(ctx, tx, &cp)
	}
	if err != nil {
		return nil, err
	}
	if capture {
		if err = recordSharedSettlement(ctx, tx, cmd.RequestID, cmd.APIKeyID, &funding, actual, left, used); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE shared_subscription_holds SET status=$2 WHERE batch_id=$1", cmd.BatchID, target)
	return result, err
}
func captureSharedHold(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return finishSharedHold(ctx, tx, cmd, true)
}
func releaseSharedHold(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return finishSharedHold(ctx, tx, cmd, false)
}
