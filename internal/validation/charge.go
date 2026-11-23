// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ChargeDateLayout is how a charge's service period dates are written.
const ChargeDateLayout = "2006-01-02"

// Bounds on a charge's service period, counted from the day it's added.
// They catch typos in the year, not real charges.
const (
	chargePeriodMaxYearsBack  = 2
	chargePeriodMaxYearsAhead = 5
	chargePeriodMaxYearsLong  = 5
)

// ChargeBillingDay is the day of the month an account bills on, for a
// charge's default service period: its contract's billing day once the
// contract has started, and otherwise its invoice day.
func ChargeBillingDay(account *billingv1alpha1.BillingAccount, now time.Time) int32 {
	if account != nil && account.Status.Contract != nil &&
		account.Status.Contract.BillingDayOfMonth > 0 &&
		!account.Status.Contract.StartsAt.After(now) {
		return account.Status.Contract.BillingDayOfMonth
	}
	return AccountInvoiceDay(account)
}

// ChargeDefaultServicePeriod is the account's current billing period on
// now's UTC day: from the most recent billing day on or before it to the
// day before the next. Months shorter than the billing day bill on their
// last day.
func ChargeDefaultServicePeriod(account *billingv1alpha1.BillingAccount, now time.Time) billingv1alpha1.ServicePeriod {
	day := int(ChargeBillingDay(account, now))
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	start := billingDateIn(today.Year(), today.Month(), day)
	if start.After(today) {
		start = billingDateIn(today.Year(), today.Month()-1, day)
	}
	next := billingDateIn(start.Year(), start.Month()+1, day)
	return billingv1alpha1.ServicePeriod{
		Start: start.Format(ChargeDateLayout),
		End:   next.AddDate(0, 0, -1).Format(ChargeDateLayout),
	}
}

// billingDateIn is day of the given month, or the month's last day when
// it's shorter. month may be out of range; it's normalized.
func billingDateIn(year int, month time.Month, day int) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	if last := first.AddDate(0, 1, -1).Day(); day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

// ApplyChargeDefaults fills what staff left out of a charge: the catalog
// item's description, price and tax, and the account's current billing
// period. pricing and account may be nil when they couldn't be read; those
// defaults are then left for validation to report.
func ApplyChargeDefaults(spec *billingv1alpha1.ChargeSpec, pricing *billingv1alpha1.ServicePricing,
	account *billingv1alpha1.BillingAccount, now time.Time) {
	if spec.Currency == "" {
		spec.Currency = "USD"
	}
	if spec.Invoice == "" {
		spec.Invoice = billingv1alpha1.ChargeInvoiceNext
	}
	if spec.ServicePricingRef != nil && spec.ServicePricingRef.Namespace == "" {
		spec.ServicePricingRef.Namespace = defaultPricingNamespace
	}
	if pricing != nil {
		if spec.Description == "" {
			spec.Description = pricing.Spec.DisplayName
			if spec.Description == "" {
				spec.Description = pricing.Name
			}
		}
		if spec.UnitAmount == "" {
			if amount, ok := CentsString(pricing.Spec.Amount); ok {
				spec.UnitAmount = amount
			}
		}
		if spec.Tax == nil && pricing.Spec.Tax != nil {
			spec.Tax = pricing.Spec.Tax.DeepCopy()
		}
	}
	if spec.ServicePeriod == nil && account != nil {
		period := ChargeDefaultServicePeriod(account, now)
		spec.ServicePeriod = &period
	}
}

// defaultPricingNamespace is where the catalog's ServicePricings live.
const defaultPricingNamespace = "milo-system"

// ChargeAmounts works out a charge's amount (quantity × unitAmount) and
// amount due (after its discount), as decimal strings with two decimal
// places. Both must come to whole cents: Stripe's rounding is never
// allowed to change an amount, so a charge that doesn't divide is refused
// rather than rounded.
func ChargeAmounts(spec *billingv1alpha1.ChargeSpec) (amount, amountDue string, err error) {
	qty, ok := new(big.Rat).SetString(spec.Quantity)
	if !ok || qty.Sign() <= 0 {
		return "", "", fmt.Errorf("quantity %q isn't a positive decimal", spec.Quantity)
	}
	unit, ok := new(big.Rat).SetString(spec.UnitAmount)
	if !ok || unit.Sign() < 0 {
		return "", "", fmt.Errorf("unitAmount %q isn't a decimal amount", spec.UnitAmount)
	}
	total := new(big.Rat).Mul(qty, unit)
	if !wholeCents(total) {
		return "", "", fmt.Errorf("%s × %s comes to %s, which isn't whole cents; change the quantity or the price",
			spec.Quantity, spec.UnitAmount, total.FloatString(4))
	}
	due := total
	if spec.DiscountPercent != "" {
		pct, ok := new(big.Rat).SetString(spec.DiscountPercent)
		if !ok || pct.Sign() <= 0 || pct.Cmp(big.NewRat(100, 1)) > 0 {
			return "", "", fmt.Errorf("discountPercent %q must be more than 0 and at most 100", spec.DiscountPercent)
		}
		discount := new(big.Rat).Mul(total, new(big.Rat).Quo(pct, big.NewRat(100, 1)))
		if !wholeCents(discount) {
			return "", "", fmt.Errorf("a %s%% discount on %s comes to %s, which isn't whole cents; change the discount",
				spec.DiscountPercent, total.FloatString(2), discount.FloatString(4))
		}
		due = new(big.Rat).Sub(total, discount)
	}
	return total.FloatString(2), due.FloatString(2), nil
}

// wholeCents reports whether r is a whole number of cents.
func wholeCents(r *big.Rat) bool {
	return new(big.Rat).Mul(r, big.NewRat(100, 1)).IsInt()
}

// CentsString returns amount with exactly two decimal places, or false
// when it has more than two that aren't zeros.
func CentsString(amount string) (string, bool) {
	r, ok := new(big.Rat).SetString(amount)
	if !ok || r.Sign() < 0 || !wholeCents(r) {
		return "", false
	}
	return r.FloatString(2), true
}

// ValidateChargeCreate validates a Charge on creation, after defaulting.
func ValidateChargeCreate(ctx context.Context, c client.Reader, charge *billingv1alpha1.Charge, now time.Time) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")
	spec := &charge.Spec

	account, errs := getReferencedBillingAccount(ctx, c, charge.Namespace, spec.BillingAccountRef.Name)
	allErrs = append(allErrs, errs...)
	if account != nil {
		accountPath := specPath.Child("billingAccountRef", "name")
		if account.Spec.CurrencyCode != "USD" {
			allErrs = append(allErrs, field.Invalid(accountPath, account.Name,
				fmt.Sprintf("billing account %q is billed in %s; charges are USD only", account.Name, account.Spec.CurrencyCode)))
		}
		if !apimeta.IsStatusConditionTrue(account.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoiceWriterReady) {
			allErrs = append(allErrs, field.Forbidden(accountPath,
				fmt.Sprintf("charges aren't available for billing account %q yet: its invoices aren't written by the invoice writer (condition %s)",
					account.Name, billingv1alpha1.BillingAccountConditionInvoiceWriterReady)))
		}
	}

	if spec.ServicePricingRef != nil {
		allErrs = append(allErrs, validateChargePricing(ctx, c, charge, account, specPath.Child("servicePricingRef"))...)
	}

	if spec.Description == "" {
		allErrs = append(allErrs, field.Required(specPath.Child("description"), "a description is required"))
	}
	if spec.Currency != "USD" {
		allErrs = append(allErrs, field.Invalid(specPath.Child("currency"), spec.Currency, "currency must be USD"))
	}
	if spec.UnitAmount == "" {
		allErrs = append(allErrs, field.Required(specPath.Child("unitAmount"),
			"a price is required; the catalog item's amount has more than two decimal places, or there's no catalog item"))
	} else if _, _, err := ChargeAmounts(spec); err != nil {
		allErrs = append(allErrs, field.Invalid(specPath.Child("quantity"), spec.Quantity, err.Error()))
	}
	if spec.Tax != nil && spec.Tax.Taxable != nil && !*spec.Tax.Taxable {
		if spec.Tax.Code != "" {
			allErrs = append(allErrs, field.Invalid(specPath.Child("tax", "code"), spec.Tax.Code,
				"a charge that isn't taxable can't have a tax code"))
		}
		if strings.TrimSpace(spec.Note) == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("note"), "a charge that isn't taxable needs a note saying why"))
		}
	}

	allErrs = append(allErrs, validateChargePeriod(spec.ServicePeriod, now, specPath.Child("servicePeriod"))...)
	allErrs = append(allErrs, validateChargeInvoiceGroup(ctx, c, charge, specPath.Child("invoiceGroup"))...)
	return allErrs
}

// validateChargeInvoiceGroup checks a charge joining an invoice group: it's
// invoiced now, it gives the same size as the group's other charges, and
// the group still has room and isn't on an invoice yet.
func validateChargeInvoiceGroup(ctx context.Context, c client.Reader, charge *billingv1alpha1.Charge, fldPath *field.Path) field.ErrorList {
	group := charge.Spec.InvoiceGroup
	if group == nil {
		return nil
	}
	if charge.Spec.Invoice != billingv1alpha1.ChargeInvoiceNow {
		return field.ErrorList{field.Invalid(field.NewPath("spec", "invoice"), charge.Spec.Invoice,
			"only a charge invoiced now can be in an invoice group")}
	}
	if c == nil {
		return nil
	}
	members, err := ChargeInvoiceGroupMembers(ctx, c, charge)
	if err != nil {
		return field.ErrorList{field.InternalError(fldPath, fmt.Errorf("listing the group's charges: %w", err))}
	}
	var allErrs field.ErrorList
	for i := range members {
		m := &members[i]
		if m.Spec.InvoiceGroup.Size != group.Size {
			return field.ErrorList{field.Invalid(fldPath.Child("size"), group.Size,
				fmt.Sprintf("invoice group %q has %d charges, as charge %q says", group.Name, m.Spec.InvoiceGroup.Size, m.Name))}
		}
		if m.Status.Phase == billingv1alpha1.ChargePhaseInvoiced || ChargeOnInvoice(m) {
			return field.ErrorList{field.Forbidden(fldPath.Child("name"),
				fmt.Sprintf("invoice group %q is already on an invoice; use a new group", group.Name))}
		}
	}
	if int32(len(members)) >= group.Size {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("name"),
			fmt.Sprintf("invoice group %q already has all %d of its charges", group.Name, group.Size)))
	}
	return allErrs
}

// ChargeInvoiceGroupMembers returns the other charges of charge's billing
// account in its invoice group, leaving out any being removed.
func ChargeInvoiceGroupMembers(ctx context.Context, c client.Reader, charge *billingv1alpha1.Charge) ([]billingv1alpha1.Charge, error) {
	group := charge.Spec.InvoiceGroup
	if group == nil {
		return nil, nil
	}
	var list billingv1alpha1.ChargeList
	if err := c.List(ctx, &list, client.InNamespace(charge.Namespace)); err != nil {
		return nil, err
	}
	var members []billingv1alpha1.Charge
	for _, m := range list.Items {
		if m.Name == charge.Name || !m.DeletionTimestamp.IsZero() ||
			m.Spec.BillingAccountRef.Name != charge.Spec.BillingAccountRef.Name ||
			m.Spec.InvoiceGroup == nil || m.Spec.InvoiceGroup.Name != group.Name {
			continue
		}
		members = append(members, m)
	}
	return members, nil
}

// validateChargePricing checks a charge's catalog item: a OneTime or
// Recurring pricing in USD. A customer's own price (basedOn) must be one of
// the account's contract fees.
func validateChargePricing(ctx context.Context, c client.Reader, charge *billingv1alpha1.Charge,
	account *billingv1alpha1.BillingAccount, fldPath *field.Path) field.ErrorList {
	ref := charge.Spec.ServicePricingRef
	ns := ref.Namespace
	if ns == "" {
		ns = defaultPricingNamespace
	}
	var pricing billingv1alpha1.ServicePricing
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &pricing); err != nil {
		if apierrors.IsNotFound(err) {
			return field.ErrorList{field.NotFound(fldPath.Child("name"), ref.Name)}
		}
		return field.ErrorList{field.InternalError(fldPath.Child("name"), fmt.Errorf("reading service pricing: %w", err))}
	}

	var allErrs field.ErrorList
	switch pricing.Spec.ChargeType {
	case billingv1alpha1.ChargeTypeOneTime, billingv1alpha1.ChargeTypeRecurring:
	default:
		allErrs = append(allErrs, field.Invalid(fldPath.Child("name"), ref.Name,
			fmt.Sprintf("service pricing %q is %s; a charge needs a OneTime or Recurring pricing", ref.Name, pricing.Spec.ChargeType)))
	}
	if pricing.Spec.Currency != "" && pricing.Spec.Currency != "USD" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("name"), ref.Name,
			fmt.Sprintf("service pricing %q is in %s; charges are USD only", ref.Name, pricing.Spec.Currency)))
	}
	if pricing.Spec.BasedOn != "" && account != nil {
		ok, err := accountContractHasPricing(ctx, c, account, ns, pricing.Name)
		if err != nil {
			allErrs = append(allErrs, field.InternalError(fldPath.Child("name"), err))
		} else if !ok {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("name"), ref.Name,
				fmt.Sprintf("service pricing %q is a customer's own price, and it isn't one of billing account %q's contract fees", ref.Name, account.Name)))
		}
	}
	return allErrs
}

// accountContractHasPricing reports whether the account's Offer, through
// its BillingEntitlement, references the named pricing.
func accountContractHasPricing(ctx context.Context, c client.Reader, account *billingv1alpha1.BillingAccount,
	pricingNamespace, pricingName string) (bool, error) {
	var entitlements billingv1alpha1.BillingEntitlementList
	if err := c.List(ctx, &entitlements, client.InNamespace(account.Namespace)); err != nil {
		return false, fmt.Errorf("listing billing entitlements: %w", err)
	}
	for _, ent := range entitlements.Items {
		if ent.Spec.BillingAccountRef.Name != account.Name || !ent.DeletionTimestamp.IsZero() {
			continue
		}
		var offer billingv1alpha1.Offer
		if err := c.Get(ctx, types.NamespacedName{Name: ent.Spec.OfferRef.Name}, &offer); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("reading offer %q: %w", ent.Spec.OfferRef.Name, err)
		}
		for _, ref := range offer.Spec.ServicePricingRefs {
			refNS := ref.Namespace
			if refNS == "" {
				refNS = defaultPricingNamespace
			}
			if ref.Name == pricingName && refNS == pricingNamespace {
				return true, nil
			}
		}
	}
	return false, nil
}

// validateChargePeriod checks a charge's service period: set (defaulting
// fills it), real dates, end on or after start, and within the bounds.
func validateChargePeriod(period *billingv1alpha1.ServicePeriod, now time.Time, fldPath *field.Path) field.ErrorList {
	if period == nil {
		return field.ErrorList{field.Required(fldPath, "a service period is required")}
	}
	var allErrs field.ErrorList
	start, err := time.Parse(ChargeDateLayout, period.Start)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("start"), period.Start, "must be a date, YYYY-MM-DD"))
	}
	end, err2 := time.Parse(ChargeDateLayout, period.End)
	if err2 != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("end"), period.End, "must be a date, YYYY-MM-DD"))
	}
	if err != nil || err2 != nil {
		return allErrs
	}

	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case end.Before(start):
		allErrs = append(allErrs, field.Invalid(fldPath.Child("end"), period.End, "the service period can't end before it starts"))
	case end.After(start.AddDate(chargePeriodMaxYearsLong, 0, 0)):
		allErrs = append(allErrs, field.Invalid(fldPath.Child("end"), period.End,
			fmt.Sprintf("the service period can't be longer than %d years", chargePeriodMaxYearsLong)))
	}
	if start.Before(today.AddDate(-chargePeriodMaxYearsBack, 0, 0)) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("start"), period.Start,
			fmt.Sprintf("the service period can't start more than %d years ago", chargePeriodMaxYearsBack)))
	}
	if end.After(today.AddDate(chargePeriodMaxYearsAhead, 0, 0)) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("end"), period.End,
			fmt.Sprintf("the service period can't end more than %d years from now", chargePeriodMaxYearsAhead)))
	}
	return allErrs
}

// ValidateChargeUpdate refuses any change to a charge's spec. CEL refuses
// it too; this repeats it for defense in depth.
func ValidateChargeUpdate(oldCharge, newCharge *billingv1alpha1.Charge) field.ErrorList {
	if !reflect.DeepEqual(oldCharge.Spec, newCharge.Spec) {
		return field.ErrorList{field.Forbidden(field.NewPath("spec"),
			"a charge can't be changed; remove it and add another")}
	}
	return nil
}

// ValidateChargeDelete refuses to delete an invoiced charge: it's the
// record of what was billed, and a mistake on an issued invoice is
// corrected with a credit note. A charge that isn't invoiced can be
// removed; the billing provider takes it off the invoice first.
//
// Deletion is always let through when the namespace is terminating, or
// the billing account is being deleted or already gone, so an account or
// organization can always be removed.
func ValidateChargeDelete(ctx context.Context, c client.Reader, charge *billingv1alpha1.Charge) field.ErrorList {
	invoiced := charge.Status.Phase == billingv1alpha1.ChargePhaseInvoiced
	if !invoiced && !ChargeOnInvoice(charge) {
		return nil
	}
	if c != nil && charge.Namespace != "" {
		var ns corev1.Namespace
		if err := c.Get(ctx, client.ObjectKey{Name: charge.Namespace}, &ns); err == nil && !ns.DeletionTimestamp.IsZero() {
			return nil
		}
	}
	if c != nil && charge.Spec.BillingAccountRef.Name != "" {
		var account billingv1alpha1.BillingAccount
		key := client.ObjectKey{Namespace: charge.Namespace, Name: charge.Spec.BillingAccountRef.Name}
		if err := c.Get(ctx, key, &account); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return field.ErrorList{field.InternalError(field.NewPath("spec", "billingAccountRef", "name"),
				fmt.Errorf("reading billing account: %w", err))}
		}
		for _, ref := range charge.GetOwnerReferences() {
			if ref.Kind == "BillingAccount" && ref.UID != "" && ref.UID != account.UID {
				return nil
			}
		}
		if !account.DeletionTimestamp.IsZero() {
			return nil
		}
	}
	if invoiced {
		return field.ErrorList{field.Forbidden(field.NewPath("metadata"),
			"an invoiced charge can't be removed: it's the record of what was billed; correct the invoice with a credit note")}
	}
	return field.ErrorList{field.Forbidden(field.NewPath("metadata"),
		"the charge is already on its invoice in the billing provider, so it can't be removed; once the invoice is issued, correct it with a credit note")}
}

// ChargeOnInvoice reports whether the billing provider has put the charge
// on an invoice, or has started to (ChargeConditionOnInvoice is True).
func ChargeOnInvoice(charge *billingv1alpha1.Charge) bool {
	return apimeta.IsStatusConditionTrue(charge.Status.Conditions, billingv1alpha1.ChargeConditionOnInvoice)
}
