// SPDX-License-Identifier: AGPL-3.0-only

package features_test

import (
	"testing"

	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"go.miloapis.com/billing/pkg/features"
)

func TestCreditGrantsFeatureGate_DefaultsToDisabled(t *testing.T) {
	if utilfeature.DefaultFeatureGate.Enabled(features.CreditGrants) {
		t.Fatal("CreditGrants feature gate must default to disabled (Alpha)")
	}
}
