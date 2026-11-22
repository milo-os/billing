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

// The Charge CRD's own rules, checked by the API server.
var _ = Describe("Charge CRD", func() {
	ctx := context.Background()
	ns := "charge-crd"

	BeforeEach(func() {
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	charge := func(name string) *billingv1alpha1.Charge {
		return &billingv1alpha1.Charge{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: billingv1alpha1.ChargeSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				Description:       "Remote hands",
				Quantity:          "3.5",
				UnitAmount:        "150.00",
				ServicePeriod:     &billingv1alpha1.ServicePeriod{Start: "2026-12-01", End: "2026-12-31"},
			},
		}
	}
	expectInvalid := func(err error, msg string) {
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring(msg))
	}

	It("accepts a custom charge and defaults its currency and timing", func() {
		c := charge("custom")
		Expect(k8sClient.Create(ctx, c)).To(Succeed())
		Expect(c.Spec.Currency).To(Equal("USD"))
		Expect(c.Spec.Invoice).To(Equal(billingv1alpha1.ChargeInvoiceNext))
	})

	It("accepts a single day", func() {
		c := charge("one-day")
		c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-12-15", End: "2026-12-15"}
		Expect(k8sClient.Create(ctx, c)).To(Succeed())
	})

	It("refuses a period that ends before it starts", func() {
		c := charge("backwards")
		c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-12-15", End: "2026-12-14"}
		expectInvalid(k8sClient.Create(ctx, c), "can't end before it starts")
	})

	It("refuses a custom charge without a price", func() {
		c := charge("no-price")
		c.Spec.UnitAmount = ""
		expectInvalid(k8sClient.Create(ctx, c), "needs a description and a unitAmount")
	})

	It("refuses a charge that isn't taxable without a note", func() {
		c := charge("untaxed")
		c.Spec.Tax = &billingv1alpha1.PricingTax{Taxable: new(bool)}
		expectInvalid(k8sClient.Create(ctx, c), "needs a note")
	})

	It("refuses a quantity with three decimal places", func() {
		c := charge("fine-qty")
		c.Spec.Quantity = "1.125"
		expectInvalid(k8sClient.Create(ctx, c), "spec.quantity")
	})

	It("refuses any change to the spec", func() {
		c := charge("immutable")
		Expect(k8sClient.Create(ctx, c)).To(Succeed())
		c.Spec.Quantity = "4"
		expectInvalid(k8sClient.Update(ctx, c), "can't be changed")
	})
})
