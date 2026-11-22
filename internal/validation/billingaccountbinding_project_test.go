// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func validateCreateAgainstProjects(t *testing.T, namespace, projectName string, projects ...*resourcemanagerv1alpha1.Project) field.ErrorList {
	t.Helper()

	scheme := newScheme()
	if err := resourcemanagerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: namespace},
		Status:     billingv1alpha1.BillingAccountStatus{Phase: billingv1alpha1.BillingAccountPhaseReady},
	}
	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(account).
		WithStatusSubresource(account).
		WithIndex(&billingv1alpha1.BillingAccountBinding{}, ".spec.projectRef.name", func(obj client.Object) []string {
			return []string{obj.(*billingv1alpha1.BillingAccountBinding).Spec.ProjectRef.Name}
		})
	for _, p := range projects {
		builder = builder.WithObjects(p)
	}
	cl := builder.Build()

	binding := &billingv1alpha1.BillingAccountBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: namespace},
		Spec: billingv1alpha1.BillingAccountBindingSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acct"},
			ProjectRef:        billingv1alpha1.ProjectRef{Name: projectName},
		},
	}
	return ValidateBillingAccountBindingCreate(binding, BillingAccountBindingValidationOptions{
		Context:       context.Background(),
		Client:        cl,
		ProjectReader: cl,
	})
}

func projectOwnedBy(name, org string) *resourcemanagerv1alpha1.Project {
	return &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: org},
		},
	}
}

func TestValidateBindingCreate_ProjectInOwnOrganizationAllowed(t *testing.T) {
	errs := validateCreateAgainstProjects(t, "organization-acme", "acme-web", projectOwnedBy("acme-web", "acme"))
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateBindingCreate_ProjectInAnotherOrganizationRejectedAsNotFound(t *testing.T) {
	foreign := validateCreateAgainstProjects(t, "organization-acme", "globex-web", projectOwnedBy("globex-web", "globex"))
	missing := validateCreateAgainstProjects(t, "organization-acme", "globex-web")

	if len(foreign) != 1 || foreign[0].Type != field.ErrorTypeNotFound {
		t.Fatalf("expected one NotFound error for a foreign project, got %v", foreign)
	}
	if foreign.ToAggregate().Error() != missing.ToAggregate().Error() {
		t.Fatalf("foreign and missing projects must be indistinguishable:\n foreign: %v\n missing: %v", foreign, missing)
	}
}

func TestValidateBindingCreate_NamespaceOutsideAnyOrganizationRejected(t *testing.T) {
	errs := validateCreateAgainstProjects(t, "default", "acme-web", projectOwnedBy("acme-web", "acme"))
	if len(errs) != 1 || errs[0].Type != field.ErrorTypeNotFound {
		t.Fatalf("expected one NotFound error, got %v", errs)
	}
}
