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

// The InvoiceRequest CRD's own rules on drafts and contract drafts,
// checked by the API server.
var _ = Describe("InvoiceRequest draft", func() {
	ctx := context.Background()
	ns := "invoicerequest-crd"

	BeforeEach(func() {
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	request := func(name string, mode billingv1alpha1.InvoiceRequestMode, draft bool) *billingv1alpha1.InvoiceRequest {
		return &billingv1alpha1.InvoiceRequest{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: billingv1alpha1.InvoiceRequestSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				Mode:              mode,
				Draft:             draft,
			},
		}
	}

	It("accepts a draft with a Preview", func() {
		Expect(k8sClient.Create(ctx, request("preview-draft", billingv1alpha1.InvoiceRequestModePreview, true))).To(Succeed())
	})

	It("refuses a draft with an Issue", func() {
		err := k8sClient.Create(ctx, request("issue-draft", billingv1alpha1.InvoiceRequestModeIssue, true))
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring("draft is only for a Preview"))
	})

	contractDraft := func(name string, mode billingv1alpha1.InvoiceRequestMode) *billingv1alpha1.InvoiceRequest {
		ir := request(name, mode, false)
		ir.Spec.ContractDraft = &billingv1alpha1.InvoiceRequestContractDraft{
			OfferRef:   billingv1alpha1.OfferReference{Name: "contract-acme-draft"},
			StartsAt:   metav1.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
			BillingDay: billingv1alpha1.ContractBillingDayInvoiceDay,
		}
		return ir
	}

	It("accepts a contract draft with a Preview", func() {
		Expect(k8sClient.Create(ctx, contractDraft("preview-contract-draft", billingv1alpha1.InvoiceRequestModePreview))).To(Succeed())
	})

	It("refuses a contract draft with an Issue", func() {
		err := k8sClient.Create(ctx, contractDraft("issue-contract-draft", billingv1alpha1.InvoiceRequestModeIssue))
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring("contractDraft is only for a Preview"))
	})

	chargeDraft := func(name string, mode billingv1alpha1.InvoiceRequestMode) *billingv1alpha1.InvoiceRequest {
		ir := request(name, mode, true)
		if mode != billingv1alpha1.InvoiceRequestModePreview {
			ir.Spec.Draft = false
		}
		ir.Spec.ChargeDraftRef = &billingv1alpha1.ChargeDraftReference{Name: "acme-charges"}
		return ir
	}

	It("accepts a charge draft with a Preview", func() {
		Expect(k8sClient.Create(ctx, chargeDraft("preview-charge-draft", billingv1alpha1.InvoiceRequestModePreview))).To(Succeed())
	})

	It("refuses a charge draft with an Issue", func() {
		err := k8sClient.Create(ctx, chargeDraft("issue-charge-draft", billingv1alpha1.InvoiceRequestModeIssue))
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring("chargeDraftRef is only for a Preview"))
	})

	It("refuses a charge draft and a contract draft together", func() {
		ir := contractDraft("both-drafts", billingv1alpha1.InvoiceRequestModePreview)
		ir.Spec.ChargeDraftRef = &billingv1alpha1.ChargeDraftReference{Name: "acme-charges"}
		err := k8sClient.Create(ctx, ir)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring("not both"))
	})
})
