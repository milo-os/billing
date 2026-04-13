// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

// BillingOperator is the configuration for the billing operator.
type BillingOperator struct {
	metav1.TypeMeta

	MetricsServer MetricsServerConfig `json:"metricsServer"`

	// WebhookServer configures the admission webhook server. When unset, the
	// manager runs without an admission webhook server and no serving cert
	// is required.
	WebhookServer *WebhookServerConfig `json:"webhookServer,omitempty"`

	Discovery DiscoveryConfig `json:"discovery"`
}

// +k8s:deepcopy-gen=true

// WebhookServerConfig configures the admission webhook server.
type WebhookServerConfig struct {
	// Host is the address that the server will listen on.
	// Defaults to "" - all addresses.
	Host string `json:"host"`

	// Port is the port number that the server will serve.
	// It will be defaulted to 9443 if unspecified.
	Port int `json:"port"`

	// TLS is the TLS configuration for the webhook server.
	TLS TLSConfig `json:"tls"`

	// ClientCAName is the CA certificate name which server used to verify remote(client)'s certificate.
	ClientCAName string `json:"clientCAName"`
}

func SetDefaults_WebhookServerConfig(obj *WebhookServerConfig) {
	if obj.TLS.CertDir == "" {
		obj.TLS.CertDir = filepath.Join(os.TempDir(), "k8s-webhook-server", "serving-certs")
	}
}

func (c *WebhookServerConfig) Options(ctx context.Context, secretsClient client.Client) webhook.Options {
	opts := webhook.Options{
		Host:     c.Host,
		Port:     c.Port,
		CertDir:  c.TLS.CertDir,
		CertName: c.TLS.CertName,
		KeyName:  c.TLS.KeyName,
	}

	if secretRef := c.TLS.SecretRef; secretRef != nil {
		opts.TLSOpts = c.TLS.Options(ctx, secretsClient)
	}

	return opts
}

// +k8s:deepcopy-gen=true

// MetricsServerConfig configures the metrics server.
type MetricsServerConfig struct {
	// SecureServing enables serving metrics via https.
	SecureServing *bool `json:"secureServing,omitempty"`

	// BindAddress is the bind address for the metrics server.
	BindAddress string `json:"bindAddress"`

	// TLS is the TLS configuration for the metrics server.
	TLS TLSConfig `json:"tls"`
}

func SetDefaults_MetricsServerConfig(obj *MetricsServerConfig) {
	if obj.SecureServing == nil {
		obj.SecureServing = ptr.To(true)
	}

	if obj.BindAddress == "" {
		obj.BindAddress = "0"
	}

	if len(obj.TLS.CertDir) == 0 {
		obj.TLS.CertDir = filepath.Join(os.TempDir(), "k8s-metrics-server", "serving-certs")
	}
}

func (c *MetricsServerConfig) Options(ctx context.Context, secretsClient client.Client) metricsserver.Options {
	secureServing := c.SecureServing != nil && *c.SecureServing

	opts := metricsserver.Options{
		SecureServing: secureServing,
		BindAddress:   c.BindAddress,
		CertDir:       c.TLS.CertDir,
		CertName:      c.TLS.CertName,
		KeyName:       c.TLS.KeyName,
	}

	if secureServing {
		opts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if secretRef := c.TLS.SecretRef; secretRef != nil {
		opts.TLSOpts = c.TLS.Options(ctx, secretsClient)
	}

	return opts
}

// +k8s:deepcopy-gen=true

// TLSConfig configures TLS certificate management.
type TLSConfig struct {
	// SecretRef is a reference to a secret that contains the server key and certificate.
	SecretRef *corev1.ObjectReference `json:"secretRef,omitempty"`

	// CertDir is the directory that contains the server key and certificate.
	CertDir string `json:"certDir"`

	// CertName is the server certificate name. Defaults to tls.crt.
	CertName string `json:"certName"`

	// KeyName is the server key name. Defaults to tls.key.
	KeyName string `json:"keyName"`
}

func (c *TLSConfig) Options(ctx context.Context, secretsClient client.Client) []func(*tls.Config) {
	var tlsOpts []func(*tls.Config)

	if secretRef := c.SecretRef; secretRef != nil {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			logger := ctrl.Log.WithName("tls-client")
			c.GetCertificate = func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				logger.Info("getting certificate")

				var secret corev1.Secret
				secretObjectKey := types.NamespacedName{
					Name:      secretRef.Name,
					Namespace: secretRef.Namespace,
				}
				if err := secretsClient.Get(ctx, secretObjectKey, &secret); err != nil {
					return nil, fmt.Errorf("failed to get secret: %w", err)
				}

				cert, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
				if err != nil {
					return nil, fmt.Errorf("failed to parse certificate: %w", err)
				}

				return &cert, nil
			}
		})
	}

	return tlsOpts
}

func SetDefaults_TLSConfig(obj *TLSConfig) {
	if len(obj.CertName) == 0 {
		obj.CertName = "tls.crt"
	}

	if len(obj.KeyName) == 0 {
		obj.KeyName = "tls.key"
	}
}

// +k8s:deepcopy-gen=true

// DiscoveryConfig configures cluster discovery.
type DiscoveryConfig struct {
	// Mode is the mode that the operator should use to discover clusters.
	// Defaults to "single"
	Mode multiclusterproviders.Provider `json:"mode"`

	// InternalServiceDiscovery will result in the operator connecting to internal
	// service addresses for projects.
	InternalServiceDiscovery bool `json:"internalServiceDiscovery"`

	// DiscoveryKubeconfigPath is the path to the kubeconfig file to use for
	// project discovery.
	DiscoveryKubeconfigPath string `json:"discoveryKubeconfigPath"`

	// ProjectKubeconfigPath is the path to the kubeconfig file to use as a
	// template when connecting to project control planes.
	ProjectKubeconfigPath string `json:"projectKubeconfigPath"`
}

func SetDefaults_DiscoveryConfig(obj *DiscoveryConfig) {
	if obj.Mode == "" {
		obj.Mode = multiclusterproviders.ProviderSingle
	}
}

func (c *DiscoveryConfig) DiscoveryRestConfig() (*rest.Config, error) {
	if c.DiscoveryKubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	return clientcmd.BuildConfigFromFlags("", c.DiscoveryKubeconfigPath)
}

func (c *DiscoveryConfig) ProjectRestConfig() (*rest.Config, error) {
	if c.ProjectKubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	return clientcmd.BuildConfigFromFlags("", c.ProjectKubeconfigPath)
}

// SetDefaults_BillingOperator sets defaults for BillingOperator.
// The generated SetObjectDefaults_BillingOperator handles calling nested
// defaults (MetricsServerConfig, WebhookServerConfig, TLSConfig,
// DiscoveryConfig), so this function only sets top-level defaults.
func SetDefaults_BillingOperator(obj *BillingOperator) {
	// Top-level defaults are handled by nested SetDefaults_* functions
	// which are called by the generated SetObjectDefaults_BillingOperator.
}

func init() {
	SchemeBuilder.Register(&BillingOperator{})
}
