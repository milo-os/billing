// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func invoiceApplication(name string) *billingv1alpha1.BillingArrangementApplication {
	return &billingv1alpha1.BillingArrangementApplication{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme", UID: types.UID(name)},
		Spec: billingv1alpha1.BillingArrangementApplicationSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			PaymentOption:     billingv1alpha1.ApplicationPaymentOptionInvoice,
			Organization: billingv1alpha1.ApplicantOrganization{
				LegalName: "Acme Corporation Ltd",
				Type:      billingv1alpha1.ApplicantOrganizationTypeCompany,
				Website:   "https://acme.example",
			},
			Situation: "We pay vendors by purchase order on net-30 terms.",
			Invoice: &billingv1alpha1.ApplicationInvoiceDetails{
				EstimatedMonthlySpend: billingv1alpha1.EstimatedMonthlySpendFrom2500To10000,
				AccountsPayableEmail:  "ap@acme.example",
				PurchaseOrderNumber:   "PO-4471",
			},
			Authorized: true,
			Applicant:  &billingv1alpha1.Applicant{Username: "jo@acme.example", UID: "user-jo"},
		},
	}
}

func applicationDecline(name, application string) *billingv1alpha1.BillingArrangementDecline {
	return &billingv1alpha1.BillingArrangementDecline{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme"},
		Spec: billingv1alpha1.BillingArrangementDeclineSpec{
			ApplicationRef: billingv1alpha1.BillingArrangementApplicationRef{Name: application},
			Message:        "Add a card to continue.",
		},
	}
}

// approval grants terms with an end date, so the account can still apply
// to renew them.
func approval(name, application string) *billingv1alpha1.BillingArrangement {
	arr := invoiceArrangement(name, arrangementTime(1), arrangementTime(30))
	arr.Spec.ApplicationRef = &billingv1alpha1.BillingArrangementApplicationRef{Name: application}
	return arr
}

// acmeOwner is the owner reference the defaulting webhook adds to an
// application for the acme billing account.
var acmeOwner = metav1.OwnerReference{
	APIVersion: billingv1alpha1.GroupVersion.String(), Kind: "BillingAccount", Name: "acme", UID: "acme-uid",
}

// applicationTestClient is arrangementTestClient with an account that has
// billing contact details and an address, which applying requires.
func applicationTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "acme-uid"},
		Spec: billingv1alpha1.BillingAccountSpec{
			CurrencyCode: "USD",
			ContactInfo: &billingv1alpha1.BillingContactInfo{
				Email:   "billing@acme.example",
				Address: &billingv1alpha1.BillingAddress{Country: "GB"},
			},
		},
	}
	bare := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "no-contact", Namespace: "org-acme"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	noAddress := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "no-address", Namespace: "org-acme"},
		Spec: billingv1alpha1.BillingAccountSpec{
			CurrencyCode: "USD",
			ContactInfo:  &billingv1alpha1.BillingContactInfo{Email: "billing@acme.example"},
		},
	}
	leaving := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "leaving", Namespace: "org-acme", UID: "leaving-uid",
			DeletionTimestamp: arrangementTime(5), Finalizers: []string{"billing.miloapis.com/test"},
		},
		Spec: billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(append(objs, account, bare, noAddress, leaving)...).Build()
}

func TestValidateBillingArrangementApplicationCreate(t *testing.T) {
	withdrawn := invoiceApplication("withdrawn")
	withdrawn.Spec.Withdrawn = true
	otherAccount := invoiceApplication("globex")
	otherAccount.Spec.BillingAccountRef.Name = "globex"
	orphan := invoiceApplication("orphan")
	orphan.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "acme", UID: "deleted-account"}}
	globexTerms := invoiceArrangement("globex-terms", arrangementTime(1), nil)
	globexTerms.Spec.BillingAccountRef.Name = "globex"

	tests := []struct {
		name     string
		existing []client.Object
		mutate   func(*billingv1alpha1.BillingArrangementApplication)
		wantErr  string
	}{
		{
			name: "valid invoice application",
		},
		{
			name: "valid not-sure application",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.Spec.PaymentOption = billingv1alpha1.ApplicationPaymentOptionNotSure
				a.Spec.Invoice = nil
			},
		},
		{
			name:    "missing billing account",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.BillingAccountRef.Name = "nope" },
			wantErr: "spec.billingAccountRef.name: Not found",
		},
		{
			name:    "billing account without contact details",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.BillingAccountRef.Name = "no-contact" },
			wantErr: "add billing contact details",
		},
		{
			name:    "billing account without an address",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.BillingAccountRef.Name = "no-address" },
			wantErr: "and a billing address",
		},
		{
			name: "owned by its billing account",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.OwnerReferences = []metav1.OwnerReference{acmeOwner}
			},
		},
		{
			name: "owned by another billing account",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				ref := acmeOwner
				ref.UID = "someone-else"
				a.OwnerReferences = []metav1.OwnerReference{acmeOwner, ref}
			},
			wantErr: `can only be owned by billing account "acme"`,
		},
		{
			name: "owned by something else",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "x", UID: "x"}}
			},
			wantErr: `can only be owned by billing account "acme"`,
		},
		{
			name:     "open-ended terms are already active",
			existing: []client.Object{invoiceArrangement("terms", arrangementTime(1), nil)},
			wantErr:  `billing account "acme" already has payment terms with no end date`,
		},
		{
			name:     "terms are scheduled to start",
			existing: []client.Object{invoiceArrangement("terms", arrangementTime(10), nil)},
			wantErr:  `billing account "acme" already has payment terms starting at 2026-10-10T00:00:00Z`,
		},
		{
			name:     "renewing active terms with an end date",
			existing: []client.Object{invoiceArrangement("terms", arrangementTime(1), arrangementTime(30))},
		},
		{
			name:     "terms that have ended",
			existing: []client.Object{invoiceArrangement("terms", arrangementTime(1), arrangementTime(5))},
		},
		{
			name:     "open-ended terms on another account",
			existing: []client.Object{globexTerms},
		},
		{
			name:    "invoice option without invoice details",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Invoice = nil },
			wantErr: "invoice is required when paymentOption is Invoice",
		},
		{
			name: "invoice details with another option",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.Spec.PaymentOption = billingv1alpha1.ApplicationPaymentOptionNotSure
			},
			wantErr: "invoice may only be set when paymentOption is Invoice",
		},
		{
			name: "accounts payable email with a display name",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.Spec.Invoice.AccountsPayableEmail = "Accounts <ap@acme.example>"
			},
			wantErr: "must be a plain email address",
		},
		{
			name:    "website without a scheme",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Organization.Website = "acme.example" },
			wantErr: "must be an http or https URL",
		},
		{
			name: "website with another scheme",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.Spec.Organization.Website = "ftp://acme.example"
			},
			wantErr: "must be an http or https URL",
		},
		{
			name:    "not authorized",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Authorized = false },
			wantErr: "must confirm they're authorized",
		},
		{
			name:    "no applicant",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Applicant = nil },
			wantErr: "spec.applicant: Required",
		},
		{
			name:    "withdrawn on submission",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Withdrawn = true },
			wantErr: "can't be withdrawn when it's submitted",
		},
		{
			name:     "another application awaiting review",
			existing: []client.Object{invoiceApplication("first")},
			wantErr:  `application "first" for billing account "acme" is already awaiting review`,
		},
		{
			name: "earlier applications that were decided or withdrawn",
			existing: []client.Object{
				invoiceApplication("declined"), applicationDecline("declined-no", "declined"),
				invoiceApplication("approved"), approval("approved-terms", "approved"),
				withdrawn,
			},
		},
		{
			name:     "a pending application for another account",
			existing: []client.Object{otherAccount},
		},
		{
			name:     "a pending application left over from a deleted account",
			existing: []client.Object{orphan},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := invoiceApplication("apply")
			if tt.mutate != nil {
				tt.mutate(app)
			}
			c := applicationTestClient(t, tt.existing...)
			errs := ValidateBillingArrangementApplicationCreate(context.Background(), c, app, arrangementTime(6).Time)
			checkErrs(t, errs.ToAggregate(), tt.wantErr)
		})
	}
}

func TestValidateBillingArrangementApplicationUpdate(t *testing.T) {
	tests := []struct {
		name     string
		existing []client.Object
		old      func(*billingv1alpha1.BillingArrangementApplication)
		mutate   func(*billingv1alpha1.BillingArrangementApplication)
		wantErr  string
	}{
		{
			name:   "metadata-only change",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) { a.Labels = map[string]string{"x": "y"} },
		},
		{
			name:   "withdraw a pending application",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Withdrawn = true },
		},
		{
			name:     "withdraw a declined application",
			existing: []client.Object{applicationDecline("no", "apply")},
			mutate:   func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Withdrawn = true },
			wantErr:  "already been declined",
		},
		{
			name:     "withdraw an approved application",
			existing: []client.Object{approval("terms", "apply")},
			mutate:   func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Withdrawn = true },
			wantErr:  `already been approved by arrangement "terms"`,
		},
		{
			name:    "reopen a withdrawn application",
			old:     func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Withdrawn = true },
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Withdrawn = false },
			wantErr: "can't be reopened",
		},
		{
			name:    "edit a submitted application",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.Spec.Situation = "Something else" },
			wantErr: "can't be changed after it's submitted",
		},
		{
			name: "change the applicant",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.Spec.Applicant.Username = "mallory@acme.example"
			},
			wantErr: "can't be changed after it's submitted",
		},
		{
			name: "point the owner reference at another account",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.OwnerReferences[0].UID = "someone-else"
			},
			wantErr: "owner references can't be added or changed",
		},
		{
			name: "add an owner reference",
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.OwnerReferences = append(a.OwnerReferences, metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: "x", UID: "x"})
			},
			wantErr: "owner references can't be added or changed",
		},
		{
			name:    "remove the owner reference while the account exists",
			mutate:  func(a *billingv1alpha1.BillingArrangementApplication) { a.OwnerReferences = nil },
			wantErr: `the owner reference to billing account "acme" can't be removed while the account exists`,
		},
		{
			name: "garbage collector orphans the application from a deleting account",
			old: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "leaving", UID: "leaving-uid"}}
			},
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) { a.OwnerReferences = nil },
		},
		{
			name: "garbage collector removes a reference to a deleted account",
			old: func(a *billingv1alpha1.BillingArrangementApplication) {
				a.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "acme", UID: "deleted-account"}}
			},
			mutate: func(a *billingv1alpha1.BillingArrangementApplication) { a.OwnerReferences = nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldApp := invoiceApplication("apply")
			oldApp.OwnerReferences = []metav1.OwnerReference{acmeOwner}
			if tt.old != nil {
				tt.old(oldApp)
			}
			newApp := oldApp.DeepCopy()
			if tt.mutate != nil {
				tt.mutate(newApp)
			}
			c := applicationTestClient(t, append(tt.existing, oldApp.DeepCopy())...)
			errs := ValidateBillingArrangementApplicationUpdate(context.Background(), c, oldApp, newApp)
			checkErrs(t, errs.ToAggregate(), tt.wantErr)
		})
	}
}

func TestValidateBillingArrangementDecline(t *testing.T) {
	withdrawn := invoiceApplication("withdrawn")
	withdrawn.Spec.Withdrawn = true

	tests := []struct {
		name        string
		application string
		existing    []client.Object
		wantErr     string
	}{
		{
			name:        "decline a pending application",
			application: "apply",
		},
		{
			name:        "missing application",
			application: "nope",
			wantErr:     "spec.applicationRef.name: Not found",
		},
		{
			name:        "withdrawn application",
			application: "withdrawn",
			wantErr:     `application "withdrawn" was withdrawn`,
		},
		{
			name:        "already declined",
			application: "apply",
			existing:    []client.Object{applicationDecline("first-no", "apply")},
			wantErr:     "already been declined",
		},
		{
			name:        "already approved",
			application: "apply",
			existing:    []client.Object{approval("terms", "apply")},
			wantErr:     `already been approved by arrangement "terms"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := applicationTestClient(t, append(tt.existing, invoiceApplication("apply"), withdrawn)...)
			errs := ValidateBillingArrangementDeclineCreate(context.Background(), c, applicationDecline("no", tt.application))
			checkErrs(t, errs.ToAggregate(), tt.wantErr)
		})
	}

	t.Run("message can be corrected", func(t *testing.T) {
		oldDecline := applicationDecline("no", "apply")
		newDecline := oldDecline.DeepCopy()
		newDecline.Spec.Message = "Reply with your registration number."
		newDecline.Spec.InternalNotes = "Asked for registration number"
		checkErrs(t, ValidateBillingArrangementDeclineUpdate(oldDecline, newDecline).ToAggregate(), "")
	})
	t.Run("applicationRef is immutable", func(t *testing.T) {
		oldDecline := applicationDecline("no", "apply")
		newDecline := oldDecline.DeepCopy()
		newDecline.Spec.ApplicationRef.Name = "other"
		checkErrs(t, ValidateBillingArrangementDeclineUpdate(oldDecline, newDecline).ToAggregate(), "applicationRef is immutable")
	})
}

func TestValidateBillingArrangementApprovingApplication(t *testing.T) {
	now := arrangementTime(6).Time
	globex := invoiceApplication("globex-apply")
	globex.Spec.BillingAccountRef.Name = "globex"

	tests := []struct {
		name        string
		application string
		existing    []client.Object
		wantErr     string
	}{
		{
			name:        "approve a pending application",
			application: "apply",
		},
		{
			name:        "missing application",
			application: "nope",
			wantErr:     "spec.applicationRef.name: Not found",
		},
		{
			name:        "application for another billing account",
			application: "globex-apply",
			wantErr:     `application is for billing account "globex", not "acme"`,
		},
		{
			name:        "declined application",
			application: "apply",
			existing:    []client.Object{applicationDecline("no", "apply")},
			wantErr:     "already been declined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := applicationTestClient(t, append(tt.existing, invoiceApplication("apply"), globex)...)
			arr := invoiceArrangement("terms", nil, nil)
			arr.Spec.ApplicationRef = &billingv1alpha1.BillingArrangementApplicationRef{Name: tt.application}
			errs := ValidateBillingArrangementCreate(context.Background(), c, arr, now)
			checkErrs(t, errs.ToAggregate(), tt.wantErr)
		})
	}

	t.Run("applicationRef is immutable", func(t *testing.T) {
		c := applicationTestClient(t)
		oldArr := approval("terms", "apply")
		for _, ref := range []*billingv1alpha1.BillingArrangementApplicationRef{nil, {Name: "other"}} {
			newArr := oldArr.DeepCopy()
			newArr.Spec.ApplicationRef = ref
			errs := ValidateBillingArrangementUpdate(context.Background(), c, oldArr, newArr, now)
			checkErrs(t, errs.ToAggregate(), "applicationRef is immutable")
		}
	})
}

func checkErrs(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Errorf("expected error containing %q, got none", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
}
