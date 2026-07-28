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

	It("InvoicingReady=True NoInvoicesYet when no invoices exist", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-none", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("NoInvoicesYet"))
			g.Expect(fetched.Status.LatestInvoiceRef).To(BeNil())
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("InvoicingReady=True Current and latestInvoiceRef when an Open invoice exists", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-open-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		start := metav1.NewTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
		end := metav1.NewTime(time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC))
		invoice := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-open-acct-2026-06", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-open-acct"},
				Period:            billingv1alpha1.InvoicePeriod{Start: start, End: end},
			},
		}
		Expect(k8sClient.Create(ctx, invoice)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(invoice), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhaseOpen
			fetched.Status.CurrencyCode = "USD"
			fetched.Status.Total = "100.00"
			fetched.Status.AmountPaid = "0.00"
			fetched.Status.AmountDue = "100.00"
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.LatestInvoiceRef).NotTo(BeNil())
			g.Expect(fetched.Status.LatestInvoiceRef.Name).To(Equal("inv-open-acct-2026-06"))
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("Current"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, invoice)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("InvoicingReady=False PastDue when the latest invoice is PastDue", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-pastdue-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		start := metav1.NewTime(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
		end := metav1.NewTime(time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC))
		invoice := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-pastdue-acct-2026-05", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-pastdue-acct"},
				Period:            billingv1alpha1.InvoicePeriod{Start: start, End: end},
			},
		}
		Expect(k8sClient.Create(ctx, invoice)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(invoice), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhasePastDue
			fetched.Status.CurrencyCode = "USD"
			fetched.Status.Total = "50.00"
			fetched.Status.AmountPaid = "0.00"
			fetched.Status.AmountDue = "50.00"
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.LatestInvoiceRef).NotTo(BeNil())
			g.Expect(fetched.Status.LatestInvoiceRef.Name).To(Equal("inv-pastdue-acct-2026-05"))
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal("PastDue"))
			// InvoicingReady must not affect account phase.
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, invoice)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("InvoicingReady=True Current when the latest invoice is Void", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-void-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		start := metav1.NewTime(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
		end := metav1.NewTime(time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC))
		invoice := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-void-acct-2026-04", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-void-acct"},
				Period:            billingv1alpha1.InvoicePeriod{Start: start, End: end},
			},
		}
		Expect(k8sClient.Create(ctx, invoice)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(invoice), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhaseVoid
			fetched.Status.CurrencyCode = "USD"
			fetched.Status.Total = "0.00"
			fetched.Status.AmountPaid = "0.00"
			fetched.Status.AmountDue = "0.00"
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("Current"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, invoice)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("InvoicingReady=Unknown PhasePending when invoice phase is empty", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-pending-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		start := metav1.NewTime(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
		end := metav1.NewTime(time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC))
		invoice := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-pending-acct-2026-03", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-pending-acct"},
				Period:            billingv1alpha1.InvoicePeriod{Start: start, End: end},
			},
		}
		Expect(k8sClient.Create(ctx, invoice)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.LatestInvoiceRef).NotTo(BeNil())
			g.Expect(fetched.Status.LatestInvoiceRef.Name).To(Equal("inv-pending-acct-2026-03"))
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionUnknown))
			g.Expect(c.Reason).To(Equal("PhasePending"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, invoice)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("InvoicingReady=True Current when an invoice is Paid", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-paid-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		start := metav1.NewTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
		end := metav1.NewTime(time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC))
		invoice := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-paid-acct-2026-02", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-paid-acct"},
				Period:            billingv1alpha1.InvoicePeriod{Start: start, End: end},
			},
		}
		Expect(k8sClient.Create(ctx, invoice)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(invoice), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhasePaid
			fetched.Status.CurrencyCode = "USD"
			fetched.Status.Total = "10.00"
			fetched.Status.AmountPaid = "10.00"
			fetched.Status.AmountDue = "0.00"
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("Current"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, invoice)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("skips a newer Void when projecting InvoicingReady from an older PastDue", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-mask-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		pastDue := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-mask-acct-2026-01", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-mask-acct"},
				Period: billingv1alpha1.InvoicePeriod{
					Start: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   metav1.NewTime(time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)),
				},
			},
		}
		Expect(k8sClient.Create(ctx, pastDue)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pastDue), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhasePastDue
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Reason).To(Equal("PastDue"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		// Ensure the void invoice is created after the past-due one so it is
		// newest by creationTimestamp.
		time.Sleep(1100 * time.Millisecond)
		voided := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-mask-acct-2026-02", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-mask-acct"},
				Period: billingv1alpha1.InvoicePeriod{
					Start: metav1.NewTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)),
					End:   metav1.NewTime(time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC)),
				},
			},
		}
		Expect(k8sClient.Create(ctx, voided)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(voided), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhaseVoid
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.LatestInvoiceRef).NotTo(BeNil())
			g.Expect(fetched.Status.LatestInvoiceRef.Name).To(Equal("inv-mask-acct-2026-02"))
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal("PastDue"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, voided)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pastDue)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
	})

	It("clears latestInvoiceRef and returns to NoInvoicesYet when the last invoice is deleted", func() {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-delete-acct", Namespace: "default"},
			Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountPhaseReady))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		invoice := &billingv1alpha1.Invoice{
			ObjectMeta: metav1.ObjectMeta{Name: "inv-delete-acct-2026-01", Namespace: "default"},
			Spec: billingv1alpha1.InvoiceSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "inv-delete-acct"},
				Period: billingv1alpha1.InvoicePeriod{
					Start: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   metav1.NewTime(time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)),
				},
			},
		}
		Expect(k8sClient.Create(ctx, invoice)).To(Succeed())
		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.Invoice
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(invoice), &fetched)).To(Succeed())
			fetched.Status.Phase = billingv1alpha1.InvoicePhaseOpen
			g.Expect(k8sClient.Status().Update(ctx, &fetched)).To(Succeed())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.LatestInvoiceRef).NotTo(BeNil())
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, invoice)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched billingv1alpha1.BillingAccount
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(account), &fetched)).To(Succeed())
			g.Expect(fetched.Status.LatestInvoiceRef).To(BeNil())
			c := apimeta.FindStatusCondition(fetched.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal("NoInvoicesYet"))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())

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
