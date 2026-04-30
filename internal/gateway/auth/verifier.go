// SPDX-License-Identifier: AGPL-3.0-only

// Package auth provides bearer token verification for the ingestion gateway.
package auth

import (
	"context"
	"errors"
	"fmt"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	authv1client "k8s.io/client-go/kubernetes/typed/authentication/v1"
)

// ErrTokenNotAuthenticated is returned when a token fails authentication.
var ErrTokenNotAuthenticated = errors.New("token not authenticated")

// TokenVerifier abstracts bearer token verification.
// Handler code depends only on this interface — never on Kubernetes types.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) error
}

// ServiceAccountTokenVerifier implements TokenVerifier using the Kubernetes
// TokenReview API.
type ServiceAccountTokenVerifier struct {
	client   authv1client.AuthenticationV1Interface
	audience string
}

// NewServiceAccountTokenVerifier creates a verifier that calls the Kubernetes
// TokenReview API and validates the token against the given audience.
func NewServiceAccountTokenVerifier(
	client kubernetes.Interface,
	audience string,
) *ServiceAccountTokenVerifier {
	return &ServiceAccountTokenVerifier{
		client:   client.AuthenticationV1(),
		audience: audience,
	}
}

// Verify calls the Kubernetes TokenReview API. Returns nil if the token is
// valid and authenticated; returns an opaque error (never exposing internal
// Kubernetes API detail) if authentication fails.
func (v *ServiceAccountTokenVerifier) Verify(ctx context.Context, token string) error {
	review, err := v.client.TokenReviews().Create(ctx, &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{v.audience},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		// Return an opaque error — do not expose internal Kubernetes API details.
		return fmt.Errorf("token verification failed")
	}
	if !review.Status.Authenticated {
		return ErrTokenNotAuthenticated
	}
	return nil
}
