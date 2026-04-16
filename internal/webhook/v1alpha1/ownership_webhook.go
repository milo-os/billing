// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// ManagedByLabel is the well-known label used to indicate which operator
	// manages a resource.
	ManagedByLabel = "app.kubernetes.io/managed-by"

	// ServicesOperatorValue is the label value set by the services operator.
	ServicesOperatorValue = "services-operator"
)

var ownershipLog = logf.Log.WithName("ownership-webhook")

// SetupMeterDefinitionOwnershipWebhookWithManager registers the MeterDefinition
// ownership webhook with the manager.
//
// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-meterdefinition-ownership,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=meterdefinitions,verbs=create;update;delete,versions=v1alpha1,name=vmeterdefinitionownership.kb.io,admissionReviewVersions=v1
func SetupMeterDefinitionOwnershipWebhookWithManager(mgr ctrl.Manager, servicesOperatorServiceAccount string) error {
	wh := &ownershipWebhook{servicesOperatorSA: servicesOperatorServiceAccount}

	mgr.GetWebhookServer().Register(
		"/validate-billing-miloapis-com-v1alpha1-meterdefinition-ownership",
		admission.WithCustomValidator(
			mgr.GetScheme(),
			&billingv1alpha1.MeterDefinition{},
			wh,
		),
	)
	return nil
}

// SetupMonitoredResourceTypeOwnershipWebhookWithManager registers the
// MonitoredResourceType ownership webhook with the manager.
//
// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-monitoredresourcetype-ownership,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=monitoredresourcetypes,verbs=create;update;delete,versions=v1alpha1,name=vmonitoredresourcetypeownership.kb.io,admissionReviewVersions=v1
func SetupMonitoredResourceTypeOwnershipWebhookWithManager(mgr ctrl.Manager, servicesOperatorServiceAccount string) error {
	wh := &ownershipWebhook{servicesOperatorSA: servicesOperatorServiceAccount}

	mgr.GetWebhookServer().Register(
		"/validate-billing-miloapis-com-v1alpha1-monitoredresourcetype-ownership",
		admission.WithCustomValidator(
			mgr.GetScheme(),
			&billingv1alpha1.MonitoredResourceType{},
			wh,
		),
	)
	return nil
}

// ownershipWebhook rejects mutations to resources managed by the services
// operator unless the requester IS the services operator service account.
type ownershipWebhook struct {
	servicesOperatorSA string
}

var _ admission.CustomValidator = &ownershipWebhook{}

// ValidateCreate implements admission.CustomValidator.
func (w *ownershipWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return w.validateOwnership(ctx, obj)
}

// ValidateUpdate implements admission.CustomValidator.
func (w *ownershipWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return w.validateOwnership(ctx, newObj)
}

// ValidateDelete implements admission.CustomValidator.
func (w *ownershipWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return w.validateOwnership(ctx, obj)
}

// validateOwnership rejects the request when the object is managed by the
// services operator but the caller is not the services operator service account.
func (w *ownershipWebhook) validateOwnership(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	metaObj, ok := obj.(interface {
		GetLabels() map[string]string
		GetName() string
	})
	if !ok {
		return nil, fmt.Errorf("object does not implement metav1.Object")
	}

	labels := metaObj.GetLabels()
	if labels[ManagedByLabel] != ServicesOperatorValue {
		// Not managed by services operator; no ownership restriction.
		return nil, nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		ownershipLog.Error(err, "failed to retrieve admission request from context; denying for safety")
		return nil, fmt.Errorf("unable to determine request identity; denying for safety")
	}

	caller := req.UserInfo.Username
	if caller == w.servicesOperatorSA {
		return nil, nil
	}

	ownershipLog.Info("ownership check failed",
		"name", metaObj.GetName(),
		"caller", caller,
		"requiredSA", w.servicesOperatorSA,
	)

	return nil, fmt.Errorf(
		"resource is managed by the services operator (label %s=%s); only %s may mutate it, got %s",
		ManagedByLabel, ServicesOperatorValue, w.servicesOperatorSA, caller,
	)
}
