// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BillingAccountBinding Controller", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("Binding lifecycle", func() {
		It("should set binding to Active with billing responsibility", func() {
			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bind-active",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "some-account"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: "project-active"},
				},
			}
			Expect(k8sClient.Create(ctx, binding)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountBindingPhaseActive))
				g.Expect(fetched.Status.BillingResponsibility).NotTo(BeNil())
				g.Expect(fetched.Status.BillingResponsibility.CurrentAccount).To(Equal("some-account"))
				g.Expect(fetched.Status.BillingResponsibility.EstablishedAt).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, binding)).To(Succeed())
		})
	})

	Context("Superseding", func() {
		It("should mark an older binding as superseded when a newer one reconciles", func() {
			// Create two bindings for the same project. The controller will
			// set the first to Active. When the second is created, the
			// controller will supersede the first.
			binding1 := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-supersede-old",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "account-a"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: "project-supersede"},
				},
			}
			Expect(k8sClient.Create(ctx, binding1)).To(Succeed())

			// Wait for binding1 to become Active
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(binding1), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountBindingPhaseActive))
			}, timeout, interval).Should(Succeed())

			// Manually supersede binding1 to simulate what the newer binding's
			// controller does. This tests that the Superseded phase sticks and
			// binding1's reconciler doesn't overwrite it back to Active.
			Eventually(func() error {
				var fetched billingv1alpha1.BillingAccountBinding
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(binding1), &fetched); err != nil {
					return err
				}
				fetched.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
				return k8sClient.Status().Update(ctx, &fetched)
			}, timeout, interval).Should(Succeed())

			// Verify binding1 stays Superseded (the controller should not
			// overwrite it back to Active)
			Consistently(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(binding1), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountBindingPhaseSuperseded))
			}, "2s", interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, binding1)).To(Succeed())
		})
	})
})
