// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// The ServicePricing CRD's own rules on tax, checked by the API server.
var _ = Describe("ServicePricing tax", func() {
	ctx := context.Background()
	ns := "pricing-tax-crd"

	BeforeEach(func() {
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	fee := func(name string, tax *billingv1alpha1.PricingTax) *billingv1alpha1.ServicePricing {
		return &billingv1alpha1.ServicePricing{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: billingv1alpha1.ServicePricingSpec{
				ChargeType: billingv1alpha1.ChargeTypeRecurring,
				Currency:   "USD",
				Amount:     "300.00",
				Interval:   billingv1alpha1.ChargeIntervalMonthly,
				Tax:        tax,
			},
		}
	}

	It("accepts a tax code", func() {
		Expect(k8sClient.Create(ctx, fee("with-code", &billingv1alpha1.PricingTax{Code: "txcd_10103001"}))).To(Succeed())
	})

	It("accepts a charge that isn't taxable", func() {
		Expect(k8sClient.Create(ctx, fee("not-taxable", &billingv1alpha1.PricingTax{Taxable: ptr.To(false)}))).To(Succeed())
	})

	It("refuses a code on a charge that isn't taxable", func() {
		err := k8sClient.Create(ctx, fee("code-not-taxable", &billingv1alpha1.PricingTax{Code: "txcd_10103001", Taxable: ptr.To(false)}))
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring("code can't be set on a pricing that isn't taxable"))
	})

	It("refuses an empty tax", func() {
		err := k8sClient.Create(ctx, fee("empty-tax", &billingv1alpha1.PricingTax{}))
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
	})

	It("refuses a code that isn't one", func() {
		err := k8sClient.Create(ctx, fee("bad-code", &billingv1alpha1.PricingTax{Code: "txcd 1010"}))
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
	})
})
