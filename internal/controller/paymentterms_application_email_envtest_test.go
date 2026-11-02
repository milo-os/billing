// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const emailCacheSyncTimeout = 3 * time.Second

// TestPaymentTermsApplicationEmail_Manager runs the email controller in a
// manager against an API server, with and without Milo's APIs. Billing's
// own e2e cluster has no Milo, and the controller must not keep the
// manager from starting there.
func TestPaymentTermsApplicationEmail_Manager(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}

	t.Run("without Milo's notification API the manager still starts", func(t *testing.T) {
		cfg := startEmailEnv(t, nil)
		startEmailManager(t, cfg)
	})

	t.Run("with Milo's APIs a new application is confirmed to support", func(t *testing.T) {
		cfg := startEmailEnv(t, []*apiextensionsv1.CustomResourceDefinition{
			miloCRD(notificationv1alpha1.SchemeGroupVersion.Group, "Email", "emails", apiextensionsv1.NamespaceScoped),
			miloCRD(iamv1alpha1.SchemeGroupVersion.Group, "User", "users", apiextensionsv1.ClusterScoped),
			miloCRD(resourcemanagerv1alpha1.GroupVersion.Group, "Organization", "organizations", apiextensionsv1.ClusterScoped),
		})
		c := startEmailManager(t, cfg)
		ctx := context.Background()

		must(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-acme"}}))
		application := &billingv1alpha1.BillingArrangementApplication{
			ObjectMeta: metav1.ObjectMeta{Name: "app-1", Namespace: "organization-acme"},
			Spec: billingv1alpha1.BillingArrangementApplicationSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "ba-1"},
				PaymentOption:     billingv1alpha1.ApplicationPaymentOptionNotSure,
				Organization: billingv1alpha1.ApplicantOrganization{
					LegalName: "Acme Corporation Ltd",
					Type:      billingv1alpha1.ApplicantOrganizationTypeCompany,
					Website:   "https://acme.com",
				},
				Situation:  "We pay suppliers by bank transfer.",
				Authorized: true,
			},
		}
		must(t, c.Create(ctx, application))

		name := types.NamespacedName{
			Namespace: "milo-system",
			Name:      "payment-terms-application-" + string(application.UID) + "-submitted",
		}
		deadline := time.Now().Add(30 * time.Second)
		for c.Get(ctx, name, &notificationv1alpha1.Email{}) != nil {
			if time.Now().After(deadline) {
				t.Fatal("the support notice was never created")
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
}

// startEmailEnv starts an API server serving billing's CRDs and crds.
func startEmailEnv(t *testing.T, crds []*apiextensionsv1.CustomResourceDefinition) *rest.Config {
	t.Helper()
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "base", "crd", "bases")},
		CRDs:                  crds,
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	must(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })
	return cfg
}

// startEmailManager runs the controller in a manager until the test ends,
// failing the test if the manager stops on its own.
func startEmailManager(t *testing.T, cfg *rest.Config) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	must(t, clientgoscheme.AddToScheme(scheme))
	must(t, billingv1alpha1.AddToScheme(scheme))
	must(t, iamv1alpha1.AddToScheme(scheme))
	must(t, notificationv1alpha1.AddToScheme(scheme))
	must(t, resourcemanagerv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		// A controller watching an unserved API stops the manager once its
		// cache sync times out, two minutes by default.
		Controller: ctrlconfig.Controller{CacheSyncTimeout: emailCacheSyncTimeout},
	})
	must(t, err)
	must(t, (&PaymentTermsApplicationEmailReconciler{
		EmailNamespace:         "milo-system",
		ReceivedEmailTemplate:  "received",
		SubmittedEmailTemplate: "submitted",
		ApprovedEmailTemplate:  "approved",
		DeclinedEmailTemplate:  "declined",
		SupportEmailAddress:    "support@example.com",
		PortalURL:              "https://cloud.example.com",
		StaffPortalURL:         "https://staff.example.com",
		MaxEventAge:            time.Hour,
	}).SetupWithManager(mgr))

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	must(t, err)
	must(t, c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "milo-system"}}))

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	var startErr error
	go func() {
		defer close(stopped)
		startErr = mgr.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("the manager's caches never synced")
	}
	select {
	case <-stopped:
		t.Fatalf("the manager stopped: %v", startErr)
	case <-time.After(2 * emailCacheSyncTimeout):
	}
	return c
}

// miloCRD stands in for one of Milo's CRDs: the controller only needs the
// API served, not Milo's validation.
func miloCRD(group, kind, plural string, scope apiextensionsv1.ResourceScope) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: kind, ListKind: kind + "List", Plural: plural},
			Scope: scope,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1alpha1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type:                   "object",
					XPreserveUnknownFields: ptr.To(true),
				}},
				Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}},
			}},
		},
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
