// SPDX-License-Identifier: AGPL-3.0-only

package consumer

// AttributionResult carries the outcome of the attribute step.
type AttributionResult struct {
	// OK is true when attribution succeeded.
	OK bool
	// Reason is set when OK is false.
	Reason QuarantineReason
	// BillingAccountRef is the attributed billing account name when OK is true.
	BillingAccountRef string
}

// attribute finds the Active BillingAccountBinding for the project and verifies
// the referenced BillingAccount is Ready. Both lookups are pure map reads
// against informer-backed caches; no API server calls are made.
func attribute(project string, bc *BillingAccountBindingCache, ac *BillingAccountCache) AttributionResult {
	binding := bc.GetActive(project)
	if binding == nil {
		return AttributionResult{OK: false, Reason: ReasonAttributionFailure}
	}

	if ac.GetReady(binding.Namespace, binding.Spec.BillingAccountRef.Name) == nil {
		return AttributionResult{OK: false, Reason: ReasonAttributionFailure}
	}

	return AttributionResult{OK: true, BillingAccountRef: binding.Spec.BillingAccountRef.Name}
}
