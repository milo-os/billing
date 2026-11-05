// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const binderOrg = "acme"

var binderNamespace = organizationNamespacePrefix + binderOrg

func binderScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	must(t, clientgoscheme.AddToScheme(s))
	must(t, billingv1alpha1.AddToScheme(s))
	must(t, resourcemanagerv1alpha1.AddToScheme(s))
	return s
}

func binderProject(name string, mutate ...func(*resourcemanagerv1alpha1.Project)) *resourcemanagerv1alpha1.Project {
	p := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: binderOrg},
		},
	}
	for _, m := range mutate {
		m(p)
	}
	return p
}

func binderAccount(name string, phase billingv1alpha1.BillingAccountPhase, isDefault bool) *billingv1alpha1.BillingAccount {
	return &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: binderNamespace},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD", DefaultForNewProjects: isDefault},
		Status:     billingv1alpha1.BillingAccountStatus{Phase: phase},
	}
}

func binderBinding(name, project, account string, phase billingv1alpha1.BillingAccountBindingPhase) *billingv1alpha1.BillingAccountBinding {
	return &billingv1alpha1.BillingAccountBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: binderNamespace},
		Spec: billingv1alpha1.BillingAccountBindingSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: account},
			ProjectRef:        billingv1alpha1.ProjectRef{Name: project},
		},
		Status: billingv1alpha1.BillingAccountBindingStatus{Phase: phase},
	}
}

func newBinder(t *testing.T, objs ...client.Object) (*ProjectBillingBinder, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(binderScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&billingv1alpha1.BillingAccount{}, &billingv1alpha1.BillingAccountBinding{}).
		WithIndex(&resourcemanagerv1alpha1.Project{}, ProjectOwnerOrganizationField, func(obj client.Object) []string {
			return []string{obj.(*resourcemanagerv1alpha1.Project).Spec.OwnerRef.Name}
		}).
		Build()
	return &ProjectBillingBinder{client: c, reader: c}, c
}

func reconcileProject(t *testing.T, r *ProjectBillingBinder, name string) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: name}}); err != nil {
		t.Fatalf("Reconcile(%s): %v", name, err)
	}
}

// bindingsFor returns the project's bindings, by billing account.
func bindingsFor(t *testing.T, c client.Client, project string) []billingv1alpha1.BillingAccountBinding {
	t.Helper()
	var list billingv1alpha1.BillingAccountBindingList
	must(t, c.List(context.Background(), &list, client.InNamespace(binderNamespace)))
	var out []billingv1alpha1.BillingAccountBinding
	for _, b := range list.Items {
		if b.Spec.ProjectRef.Name == project {
			out = append(out, b)
		}
	}
	return out
}

func TestProjectBillingBinder(t *testing.T) {
	ready := billingv1alpha1.BillingAccountPhaseReady

	t.Run("binds a new project to the organization's only account", func(t *testing.T) {
		r, c := newBinder(t, binderProject("web"), binderAccount("ba-acme", ready, false))
		reconcileProject(t, r, "web")

		got := bindingsFor(t, c, "web")
		if len(got) != 1 || got[0].Spec.BillingAccountRef.Name != "ba-acme" {
			t.Fatalf("bindings = %+v, want one to ba-acme", got)
		}
		if got[0].Annotations[AutoBoundAnnotation] != "true" || !strings.HasPrefix(got[0].Name, "bab-web-") {
			t.Errorf("binding %s, annotations %v; want a bab-web- binding marked auto-bound", got[0].Name, got[0].Annotations)
		}

		// Reconciling again doesn't bind it twice.
		reconcileProject(t, r, "web")
		if n := len(bindingsFor(t, c, "web")); n != 1 {
			t.Errorf("bindings after a second reconcile = %d, want 1", n)
		}
	})

	t.Run("leaves a project that's already bound, even to another account", func(t *testing.T) {
		r, c := newBinder(t,
			binderProject("web"),
			binderAccount("ba-acme", ready, false),
			binderBinding("chosen", "web", "ba-other", billingv1alpha1.BillingAccountBindingPhaseActive),
		)
		reconcileProject(t, r, "web")
		if got := bindingsFor(t, c, "web"); len(got) != 1 || got[0].Name != "chosen" {
			t.Errorf("bindings = %+v, want only the client's", got)
		}
	})

	t.Run("treats a binding still settling as bound", func(t *testing.T) {
		r, c := newBinder(t,
			binderProject("web"),
			binderAccount("ba-acme", ready, false),
			binderBinding("just-made", "web", "ba-acme", ""),
		)
		reconcileProject(t, r, "web")
		if n := len(bindingsFor(t, c, "web")); n != 1 {
			t.Errorf("bindings = %d, want 1", n)
		}
	})

	t.Run("rebinds a project whose only binding was superseded", func(t *testing.T) {
		r, c := newBinder(t,
			binderProject("web"),
			binderAccount("ba-acme", ready, false),
			binderBinding("old", "web", "ba-gone", billingv1alpha1.BillingAccountBindingPhaseSuperseded),
		)
		reconcileProject(t, r, "web")
		if n := len(bindingsFor(t, c, "web")); n != 2 {
			t.Errorf("bindings = %d, want the old one and a new one", n)
		}
	})

	t.Run("waits until the organization has a Ready account", func(t *testing.T) {
		account := binderAccount("ba-acme", billingv1alpha1.BillingAccountPhaseProvisioning, false)
		r, c := newBinder(t, binderProject("web"), account)
		reconcileProject(t, r, "web")
		if n := len(bindingsFor(t, c, "web")); n != 0 {
			t.Fatalf("bound to a Provisioning account: %d bindings", n)
		}

		account.Status.Phase = ready
		must(t, c.Status().Update(context.Background(), account))
		// The account's update brings the organization's projects back.
		requests := r.projectsForOrganization(context.Background(), account)
		if len(requests) != 1 || requests[0].Name != "web" {
			t.Fatalf("requests for the account = %v, want web", requests)
		}
		reconcileProject(t, r, "web")
		if n := len(bindingsFor(t, c, "web")); n != 1 {
			t.Errorf("bindings once Ready = %d, want 1", n)
		}
	})

	t.Run("with several accounts, uses the default or leaves the project", func(t *testing.T) {
		r, c := newBinder(t,
			binderProject("web"),
			binderAccount("ba-one", ready, false),
			binderAccount("ba-two", ready, false),
		)
		reconcileProject(t, r, "web")
		if n := len(bindingsFor(t, c, "web")); n != 0 {
			t.Fatalf("guessed between two accounts: %d bindings", n)
		}

		r, c = newBinder(t,
			binderProject("web"),
			binderAccount("ba-one", ready, false),
			binderAccount("ba-two", ready, true),
		)
		reconcileProject(t, r, "web")
		if got := bindingsFor(t, c, "web"); len(got) != 1 || got[0].Spec.BillingAccountRef.Name != "ba-two" {
			t.Errorf("bindings = %+v, want one to the default, ba-two", got)
		}
	})

	t.Run("skips projects that opt out, are deleting, or aren't an organization's", func(t *testing.T) {
		now := metav1.Now()
		r, c := newBinder(t,
			binderProject("canary", func(p *resourcemanagerv1alpha1.Project) {
				p.Annotations = map[string]string{ProjectAutoBindAnnotation: "false"}
			}),
			binderProject("leaving", func(p *resourcemanagerv1alpha1.Project) {
				p.DeletionTimestamp = &now
				p.Finalizers = []string{"test"}
			}),
			binderAccount("ba-acme", ready, false),
		)
		for _, name := range []string{"canary", "leaving", "missing"} {
			reconcileProject(t, r, name)
			if n := len(bindingsFor(t, c, name)); n != 0 {
				t.Errorf("%s: %d bindings, want none", name, n)
			}
		}
	})
}

func TestAccountForNewProjects(t *testing.T) {
	now := metav1.Now()
	ready := billingv1alpha1.BillingAccountPhaseReady
	deleting := binderAccount("deleting", ready, true)
	deleting.DeletionTimestamp = &now

	for _, tc := range []struct {
		name       string
		accounts   []billingv1alpha1.BillingAccount
		want       string
		wantReason bool
	}{
		{name: "none"},
		{name: "only Provisioning", accounts: []billingv1alpha1.BillingAccount{*binderAccount("a", billingv1alpha1.BillingAccountPhaseProvisioning, false)}},
		{name: "one Ready", accounts: []billingv1alpha1.BillingAccount{*binderAccount("a", ready, false)}, want: "a"},
		{name: "a deleting default doesn't count", accounts: []billingv1alpha1.BillingAccount{*binderAccount("a", ready, false), *deleting}, want: "a"},
		{name: "two, one default", accounts: []billingv1alpha1.BillingAccount{*binderAccount("a", ready, false), *binderAccount("b", ready, true)}, want: "b"},
		{name: "two, no default", accounts: []billingv1alpha1.BillingAccount{*binderAccount("a", ready, false), *binderAccount("b", ready, false)}, wantReason: true},
		{name: "two defaults", accounts: []billingv1alpha1.BillingAccount{*binderAccount("a", ready, true), *binderAccount("b", ready, true)}, wantReason: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := accountForNewProjects(tc.accounts)
			name := ""
			if got != nil {
				name = got.Name
			}
			if name != tc.want || (reason != "") != tc.wantReason {
				t.Errorf("accountForNewProjects = %q, %q; want %q (reason: %v)", name, reason, tc.want, tc.wantReason)
			}
		})
	}
}

func TestAutoBindingName(t *testing.T) {
	if got := autoBindingName("web"); got != "bab-web-" {
		t.Errorf("autoBindingName(web) = %q", got)
	}
	long := autoBindingName(strings.Repeat("p", 80))
	if len(long)+5 > 63 || !strings.HasSuffix(long, "-") {
		t.Errorf("autoBindingName(long) = %q (%d chars), want room for a 5-character suffix", long, len(long))
	}
}
