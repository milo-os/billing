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

// The InvoiceRequest CRD's own rules on drafts, checked by the API server.
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
})
