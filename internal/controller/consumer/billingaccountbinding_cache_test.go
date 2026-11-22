// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"testing"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func activeBindingIn(namespace, name, project, account string) *billingv1alpha1.BillingAccountBinding {
	b := activeBinding(name, project, account)
	b.Namespace = namespace
	return &b
}

func TestBindingCache_SameProjectActiveInTwoNamespacesIsNotAttributed(t *testing.T) {
	bc := newBillingAccountBindingCache()
	bc.upsert(activeBindingIn("organization-owner", "binding", "shared-project", "owner-acct"))
	bc.upsert(activeBindingIn("organization-other", "binding", "shared-project", "other-acct"))

	if got := bc.GetActive("shared-project"); got != nil {
		t.Fatalf("expected no active binding while two namespaces claim the project, got %s/%s", got.Namespace, got.Name)
	}

	bc.delete(activeBindingIn("organization-other", "binding", "shared-project", "other-acct"))

	got := bc.GetActive("shared-project")
	if got == nil || got.Namespace != "organization-owner" {
		t.Fatalf("expected the owner's binding once the other is gone, got %v", got)
	}
}

func TestBindingCache_EvictionIsScopedToNamespace(t *testing.T) {
	bc := newBillingAccountBindingCache()
	bc.upsert(activeBindingIn("organization-owner", "binding", "my-project", "owner-acct"))

	other := activeBindingIn("organization-other", "binding", "my-project", "other-acct")
	other.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
	bc.upsert(other)
	bc.delete(other)

	got := bc.GetActive("my-project")
	if got == nil || got.Namespace != "organization-owner" || got.Spec.BillingAccountRef.Name != "owner-acct" {
		t.Fatalf("expected the owner's binding to survive another namespace's eviction, got %v", got)
	}
}

func TestBindingCache_SupersededInSameNamespaceEvicts(t *testing.T) {
	bc := newBillingAccountBindingCache()
	b := activeBindingIn("organization-owner", "binding", "my-project", "owner-acct")
	bc.upsert(b)

	superseded := b.DeepCopy()
	superseded.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
	bc.upsert(superseded)

	if got := bc.GetActive("my-project"); got != nil {
		t.Fatalf("expected no active binding after supersede, got %s/%s", got.Namespace, got.Name)
	}
}
