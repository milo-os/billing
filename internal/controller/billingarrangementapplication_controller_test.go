// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BillingArrangementApplication", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	newAccount := func(name string) *billingv1alpha1.BillingAccount {
		return &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode: "USD",
				ContactInfo:  &billingv1alpha1.BillingContactInfo{Email: "billing@example.com"},
			},
		}
	}
	// Webhooks don't run in envtest, so set the applicant explicitly.
	newApplication := func(name, account string) *billingv1alpha1.BillingArrangementApplication {
		return &billingv1alpha1.BillingArrangementApplication{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: billingv1alpha1.BillingArrangementApplicationSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: account},
				PaymentOption:     billingv1alpha1.ApplicationPaymentOptionInvoice,
				Organization: billingv1alpha1.ApplicantOrganization{
					LegalName: "Example Corporation Ltd",
					Type:      billingv1alpha1.ApplicantOrganizationTypeCompany,
					Website:   "https://example.com",
				},
				Situation: "We pay vendors by purchase order.",
				Invoice: &billingv1alpha1.ApplicationInvoiceDetails{
					EstimatedMonthlySpend: billingv1alpha1.EstimatedMonthlySpendUnder500,
					AccountsPayableEmail:  "ap@example.com",
				},
				Authorized: true,
				Applicant:  &billingv1alpha1.Applicant{Username: "jo@example.com"},
			},
		}
	}
	paymentReadyReason := func(g Gomega, account *billingv1alpha1.BillingAccount) string {
		var fetched billingv1alpha1.BillingAccount
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
		cond := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionPaymentReady)
		g.Expect(cond).NotTo(BeNil())
		return cond.Reason
	}
	applicationPhase := func(g Gomega, app *billingv1alpha1.BillingArrangementApplication) billingv1alpha1.BillingArrangementApplicationPhase {
		var fetched billingv1alpha1.BillingArrangementApplication
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), &fetched)).To(Succeed())
		return fetched.Status.Phase
	}

	It("projects a decline and then an approval onto the application and the account", func() {
		account := newAccount("test-application")
		Expect(k8sClient.Create(ctx, account)).To(Succeed())

		first := newApplication("test-application-first", account.Name)
		Expect(k8sClient.Create(ctx, first)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(applicationPhase(g, first)).To(Equal(billingv1alpha1.BillingArrangementApplicationPhasePending))
			g.Expect(paymentReadyReason(g, account)).To(Equal("ApplicationPending"))
		}, timeout, interval).Should(Succeed())

		decline := &billingv1alpha1.BillingArrangementDecline{
			ObjectMeta: metav1.ObjectMeta{Name: "test-application-decline", Namespace: "default"},
			Spec: billingv1alpha1.BillingArrangementDeclineSpec{
				ApplicationRef: billingv1alpha1.BillingArrangementApplicationRef{Name: first.Name},
				Message:        "Add a card to continue.",
				InternalNotes:  "Couldn't verify the company",
			},
		}
		Expect(k8sClient.Create(ctx, decline)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingArrangementApplication
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(first), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingArrangementApplicationPhaseDeclined))
			g.Expect(fetched.Status.DeclineMessage).To(Equal("Add a card to continue."))
			g.Expect(paymentReadyReason(g, account)).To(Equal("NotConfigured"))
		}, timeout, interval).Should(Succeed())

		second := newApplication("test-application-second", account.Name)
		Expect(k8sClient.Create(ctx, second)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(paymentReadyReason(g, account)).To(Equal("ApplicationPending"))
		}, timeout, interval).Should(Succeed())

		startsAt := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
		approval := &billingv1alpha1.BillingArrangement{
			ObjectMeta: metav1.ObjectMeta{Name: "test-application-terms", Namespace: "default"},
			Spec: billingv1alpha1.BillingArrangementSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: account.Name},
				ApplicationRef:    &billingv1alpha1.BillingArrangementApplicationRef{Name: second.Name},
				Type:              billingv1alpha1.BillingArrangementTypeInvoice,
				Invoice:           &billingv1alpha1.InvoiceArrangementTerms{CreditLimit: "1000"},
				StartsAt:          &startsAt,
				Reason:            "Approved application",
			},
		}
		Expect(k8sClient.Create(ctx, approval)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingArrangementApplication
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(second), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingArrangementApplicationPhaseApproved))
			g.Expect(fetched.Status.ArrangementRef).NotTo(BeNil())
			g.Expect(fetched.Status.ArrangementRef.Name).To(Equal(approval.Name))
			g.Expect(paymentReadyReason(g, account)).To(Equal("InvoiceTerms"))
		}, timeout, interval).Should(Succeed())

		// The applicationRef on terms can't be changed or removed.
		var arr billingv1alpha1.BillingArrangement
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(approval), &arr)).To(Succeed())
		arr.Spec.ApplicationRef = &billingv1alpha1.BillingArrangementApplicationRef{Name: first.Name}
		Expect(k8sClient.Update(ctx, &arr)).To(MatchError(ContainSubstring("applicationRef is immutable")))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(approval), &arr)).To(Succeed())
		arr.Spec.ApplicationRef = nil
		Expect(k8sClient.Update(ctx, &arr)).To(MatchError(ContainSubstring("applicationRef is immutable")))

		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	Context("schema rules", func() {
		It("rejects details that don't match the payment option", func() {
			app := newApplication("test-application-mismatch", "any")
			app.Spec.PaymentOption = billingv1alpha1.ApplicationPaymentOptionNotSure
			Expect(k8sClient.Create(ctx, app)).To(MatchError(ContainSubstring("invoice may only be set when paymentOption is Invoice")))

			app = newApplication("test-application-sponsored", "any")
			app.Spec.PaymentOption = "Sponsored"
			app.Spec.Invoice = nil
			Expect(k8sClient.Create(ctx, app)).To(MatchError(ContainSubstring(`Unsupported value: "Sponsored"`)))
		})

		It("requires the authorization confirmation", func() {
			app := newApplication("test-application-unauthorized", "any")
			app.Spec.Authorized = false
			Expect(k8sClient.Create(ctx, app)).To(MatchError(ContainSubstring("must confirm they're authorized")))
		})

		It("allows only withdrawing a submitted application, once", func() {
			app := newApplication("test-application-immutable", "any")
			Expect(k8sClient.Create(ctx, app)).To(Succeed())

			edited := app.DeepCopy()
			edited.Spec.Situation = "Something else"
			Expect(k8sClient.Update(ctx, edited)).To(MatchError(ContainSubstring("can't be changed after it's submitted")))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
			app.Spec.Withdrawn = true
			Expect(k8sClient.Update(ctx, app)).To(Succeed())

			app.Spec.Withdrawn = false
			Expect(k8sClient.Update(ctx, app)).To(MatchError(ContainSubstring("can't be reopened")))

			Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		})

		It("keeps a decline on the application it was created for", func() {
			decline := &billingv1alpha1.BillingArrangementDecline{
				ObjectMeta: metav1.ObjectMeta{Name: "test-decline-immutable", Namespace: "default"},
				Spec: billingv1alpha1.BillingArrangementDeclineSpec{
					ApplicationRef: billingv1alpha1.BillingArrangementApplicationRef{Name: "one"},
					Message:        "No.",
				},
			}
			Expect(k8sClient.Create(ctx, decline)).To(Succeed())
			decline.Spec.ApplicationRef.Name = "two"
			Expect(k8sClient.Update(ctx, decline)).To(MatchError(ContainSubstring("applicationRef is immutable")))
			Expect(k8sClient.Delete(ctx, &billingv1alpha1.BillingArrangementDecline{ObjectMeta: decline.ObjectMeta})).To(Succeed())
		})
	})
})
