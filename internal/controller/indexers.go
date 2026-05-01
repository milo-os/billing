// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// BindingBillingAccountRefField is the field index for looking up bindings
	// by their billing account reference.
	BindingBillingAccountRefField = ".spec.billingAccountRef.name"

	// BindingProjectRefField is the field index for looking up bindings
	// by their project reference.
	BindingProjectRefField = ".spec.projectRef.name"

	// MeterDefinitionMeterNameField is the field index for looking up
	// MeterDefinitions by their canonical meter name.
	MeterDefinitionMeterNameField = "spec.meterName"
)

// AddIndexers adds field indexers to the manager for efficient lookups.
func AddIndexers(ctx context.Context, mgr ctrl.Manager) error {
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

// AddMeterDefinitionIndexers adds the MeterDefinition field index to the
// manager. Must be called before mgr.Start so WaitForCacheSync covers the index.
func AddMeterDefinitionIndexers(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.MeterDefinition{},
		MeterDefinitionMeterNameField,
		func(obj client.Object) []string {
			md := obj.(*billingv1alpha1.MeterDefinition)
			return []string{md.Spec.MeterName}
		},
	)
}
