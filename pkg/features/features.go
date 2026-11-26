// SPDX-License-Identifier: AGPL-3.0-only

// Package features defines feature gates for the Billing operator and webhooks.
//
// Feature gates follow the Kubernetes pattern for managing feature lifecycle:
//   - Alpha: Disabled by default, may be removed without notice
//   - Beta: Enabled by default, API may change
//   - GA: Enabled by default, stable
//
// Usage:
//
//	import (
//	    utilfeature "k8s.io/apiserver/pkg/util/feature"
//	    "go.miloapis.com/billing/pkg/features"
//	)
//
//	if utilfeature.DefaultFeatureGate.Enabled(features.CreditGrants) {
//	    // feature is enabled
//	}
package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

const (
	// CreditGrants enables CreditGrant creation, Offer signupCredit
	// configuration, and automated signup credit reconciliation.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.6.0
	CreditGrants featuregate.Feature = "CreditGrants"

	// ContractStop lets a contract's term be set to stop (term.atEnd Stop).
	// Billing moves the account onto term.offerAfterRef at the stop, which
	// openmeter-provider must handle as a stop rather than an amendment, so
	// it stays off until a provider release that does is running.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.14.0
	ContractStop featuregate.Feature = "ContractStop"
)

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultFeatureGates))
}

// defaultFeatureGates defines the default state of Billing feature gates.
// Features are listed in alphabetical order.
var defaultFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	CreditGrants: {
		Default:    false,
		PreRelease: featuregate.Alpha,
	},
	ContractStop: {
		Default:    false,
		PreRelease: featuregate.Alpha,
	},
}
