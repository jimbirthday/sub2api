package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type modelManagementTxKey struct{}

func beginModelManagementTx(ctx context.Context, db *sql.DB) (*sql.Tx, bool, error) {
	if tx, ok := ctx.Value(modelManagementTxKey{}).(*sql.Tx); ok {
		return tx, false, nil
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return tx, true, err
}

func (r *userModelRestrictionRepository) ExecuteModelManagementMutation(ctx context.Context, m service.ModelManagementMutation, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Transaction advisory lock serializes identical keys before checking replay.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmtMutationKey(m.ActorID, m.Key)); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(struct {
		Resource, Reason string
		Payload          json.RawMessage
	}{m.Resource, m.Reason, m.Payload})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var previous string
	var result []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM model_management_mutations WHERE actor_id=$1 AND idempotency_key=$2`, m.ActorID, m.Key).Scan(&previous, &result)
	if err == nil {
		if previous != hash {
			return nil, service.ErrModelManagementIdempotencyConflict
		}
		return json.RawMessage(result), tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	result, err = fn(context.WithValue(ctx, modelManagementTxKey{}, tx))
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO model_management_mutations(actor_id,idempotency_key,request_hash,resource,reason,result) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, m.ActorID, m.Key, hash, m.Resource, m.Reason, string(result))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result), tx.Commit()
}

func fmtMutationKey(actor int64, key string) string {
	b, _ := json.Marshal([]any{actor, key})
	return string(b)
}
