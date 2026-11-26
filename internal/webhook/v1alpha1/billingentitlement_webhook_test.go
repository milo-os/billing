// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/pkg/features"
)

func TestValidateContractStopGate(t *testing.T) {
	withAtEnd := func(atEnd billingv1alpha1.ContractTermAtEnd) *billingv1alpha1.BillingEntitlement {
		return &billingv1alpha1.BillingEntitlement{Spec: billingv1alpha1.BillingEntitlementSpec{
			Contract: &billingv1alpha1.BillingContract{
				StartsAt: metav1.NewTime(time.Date(2026, time.April, 18, 0, 0, 0, 0, time.UTC)),
				Term:     &billingv1alpha1.ContractTerm{Months: 36, AtEnd: atEnd},
			},
		}}
	}
	stop, monthly := billingv1alpha1.ContractTermAtEndStop, billingv1alpha1.ContractTermAtEndMonthToMonth

	if errs := validateContractStopGate(nil, withAtEnd(stop)); len(errs) == 0 {
		t.Error("creating a contract set to stop with the gate off: no error")
	}
	if errs := validateContractStopGate(withAtEnd(monthly), withAtEnd(stop)); len(errs) == 0 {
		t.Error("setting a contract to stop with the gate off: no error")
	}
	if errs := validateContractStopGate(withAtEnd(stop), withAtEnd(stop)); len(errs) > 0 {
		t.Errorf("changing a contract already set to stop: %v", errs)
	}
	if errs := validateContractStopGate(nil, withAtEnd(monthly)); len(errs) > 0 {
		t.Errorf("a contract carrying on month to month: %v", errs)
	}

	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ContractStop, true)
	if errs := validateContractStopGate(withAtEnd(monthly), withAtEnd(stop)); len(errs) > 0 {
		t.Errorf("setting a contract to stop with the gate on: %v", errs)
	}
}
