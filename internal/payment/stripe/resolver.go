// SPDX-License-Identifier: AGPL-3.0-only

// Package stripe implements the Stripe payment provider: an HTTP webhook
// receiver and a thin SDK client wrapper used by the PaymentMethodSetup
// reconciler and the BillingAccount phase controller.
package stripe

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ResolvedConfig is the materialized configuration for a Stripe
// PaymentProvider — secret values resolved into bytes ready for use.
type ResolvedConfig struct {
	ProviderName    string
	Mode            billingv1alpha1.PaymentProviderMode
	APIVersion      string
	PublishableKey  string
	SecretKey       string
	WebhookSecret   string
}

// ProviderSecretsNamespace is the namespace the PaymentProvider's referenced
// Secrets are expected to live in. Stripe-related credentials are owned by
// the billing controller manager and not exposed cross-tenant; we pin the
// lookup to billing-system to keep the trust boundary explicit.
const ProviderSecretsNamespace = "billing-system"

// ResolveProviderConfig loads a PaymentProvider by name and dereferences its
// secret-key selectors against the Kubernetes API.
func ResolveProviderConfig(ctx context.Context, c client.Reader, name string) (*ResolvedConfig, error) {
	var pp billingv1alpha1.PaymentProvider
	if err := c.Get(ctx, types.NamespacedName{Name: name}, &pp); err != nil {
		return nil, fmt.Errorf("getting PaymentProvider %q: %w", name, err)
	}
	if pp.Spec.Type != billingv1alpha1.PaymentProviderTypeStripe {
		return nil, fmt.Errorf("PaymentProvider %q is type %q, not stripe", name, pp.Spec.Type)
	}
	if pp.Spec.Config.Stripe == nil {
		return nil, fmt.Errorf("PaymentProvider %q has spec.type=stripe but spec.config.stripe is unset", name)
	}
	cfg := pp.Spec.Config.Stripe

	pub, err := readSecretKey(ctx, c, cfg.PublishableKeyRef)
	if err != nil {
		return nil, fmt.Errorf("reading publishableKey: %w", err)
	}
	sec, err := readSecretKey(ctx, c, cfg.SecretKeyRef)
	if err != nil {
		return nil, fmt.Errorf("reading secretKey: %w", err)
	}
	wh, err := readSecretKey(ctx, c, cfg.WebhookSecretRef)
	if err != nil {
		return nil, fmt.Errorf("reading webhookSecret: %w", err)
	}

	return &ResolvedConfig{
		ProviderName:   pp.Name,
		Mode:           pp.Spec.Mode,
		APIVersion:     cfg.APIVersion,
		PublishableKey: pub,
		SecretKey:      sec,
		WebhookSecret:  wh,
	}, nil
}

// readSecretKey dereferences a SecretKeySelector against the well-known
// ProviderSecretsNamespace. The reference's optional `Optional` flag is
// honored — when set, a missing secret returns an empty string with no
// error.
func readSecretKey(ctx context.Context, c client.Reader, ref corev1.SecretKeySelector) (string, error) {
	if ref.Name == "" || ref.Key == "" {
		return "", errors.New("SecretKeySelector must set both name and key")
	}
	var s corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: ProviderSecretsNamespace, Name: ref.Name}, &s); err != nil {
		return "", fmt.Errorf("getting Secret %s/%s: %w", ProviderSecretsNamespace, ref.Name, err)
	}
	v, ok := s.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("Secret %s/%s has no key %q", ProviderSecretsNamespace, ref.Name, ref.Key)
	}
	return string(v), nil
}
