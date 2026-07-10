// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import "fmt"

// AttributionResult carries the outcome of the attribute step.
type AttributionResult struct {
	// OK is true when attribution succeeded.
	OK bool
	// Reason is set when OK is false.
	Reason QuarantineReason
	// Detail describes the specific cause of attribution failure when OK is false.
	Detail string
	// BillingAccountRef is the attributed billing account name when OK is true.
	BillingAccountRef string
}

// attribute finds the Active BillingAccountBinding for the project and verifies
// the referenced BillingAccount is Ready. Both lookups are pure map reads
// against informer-backed caches; no API server calls are made.
func attribute(project string, bc *BillingAccountBindingCache, ac *BillingAccountCache) AttributionResult {
	binding := bc.GetActive(project)
	if binding == nil {
		return AttributionResult{OK: false, Reason: ReasonAttributionFailure, Detail: fmt.Sprintf("no active billing account binding found for project %q", project)}
	}

	if ac.GetReady(binding.Namespace, binding.Spec.BillingAccountRef.Name) == nil {
		return AttributionResult{OK: false, Reason: ReasonAttributionFailure, Detail: fmt.Sprintf("referenced billing account %q in namespace %q is not ready or does not exist", binding.Spec.BillingAccountRef.Name, binding.Namespace)}
	}

	return AttributionResult{OK: true, BillingAccountRef: binding.Spec.BillingAccountRef.Name}
}
