// SPDX-License-Identifier: AGPL-3.0-only

// Package v1alpha1 contains API Schema definitions for the
// usage.billing.miloapis.com v1alpha1 API group: read-only queries over
// metered usage.
//
// These types are not CRDs. They are served by an aggregated API server that
// Milo proxies to, implemented by the usage provider (openmeter-provider
// today), so the caller never talks to the metering backend directly. Milo
// authenticates and authorizes every request; the server reads the caller's
// parent context (platform, organization or project) to decide which billing
// accounts a query may read. See ScopeFromUserExtra.
//
// +kubebuilder:object:generate=true
// +kubebuilder:skip
// +k8s:openapi-gen=true
// +groupName=usage.billing.miloapis.com
package v1alpha1
