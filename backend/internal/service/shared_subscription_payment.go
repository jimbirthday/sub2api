package service

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	app "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *PaymentService) validateSharedOrder(ctx context.Context, req *CreateOrderRequest) error {
	if s.sharedSubscriptions == nil {
		return app.ServiceUnavailable("SHARED_SUBSCRIPTIONS_UNAVAILABLE", "shared subscriptions unavailable")
	}
	p, err := s.sharedSubscriptions.GetPlan(ctx, req.PlanID)
	if err != nil {
		return err
	}
	if !p.ForSale {
		return app.BadRequest("PLAN_NOT_AVAILABLE", "plan is not for sale")
	}
	for _, id := range p.GroupIDs {
		g, err := s.groupRepo.GetByID(ctx, id)
		if err != nil || g == nil || !g.IsActive() {
			return app.BadRequest("GROUP_NOT_AVAILABLE", "a covered group is unavailable")
		}
	}
	if req.RenewSharedSubscriptionID > 0 && req.ReplaceSharedSubscriptionID > 0 {
		return app.BadRequest("SHARED_PURCHASE_MODE_INVALID", "choose either renewal or replacement")
	}
	if req.RenewSharedSubscriptionID > 0 {
		sub, err := s.sharedSubscriptions.Get(ctx, req.RenewSharedSubscriptionID)
		if err != nil {
			return err
		}
		if sub.UserID != req.UserID {
			return app.Forbidden("FORBIDDEN", "subscription belongs to another user")
		}
		if sub.Status != "active" || sub.PlanID != p.ID {
			return app.BadRequest("SHARED_RENEWAL_CHANGED", "only an active subscription with the same plan can renew")
		}
	}
	if req.ReplaceSharedSubscriptionID > 0 {
		sub, err := s.sharedSubscriptions.Get(ctx, req.ReplaceSharedSubscriptionID)
		if err != nil {
			return err
		}
		if sub.UserID != req.UserID {
			return app.Forbidden("FORBIDDEN", "subscription belongs to another user")
		}
		if sub.Status != "active" {
			return app.BadRequest("SHARED_REPLACEMENT_CHANGED", "only an active subscription can be replaced")
		}
	}
	req.sharedPlan = p
	return nil
}
func (s *PaymentService) ExecuteSharedSubscriptionFulfillment(ctx context.Context, id int64) error {
	if s.sharedSubscriptions == nil {
		return fmt.Errorf("shared subscription service is unavailable")
	}
	o, err := s.entClient.PaymentOrder.Get(ctx, id)
	if err != nil {
		return err
	}
	if o.OrderType != SharedSubscriptionOrderType {
		return app.BadRequest("INVALID_ORDER_TYPE", "not a shared subscription order")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return app.BadRequest("INVALID_STATUS", "order cannot fulfill")
	}
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	if err != nil || lease == nil {
		return err
	}
	if err = s.sharedSubscriptions.repo.FulfillOrder(ctx, id); err != nil {
		s.markFailed(ctx, id, lease, err)
		return err
	}
	if err = s.applyAffiliateRebateForOrder(ctx, o); err != nil {
		s.markFailed(ctx, id, lease, err)
		return err
	}
	return s.markCompleted(ctx, o, lease, "SHARED_SUBSCRIPTION_SUCCESS")
}

// SharedPlanToPaymentDisplay is only an adapter for the payment provider's
// product description/price. It is never written to the legacy plan table.
func SharedPlanToPaymentDisplay(p *SharedSubscriptionPlan) *dbent.SubscriptionPlan {
	return &dbent.SubscriptionPlan{ID: p.ID, Name: p.Name, Description: p.Description, ProductName: p.Name, Price: p.Price, ValidityDays: p.ValidityDays, ValidityUnit: "days"}
}
