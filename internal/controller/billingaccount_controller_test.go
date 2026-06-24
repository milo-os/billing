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

var _ = Describe("BillingAccount Controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("Phase transitions via reconciliation", func() {
		It("should transition to Ready after creation", func() {
			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ready",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
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

	It("DefaultPaymentMethodReady=False NotConfigured when no ref is set", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "dpmr-notconfigured", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal("NotConfigured"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("DefaultPaymentMethodReady=False PaymentMethodNotFound when ref points at a missing PM", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "dpmr-notfound", Namespace: "default"},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode:            "USD",
				DefaultPaymentMethodRef: &billingv1alpha1.DefaultPaymentMethodRef{Name: "missing-pm"},
			},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal("PaymentMethodNotFound"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("DefaultPaymentMethodReady=False PaymentMethodDegraded when ref points at a non-Active PM", func() {
		// PM left in Pending — the test adapter's defaulting will set
		// it but never advance to Active without a provider.
		pm := &billingv1alpha1.PaymentMethod{
			ObjectMeta: metav1.ObjectMeta{Name: "pm-degraded", Namespace: "default"},
			Spec: billingv1alpha1.PaymentMethodSpec{
				BillingAccountRef:     billingv1alpha1.BillingAccountRef{Name: "dpmr-degraded"},
				DisplayName:           "Pending Card",
				PaymentMethodClassRef: &billingv1alpha1.PaymentMethodClassRef{Name: "test-class"},
			},
		}
		Expect(k8sClient.Create(ctx, pm)).To(Succeed())
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "dpmr-degraded", Namespace: "default"},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode:            "USD",
				DefaultPaymentMethodRef: &billingv1alpha1.DefaultPaymentMethodRef{Name: "pm-degraded"},
			},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal("PaymentMethodDegraded"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pm)).To(Succeed())
	})

	It("DefaultPaymentMethodReady=True Ready when ref points at an Active PM", func() {
		pm := &billingv1alpha1.PaymentMethod{
			ObjectMeta: metav1.ObjectMeta{Name: "pm-active", Namespace: "default"},
			Spec: billingv1alpha1.PaymentMethodSpec{
				BillingAccountRef:     billingv1alpha1.BillingAccountRef{Name: "dpmr-ready"},
				DisplayName:           "Active Card",
				PaymentMethodClassRef: &billingv1alpha1.PaymentMethodClassRef{Name: "test-class"},
			},
		}
		Expect(k8sClient.Create(ctx, pm)).To(Succeed())

		// Create the account before setting the PM Active so that the PM watch
		// event (triggered below) fires while the account already exists and
		// drives a re-reconcile that observes the Active phase.
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "dpmr-ready", Namespace: "default"},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode:            "USD",
				DefaultPaymentMethodRef: &billingv1alpha1.DefaultPaymentMethodRef{Name: "pm-active"},
			},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())

		// Force the PM to Active. The PaymentMethod watch enqueues a reconcile
		// for the referenced BillingAccount; by this point the account exists, so
		// the reconcile observes the Active phase and sets the condition True.
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.PaymentMethod
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.PaymentMethodPhaseActive
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("Ready"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pm)).To(Succeed())
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
