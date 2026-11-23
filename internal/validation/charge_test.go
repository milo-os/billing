// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

var chargeNow = time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)

func chargeTestAccount(writerReady bool) *billingv1alpha1.BillingAccount {
	status := metav1.ConditionFalse
	if writerReady {
		status = metav1.ConditionTrue
	}
	return &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "acme-uid"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		Status: billingv1alpha1.BillingAccountStatus{Conditions: []metav1.Condition{{
			Type: billingv1alpha1.BillingAccountConditionInvoiceWriterReady, Status: status, Reason: "Test",
		}}},
	}
}

func chargeTestPricing(name string, chargeType billingv1alpha1.ChargeType, amount string) *billingv1alpha1.ServicePricing {
	return &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  chargeType,
			Currency:    "USD",
			DisplayName: "Cross-connect install",
			Amount:      amount,
			Tax:         &billingv1alpha1.PricingTax{Code: "txcd_20030000"},
		},
	}
}

func chargeTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func customCharge() *billingv1alpha1.Charge {
	return &billingv1alpha1.Charge{
		ObjectMeta: metav1.ObjectMeta{Name: "remote-hands", Namespace: "org-acme"},
		Spec: billingv1alpha1.ChargeSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Description:       "Remote hands, December",
			Quantity:          "3.5",
			UnitAmount:        "150.00",
			Currency:          "USD",
			ServicePeriod:     &billingv1alpha1.ServicePeriod{Start: "2026-12-01", End: "2026-12-31"},
			Invoice:           billingv1alpha1.ChargeInvoiceNext,
		},
	}
}

func TestChargeDefaultServicePeriod(t *testing.T) {
	contract := func(day int32, starts time.Time) *billingv1alpha1.BillingAccountContract {
		return &billingv1alpha1.BillingAccountContract{StartsAt: metav1.NewTime(starts), BillingDayOfMonth: day}
	}
	tests := []struct {
		name     string
		now      time.Time
		contract *billingv1alpha1.BillingAccountContract
		invDay   int32
		want     billingv1alpha1.ServicePeriod
	}{
		{name: "standard terms bill on the 1st", now: chargeNow,
			want: billingv1alpha1.ServicePeriod{Start: "2026-11-01", End: "2026-11-30"}},
		{name: "on the billing day itself", now: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
			want: billingv1alpha1.ServicePeriod{Start: "2026-12-01", End: "2026-12-31"}},
		{name: "contract billing on the 15th, before it", now: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
			contract: contract(15, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)),
			want:     billingv1alpha1.ServicePeriod{Start: "2026-10-15", End: "2026-11-14"}},
		{name: "contract billing on the 31st, through February", now: time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC),
			contract: contract(31, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)),
			want:     billingv1alpha1.ServicePeriod{Start: "2027-02-28", End: "2027-03-30"}},
		{name: "contract billing on the 31st, in January", now: time.Date(2027, 1, 20, 12, 0, 0, 0, time.UTC),
			contract: contract(31, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)),
			want:     billingv1alpha1.ServicePeriod{Start: "2026-12-31", End: "2027-01-30"}},
		{name: "a contract that hasn't started uses the invoice day", now: chargeNow,
			contract: contract(15, time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)), invDay: 5,
			want: billingv1alpha1.ServicePeriod{Start: "2026-11-05", End: "2026-12-04"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := chargeTestAccount(true)
			account.Status.Contract = tt.contract
			if tt.invDay > 0 {
				account.Spec.PaymentTerms = &billingv1alpha1.PaymentTerms{InvoiceDayOfMonth: tt.invDay}
			}
			if got := ChargeDefaultServicePeriod(account, tt.now); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestChargeAmounts(t *testing.T) {
	tests := []struct {
		qty, unit, pct  string
		amount, due     string
		wantErrContains string
	}{
		{qty: "2", unit: "300", amount: "600.00", due: "600.00"},
		{qty: "3.5", unit: "150.00", amount: "525.00", due: "525.00"},
		{qty: "2", unit: "250.00", pct: "10", amount: "500.00", due: "450.00"},
		{qty: "1", unit: "0", amount: "0.00", due: "0.00"},
		{qty: "1", unit: "99.99", pct: "100", amount: "99.99", due: "0.00"},
		{qty: "3.33", unit: "0.15", wantErrContains: "isn't whole cents"},
		{qty: "1", unit: "3.35", pct: "10", wantErrContains: "discount on 3.35"},
		{qty: "0", unit: "1", wantErrContains: "positive"},
	}
	for _, tt := range tests {
		spec := &billingv1alpha1.ChargeSpec{Quantity: tt.qty, UnitAmount: tt.unit, DiscountPercent: tt.pct}
		amount, due, err := ChargeAmounts(spec)
		if tt.wantErrContains != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Errorf("%s × %s less %s%%: got err %v, want it to contain %q", tt.qty, tt.unit, tt.pct, err, tt.wantErrContains)
			}
			continue
		}
		if err != nil || amount != tt.amount || due != tt.due {
			t.Errorf("%s × %s less %s%%: got %s, %s, %v; want %s, %s", tt.qty, tt.unit, tt.pct, amount, due, err, tt.amount, tt.due)
		}
	}
}

func TestApplyChargeDefaults(t *testing.T) {
	pricing := chargeTestPricing("cross-connect-install-v1", billingv1alpha1.ChargeTypeOneTime, "300")
	spec := &billingv1alpha1.ChargeSpec{
		BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
		ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: pricing.Name},
		Quantity:          "2",
	}
	ApplyChargeDefaults(spec, pricing, chargeTestAccount(true), chargeNow)

	if spec.Description != "Cross-connect install" || spec.UnitAmount != "300.00" ||
		spec.Tax == nil || spec.Tax.Code != "txcd_20030000" || spec.Currency != "USD" ||
		spec.Invoice != billingv1alpha1.ChargeInvoiceNext || spec.ServicePricingRef.Namespace != "milo-system" {
		t.Fatalf("catalog defaults not applied: %+v", spec)
	}
	if spec.ServicePeriod == nil || *spec.ServicePeriod != (billingv1alpha1.ServicePeriod{Start: "2026-11-01", End: "2026-11-30"}) {
		t.Fatalf("period not defaulted: %+v", spec.ServicePeriod)
	}

	// Staff's own values win over the catalog's.
	own := &billingv1alpha1.ChargeSpec{
		ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: pricing.Name},
		Description:       "Cross-connect install, cage 12",
		UnitAmount:        "250.00",
		Tax:               &billingv1alpha1.PricingTax{Code: "txcd_99999999"},
		ServicePeriod:     &billingv1alpha1.ServicePeriod{Start: "2026-12-15", End: "2026-12-15"},
	}
	ApplyChargeDefaults(own, pricing, chargeTestAccount(true), chargeNow)
	if own.Description != "Cross-connect install, cage 12" || own.UnitAmount != "250.00" ||
		own.Tax.Code != "txcd_99999999" || own.ServicePeriod.Start != "2026-12-15" {
		t.Fatalf("staff values overwritten: %+v", own)
	}

	// A pricing priced in fractions of a cent can't be a default price.
	fractional := &billingv1alpha1.ChargeSpec{ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: "p"}}
	ApplyChargeDefaults(fractional, chargeTestPricing("p", billingv1alpha1.ChargeTypeOneTime, "0.005"), nil, chargeNow)
	if fractional.UnitAmount != "" || fractional.ServicePeriod != nil {
		t.Fatalf("got %+v; want no price and no period", fractional)
	}
}

func TestValidateChargeCreate(t *testing.T) {
	oneTime := chargeTestPricing("cross-connect-install-v1", billingv1alpha1.ChargeTypeOneTime, "300")
	usage := chargeTestPricing("egress-v1", billingv1alpha1.ChargeTypeUsage, "0.05")
	customerPrice := chargeTestPricing("acme-install", billingv1alpha1.ChargeTypeOneTime, "200")
	customerPrice.Spec.BasedOn = oneTime.Name

	tests := []struct {
		name    string
		mutate  func(*billingv1alpha1.Charge)
		account *billingv1alpha1.BillingAccount
		extra   []client.Object
		wantErr string
	}{
		{name: "a custom charge"},
		{name: "a catalog charge at a changed price", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePricingRef = &billingv1alpha1.ChargeServicePricingRef{Name: oneTime.Name, Namespace: "milo-system"}
			c.Spec.UnitAmount = "250.00"
		}},
		{name: "an account not on the invoice writer", account: chargeTestAccount(false), wantErr: "charges aren't available"},
		{name: "an account billed in EUR", account: func() *billingv1alpha1.BillingAccount {
			a := chargeTestAccount(true)
			a.Spec.CurrencyCode = "EUR"
			return a
		}(), wantErr: "USD only"},
		{name: "a missing account", account: &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "org-acme"}}, wantErr: "Not found"},
		{name: "a usage pricing", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePricingRef = &billingv1alpha1.ChargeServicePricingRef{Name: usage.Name, Namespace: "milo-system"}
		}, wantErr: "needs a OneTime or Recurring pricing"},
		{name: "a missing pricing", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePricingRef = &billingv1alpha1.ChargeServicePricingRef{Name: "gone", Namespace: "milo-system"}
		}, wantErr: "Not found"},
		{name: "another customer's price", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePricingRef = &billingv1alpha1.ChargeServicePricingRef{Name: customerPrice.Name, Namespace: "milo-system"}
		}, wantErr: "isn't one of billing account"},
		{name: "this account's own customer price", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePricingRef = &billingv1alpha1.ChargeServicePricingRef{Name: customerPrice.Name, Namespace: "milo-system"}
		}, extra: []client.Object{
			&billingv1alpha1.BillingEntitlement{
				ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
				Spec: billingv1alpha1.BillingEntitlementSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
					OfferRef:          billingv1alpha1.OfferReference{Name: "contract-acme"},
				},
			},
			&billingv1alpha1.Offer{
				ObjectMeta: metav1.ObjectMeta{Name: "contract-acme"},
				Spec: billingv1alpha1.OfferSpec{ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
					{Name: customerPrice.Name, Namespace: "milo-system"},
				}},
			},
		}},
		{name: "no price", mutate: func(c *billingv1alpha1.Charge) { c.Spec.UnitAmount = "" }, wantErr: "a price is required"},
		{name: "cents that don't divide", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.Quantity, c.Spec.UnitAmount = "3.33", "0.15"
		}, wantErr: "isn't whole cents"},
		{name: "a discount that doesn't divide", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.Quantity, c.Spec.UnitAmount, c.Spec.DiscountPercent = "1", "3.35", "10"
		}, wantErr: "change the discount"},
		{name: "not taxable without a note", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.Tax = &billingv1alpha1.PricingTax{Taxable: new(bool)}
		}, wantErr: "needs a note"},
		{name: "not taxable with a note", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.Tax = &billingv1alpha1.PricingTax{Taxable: new(bool)}
			c.Spec.Note = "Reseller, exempt"
		}},
		{name: "no period", mutate: func(c *billingv1alpha1.Charge) { c.Spec.ServicePeriod = nil }, wantErr: "service period is required"},
		{name: "a single day", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-12-15", End: "2026-12-15"}
		}},
		{name: "a past period", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-10-01", End: "2026-10-31"}
		}},
		{name: "a period ending before it starts", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-12-15", End: "2026-12-14"}
		}, wantErr: "can't end before it starts"},
		{name: "a typo in the year", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2062-12-01", End: "2062-12-31"}
		}, wantErr: "more than 5 years from now"},
		{name: "starting too long ago", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2024-01-01", End: "2024-01-31"}
		}, wantErr: "more than 2 years ago"},
		{name: "too long", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-01-01", End: "2031-06-30"}
		}, wantErr: "longer than 5 years"},
		{name: "not a real date", mutate: func(c *billingv1alpha1.Charge) {
			c.Spec.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-02-30", End: "2026-03-01"}
		}, wantErr: "must be a date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := tt.account
			if account == nil {
				account = chargeTestAccount(true)
			}
			objs := append([]client.Object{account, oneTime, usage, customerPrice}, tt.extra...)
			c := chargeTestClient(t, objs...)
			charge := customCharge()
			if tt.mutate != nil {
				tt.mutate(charge)
			}
			errs := ValidateChargeCreate(context.Background(), c, charge, chargeNow)
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if !strings.Contains(errs.ToAggregate().Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateChargeUpdate(t *testing.T) {
	old := customCharge()
	same := old.DeepCopy()
	same.Finalizers = []string{"openmeter.billing.miloapis.com/charge"}
	if errs := ValidateChargeUpdate(old, same); len(errs) > 0 {
		t.Fatalf("a metadata-only update was refused: %v", errs)
	}
	changed := old.DeepCopy()
	changed.Spec.Quantity = "4"
	if errs := ValidateChargeUpdate(old, changed); len(errs) == 0 {
		t.Fatal("a spec change was accepted")
	}
}

func TestValidateChargeDelete(t *testing.T) {
	invoiced := func() *billingv1alpha1.Charge {
		c := customCharge()
		c.Status.Phase = billingv1alpha1.ChargePhaseInvoiced
		c.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "acme", UID: "acme-uid"}}
		return c
	}
	onInvoice := func(status metav1.ConditionStatus) *billingv1alpha1.Charge {
		c := customCharge()
		c.Status.Phase = billingv1alpha1.ChargePhaseScheduled
		c.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "acme", UID: "acme-uid"}}
		c.Status.Conditions = []metav1.Condition{{Type: billingv1alpha1.ChargeConditionOnInvoice, Status: status, Reason: "Added"}}
		return c
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "org-acme"}}
	now := metav1.Now()

	tests := []struct {
		name    string
		charge  *billingv1alpha1.Charge
		objs    []client.Object
		allowed bool
	}{
		{name: "a scheduled charge", charge: func() *billingv1alpha1.Charge {
			c := customCharge()
			c.Status.Phase = billingv1alpha1.ChargePhaseScheduled
			return c
		}(), objs: []client.Object{ns, chargeTestAccount(true)}, allowed: true},
		{name: "an invoiced charge", charge: invoiced(), objs: []client.Object{ns, chargeTestAccount(true)}},
		{name: "a scheduled charge held in billing", charge: onInvoice(metav1.ConditionFalse), objs: []client.Object{ns, chargeTestAccount(true)}, allowed: true},
		{name: "a scheduled charge on its invoice", charge: onInvoice(metav1.ConditionTrue), objs: []client.Object{ns, chargeTestAccount(true)}},
		{name: "a charge on its invoice whose account is deleting", charge: onInvoice(metav1.ConditionTrue), objs: []client.Object{ns, func() *billingv1alpha1.BillingAccount {
			a := chargeTestAccount(true)
			a.DeletionTimestamp = &now
			a.Finalizers = []string{"test"}
			return a
		}()}, allowed: true},
		{name: "an invoiced charge whose account is deleting", charge: invoiced(), objs: []client.Object{ns, func() *billingv1alpha1.BillingAccount {
			a := chargeTestAccount(true)
			a.DeletionTimestamp = &now
			a.Finalizers = []string{"test"}
			return a
		}()}, allowed: true},
		{name: "an invoiced charge whose account is gone", charge: invoiced(), objs: []client.Object{ns}, allowed: true},
		{name: "an invoiced charge in a terminating namespace", charge: invoiced(), objs: []client.Object{
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "org-acme", DeletionTimestamp: &now, Finalizers: []string{"kubernetes"}}},
			chargeTestAccount(true),
		}, allowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateChargeDelete(context.Background(), chargeTestClient(t, tt.objs...), tt.charge)
			if tt.allowed != (len(errs) == 0) {
				t.Fatalf("allowed = %v, want %v (%v)", len(errs) == 0, tt.allowed, errs)
			}
		})
	}
}
