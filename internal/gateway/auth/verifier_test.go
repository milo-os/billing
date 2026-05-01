// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"context"
	"testing"

	"go.miloapis.com/billing/internal/gateway/auth"
)

// fakeVerifier is a TokenVerifier for use in unit tests.
type fakeVerifier struct {
	err error
}

func (f *fakeVerifier) Verify(_ context.Context, _ string) error {
	return f.err
}

func TestFakeVerifier_nil(t *testing.T) {
	v := &fakeVerifier{err: nil}
	if err := v.Verify(context.Background(), "token"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestFakeVerifier_error(t *testing.T) {
	v := &fakeVerifier{err: auth.ErrTokenNotAuthenticated}
	if err := v.Verify(context.Background(), "bad"); err == nil {
		t.Fatal("expected error, got nil")
	}
}
