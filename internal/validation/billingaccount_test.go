// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
		name     string
		oldPhase billingv1alpha1.BillingAccountPhase
		oldCurr  string
		newCurr  string
		wantErr  bool
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

func TestValidateBillingAccountDefaultPaymentMethodRef(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register scheme: %v", err)
	}

	const ns = "acme-corp"
	activePM := &billingv1alpha1.PaymentMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-visa", Namespace: ns},
		Status:     billingv1alpha1.PaymentMethodStatus{Phase: billingv1alpha1.PaymentMethodPhaseActive},
	}
	pendingPM := &billingv1alpha1.PaymentMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-mastercard", Namespace: ns},
		Status:     billingv1alpha1.PaymentMethodStatus{Phase: billingv1alpha1.PaymentMethodPhasePending},
	}

	tests := []struct {
		name    string
		ref     *billingv1alpha1.DefaultPaymentMethodRef
		objects []client.Object
		wantErr bool
	}{
		{
			name:    "no ref set is valid",
			ref:     nil,
			objects: []client.Object{activePM},
			wantErr: false,
		},
		{
			name:    "empty name is treated as unset",
			ref:     &billingv1alpha1.DefaultPaymentMethodRef{Name: ""},
			objects: []client.Object{activePM},
			wantErr: false,
		},
		{
			name:    "ref to missing payment method fails",
			ref:     &billingv1alpha1.DefaultPaymentMethodRef{Name: "does-not-exist"},
			objects: []client.Object{activePM},
			wantErr: true,
		},
		{
			name:    "ref to non-active payment method fails",
			ref:     &billingv1alpha1.DefaultPaymentMethodRef{Name: "corp-mastercard"},
			objects: []client.Object{activePM, pendingPM},
			wantErr: true,
		},
		{
			name:    "ref to active payment method passes",
			ref:     &billingv1alpha1.DefaultPaymentMethodRef{Name: "corp-visa"},
			objects: []client.Object{activePM},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "acme-billing", Namespace: ns},
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode:            "USD",
					DefaultPaymentMethodRef: tt.ref,
				},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()
			errs := ValidateBillingAccountDefaultPaymentMethodRef(context.Background(), c, account)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateBillingAccountDefaultPaymentMethodRef() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateContactInfo_AddressAndInvoiceEmail(t *testing.T) {
	tests := []struct {
		name    string
		contact *billingv1alpha1.BillingContactInfo
		wantErr bool
	}{
		{
			name: "valid full contact",
			contact: &billingv1alpha1.BillingContactInfo{
				Email:        "billing@example.com",
				InvoiceEmail: "ar@example.com",
				Address: &billingv1alpha1.BillingAddress{
					FirstName: "Matt", LastName: "Jenkinson",
					Country: "GB", Line1: "1 King St", City: "London", PostalCode: "W1 1AA",
				},
			},
		},
		{
			name: "invalid invoice email",
			contact: &billingv1alpha1.BillingContactInfo{
				Email:        "billing@example.com",
				InvoiceEmail: "not-an-email",
			},
			wantErr: true,
		},
		{
			name: "address missing country",
			contact: &billingv1alpha1.BillingContactInfo{
				Email:   "billing@example.com",
				Address: &billingv1alpha1.BillingAddress{Line1: "1 King St"},
			},
			wantErr: true,
		},
		{
			name: "address invalid country code",
			contact: &billingv1alpha1.BillingContactInfo{
				Email:   "billing@example.com",
				Address: &billingv1alpha1.BillingAddress{Country: "GBR"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "GBP",
					ContactInfo:  tt.contact,
				},
			}
			errs := ValidateBillingAccountCreate(account)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("got errs=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateTaxIDs(t *testing.T) {
	tests := []struct {
		name    string
		taxIDs  []billingv1alpha1.TaxID
		wantErr bool
	}{
		{name: "nil ok", taxIDs: nil, wantErr: false},
		{
			name: "multiple jurisdictions valid",
			taxIDs: []billingv1alpha1.TaxID{
				{Type: "gb_vat", Value: "GB123456789"},
				{Type: "eu_vat", Value: "DE987654321"},
			},
		},
		{
			name:    "bad type casing",
			taxIDs:  []billingv1alpha1.TaxID{{Type: "GB_VAT", Value: "GB123"}},
			wantErr: true,
		},
		{
			name:    "missing value",
			taxIDs:  []billingv1alpha1.TaxID{{Type: "gb_vat", Value: ""}},
			wantErr: true,
		},
		{
			name: "duplicate type",
			taxIDs: []billingv1alpha1.TaxID{
				{Type: "gb_vat", Value: "GB123"},
				{Type: "gb_vat", Value: "GB456"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &billingv1alpha1.BillingAccount{
				Spec: billingv1alpha1.BillingAccountSpec{
					CurrencyCode: "GBP",
					TaxIDs:       tt.taxIDs,
				},
			}
			errs := ValidateBillingAccountCreate(account)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("got errs=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}
