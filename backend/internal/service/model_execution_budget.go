package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrModelExecutionBudgetUnavailable = errors.New("model execution budget unavailable")
	ErrModelExecutionBudgetExceeded    = errors.New("model execution budget exceeded")
	ErrModelExecutionRevisionConflict  = errors.New("model execution budget revision conflict")
)

type ModelExecutionReservation struct {
	UserID           int64
	OperationID      string
	Targets          []string
	PolicyRevision   int64
	CatalogRevision  int64
	PayloadHash      string
	UserLimit        int
	RPMLimit         int
	BatchOutstanding int
	BatchLimit       int
	// TargetWeights is optional; absent entries have weight one. Weights are
	// retained in Redis so a later retarget can change model capacity by CAS.
	TargetWeights   map[string]int
	TargetLimits    map[string]int
	TargetRPMLimits map[string]int
}

type ModelExecutionExtension struct {
	UserID           int64
	OperationID      string
	ExpectedRevision int64
	ExpectedEpoch    int64
	Targets          []string
	PolicyRevision   int64
	CatalogRevision  int64
	PayloadHash      string
	TargetWeights    map[string]int
	TargetLimits     map[string]int
	TargetRPMLimits  map[string]int
}

type ModelExecutionRetarget struct {
	UserID           int64
	OperationID      string
	ExpectedRevision int64
	ExpectedEpoch    int64
	Targets          []string
	PolicyRevision   int64
	CatalogRevision  int64
	PayloadHash      string
	TargetWeights    map[string]int
	TargetLimits     map[string]int
	TargetRPMLimits  map[string]int
}

type ModelExecutionConfirmation struct {
	UserID           int64
	OperationID      string
	ExpectedRevision int64
	ExpectedEpoch    int64
	Targets          []string
	PolicyRevision   int64
	CatalogRevision  int64
	PayloadHash      string
	TargetWeights    map[string]int
	TargetLimits     map[string]int
	TargetRPMLimits  map[string]int
}

type ModelExecutionLease struct {
	UserID          int64
	OperationID     string
	Revision        int64
	Epoch           int64
	State           string
	Targets         []string
	PayloadHash     string
	PolicyRevision  int64
	CatalogRevision int64
}

type modelExecutionBudgetContextKey struct{}

type modelExecutionBudgetContext struct {
	cache                           ModelExecutionBudgetCache
	telemetry                       *DistillationTelemetryService
	userID                          int64
	batchOutstanding, batchLimit    int
	operationID                     string
	payloadHash                     string
	lease                           *ModelExecutionLease
	mu                              sync.Mutex
	unknown                         bool
	completed                       bool
	budgetBypassed                  bool
	durable                         bool
	attempts                        []string
	heartbeatStop                   chan struct{}
	policyRevision, catalogRevision int64
	ownerID, holdID, fundsState     string
}

func WithModelExecutionBudget(ctx context.Context, cache ModelExecutionBudgetCache, telemetry *DistillationTelemetryService, userID int64, userLimit, rpmLimit int, operationID string, payload []byte) context.Context {
	if ctx == nil || cache == nil || telemetry == nil || userID <= 0 {
		return ctx
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		operationID = generateRequestID()
	}
	sum := sha256.Sum256(payload)
	return context.WithValue(ctx, modelExecutionBudgetContextKey{}, &modelExecutionBudgetContext{
		cache: cache, telemetry: telemetry, userID: userID,
		fundsState: "none", ownerID: generateRequestID(), operationID: operationID, payloadHash: hex.EncodeToString(sum[:]),
	})
}

func ModelExecutionOperationID(ctx context.Context) string {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return ""
	}
	return b.operationID
}

func UpdateModelExecutionPayload(ctx context.Context, payload []byte) {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return
	}
	sum := sha256.Sum256(payload)
	b.mu.Lock()
	b.payloadHash = hex.EncodeToString(sum[:])
	b.mu.Unlock()
}

func normalizeModelExecutionTargets(identities []CanonicalModelIdentity) []string {
	seen := make(map[string]struct{}, len(identities))
	result := make([]string, 0, len(identities))
	for _, identity := range identities {
		target := normalizeRestrictionNamespace(identity.Namespace) + ":" + normalizeRestrictionModel(identity.CanonicalModel)
		if target == ":" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		result = append(result, target)
	}
	sort.Strings(result)
	return result
}

func modelExecutionTargetCaps(decision *UserModelRestrictionDecision) (map[string]int, map[string]int) {
	concurrency := make(map[string]int)
	rpm := make(map[string]int)
	if decision == nil {
		return concurrency, rpm
	}
	for target, caps := range decision.ModelCaps {
		target = strings.TrimSpace(target)
		if caps.Concurrency > 0 {
			concurrency[target] = caps.Concurrency
		}
		if caps.RPM > 0 {
			rpm[target] = caps.RPM
		}
	}
	return concurrency, rpm
}

// ReserveAndConfirmModelExecution creates the durable intent before reserving
// Redis capacity, then confirms that the final targets and policy versions are
// still exactly the ones authorized immediately before the network write.
func ReserveAndConfirmModelExecution(ctx context.Context, decision *UserModelRestrictionDecision) error {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil || decision == nil {
		return nil
	}
	b.mu.Lock()
	if b.unknown || b.completed {
		b.mu.Unlock()
		return ErrModelExecutionBudgetUnavailable
	}
	limits, rpm := modelExecutionTargetCaps(decision)
	// Observation is telemetry, not a second user concurrency/RPM limiter.
	// Only explicit model caps and remote asynchronous work need this lease.
	if b.lease == nil && !b.durable && len(limits) == 0 && len(rpm) == 0 {
		b.budgetBypassed = true
		b.mu.Unlock()
		return nil
	}
	b.budgetBypassed = false
	b.mu.Unlock()
	if err := b.telemetry.RecoverExecutionBudgets(ctx, b.cache); err != nil {
		return ErrModelExecutionBudgetUnavailable
	}
	targets := normalizeModelExecutionTargets(decision.Identities)
	if len(targets) == 0 {
		// Unknown identities remain compatible, but still consume user capacity
		// and require a durable intent before any provider write.
		targets = []string{"unknown:unresolved"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unknown || b.completed {
		return ErrModelExecutionBudgetUnavailable
	}
	targetLimits, targetRPMLimits := modelExecutionTargetCaps(decision)
	if b.lease != nil {
		if modelExecutionLeaseMatches(b.lease, targets, decision, b.payloadHash) {
			return confirmModelExecution(ctx, b, decision, targets, targetLimits, targetRPMLimits)
		}
		intent, err := b.telemetry.GetOperationIntent(ctx, b.operationID)
		if err != nil || intent == nil {
			return ErrModelExecutionBudgetUnavailable
		}
		if _, err = b.telemetry.UpdateOperationIntentTargets(ctx, b.operationID, intent.Revision, targets, time.Now().UTC()); err != nil {
			return ErrModelExecutionRevisionConflict
		}
		extension := ModelExecutionExtension{UserID: b.userID, OperationID: b.operationID, ExpectedRevision: b.lease.Revision, ExpectedEpoch: b.lease.Epoch, Targets: targets, PolicyRevision: decision.PolicyRevision, CatalogRevision: decision.CatalogRevision, PayloadHash: b.payloadHash, TargetLimits: targetLimits, TargetRPMLimits: targetRPMLimits}
		if modelExecutionTargetsSuperset(targets, b.lease.Targets) {
			lease, err := b.cache.ExtendModelExecution(ctx, extension)
			if err == nil {
				b.lease = lease
				return confirmModelExecution(ctx, b, decision, targets, targetLimits, targetRPMLimits)
			}
			_, _ = b.telemetry.TransitionOperationIntent(context.Background(), b.operationID, intent.Revision+1, "reconcile_required", time.Now().UTC())
			return err
		}
		retarget := ModelExecutionRetarget{UserID: b.userID, OperationID: b.operationID, ExpectedRevision: b.lease.Revision, ExpectedEpoch: b.lease.Epoch, Targets: targets, PolicyRevision: decision.PolicyRevision, CatalogRevision: decision.CatalogRevision, PayloadHash: b.payloadHash, TargetLimits: targetLimits, TargetRPMLimits: targetRPMLimits}
		lease, err := b.cache.RetargetModelExecution(ctx, retarget)
		if err == nil {
			b.lease = lease
			return confirmModelExecution(ctx, b, decision, targets, targetLimits, targetRPMLimits)
		}
		_, _ = b.telemetry.TransitionOperationIntent(context.Background(), b.operationID, intent.Revision+1, "reconcile_required", time.Now().UTC())
		return err
	}
	intent, err := b.telemetry.CreateOperationIntent(ctx, b.operationID, b.userID, "generation", targets)
	if err != nil {
		return ErrModelExecutionBudgetUnavailable
	}
	lease, err := b.cache.ReserveModelExecution(ctx, ModelExecutionReservation{
		UserID: b.userID, OperationID: b.operationID, Targets: targets,
		PolicyRevision: decision.PolicyRevision, CatalogRevision: decision.CatalogRevision,
		PayloadHash: b.payloadHash, BatchOutstanding: b.batchOutstanding, BatchLimit: b.batchLimit,
		TargetLimits: targetLimits, TargetRPMLimits: targetRPMLimits,
	})
	if err != nil {
		if errors.Is(err, ErrModelExecutionBudgetExceeded) {
			_, _ = b.telemetry.TransitionOperationIntent(context.Background(), b.operationID, intent.Revision, "cancelled", time.Now().UTC())
		}
		return err
	}
	confirmation := ModelExecutionConfirmation{
		UserID: b.userID, OperationID: b.operationID, ExpectedRevision: lease.Revision, ExpectedEpoch: lease.Epoch,
		Targets: targets, PolicyRevision: decision.PolicyRevision,
		CatalogRevision: decision.CatalogRevision, PayloadHash: b.payloadHash,
		TargetLimits: targetLimits, TargetRPMLimits: targetRPMLimits,
	}
	lease, err = b.cache.ConfirmModelExecution(ctx, confirmation)
	if err != nil {
		if lease != nil {
			_ = b.cache.ReleaseModelExecution(context.Background(), b.userID, b.operationID, lease.Revision, lease.Epoch)
		}
		_, _ = b.telemetry.TransitionOperationIntent(context.Background(), b.operationID, intent.Revision, "cancelled", time.Now().UTC())
		return err
	}
	b.lease = lease
	b.policyRevision, b.catalogRevision = decision.PolicyRevision, decision.CatalogRevision
	if intent.State == "reserved" {
		if _, err = b.telemetry.TransitionOperationIntent(ctx, b.operationID, intent.Revision, "running", time.Now().UTC()); err != nil {
			_ = b.cache.MarkModelExecutionUnknown(context.Background(), b.userID, b.operationID, lease.Revision, lease.Epoch)
			return ErrModelExecutionBudgetUnavailable
		}
	}
	return nil
}

func confirmModelExecution(ctx context.Context, b *modelExecutionBudgetContext, decision *UserModelRestrictionDecision, targets []string, limits, rpm map[string]int) error {
	lease, err := b.cache.ConfirmModelExecution(ctx, ModelExecutionConfirmation{UserID: b.userID, OperationID: b.operationID, ExpectedRevision: b.lease.Revision, ExpectedEpoch: b.lease.Epoch, Targets: targets, PolicyRevision: decision.PolicyRevision, CatalogRevision: decision.CatalogRevision, PayloadHash: b.payloadHash, TargetLimits: limits, TargetRPMLimits: rpm})
	if err != nil {
		return err
	}
	b.lease = lease
	b.policyRevision, b.catalogRevision = decision.PolicyRevision, decision.CatalogRevision
	return nil
}

func modelExecutionLeaseMatches(lease *ModelExecutionLease, targets []string, decision *UserModelRestrictionDecision, payload string) bool {
	if lease == nil || lease.PayloadHash != payload || decision == nil || lease.PolicyRevision != decision.PolicyRevision || lease.CatalogRevision != decision.CatalogRevision {
		return false
	}
	if len(lease.Targets) != len(targets) {
		return false
	}
	for i := range targets {
		if lease.Targets[i] != targets[i] {
			return false
		}
	}
	return true
}

func modelExecutionTargetsSuperset(next, previous []string) bool {
	set := make(map[string]struct{}, len(next))
	for _, target := range next {
		set[target] = struct{}{}
	}
	for _, target := range previous {
		if _, ok := set[target]; !ok {
			return false
		}
	}
	return len(next) >= len(previous)
}

func CompleteModelExecution(ctx context.Context, success bool) {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lease == nil || b.completed || b.unknown {
		return
	}
	for _, id := range b.attempts {
		state := "completed"
		if !success {
			state = "unknown"
		}
		if err := b.telemetry.TransitionExecutionAttempt(context.Background(), id, state, b.lease.Epoch); err != nil {
			success = false
		}
	}
	if b.heartbeatStop != nil {
		close(b.heartbeatStop)
		b.heartbeatStop = nil
	}
	state := "completed"
	if !success {
		state = "reconcile_required"
	}
	intent, err := b.telemetry.GetOperationIntent(context.Background(), b.operationID)
	if err == nil && intent != nil {
		_, _ = b.telemetry.TransitionOperationIntent(context.Background(), b.operationID, intent.Revision, state, time.Now().UTC())
	}
	if success {
		_ = b.cache.ReleaseModelExecution(context.Background(), b.userID, b.operationID, b.lease.Revision, b.lease.Epoch)
		b.completed = true
	} else {
		_ = b.cache.MarkModelExecutionUnknown(context.Background(), b.userID, b.operationID, b.lease.Revision, b.lease.Epoch)
		b.unknown = true
	}
}

func CarryModelExecutionBudget(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent == nil {
		return base
	}
	if value, ok := parent.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext); ok && value != nil {
		return context.WithValue(base, modelExecutionBudgetContextKey{}, value)
	}
	return base
}

// PersistModelExecutionAttempt must run after final authorization and before the
// network write. An uncertain previous attempt prevents automatic resubmission.
func PersistModelExecutionAttempt(ctx context.Context, account *Account) error {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lease == nil && b.budgetBypassed {
		return nil
	}
	if b.lease == nil || b.unknown || b.completed {
		return ErrModelExecutionBudgetUnavailable
	}
	a := ExecutionAttempt{PolicyRevision: b.policyRevision, CatalogRevision: b.catalogRevision, OwnerID: b.ownerID, HoldID: b.holdID, FundsState: b.fundsState, OperationID: b.operationID, AttemptID: generateRequestID(), UserID: b.userID, OwnerEpoch: b.lease.Epoch, PayloadHash: b.payloadHash, Targets: b.lease.Targets}
	if account != nil {
		a.AccountID = account.ID
		a.Provider = account.Platform
	}
	if err := b.telemetry.CreateExecutionAttempt(ctx, a); err != nil {
		return ErrModelExecutionBudgetUnavailable
	}
	b.attempts = append(b.attempts, a.AttemptID)
	if b.heartbeatStop == nil {
		stop := make(chan struct{})
		b.heartbeatStop = stop
		go func() {
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					b.mu.Lock()
					ids := append([]string(nil), b.attempts...)
					epoch := b.lease.Epoch
					b.mu.Unlock()
					for _, id := range ids {
						if b.telemetry.TransitionExecutionAttempt(context.Background(), id, "running", epoch) != nil {
							b.mu.Lock()
							b.unknown = true
							b.mu.Unlock()
							return
						}
					}
				}
			}
		}()
	}
	return nil
}

// BeginModelExecutionTurn rotates the logical operation after the preceding
// turn's settlement barrier. Unknown attempts cannot be silently rotated away.
func BeginModelExecutionTurn(ctx context.Context, turn int) error {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil || turn <= 1 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unknown {
		return ErrModelExecutionBudgetUnavailable
	}
	if !b.completed && b.lease != nil {
		return ErrModelExecutionBudgetUnavailable
	}
	b.operationID = generateRequestID()
	b.lease = nil
	b.attempts = nil
	b.completed = false
	b.budgetBypassed = false
	return nil
}

// RequireDurableModelExecution keeps asynchronous remote work recoverable even
// without an explicit model cap. Ordinary observed HTTP requests use telemetry.
func RequireDurableModelExecution(ctx context.Context) {
	if ctx == nil {
		return
	}
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b != nil {
		b.mu.Lock()
		b.durable = true
		b.mu.Unlock()
	}
}

// BindModelExecutionHold records the already reserved hold; this layer never
// performs a refund, leaving the existing idempotent billing command owner.
func BindModelExecutionHold(ctx context.Context, holdID string) {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.holdID = holdID
	b.fundsState = "reserved"
}

// SetModelExecutionBatchOutstanding binds pending item count before reservation.
func SetModelExecutionBatchOutstanding(ctx context.Context, count, limit int) error {
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if count < 0 || b.lease != nil {
		return ErrModelExecutionRevisionConflict
	}
	b.batchOutstanding = count
	b.batchLimit = limit
	b.durable = true
	return nil
}

// CompleteConfirmedLiveExecution is called only for a remote terminal event.
// Expiry, transport loss and local lease failure are not termination evidence.
func CompleteConfirmedLiveExecution(ctx context.Context) {
	if ctx == nil {
		return
	}
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.completed || b.lease == nil {
		b.mu.Unlock()
		return
	}
	b.unknown = false
	b.mu.Unlock()
	CompleteModelExecution(ctx, true)
}

// MarkCurrentModelExecutionAttemptUnknown fences retries as soon as a provider
// write has an uncertain outcome. A later successful retry is not termination
// evidence for the earlier write.
func MarkCurrentModelExecutionAttemptUnknown(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	CompleteModelExecution(ctx, false)
	b, _ := ctx.Value(modelExecutionBudgetContextKey{}).(*modelExecutionBudgetContext)
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.unknown
}
