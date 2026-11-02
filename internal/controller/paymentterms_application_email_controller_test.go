// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	emailTestNamespace = "milo-system"
	emailTestOrgNamespace   = "organization-acme"
)

var emailTestNow = time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)

func emailTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = billingv1alpha1.AddToScheme(scheme)
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = notificationv1alpha1.AddToScheme(scheme)
	_ = resourcemanagerv1alpha1.AddToScheme(scheme)
	return scheme
}

// testApplication is a pending application for invoice terms, submitted an
// hour ago by user u1.
func emailTestApplication() *billingv1alpha1.BillingArrangementApplication {
	return &billingv1alpha1.BillingArrangementApplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "apply-acme-1",
			Namespace:         emailTestOrgNamespace,
			UID:               "app-uid",
			CreationTimestamp: metav1.NewTime(emailTestNow.Add(-time.Hour)),
		},
		Spec: billingv1alpha1.BillingArrangementApplicationSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme-billing"},
			PaymentOption:     billingv1alpha1.ApplicationPaymentOptionInvoice,
			Organization: billingv1alpha1.ApplicantOrganization{
				LegalName: "Acme Corporation Ltd",
				Type:      billingv1alpha1.ApplicantOrganizationTypeCompany,
				Website:   "https://acme.com",
			},
			Situation: "We pay vendors by purchase order.",
			Invoice: &billingv1alpha1.ApplicationInvoiceDetails{
				EstimatedMonthlySpend: billingv1alpha1.EstimatedMonthlySpendFrom2500To10000,
				AccountsPayableEmail:  "ap@acme.com",
			},
			Authorized: true,
			Applicant:  &billingv1alpha1.Applicant{Username: "jane@acme.com", UID: "u1"},
		},
		Status: billingv1alpha1.BillingArrangementApplicationStatus{
			Phase: billingv1alpha1.BillingArrangementApplicationPhasePending,
		},
	}
}

func decidedAt(application *billingv1alpha1.BillingArrangementApplication, phase billingv1alpha1.BillingArrangementApplicationPhase, ago time.Duration) {
	at := metav1.NewTime(emailTestNow.Add(-ago))
	application.Status.Phase = phase
	application.Status.DecidedAt = &at
}

func emailTestFixtures() []client.Object {
	return []client.Object{
		&resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{
			Name:        "acme",
			Annotations: map[string]string{"kubernetes.io/display-name": "Acme Corp"},
		}},
		&iamv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "u1"},
			Spec:       iamv1alpha1.UserSpec{Email: "jane@acme.com", GivenName: "Jane"},
		},
		&billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "acme-billing", Namespace: emailTestOrgNamespace},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		},
	}
}

func newEmailReconciler(objs ...client.Object) (*PaymentTermsApplicationEmailReconciler, client.Client) {
	c := fake.NewClientBuilder().WithScheme(emailTestScheme()).
		WithObjects(append(emailTestFixtures(), objs...)...).Build()
	return &PaymentTermsApplicationEmailReconciler{
		client:                 c,
		reader:                 c,
		EmailNamespace:         emailTestNamespace,
		ReceivedEmailTemplate:  "tmpl-received",
		SubmittedEmailTemplate: "tmpl-submitted",
		ApprovedEmailTemplate:  "tmpl-approved",
		DeclinedEmailTemplate:  "tmpl-declined",
		SupportEmailAddress:    "support@datum.net",
		PortalURL:              "https://cloud.datum.net",
		StaffPortalURL:         "https://staff.datum.net/",
		MaxEventAge:            72 * time.Hour,
		now:                    func() time.Time { return emailTestNow },
	}, c
}

func reconcileEmails(t *testing.T, r *PaymentTermsApplicationEmailReconciler) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: emailTestOrgNamespace, Name: "apply-acme-1"}}
	if _, err := r.Reconcile(context.TODO(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// sentEmails maps each sent Email's template to the Email.
func sentEmailsByTemplate(t *testing.T, c client.Client) map[string]notificationv1alpha1.Email {
	t.Helper()
	list := &notificationv1alpha1.EmailList{}
	if err := c.List(context.TODO(), list, client.InNamespace(emailTestNamespace)); err != nil {
		t.Fatalf("list emails: %v", err)
	}
	sent := map[string]notificationv1alpha1.Email{}
	for _, email := range list.Items {
		sent[email.Spec.TemplateRef.Name] = email
	}
	if len(sent) != len(list.Items) {
		t.Fatalf("two emails share a template: %d emails, %d templates", len(list.Items), len(sent))
	}
	return sent
}

func variables(email notificationv1alpha1.Email) map[string]string {
	vars := map[string]string{}
	for _, v := range email.Spec.Variables {
		vars[v.Name] = v.Value
	}
	return vars
}

func assertVariables(t *testing.T, email notificationv1alpha1.Email, want map[string]string) {
	t.Helper()
	got := variables(email)
	if len(got) != len(want) {
		t.Errorf("%s: variables = %v, want %v", email.Spec.TemplateRef.Name, got, want)
		return
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s: %s = %q, want %q", email.Spec.TemplateRef.Name, name, got[name], value)
		}
	}
}

func Test_PaymentTermsApplicationEmail_NewApplication_ConfirmsAndTellsSupportOnce(t *testing.T) {
	r, c := newEmailReconciler(emailTestApplication())

	reconcileEmails(t, r)
	reconcileEmails(t, r) // the Email names make a re-run a no-op

	sent := sentEmailsByTemplate(t, c)
	if len(sent) != 2 {
		t.Fatalf("sent %d emails, want 2: %v", len(sent), sent)
	}

	received := sent["tmpl-received"]
	if received.Spec.Recipient.UserRef.Name != "u1" {
		t.Errorf("received goes to %+v, want user u1", received.Spec.Recipient)
	}
	assertVariables(t, received, map[string]string{
		"UserName":         "Jane",
		"OrganizationName": "Acme Corp",
		"ActionUrl":        "https://cloud.datum.net/onboarding/payment-terms?orgId=acme",
	})

	submitted := sent["tmpl-submitted"]
	if submitted.Spec.Recipient.EmailAddress != "support@datum.net" {
		t.Errorf("submitted goes to %+v, want support@datum.net", submitted.Spec.Recipient)
	}
	assertVariables(t, submitted, map[string]string{
		"OrganizationName":      "Acme Corp",
		"LegalName":             "Acme Corporation Ltd",
		"ApplicantEmail":        "jane@acme.com",
		"PaymentOption":         "Pay by invoice",
		"EstimatedMonthlySpend": "2,500–10,000 USD",
		"Situation":             "We pay vendors by purchase order.",
		"ReviewUrl":             "https://staff.datum.net/customers/billing-accounts/applications/acme/apply-acme-1",
	})
}

func Test_PaymentTermsApplicationEmail_NoSupportAddress_OnlyConfirms(t *testing.T) {
	r, c := newEmailReconciler(emailTestApplication())
	r.SupportEmailAddress = ""

	reconcileEmails(t, r)

	sent := sentEmailsByTemplate(t, c)
	if _, ok := sent["tmpl-received"]; !ok || len(sent) != 1 {
		t.Fatalf("sent %v, want only the confirmation", sent)
	}
}

func Test_PaymentTermsApplicationEmail_NotSure_SaysSoForSpend(t *testing.T) {
	application := emailTestApplication()
	application.Spec.PaymentOption = billingv1alpha1.ApplicationPaymentOptionNotSure
	application.Spec.Invoice = nil
	r, c := newEmailReconciler(application)

	reconcileEmails(t, r)

	vars := variables(sentEmailsByTemplate(t, c)["tmpl-submitted"])
	if vars["PaymentOption"] != "Not sure yet" || vars["EstimatedMonthlySpend"] != "Not given" {
		t.Errorf("variables = %v", vars)
	}
}

func Test_PaymentTermsApplicationEmail_Approved_SendsTheTerms(t *testing.T) {
	application := emailTestApplication()
	decidedAt(application, billingv1alpha1.BillingArrangementApplicationPhaseApproved, time.Minute)
	application.Status.ArrangementRef = &billingv1alpha1.BillingArrangementRef{Name: "acme-billing-terms-x"}
	startsAt := metav1.NewTime(time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC))
	arrangement := &billingv1alpha1.BillingArrangement{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-billing-terms-x", Namespace: emailTestOrgNamespace},
		Spec: billingv1alpha1.BillingArrangementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme-billing"},
			Type:              billingv1alpha1.BillingArrangementTypeInvoice,
			Invoice:           &billingv1alpha1.InvoiceArrangementTerms{NetDays: 45},
			StartsAt:          &startsAt,
			Reason:            "Verified company",
		},
	}
	r, c := newEmailReconciler(application, arrangement)

	reconcileEmails(t, r)
	reconcileEmails(t, r)

	sent := sentEmailsByTemplate(t, c)
	if len(sent) != 3 {
		t.Fatalf("sent %d emails, want the confirmation, the notice and the approval", len(sent))
	}
	approved := sent["tmpl-approved"]
	if approved.Spec.Recipient.UserRef.Name != "u1" {
		t.Errorf("approval goes to %+v, want user u1", approved.Spec.Recipient)
	}
	assertVariables(t, approved, map[string]string{
		"UserName":         "Jane",
		"OrganizationName": "Acme Corp",
		"NetDays":          "45",
		"StartDate":        "November 1, 2026",
		"ActionUrl":        "https://cloud.datum.net/onboarding/payment-terms?orgId=acme",
	})
}

func Test_PaymentTermsApplicationEmail_Approved_WaitsForTheTerms(t *testing.T) {
	application := emailTestApplication()
	decidedAt(application, billingv1alpha1.BillingArrangementApplicationPhaseApproved, time.Minute)
	r, c := newEmailReconciler(application)

	reconcileEmails(t, r)

	if _, ok := sentEmailsByTemplate(t, c)["tmpl-approved"]; ok {
		t.Fatal("sent the approval before billing named the approving terms")
	}
}

func Test_PaymentTermsApplicationEmail_Declined_SendsTheMessage(t *testing.T) {
	application := emailTestApplication()
	decidedAt(application, billingv1alpha1.BillingArrangementApplicationPhaseDeclined, time.Minute)
	application.Status.DeclineMessage = "Add a card to continue."
	r, c := newEmailReconciler(application)

	reconcileEmails(t, r)

	declined, ok := sentEmailsByTemplate(t, c)["tmpl-declined"]
	if !ok {
		t.Fatal("no decline email")
	}
	assertVariables(t, declined, map[string]string{
		"UserName":         "Jane",
		"OrganizationName": "Acme Corp",
		"DeclineMessage":   "Add a card to continue.",
		"ActionUrl":        "https://cloud.datum.net/onboarding/payment-terms?orgId=acme",
	})
}

func Test_PaymentTermsApplicationEmail_Withdrawn_SendsNothing(t *testing.T) {
	application := emailTestApplication()
	application.Spec.Withdrawn = true
	application.Status.Phase = ""
	r, c := newEmailReconciler(application)

	reconcileEmails(t, r)

	if sent := sentEmailsByTemplate(t, c); len(sent) != 0 {
		t.Fatalf("sent %v for a withdrawn application", sent)
	}
}

func Test_PaymentTermsApplicationEmail_OldEvents_AreNotSent(t *testing.T) {
	// Submitted and declined before MaxEventAge: what a first rollout finds.
	application := emailTestApplication()
	application.CreationTimestamp = metav1.NewTime(emailTestNow.Add(-30 * 24 * time.Hour))
	decidedAt(application, billingv1alpha1.BillingArrangementApplicationPhaseDeclined, 29*24*time.Hour)
	application.Status.DeclineMessage = "Add a card to continue."
	r, c := newEmailReconciler(application)

	reconcileEmails(t, r)

	if sent := sentEmailsByTemplate(t, c); len(sent) != 0 {
		t.Fatalf("sent %v for an application decided a month ago", sent)
	}
}

func Test_PaymentTermsApplicationEmail_RecentDecisionOnAnOldApplication_SendsOnlyTheDecision(t *testing.T) {
	application := emailTestApplication()
	application.CreationTimestamp = metav1.NewTime(emailTestNow.Add(-10 * 24 * time.Hour))
	decidedAt(application, billingv1alpha1.BillingArrangementApplicationPhaseDeclined, time.Hour)
	application.Status.DeclineMessage = "Add a card to continue."
	r, c := newEmailReconciler(application)

	reconcileEmails(t, r)

	sent := sentEmailsByTemplate(t, c)
	if _, ok := sent["tmpl-declined"]; !ok || len(sent) != 1 {
		t.Fatalf("sent %v, want only the decline", sent)
	}
}

func Test_PaymentTermsApplicationEmail_MissingUserAndOrg_FallsBack(t *testing.T) {
	application := emailTestApplication()
	application.Spec.Applicant = &billingv1alpha1.Applicant{Username: "sam@acme.com", UID: "u-gone"}
	c := fake.NewClientBuilder().WithScheme(emailTestScheme()).WithObjects(application).Build()
	r, _ := newEmailReconciler()
	r.client, r.reader = c, c

	reconcileEmails(t, r)

	vars := variables(sentEmailsByTemplate(t, c)["tmpl-received"])
	if vars["UserName"] != "sam@acme.com" || vars["OrganizationName"] != "acme" {
		t.Errorf("variables = %v", vars)
	}
	if spend := variables(sentEmailsByTemplate(t, c)["tmpl-submitted"])["EstimatedMonthlySpend"]; spend != "2,500–10,000" {
		t.Errorf("spend without an account currency = %q", spend)
	}
}
