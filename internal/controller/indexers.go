// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// BindingBillingAccountRefField is the field index for looking up bindings
	// by their billing account reference.
	BindingBillingAccountRefField = ".spec.billingAccountRef.name"

	// BindingProjectRefField is the field index for looking up bindings
	// by their project reference.
	BindingProjectRefField = ".spec.projectRef.name"
)

// AddIndexers adds field indexers to the manager for efficient lookups.
func AddIndexers(ctx context.Context, mgr mcmanager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingAccountBinding{},
		BindingBillingAccountRefField,
		func(obj client.Object) []string {
			binding := obj.(*billingv1alpha1.BillingAccountBinding)
			return []string{binding.Spec.BillingAccountRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingAccountBinding{},
		BindingProjectRefField,
		func(obj client.Object) []string {
			binding := obj.(*billingv1alpha1.BillingAccountBinding)
			return []string{binding.Spec.ProjectRef.Name}
		},
	); err != nil {
		return err
	}

	return nil
}
