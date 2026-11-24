// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// The ChargeDraft CRD's own rules, checked by the API server.
var _ = Describe("ChargeDraft CRD", func() {
	ctx := context.Background()
	ns := "chargedraft-crd"

	BeforeEach(func() {
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	line := func(description string) billingv1alpha1.ChargeDraftLine {
		return billingv1alpha1.ChargeDraftLine{Description: description, Quantity: "3", UnitAmount: "150.00"}
	}
	draft := func(name string, lines ...billingv1alpha1.ChargeDraftLine) *billingv1alpha1.ChargeDraft {
		return &billingv1alpha1.ChargeDraft{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: billingv1alpha1.ChargeDraftSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				Lines:             lines,
			},
		}
	}
	expectInvalid := func(err error, msg string) {
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring(msg))
	}

	It("accepts several lines, defaults its timing, and can be changed", func() {
		d := draft("two-lines", line("Node"), line("Control node"))
		Expect(k8sClient.Create(ctx, d)).To(Succeed())
		Expect(d.Spec.Invoice).To(Equal(billingv1alpha1.ChargeInvoiceNext))
		Expect(d.Spec.Lines[0].Currency).To(Equal("USD"))

		d.Spec.Lines = append(d.Spec.Lines, line("Remote hands"))
		d.Spec.Invoice = billingv1alpha1.ChargeInvoiceNow
		Expect(k8sClient.Update(ctx, d)).To(Succeed())
	})

	It("refuses a draft without lines", func() {
		expectInvalid(k8sClient.Create(ctx, draft("empty")), "spec.lines")
	})

	It("refuses a custom line without a price", func() {
		l := line("Node")
		l.UnitAmount = ""
		expectInvalid(k8sClient.Create(ctx, draft("no-price", l)), "needs a description and a unitAmount")
	})

	It("refuses a line that isn't taxable without a note", func() {
		l := line("Node")
		l.Tax = &billingv1alpha1.PricingTax{Taxable: new(bool)}
		expectInvalid(k8sClient.Create(ctx, draft("untaxed", l)), "needs a note")
	})

	It("refuses moving a draft to another account", func() {
		d := draft("moving", line("Node"))
		Expect(k8sClient.Create(ctx, d)).To(Succeed())
		d.Spec.BillingAccountRef.Name = "other"
		expectInvalid(k8sClient.Update(ctx, d), "billingAccountRef can't be changed")
	})
})
