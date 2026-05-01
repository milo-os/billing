// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// AttributionResult carries the outcome of the attribute step.
type AttributionResult struct {
	// OK is true when attribution succeeded.
	OK bool
	// Reason is set when OK is false.
	Reason QuarantineReason
	// BillingAccountRef is the attributed billing account name when OK is true.
	BillingAccountRef string
}

// attribute finds the Active BillingAccountBinding for the project via the
// in-memory watcher cache and verifies the referenced BillingAccount is Ready.
// The binding lookup is a pure map read; only the BillingAccount check hits the
// controller-runtime cache.
func attribute(ctx context.Context, project string, bc *BillingAccountBindingCache, c client.Reader) (AttributionResult, error) {
	binding := bc.GetActive(project)
	if binding == nil {
		return AttributionResult{OK: false, Reason: ReasonAttributionFailure}, nil
	}

	var account billingv1alpha1.BillingAccount
	if err := c.Get(ctx, types.NamespacedName{
		Name:      binding.Spec.BillingAccountRef.Name,
		Namespace: binding.Namespace,
	}, &account); err != nil {
		return AttributionResult{}, fmt.Errorf("getting BillingAccount %q: %w", binding.Spec.BillingAccountRef.Name, err)
	}

	if account.Status.Phase != billingv1alpha1.BillingAccountPhaseReady {
		return AttributionResult{OK: false, Reason: ReasonAttributionFailure}, nil
	}

	return AttributionResult{OK: true, BillingAccountRef: binding.Spec.BillingAccountRef.Name}, nil
}
