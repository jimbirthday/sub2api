package service

import (
	"context"
	"time"
)

type ModelAccessDecisionAudit struct {
	RequestID       string
	OperationID     string
	UserID          int64
	PolicyRevision  int64
	CatalogRevision int64
	Decision        string
	ReasonCode      string
	Identities      []CanonicalModelIdentity
	Latency         time.Duration
	CreatedAt       time.Time
}

type ModelAccessDecisionAuditRepository interface {
	RecordModelAccessDecision(context.Context, ModelAccessDecisionAudit) error
}
