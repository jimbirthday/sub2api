package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *userModelRestrictionRepository) RecordModelAccessDecision(ctx context.Context, d service.ModelAccessDecisionAudit) error {
	identities, err := json.Marshal(d.Identities)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO model_access_decisions(request_id,operation_id,user_id,policy_revision,catalog_revision,decision,reason_code,model_identities,latency_ms,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10)`, d.RequestID, d.OperationID, d.UserID, d.PolicyRevision, d.CatalogRevision, d.Decision, d.ReasonCode, string(identities), int(d.Latency.Milliseconds()), d.CreatedAt)
	if err != nil {
		return err
	}
	return nil
}
