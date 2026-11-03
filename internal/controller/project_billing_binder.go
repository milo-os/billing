// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	// ProjectAutoBindAnnotation set to "false" on a Project keeps billing
	// from binding it to a billing account, for projects nobody should be
	// billed for (internal canaries, say).
	ProjectAutoBindAnnotation = "billing.miloapis.com/auto-bind"

	// AutoBoundAnnotation marks a BillingAccountBinding billing created
	// itself, rather than a client.
	AutoBoundAnnotation = "billing.miloapis.com/auto-bound"

	// ProjectOwnerOrganizationField indexes Projects by the organization
	// that owns them.
	ProjectOwnerOrganizationField = ".spec.ownerRef.name"

	// autoBindingNamePrefix matches the cloud portal's binding names.
	autoBindingNamePrefix = "bab-"
)

// ProjectBillingBinder binds each project to its organization's billing
// account when nothing else has, so every project's usage is billed.
//
// Clients used to do this themselves after creating a project (the cloud
// portal does), so a project created another way (datumctl, the API,
// automation, or a portal session that stopped partway) had no billing
// account and its usage was dropped. Now billing does it:
//
//   - A project without an active binding is bound to its organization's
//     only Ready billing account, or, when the organization has several,
//     to the one with spec.defaultForNewProjects. With several and no
//     default, it's left unbound and logged: billing can't guess which
//     account pays.
//   - A project created before its organization has a billing account is
//     bound once one is Ready.
//   - Projects annotated billing.miloapis.com/auto-bind=false are skipped.
//
// It never moves a project that has an active binding, so clients that
// bind projects themselves still decide where they go.
type ProjectBillingBinder struct {
	client client.Client
	// reader reads bindings from the API server rather than the cache, so a
	// binding created moments ago (by a client, or by an earlier reconcile)
	// isn't missed and doubled.
	reader client.Reader
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch

// Reconcile binds one project, if it needs a billing account.
func (r *ProjectBillingBinder) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("project", req.Name)

	var project resourcemanagerv1alpha1.Project
	if err := r.client.Get(ctx, req.NamespacedName, &project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !project.DeletionTimestamp.IsZero() ||
		project.Spec.OwnerRef.Kind != "Organization" || project.Spec.OwnerRef.Name == "" ||
		project.Annotations[ProjectAutoBindAnnotation] == "false" {
		return ctrl.Result{}, nil
	}
	namespace := organizationNamespacePrefix + project.Spec.OwnerRef.Name

	var bindings billingv1alpha1.BillingAccountBindingList
	if err := r.reader.List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list billing account bindings: %w", err)
	}
	if hasActiveBinding(bindings.Items, project.Name) {
		return ctrl.Result{}, nil
	}

	var accounts billingv1alpha1.BillingAccountList
	if err := r.client.List(ctx, &accounts, client.InNamespace(namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list billing accounts: %w", err)
	}
	account, reason := accountForNewProjects(accounts.Items)
	if account == nil {
		// Nothing to do until an account is Ready or marked default; the
		// BillingAccount watch brings the project back then.
		if reason != "" {
			logger.Info("project has no billing account", "organization", project.Spec.OwnerRef.Name, "reason", reason)
		}
		return ctrl.Result{}, nil
	}

	binding := &billingv1alpha1.BillingAccountBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: autoBindingName(project.Name),
			Namespace:    namespace,
			Annotations:  map[string]string{AutoBoundAnnotation: "true"},
		},
		Spec: billingv1alpha1.BillingAccountBindingSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: account.Name},
			ProjectRef:        billingv1alpha1.ProjectRef{Name: project.Name},
		},
	}
	if err := r.client.Create(ctx, binding); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// The generated name collided; try again with another.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("bind project to billing account %q: %w", account.Name, err)
	}
	logger.Info("bound project to its organization's billing account",
		"organization", project.Spec.OwnerRef.Name, "billingAccount", account.Name, "binding", binding.Name)
	return ctrl.Result{}, nil
}

// hasActiveBinding reports whether a project has a binding that's active,
// or new and about to be (its phase isn't set yet).
func hasActiveBinding(bindings []billingv1alpha1.BillingAccountBinding, project string) bool {
	for i := range bindings {
		b := &bindings[i]
		if b.Spec.ProjectRef.Name != project || !b.DeletionTimestamp.IsZero() {
			continue
		}
		if b.Status.Phase == "" || b.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
			return true
		}
	}
	return false
}

// accountForNewProjects picks the account an organization's new projects
// are billed to: its only Ready account, or its Ready account with
// defaultForNewProjects. Without one, it returns why, or "" when the
// organization simply has no Ready account yet.
func accountForNewProjects(accounts []billingv1alpha1.BillingAccount) (*billingv1alpha1.BillingAccount, string) {
	var ready, defaults []*billingv1alpha1.BillingAccount
	for i := range accounts {
		a := &accounts[i]
		if !a.DeletionTimestamp.IsZero() || a.Status.Phase != billingv1alpha1.BillingAccountPhaseReady {
			continue
		}
		ready = append(ready, a)
		if a.Spec.DefaultForNewProjects {
			defaults = append(defaults, a)
		}
	}
	switch {
	case len(ready) == 0:
		return nil, ""
	case len(ready) == 1:
		return ready[0], ""
	case len(defaults) == 1:
		return defaults[0], ""
	case len(defaults) == 0:
		return nil, fmt.Sprintf("the organization has %d billing accounts and none is defaultForNewProjects", len(ready))
	default:
		return nil, fmt.Sprintf("%d billing accounts are defaultForNewProjects", len(defaults))
	}
}

// autoBindingName is the generateName prefix for a project's binding,
// within the 63 characters a name may have with the generated suffix.
func autoBindingName(project string) string {
	const maxPrefix = 63 - 5 // generateName appends five characters
	name := autoBindingNamePrefix + project
	if len(name) > maxPrefix-1 {
		name = strings.TrimRight(name[:maxPrefix-1], "-.")
	}
	return name + "-"
}

// projectCreatedOrNeedsBinding passes Project creates and updates that
// aren't deletions, and generic events. A project's billing only changes
// when it's created, or when its organization's accounts or bindings do,
// which have their own watches.
var projectCreatedOrNeedsBinding = predicate.Funcs{
	CreateFunc: func(event.CreateEvent) bool { return true },
	DeleteFunc: func(event.DeleteEvent) bool { return false },
	UpdateFunc: func(e event.UpdateEvent) bool {
		// Picks up a project whose opt-out annotation was removed.
		return e.ObjectNew.GetDeletionTimestamp().IsZero() &&
			e.ObjectOld.GetAnnotations()[ProjectAutoBindAnnotation] != e.ObjectNew.GetAnnotations()[ProjectAutoBindAnnotation]
	},
	GenericFunc: func(event.GenericEvent) bool { return true },
}

// SetupWithManager sets up the binder with the manager.
func (r *ProjectBillingBinder) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	r.reader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		Named("projectbillingbinder").
		For(&resourcemanagerv1alpha1.Project{}, builder.WithPredicates(projectCreatedOrNeedsBinding)).
		// An account becoming Ready, or the default, can bind its
		// organization's waiting projects.
		Watches(&billingv1alpha1.BillingAccount{},
			handler.EnqueueRequestsFromMapFunc(r.projectsForOrganization)).
		// A binding deleted or superseded can leave its project unbound.
		Watches(&billingv1alpha1.BillingAccountBinding{},
			handler.EnqueueRequestsFromMapFunc(projectForBinding)).
		Complete(r)
}

// projectsForOrganization maps a BillingAccount to every project of its
// organization.
func (r *ProjectBillingBinder) projectsForOrganization(ctx context.Context, obj client.Object) []reconcile.Request {
	org, ok := strings.CutPrefix(obj.GetNamespace(), organizationNamespacePrefix)
	if !ok || org == "" {
		return nil
	}
	var projects resourcemanagerv1alpha1.ProjectList
	if err := r.client.List(ctx, &projects, client.MatchingFields{ProjectOwnerOrganizationField: org}); err != nil {
		log.FromContext(ctx).Error(err, "list projects of organization", "organization", org)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(projects.Items))
	for i := range projects.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: projects.Items[i].Name}})
	}
	return requests
}

// projectForBinding maps a binding to its project.
func projectForBinding(_ context.Context, obj client.Object) []reconcile.Request {
	binding, ok := obj.(*billingv1alpha1.BillingAccountBinding)
	if !ok || binding.Spec.ProjectRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: binding.Spec.ProjectRef.Name}}}
}
