// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestValidateBillingAccountCreate(t *testing.T) {
	tests := []struct {
		name    string
		account *billingv1alpha1.BillingAccount
		wantErr bool
	}{
		{
			name: "valid account with no optional fields",
			account: &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid contact info missing email",
			account: &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
					ContactInfo: &billingv1alpha1.BillingContactInfo{
						Name: "Billing Dept",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid contact info",
			account: &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
					ContactInfo: &billingv1alpha1.BillingContactInfo{
						Email: "billing@example.com",
						Name:  "Billing Dept",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid contact info bad email format",
			account: &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "USD",
					ContactInfo: &billingv1alpha1.BillingContactInfo{
						Email: "not-an-email",
						Name:  "Billing Dept",
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateBillingAccountCreate(tt.account)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateBillingAccountCreate() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateBillingAccountUpdate_CurrencyImmutability(t *testing.T) {
	tests := []struct {
		name       string
		oldPhase   billingv1alpha1.BillingAccountPhase
		oldCurr    string
		newCurr    string
		wantErr    bool
	}{
		{
			name:     "allow currency change in Provisioning",
			oldPhase: billingv1alpha1.BillingAccountPhaseProvisioning,
			oldCurr:  "USD",
			newCurr:  "EUR",
			wantErr:  false,
		},
		{
			name:     "reject currency change in Ready",
			oldPhase: billingv1alpha1.BillingAccountPhaseReady,
			oldCurr:  "USD",
			newCurr:  "EUR",
			wantErr:  true,
		},
		{
			name:     "allow same currency in Ready",
			oldPhase: billingv1alpha1.BillingAccountPhaseReady,
			oldCurr:  "USD",
			newCurr:  "USD",
			wantErr:  false,
		},
		{
			name:     "reject currency change in Suspended",
			oldPhase: billingv1alpha1.BillingAccountPhaseSuspended,
			oldCurr:  "USD",
			newCurr:  "EUR",
			wantErr:  true,
		},
		{
			name:     "allow currency change with empty phase",
			oldPhase: "",
			oldCurr:  "USD",
			newCurr:  "EUR",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldAccount := &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: tt.oldCurr,
				},
				Status: billingv1alpha1.BillingAccountStatus{
					Phase: tt.oldPhase,
				},
			}
			newAccount := &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: tt.newCurr,
				},
			}

			errs := ValidateBillingAccountUpdate(oldAccount, newAccount)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateBillingAccountUpdate() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}
