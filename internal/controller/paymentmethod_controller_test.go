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

var _ = Describe("PaymentMethod Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	It("sets initial Pending phase and InstrumentReady=False on create", func() {
		pm := &billingv1alpha1.PaymentMethod{
			ObjectMeta: metav1.ObjectMeta{Name: "pmctrl-pending", Namespace: "default"},
			Spec: billingv1alpha1.PaymentMethodSpec{
				BillingAccountRef:     billingv1alpha1.BillingAccountRef{Name: "any-acct"},
				DisplayName:           "Pending Card",
				PaymentMethodClassRef: &billingv1alpha1.PaymentMethodClassRef{Name: "stripe-default"},
			},
		}
		Expect(k8sClient.Create(ctx, pm)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.PaymentMethod
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.PaymentMethodPhasePending))
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.PaymentMethodConditionInstrumentReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
		}, timeout, interval).Should(Succeed())
		Expect(k8sClient.Delete(ctx, pm)).To(Succeed())
	})

	It("mirrors phase=Active onto InstrumentReady=True", func() {
		pm := &billingv1alpha1.PaymentMethod{
			ObjectMeta: metav1.ObjectMeta{Name: "pmctrl-active", Namespace: "default"},
			Spec: billingv1alpha1.PaymentMethodSpec{
				BillingAccountRef:     billingv1alpha1.BillingAccountRef{Name: "any-acct"},
				DisplayName:           "Active Card",
				PaymentMethodClassRef: &billingv1alpha1.PaymentMethodClassRef{Name: "stripe-default"},
			},
		}
		Expect(k8sClient.Create(ctx, pm)).To(Succeed())

		// Patch phase to Active (simulating the provider controller).
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.PaymentMethod
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.PaymentMethodPhaseActive
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.PaymentMethod
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.PaymentMethodConditionInstrumentReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("Active"))
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, pm)).To(Succeed())
	})

	It("mirrors phase=Failed onto InstrumentReady=False with Failed reason", func() {
		pm := &billingv1alpha1.PaymentMethod{
			ObjectMeta: metav1.ObjectMeta{Name: "pmctrl-failed", Namespace: "default"},
			Spec: billingv1alpha1.PaymentMethodSpec{
				BillingAccountRef:     billingv1alpha1.BillingAccountRef{Name: "any-acct"},
				DisplayName:           "Failed Card",
				PaymentMethodClassRef: &billingv1alpha1.PaymentMethodClassRef{Name: "stripe-default"},
			},
		}
		Expect(k8sClient.Create(ctx, pm)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.PaymentMethod
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.PaymentMethodPhaseFailed
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.PaymentMethod
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.PaymentMethodConditionInstrumentReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal("Failed"))
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, pm)).To(Succeed())
	})
})
