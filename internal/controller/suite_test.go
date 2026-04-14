// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	cfg          *rest.Config
	k8sClient    client.Client
	cachedClient client.Client
	testEnv      *envtest.Environment
	ctx          context.Context
	cancel       context.CancelFunc
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "base", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = billingv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Start a controller manager with controllers registered
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).NotTo(HaveOccurred())

	// Add field indexers
	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingAccountBinding{},
		BindingBillingAccountRefField,
		func(obj client.Object) []string {
			binding := obj.(*billingv1alpha1.BillingAccountBinding)
			return []string{binding.Spec.BillingAccountRef.Name}
		},
	)
	Expect(err).NotTo(HaveOccurred())

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&billingv1alpha1.BillingAccountBinding{},
		BindingProjectRefField,
		func(obj client.Object) []string {
			binding := obj.(*billingv1alpha1.BillingAccountBinding)
			return []string{binding.Spec.ProjectRef.Name}
		},
	)
	Expect(err).NotTo(HaveOccurred())

	// Register BillingAccount controller. We use a thin test adapter rather
	// than the production reconciler so that test-specific behavior (e.g.,
	// refetching before status update to avoid stale conflicts) can be
	// exercised against envtest.
	err = ctrl.NewControllerManagedBy(mgr).
		Named("billingaccount-test").
		For(&billingv1alpha1.BillingAccount{}).
		Watches(&billingv1alpha1.BillingAccountBinding{},
			reconcileAccountFromBinding(mgr.GetClient()),
		).
		Complete(&testBillingAccountReconciler{client: mgr.GetClient()})
	Expect(err).NotTo(HaveOccurred())

	// Register BillingAccountBinding controller
	err = ctrl.NewControllerManagedBy(mgr).
		Named("billingaccountbinding-test").
		For(&billingv1alpha1.BillingAccountBinding{}).
		Complete(&testBillingAccountBindingReconciler{client: mgr.GetClient()})
	Expect(err).NotTo(HaveOccurred())

	// Get the cached client from the manager (supports field indexers)
	cachedClient = mgr.GetClient()

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// Wait for cache to sync
	Eventually(func() bool {
		return mgr.GetCache().WaitForCacheSync(ctx)
	}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
