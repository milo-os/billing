// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
)

// ClusterContextKey is the context key for the cluster name.
type ClusterContextKey struct{}

// WithClusterName returns a new context with the cluster name set.
func WithClusterName(ctx context.Context, clusterName string) context.Context {
	return context.WithValue(ctx, ClusterContextKey{}, clusterName)
}

// ClusterNameFromContext returns the cluster name from the context.
func ClusterNameFromContext(ctx context.Context) string {
	if clusterName, ok := ctx.Value(ClusterContextKey{}).(string); ok {
		return clusterName
	}
	return ""
}
