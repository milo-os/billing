// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func reconcileBindingWith(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(binderScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&billingv1alpha1.BillingAccountBinding{}).
		WithIndex(&billingv1alpha1.BillingAccountBinding{}, BindingProjectRefField, func(obj client.Object) []string {
			return []string{obj.(*billingv1alpha1.BillingAccountBinding).Spec.ProjectRef.Name}
		}).
		Build()
	r := &BillingAccountBindingReconciler{client: c, projectReader: c}
	for _, obj := range objs {
		b, ok := obj.(*billingv1alpha1.BillingAccountBinding)
		if !ok {
			continue
		}
		if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
			t.Fatalf("reconcile %s: %v", b.Name, err)
		}
	}
	return c
}

func TestBindingReconcile_ProjectInOwnOrganizationBecomesActive(t *testing.T) {
	binding := binderBinding("own", "acme-web", "acct", "")
	c := reconcileBindingWith(t, binderProject("acme-web"), binding)

	var got billingv1alpha1.BillingAccountBinding
	must(t, c.Get(context.Background(), client.ObjectKeyFromObject(binding), &got))
	if got.Status.Phase != billingv1alpha1.BillingAccountBindingPhaseActive {
		t.Fatalf("expected Active, got %q", got.Status.Phase)
	}
}

func TestBindingReconcile_ProjectInAnotherOrganizationNeverActive(t *testing.T) {
	foreign := binderBinding("foreign", "globex-web", "acct", billingv1alpha1.BillingAccountBindingPhaseActive)
	project := binderProject("globex-web")
	project.Spec.OwnerRef.Name = "globex"
	c := reconcileBindingWith(t, project, foreign)

	var got billingv1alpha1.BillingAccountBinding
	must(t, c.Get(context.Background(), client.ObjectKeyFromObject(foreign), &got))
	if got.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
		t.Fatalf("binding to another organization's project must not stay Active")
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, ConditionTypeBound)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected Bound=False, got %v", cond)
	}
}

func TestBindingReconcile_ForeignBindingDoesNotSupersedeOwner(t *testing.T) {
	owner := binderBinding("owner", "acme-web", "acct", billingv1alpha1.BillingAccountBindingPhaseActive)
	owner.CreationTimestamp = metav1.Unix(1, 0)
	foreign := binderBinding("foreign", "acme-web", "acct", "")
	foreign.Namespace = organizationNamespacePrefix + "globex"
	foreign.CreationTimestamp = metav1.Unix(2, 0)
	c := reconcileBindingWith(t, binderProject("acme-web"), owner, foreign)

	var got billingv1alpha1.BillingAccountBinding
	must(t, c.Get(context.Background(), client.ObjectKeyFromObject(owner), &got))
	if got.Status.Phase != billingv1alpha1.BillingAccountBindingPhaseActive {
		t.Fatalf("expected owner binding Active, got %q", got.Status.Phase)
	}
	must(t, c.Get(context.Background(), client.ObjectKeyFromObject(foreign), &got))
	if got.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
		t.Fatalf("binding in another organization's namespace must not become Active")
	}
}
