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

var _ = Describe("BillingAccount Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("Phase transitions via reconciliation", func() {
		It("should transition to Incomplete when no payment profile is set", func() {
			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-incomplete",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			// The controller should reconcile and set phase to Incomplete
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccount
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseIncomplete))
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		})

		It("should transition to Ready when payment profile is set", func() {
			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ready",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
					PaymentProfile: &billingv1alpha1.PaymentProfileRef{
						Type:       "CreditCard",
						ExternalID: "cc-123",
					},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccount
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		})

		It("should preserve Suspended phase", func() {
			account := &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
					PaymentProfile: &billingv1alpha1.PaymentProfileRef{
						Type:       "CreditCard",
						ExternalID: "cc-123",
					},
				},
				Status: billingv1alpha1.BillingAccountStatus{
					Phase: billingv1alpha1.BillingAccountPhaseSuspended,
				},
			}

			reconciler := &BillingAccountReconciler{}
			phase := reconciler.determinePhase(account)
			Expect(phase).To(Equal(billingv1alpha1.BillingAccountPhaseSuspended))
		})

		It("should preserve Archived phase", func() {
			account := &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
				},
				Status: billingv1alpha1.BillingAccountStatus{
					Phase: billingv1alpha1.BillingAccountPhaseArchived,
				},
			}

			reconciler := &BillingAccountReconciler{}
			phase := reconciler.determinePhase(account)
			Expect(phase).To(Equal(billingv1alpha1.BillingAccountPhaseArchived))
		})
	})

	Context("Linked projects count", func() {
		It("should update linkedProjectsCount when a binding becomes Active", func() {
			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-count-acct",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
					PaymentProfile: &billingv1alpha1.PaymentProfileRef{
						Type:       "CreditCard",
						ExternalID: "cc-456",
					},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			// Wait for account to be Ready
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccount
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
			}, timeout, interval).Should(Succeed())

			// Create a binding - the binding controller will set it to Active,
			// which triggers the account controller to update linkedProjectsCount
			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-count-binding",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "test-count-acct"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: "project-count"},
				},
			}
			Expect(k8sClient.Create(ctx, binding)).To(Succeed())

			// Binding should become Active
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountBindingPhaseActive))
			}, timeout, interval).Should(Succeed())

			// Account should have linkedProjectsCount = 1
			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccount
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
				g.Expect(fetched.Status.LinkedProjectsCount).To(Equal(int32(1)))
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, binding)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		})
	})
})

var _ = Describe("BillingAccount CRD Validation", func() {
	It("should create a billing account with valid spec", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-crd-valid",
				Namespace: "default",
			},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode: "USD",
				PaymentTerms: &billingv1alpha1.PaymentTerms{
					NetDays:           30,
					InvoiceFrequency:  "Monthly",
					InvoiceDayOfMonth: 1,
				},
			},
		}

		Expect(k8sClient.Create(ctx, account)).To(Succeed())

		var fetched billingv1alpha1.BillingAccount
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
		Expect(fetched.Spec.CurrencyCode).To(Equal("USD"))

		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("should reject invalid currency code", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-crd-invalid-currency",
				Namespace: "default",
			},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode: "invalid",
			},
		}

		err := k8sClient.Create(ctx, account)
		Expect(err).To(HaveOccurred())
	})
})
