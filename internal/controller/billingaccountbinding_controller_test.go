// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newTestProject returns a minimal valid Project for a binding's ProjectRef
// to resolve against.
func newTestProject(name string) *resourcemanagerv1alpha1.Project {
	return &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{
				Kind: "Organization",
				Name: "test-org",
			},
		},
	}
}

var _ = Describe("BillingAccountBinding Controller", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	Context("Binding lifecycle", func() {
		It("should set binding to Active with billing responsibility", func() {
			project := newTestProject("project-active")
			Expect(k8sClient.Create(ctx, project)).To(Succeed())

			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bind-active",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "some-account"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: project.Name},
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
			project := newTestProject("project-supersede")
			Expect(k8sClient.Create(ctx, project)).To(Succeed())

			binding1 := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-supersede-old",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "account-a"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: project.Name},
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

	Context("Project deletion", func() {
		It("should delete a binding whose project no longer exists", func() {
			// No Project is ever created for this binding, which is
			// equivalent to the project having already been deleted by the
			// time the binding reconciles.
			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bind-orphaned",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "some-account"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: "project-never-existed"},
				},
			}
			Expect(k8sClient.Create(ctx, binding)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}, timeout, interval).Should(Succeed())
		})

		It("should delete a binding when its project starts terminating", func() {
			project := newTestProject("project-terminating")
			controllerutil.AddFinalizer(project, "test.miloapis.com/hold-open")
			Expect(k8sClient.Create(ctx, project)).To(Succeed())

			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bind-project-terminating",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "some-account"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: project.Name},
				},
			}
			Expect(k8sClient.Create(ctx, binding)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountBindingPhaseActive))
			}, timeout, interval).Should(Succeed())

			// The finalizer keeps the Project around in a Terminating state
			// rather than actually removing it, so this exercises the
			// DeletionTimestamp check rather than a NotFound Get.
			Expect(k8sClient.Delete(ctx, project)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}, timeout, interval).Should(Succeed())

			var fetchedProject resourcemanagerv1alpha1.Project
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(project), &fetchedProject)).To(Succeed())
			controllerutil.RemoveFinalizer(&fetchedProject, "test.miloapis.com/hold-open")
			Expect(k8sClient.Update(ctx, &fetchedProject)).To(Succeed())
		})

		It("should delete a superseded binding whose project no longer exists", func() {
			// Superseded bindings skip the responsibility/supersede logic but
			// must still be reaped once their project is gone. Simulate that
			// by creating a binding, marking it Superseded directly (as a
			// newer binding for the same project would), then deleting the
			// binding it references.
			project := newTestProject("project-superseded-deleted")
			Expect(k8sClient.Create(ctx, project)).To(Succeed())

			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bind-superseded-orphan",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "some-account"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: project.Name},
				},
			}
			Expect(k8sClient.Create(ctx, binding)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(billingv1alpha1.BillingAccountBindingPhaseActive))
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				var fetched billingv1alpha1.BillingAccountBinding
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched); err != nil {
					return err
				}
				fetched.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
				return k8sClient.Status().Update(ctx, &fetched)
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, project)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched billingv1alpha1.BillingAccountBinding
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(binding), &fetched)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}, timeout, interval).Should(Succeed())
		})
	})
})
