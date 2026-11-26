// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// The BillingEntitlement CRD's rules for a contract's term, checked by the
// API server. The webhook checks the rest.
var _ = Describe("BillingEntitlement CRD contract term", func() {
	ctx := context.Background()
	ns := "billingentitlement-crd"

	BeforeEach(func() {
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	withTerm := func(name string, term *billingv1alpha1.ContractTerm) *billingv1alpha1.BillingEntitlement {
		return &billingv1alpha1.BillingEntitlement{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: billingv1alpha1.BillingEntitlementSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				OfferRef:          billingv1alpha1.OfferReference{Name: "acme-contract"},
				Contract: &billingv1alpha1.BillingContract{
					StartsAt: metav1.NewTime(time.Date(2026, time.April, 18, 0, 0, 0, 0, time.UTC)),
					Term:     term,
				},
			},
		}
	}
	payg := &billingv1alpha1.OfferReference{Name: "payg"}
	expectInvalid := func(err error, msg string) {
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring(msg))
	}

	It("defaults atEnd to MonthToMonth", func() {
		be := withTerm("month-to-month", &billingv1alpha1.ContractTerm{Months: 36})
		Expect(k8sClient.Create(ctx, be)).To(Succeed())
		Expect(be.Spec.Contract.Term.AtEnd).To(Equal(billingv1alpha1.ContractTermAtEndMonthToMonth))
	})

	It("accepts a term that renews", func() {
		Expect(k8sClient.Create(ctx, withTerm("renew", &billingv1alpha1.ContractTerm{
			Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndRenew, RenewMonths: 12,
		}))).To(Succeed())
	})

	It("accepts a term that stops onto another Offer", func() {
		Expect(k8sClient.Create(ctx, withTerm("stop", &billingv1alpha1.ContractTerm{
			Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop, OfferAfterRef: payg,
		}))).To(Succeed())
	})

	It("refuses a term longer than ten years", func() {
		expectInvalid(k8sClient.Create(ctx, withTerm("too-long", &billingv1alpha1.ContractTerm{Months: 121})),
			"spec.contract.term.months")
	})

	It("refuses renewMonths unless the term renews", func() {
		expectInvalid(k8sClient.Create(ctx, withTerm("renew-months", &billingv1alpha1.ContractTerm{Months: 36, RenewMonths: 12})),
			"term.renewMonths is only allowed when atEnd is Renew")
	})

	It("refuses Stop without offerAfterRef", func() {
		expectInvalid(k8sClient.Create(ctx, withTerm("stop-nowhere", &billingv1alpha1.ContractTerm{
			Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop,
		})), "term.offerAfterRef is required when atEnd is Stop")
	})

	It("refuses offerAfterRef unless the term stops", func() {
		expectInvalid(k8sClient.Create(ctx, withTerm("offer-after", &billingv1alpha1.ContractTerm{Months: 36, OfferAfterRef: payg})),
			"term.offerAfterRef is required when atEnd is Stop, and not allowed otherwise")
	})

	It("refuses stopsAt unless the term stops", func() {
		stopsAt := metav1.NewTime(time.Date(2029, time.August, 18, 0, 0, 0, 0, time.UTC))
		expectInvalid(k8sClient.Create(ctx, withTerm("stops-at", &billingv1alpha1.ContractTerm{Months: 36, StopsAt: &stopsAt})),
			"term.stopsAt is only allowed when atEnd is Stop")
	})
})
