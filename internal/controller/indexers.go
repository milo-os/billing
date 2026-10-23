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

	// InvoiceBillingAccountRefField is the field index for looking up invoices
	// by their billing account reference.
	InvoiceBillingAccountRefField = ".spec.billingAccountRef.name"

	// MeterDefinitionMeterNameField is the field index for looking up
	// MeterDefinitions by their canonical meter name.
	MeterDefinitionMeterNameField = "spec.meterName"

	// BillingEntitlementOfferRefField indexes BillingEntitlements by offer name.
	BillingEntitlementOfferRefField = ".spec.offerRef.name"

	// ArrangementBillingAccountRefField indexes BillingArrangements by
	// billing account name.
	ArrangementBillingAccountRefField = ".spec.billingAccountRef.name"

	// ArrangementApplicationRefField indexes BillingArrangements by the
	// application they approve.
	ArrangementApplicationRefField = ".spec.applicationRef.name"

	// ApplicationBillingAccountRefField indexes
	// BillingArrangementApplications by billing account name.
	ApplicationBillingAccountRefField = ".spec.billingAccountRef.name"

	// DeclineApplicationRefField indexes BillingArrangementDeclines by the
	// application they decline.
	DeclineApplicationRefField = ".spec.applicationRef.name"

	// BillingEntitlementBillingAccountRefField indexes BillingEntitlements by
	// billing account name.
	BillingEntitlementBillingAccountRefField = ".spec.billingAccountRef.name"

	// PaymentMethodBillingAccountRefField indexes PaymentMethods by
	// billing account name.
	PaymentMethodBillingAccountRefField = ".spec.billingAccountRef.name"
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

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.Invoice{},
		InvoiceBillingAccountRefField,
		func(obj client.Object) []string {
			invoice := obj.(*billingv1alpha1.Invoice)
			return []string{invoice.Spec.BillingAccountRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingArrangement{},
		ArrangementBillingAccountRefField,
		func(obj client.Object) []string {
			arr := obj.(*billingv1alpha1.BillingArrangement)
			return []string{arr.Spec.BillingAccountRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingArrangement{},
		ArrangementApplicationRefField,
		arrangementApplicationRef,
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingArrangementApplication{},
		ApplicationBillingAccountRefField,
		func(obj client.Object) []string {
			app := obj.(*billingv1alpha1.BillingArrangementApplication)
			return []string{app.Spec.BillingAccountRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingArrangementDecline{},
		DeclineApplicationRefField,
		func(obj client.Object) []string {
			decline := obj.(*billingv1alpha1.BillingArrangementDecline)
			return []string{decline.Spec.ApplicationRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingEntitlement{},
		BillingEntitlementOfferRefField,
		func(obj client.Object) []string {
			be := obj.(*billingv1alpha1.BillingEntitlement)
			return []string{be.Spec.OfferRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingEntitlement{},
		BillingEntitlementBillingAccountRefField,
		func(obj client.Object) []string {
			be := obj.(*billingv1alpha1.BillingEntitlement)
			return []string{be.Spec.BillingAccountRef.Name}
		},
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.PaymentMethod{},
		PaymentMethodBillingAccountRefField,
		func(obj client.Object) []string {
			pm := obj.(*billingv1alpha1.PaymentMethod)
			return []string{pm.Spec.BillingAccountRef.Name}
		},
	); err != nil {
		return err
	}

	return nil
}

// arrangementApplicationRef indexes an arrangement under the application
// it approves. Arrangements granted without an application aren't
// indexed.
func arrangementApplicationRef(obj client.Object) []string {
	arr := obj.(*billingv1alpha1.BillingArrangement)
	if arr.Spec.ApplicationRef == nil {
		return nil
	}
	return []string{arr.Spec.ApplicationRef.Name}
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
