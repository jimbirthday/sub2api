package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type decisionAuditRecorder struct {
	records  []ModelAccessDecisionAudit
	err      error
	canceled bool
}

func (a *decisionAuditRecorder) RecordModelAccessDecision(ctx context.Context, d ModelAccessDecisionAudit) error {
	a.records = append(a.records, d)
	a.canceled = ctx.Err() != nil
	return a.err
}

func TestModelAccessDecisionAuditIncludesDeniedAndUnresolvedRevisions(t *testing.T) {
	for _, tc := range []struct {
		name, model, outcome string
		want                 error
	}{
		{"allowed", "gemini-image", "allowed", nil},
		{"denied", "gpt-driver", "denied", ErrUserModelRestrictionDenied},
		{"unresolved", "missing-alias", "unresolved", ErrUserModelRestrictionIdentityUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &restrictionServiceRepoStub{rules: []UserModelRestrictionRule{{Namespace: "openai", CanonicalModel: "gpt-driver", Enabled: true}}}
			svc := NewUserModelRestrictionService(repo)
			audit := &decisionAuditRecorder{err: errors.New("audit sink unavailable")}
			svc.SetDecisionAuditRepository(audit)
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxkey.RequestID, "request-42"))
			cancel()
			_, err := svc.CheckModels(ctx, 42, []ModelIdentityInput{{Model: tc.model}})
			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
			require.Len(t, audit.records, 1)
			require.Equal(t, tc.outcome, audit.records[0].Decision)
			require.Equal(t, int64(1), audit.records[0].PolicyRevision)
			require.Equal(t, int64(1), audit.records[0].CatalogRevision)
			require.Equal(t, "request-42", audit.records[0].RequestID)
			require.False(t, audit.canceled, "client cancellation must not erase audit")
		})
	}
}

func TestModelAccessDecisionAuditRecordsDatabaseUnavailable(t *testing.T) {
	svc := NewUserModelRestrictionService(&restrictionServiceRepoStub{err: errors.New("database offline")})
	audit := &decisionAuditRecorder{}
	svc.SetDecisionAuditRepository(audit)
	_, err := svc.CheckModels(context.Background(), 42, []ModelIdentityInput{{Model: "gpt-driver"}})
	require.ErrorIs(t, err, ErrUserModelRestrictionUnavailable)
	require.Len(t, audit.records, 1)
	require.Equal(t, "unavailable", audit.records[0].Decision)
}
