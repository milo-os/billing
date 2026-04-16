// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// ConditionTypeReady is the condition type used across the billing group
	// to surface overall resource readiness.
	ConditionTypeReady = "Ready"

	// ConditionTypePublished mirrors spec.phase: Status=True when the resource
	// is in one of the post-Draft phases (Published, Deprecated, Retired),
	// Status=False while the resource is still Draft.
	ConditionTypePublished = "Published"
)

// statusEqual returns true when two CatalogStatus values are semantically
// identical (same observedGeneration, publishedAt, and condition set).
func statusEqual(a, b billingv1alpha1.CatalogStatus) bool {
	if a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if (a.PublishedAt == nil) != (b.PublishedAt == nil) {
		return false
	}
	if a.PublishedAt != nil && b.PublishedAt != nil && !a.PublishedAt.Equal(b.PublishedAt) {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		ac, bc := a.Conditions[i], b.Conditions[i]
		if ac.Type != bc.Type || ac.Status != bc.Status ||
			ac.Reason != bc.Reason || ac.ObservedGeneration != bc.ObservedGeneration {
			return false
		}
	}
	return true
}
