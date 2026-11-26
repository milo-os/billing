// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// MaxContractLeadTime is how far ahead a contract may start when it's
// set.
const MaxContractLeadTime = 366 * 24 * time.Hour

// ValidateBillingEntitlementCreate validates a BillingEntitlement on creation.
func ValidateBillingEntitlementCreate(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
	now time.Time,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateBillingEntitlementOfferRef(ctx, c, be)...)
	allErrs = append(allErrs, validateSingleBillingEntitlementPerAccount(ctx, c, be)...)
	allErrs = append(allErrs, validateBillingContract(be.Spec.Contract, now)...)
	allErrs = append(allErrs, validateContractTerm(ctx, c, nil, be, now)...)

	return allErrs
}

// ValidateBillingEntitlementUpdate validates a BillingEntitlement on update.
// Changing offerRef is allowed (staff switch); uniqueness and GA checks still apply.
// A contract may be added, which moves an existing account onto one, and
// is then checked like a new one. It can't be removed or moved: CEL rules
// on the CRD enforce it, and they're repeated here for defence in depth.
// Once a contract has started, a change of Offer amends it, and the new
// Offer can't charge one-time fees (see validateContractAmendment).
func ValidateBillingEntitlementUpdate(
	ctx context.Context,
	c client.Client,
	oldBE, newBE *billingv1alpha1.BillingEntitlement,
	now time.Time,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateBillingEntitlementOfferRef(ctx, c, newBE)...)
	allErrs = append(allErrs, validateSingleBillingEntitlementPerAccount(ctx, c, newBE)...)

	oldContract, newContract := oldBE.Spec.Contract, newBE.Spec.Contract
	switch {
	case oldContract == nil:
		allErrs = append(allErrs, validateBillingContract(newContract, now)...)
	case newContract == nil:
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "contract"),
			"a contract can't be removed"))
	case !oldContract.StartsAt.Equal(&newContract.StartsAt):
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "contract", "startsAt"),
			"contract.startsAt is immutable"))
	case oldBE.Spec.OfferRef.Name != newBE.Spec.OfferRef.Name && !newContract.StartsAt.After(now):
		allErrs = append(allErrs, validateContractAmendment(ctx, c, newBE)...)
	}
	if newContract != nil {
		allErrs = append(allErrs, validateContractTerm(ctx, c, oldBE, newBE, now)...)
	}

	return allErrs
}

// validateContractAmendment checks the Offer a started contract is amended
// to charges no one-time fees. Its one-time fees were charged when it
// started, and billing providers charge an Offer's one-time fees whenever
// an account moves onto it (OpenMeter does when the amendment's
// subscription starts), so the amendment would charge them again. The
// staff portal leaves them out of an amendment for the same reason. A
// missing or unreadable Offer is reported by validateBillingEntitlementOfferRef.
func validateContractAmendment(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var offer billingv1alpha1.Offer
	if err := c.Get(ctx, types.NamespacedName{Name: be.Spec.OfferRef.Name}, &offer); err != nil {
		return nil
	}
	var oneTime []string
	for _, sp := range offer.Spec.ServicePricings {
		if sp.Spec.ChargeType == billingv1alpha1.ChargeTypeOneTime {
			oneTime = append(oneTime, sp.Name)
		}
	}
	if len(oneTime) == 0 {
		return nil
	}
	return field.ErrorList{field.Forbidden(field.NewPath("spec", "offerRef", "name"), fmt.Sprintf(
		"the contract has started, so offer %q can't charge one-time fees (%s): they'd be charged again when the amendment takes effect",
		offer.Name, strings.Join(oneTime, ", ")))}
}

// validateBillingContract checks a new contract starts at midnight UTC,
// today or later, and no more than MaxContractLeadTime after now. Billing
// providers can't start charging retroactively (OpenMeter refuses to start
// a subscription in the past), so a contract can't start before today.
func validateBillingContract(contract *billingv1alpha1.BillingContract, now time.Time) field.ErrorList {
	if contract == nil {
		return nil
	}
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "contract", "startsAt")
	startsAt := contract.StartsAt.UTC()

	if startsAt.IsZero() {
		return append(allErrs, field.Required(fldPath, "contract.startsAt is required"))
	}
	if !startsAt.Equal(startsAt.Truncate(24 * time.Hour)) {
		allErrs = append(allErrs, field.Invalid(fldPath, contract.StartsAt.Format(time.RFC3339),
			"must be midnight UTC, since monthly charges bill on the contract's start day"))
	}
	if startsAt.Before(now.UTC().Truncate(24 * time.Hour)) {
		allErrs = append(allErrs, field.Invalid(fldPath, contract.StartsAt.Format(time.RFC3339),
			"must be today or later: charges can't start in the past"))
	}
	if startsAt.After(now.Add(MaxContractLeadTime)) {
		allErrs = append(allErrs, field.Invalid(fldPath, contract.StartsAt.Format(time.RFC3339),
			"must be no more than a year ahead"))
	}
	return allErrs
}

func validateBillingEntitlementOfferRef(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "offerRef", "name")

	if be.Spec.OfferRef.Name == "" {
		allErrs = append(allErrs, field.Required(fldPath, "offerRef.name is required"))
		return allErrs
	}

	var offer billingv1alpha1.Offer
	if err := c.Get(ctx, types.NamespacedName{Name: be.Spec.OfferRef.Name}, &offer); err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.NotFound(fldPath, be.Spec.OfferRef.Name))
		} else {
			allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("reading offer: %w", err)))
		}
		return allErrs
	}

	if offer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			be.Spec.OfferRef.Name,
			fmt.Sprintf("offer %q has launchStage %q; only GA Offers may be assigned", offer.Name, offer.Spec.LaunchStage),
		))
		return allErrs
	}

	if len(offer.Spec.ServicePricings) == 0 {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			be.Spec.OfferRef.Name,
			fmt.Sprintf("offer %q is GA but has no servicePricings snapshot yet; wait for the Offer controller to publish rates", offer.Name),
		))
	}

	return allErrs
}

// validateSingleBillingEntitlementPerAccount enforces at most one non-deleting
// BillingEntitlement per billingAccountRef in the same namespace.
func validateSingleBillingEntitlementPerAccount(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "billingAccountRef", "name")

	if be.Spec.BillingAccountRef.Name == "" {
		return allErrs
	}

	var list billingv1alpha1.BillingEntitlementList
	if err := c.List(ctx, &list, client.InNamespace(be.Namespace)); err != nil {
		allErrs = append(allErrs, field.InternalError(fldPath,
			fmt.Errorf("listing billing entitlements: %w", err)))
		return allErrs
	}

	for i := range list.Items {
		existing := &list.Items[i]
		if existing.UID == be.UID {
			continue
		}
		if !existing.DeletionTimestamp.IsZero() {
			continue
		}
		if existing.Spec.BillingAccountRef.Name != be.Spec.BillingAccountRef.Name {
			continue
		}
		allErrs = append(allErrs, field.Forbidden(
			fldPath,
			fmt.Sprintf("billing account %q already has billing entitlement %q",
				be.Spec.BillingAccountRef.Name, existing.Name),
		))
		break
	}

	return allErrs
}

// validateContractTerm checks a contract's term when it, or the Offer the
// contract bills, changes. oldBE is nil on create. See
// docs/enhancements/contract-renewals.md.
func validateContractTerm(
	ctx context.Context,
	c client.Client,
	oldBE, newBE *billingv1alpha1.BillingEntitlement,
	now time.Time,
) field.ErrorList {
	contract := newBE.Spec.Contract
	if contract == nil {
		return nil
	}
	termPath := field.NewPath("spec", "contract", "term")
	invoiceDay, err := entitlementInvoiceDay(ctx, c, newBE)
	if err != nil {
		return field.ErrorList{field.InternalError(termPath, err)}
	}

	var oldTerm *billingv1alpha1.ContractTerm
	if oldBE != nil && oldBE.Spec.Contract != nil {
		oldContract := oldBE.Spec.Contract
		oldTerm = oldContract.Term
		if ContractStopped(oldContract, invoiceDay, now) {
			return validateStoppedContractTerm(ctx, c, oldBE, newBE, ContractStopDate(oldContract, invoiceDay), termPath)
		}
	}

	term := contract.Term
	termChanged := oldBE == nil || !apiequality.Semantic.DeepEqual(oldTerm, term)
	offerChanged := oldBE == nil || oldBE.Spec.OfferRef.Name != newBE.Spec.OfferRef.Name

	var allErrs field.ErrorList
	if oldBE != nil && oldBE.Spec.Contract != nil && termChanged {
		oldStop, newStop := ContractStopDate(oldBE.Spec.Contract, invoiceDay), ContractStopDate(contract, invoiceDay)
		if !oldStop.Equal(newStop) {
			allErrs = append(allErrs, validateTiedTermsStillFit(ctx, c, newBE, oldStop, newStop, termPath)...)
		}
	}
	if term == nil || (!termChanged && !offerChanged) {
		return allErrs
	}

	atEnd := ContractTermAtEnd(term)
	termEnd := ContractTermEndsAt(contract, invoiceDay)
	termEnded := !now.Before(termEnd)

	// A change to the term's length must leave the current term running:
	// one shortened into the past would end or stop it at once, over
	// months already billed. A term that renews always has a current one.
	lengthChanged := oldTerm == nil || oldTerm.Months != term.Months ||
		(atEnd == billingv1alpha1.ContractTermAtEndRenew &&
			(ContractTermAtEnd(oldTerm) != atEnd || ContractRenewMonths(oldTerm) != ContractRenewMonths(term)))
	if state, _ := ContractTermAt(contract, invoiceDay, now); lengthChanged && state.Phase != billingv1alpha1.ContractTermPhaseInTerm {
		allErrs = append(allErrs, field.Invalid(termPath.Child("months"), term.Months, fmt.Sprintf(
			"the term would end on %s: it must end on or after the contract's next billing date, %s",
			formatDate(termEnd), formatDate(NextContractBillingDate(contract, invoiceDay, now)))))
	}

	if atEnd == billingv1alpha1.ContractTermAtEndStop {
		allErrs = append(allErrs, validateContractStopsAt(contract, invoiceDay, termEnd, termEnded, now, termPath.Child("stopsAt"))...)
		allErrs = append(allErrs, validateOfferAfterRef(ctx, c, term.OfferAfterRef, termPath.Child("offerAfterRef"))...)
	}

	var offer billingv1alpha1.Offer
	if err := c.Get(ctx, types.NamespacedName{Name: newBE.Spec.OfferRef.Name}, &offer); err != nil {
		// A missing or unreadable Offer is reported by
		// validateBillingEntitlementOfferRef.
		return allErrs
	}
	allErrs = append(allErrs, validateTermFitsFees(contract, invoiceDay, offer.Spec.ServicePricings, termPath)...)
	return allErrs
}

// validateStoppedContractTerm checks a change to a contract that has
// stopped. Its term is frozen, except offerAfterRef until billing has moved
// the account onto it: if that Offer was retired since the term was set,
// staff point it at another, and billing moves the account there. Billing
// moves offerRef at the stop. Until then offerRef only changes to
// offerAfterRef; after, staff may change it like any account's.
func validateStoppedContractTerm(ctx context.Context, c client.Client, oldBE, newBE *billingv1alpha1.BillingEntitlement,
	stoppedAt time.Time, termPath *field.Path) field.ErrorList {
	oldTerm, newTerm := oldBE.Spec.Contract.Term, newBE.Spec.Contract.Term
	_, moved := oldBE.Annotations[billingv1alpha1.BillingEntitlementContractStoppedAtAnnotation]
	var allErrs field.ErrorList
	// Until billing has moved the account, offerRef only moves to
	// offerAfterRef, which is billing's own change: a hand-picked Offer
	// would be overwritten when billing moves the account.
	if !moved && newTerm != nil && newTerm.OfferAfterRef != nil &&
		newBE.Spec.OfferRef.Name != oldBE.Spec.OfferRef.Name && newBE.Spec.OfferRef.Name != newTerm.OfferAfterRef.Name {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "offerRef", "name"), fmt.Sprintf(
			"the contract stopped on %s and billing hasn't moved the account yet: set spec.contract.term.offerAfterRef to the Offer it moves onto instead",
			formatDate(stoppedAt))))
	}
	if newTerm != nil && !moved {
		oldRest, newRest := oldTerm.DeepCopy(), newTerm.DeepCopy()
		oldRest.OfferAfterRef, newRest.OfferAfterRef = nil, nil
		if apiequality.Semantic.DeepEqual(oldRest, newRest) {
			if apiequality.Semantic.DeepEqual(oldTerm.OfferAfterRef, newTerm.OfferAfterRef) {
				return allErrs
			}
			return append(allErrs, validateOfferAfterRef(ctx, c, newTerm.OfferAfterRef, termPath.Child("offerAfterRef"))...)
		}
	}
	if apiequality.Semantic.DeepEqual(oldTerm, newTerm) {
		return allErrs
	}
	msg := fmt.Sprintf("the contract stopped on %s, so its term can't change", formatDate(stoppedAt))
	if !moved {
		msg += ", except offerAfterRef until the account has moved onto it"
	}
	return append(allErrs, field.Forbidden(termPath, msg))
}

// validateContractStopsAt checks when a contract set to stop stops. A
// contract in its term stops at the term's end. Once the term has ended,
// stopsAt says when: a billing date on or after the next one, ending the
// period of every quarterly and annual fee billed in advance, so nothing
// is charged in advance for time after the stop.
func validateContractStopsAt(contract *billingv1alpha1.BillingContract, invoiceDay int32,
	termEnd time.Time, termEnded bool, now time.Time, fldPath *field.Path) field.ErrorList {
	stopsAt := contract.Term.StopsAt
	switch {
	case stopsAt == nil && termEnded:
		return field.ErrorList{field.Required(fldPath, fmt.Sprintf(
			"the term ended on %s, so say when the contract stops", formatDate(termEnd)))}
	case stopsAt == nil:
		return nil
	case !termEnded:
		return field.ErrorList{field.Forbidden(fldPath, fmt.Sprintf(
			"only allowed once the term has ended: the contract stops at the term's end, %s", formatDate(termEnd)))}
	}

	t := stopsAt.UTC()
	value := stopsAt.Format(time.RFC3339)
	if !t.Equal(t.Truncate(24 * time.Hour)) {
		return field.ErrorList{field.Invalid(fldPath, value, "must be midnight UTC")}
	}
	next := NextContractBillingDate(contract, invoiceDay, now)
	if t.Before(next) || !IsContractBillingDate(contract, invoiceDay, billingv1alpha1.ChargeIntervalMonthly, t) {
		return field.ErrorList{field.Invalid(fldPath, value, fmt.Sprintf(
			"must be one of the contract's billing dates, on or after the next one, %s", formatDate(next)))}
	}
	return nil
}

// validateStopsAtFitsFees checks stopsAt ends the period of every
// quarterly and annual fee billed in advance. Fees billed in arrears are
// charged for the part period up to the stop.
func validateStopsAtFitsFees(contract *billingv1alpha1.BillingContract, invoiceDay int32,
	pricings []billingv1alpha1.ServicePricingSnapshot, fldPath *field.Path) field.ErrorList {
	stopsAt := contract.Term.StopsAt
	if stopsAt == nil {
		return nil
	}
	var allErrs field.ErrorList
	for _, sp := range pricings {
		if sp.Spec.ChargeType != billingv1alpha1.ChargeTypeRecurring ||
			IntervalMonths(sp.Spec.Interval) == 1 ||
			SnapshotBillingTiming(sp) != billingv1alpha1.BillingTimingInAdvance {
			continue
		}
		if !IsContractBillingDate(contract, invoiceDay, sp.Spec.Interval, stopsAt.Time) {
			allErrs = append(allErrs, field.Invalid(fldPath, stopsAt.Format(time.RFC3339), fmt.Sprintf(
				"%s is billed %s in advance, so the contract can only stop at the end of one of its periods",
				sp.Name, sp.Spec.Interval)))
		}
	}
	return allErrs
}

// validateTermFitsFees checks the term against the recurring fees in the
// Offer the contract bills. The term, and each renewal, must end on a
// billing date of every fee, so it doesn't end partway through a period
// paid for in advance and an annual fee renews on the contract's day. A
// contract set to stop can't have a fee that ends after it does.
func validateTermFitsFees(contract *billingv1alpha1.BillingContract, invoiceDay int32,
	pricings []billingv1alpha1.ServicePricingSnapshot, termPath *field.Path) field.ErrorList {
	term := contract.Term
	atEnd := ContractTermAtEnd(term)
	type termEnd struct {
		path  *field.Path
		value int32
		at    time.Time
	}
	ends := []termEnd{{termPath.Child("months"), term.Months, ContractTermEndsAt(contract, invoiceDay)}}
	if atEnd == billingv1alpha1.ContractTermAtEndRenew {
		renewMonths := ContractRenewMonths(term)
		ends = append(ends, termEnd{termPath.Child("renewMonths"), renewMonths,
			contractTermEndAfter(contract, invoiceDay, term.Months+renewMonths)})
	}
	stopDate := ContractStopDate(contract, invoiceDay)

	var allErrs field.ErrorList
	for _, sp := range pricings {
		if sp.Spec.ChargeType != billingv1alpha1.ChargeTypeRecurring {
			continue
		}
		if IntervalMonths(sp.Spec.Interval) > 1 {
			for _, end := range ends {
				if !IsContractBillingDate(contract, invoiceDay, sp.Spec.Interval, end.at) {
					allErrs = append(allErrs, field.Invalid(end.path, end.value, fmt.Sprintf(
						"%s is billed %s, so the term must end at the end of one of its periods: a multiple of %d months",
						sp.Name, sp.Spec.Interval, IntervalMonths(sp.Spec.Interval))))
				}
			}
		}
		if !stopDate.IsZero() && sp.Periods > 0 {
			if feeEnd := ContractFeeEndsAt(contract, invoiceDay, sp.Spec.Interval, sp.Periods); feeEnd.After(stopDate) {
				allErrs = append(allErrs, field.Invalid(termPath.Child("atEnd"), string(atEnd), fmt.Sprintf(
					"%s runs until %s, after the contract stops on %s", sp.Name, formatDate(feeEnd), formatDate(stopDate))))
			}
		}
	}
	allErrs = append(allErrs, validateStopsAtFitsFees(contract, invoiceDay, pricings, termPath.Child("stopsAt"))...)
	return allErrs
}

// validateOfferAfterRef checks the Offer a contract set to stop moves the
// account onto (see OfferAfterRefProblem).
func validateOfferAfterRef(ctx context.Context, c client.Client, ref *billingv1alpha1.OfferReference,
	fldPath *field.Path) field.ErrorList {
	if ref == nil || ref.Name == "" {
		return field.ErrorList{field.Required(fldPath, "required when atEnd is Stop")}
	}
	namePath := fldPath.Child("name")
	problem, err := OfferAfterRefProblem(ctx, c, ref.Name)
	switch {
	case err != nil:
		return field.ErrorList{field.InternalError(namePath, err)}
	case problem != "":
		return field.ErrorList{field.Invalid(namePath, ref.Name, problem)}
	}
	return nil
}

// OfferAfterRefProblem says why an account can't be moved onto the Offer
// its contract stops to, or returns "" when it can. The Offer must exist,
// be published, and charge no one-time fees: the account has already paid
// the contract's. The webhook checks it when a term is set to stop, and
// billing again before the stop, since the Offer may change in between.
func OfferAfterRefProblem(ctx context.Context, c client.Client, name string) (string, error) {
	var offer billingv1alpha1.Offer
	if err := c.Get(ctx, types.NamespacedName{Name: name}, &offer); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Sprintf("offer %q doesn't exist", name), nil
		}
		return "", fmt.Errorf("reading offer %q: %w", name, err)
	}
	if !OfferIsAssignable(&offer) {
		return fmt.Sprintf("offer %q isn't published: only GA Offers with published rates may be assigned", name), nil
	}
	var oneTime []string
	for _, sp := range offer.Spec.ServicePricings {
		if sp.Spec.ChargeType == billingv1alpha1.ChargeTypeOneTime {
			oneTime = append(oneTime, sp.Name)
		}
	}
	if len(oneTime) > 0 {
		return fmt.Sprintf("offer %q charges one-time fees (%s), which the account would be charged when the contract stops",
			name, strings.Join(oneTime, ", ")), nil
	}
	return "", nil
}

// entitlementInvoiceDay returns the invoice day of the account an
// entitlement is for, which a contract billed on the invoice day counts
// its term from. An account that doesn't exist yet has the standard one.
func entitlementInvoiceDay(ctx context.Context, c client.Client, be *billingv1alpha1.BillingEntitlement) (int32, error) {
	var account billingv1alpha1.BillingAccount
	err := c.Get(ctx, types.NamespacedName{Namespace: be.Namespace, Name: be.Spec.BillingAccountRef.Name}, &account)
	switch {
	case apierrors.IsNotFound(err):
		return AccountInvoiceDay(nil), nil
	case err != nil:
		return 0, fmt.Errorf("reading billing account: %w", err)
	}
	return AccountInvoiceDay(&account), nil
}

// formatDate formats t as "18 April 2029".
func formatDate(t time.Time) string {
	return t.UTC().Format("2 January 2006")
}

// validateTiedTermsStillFit checks that moving a contract's stop date from
// oldStop to newStop (the zero time for none) doesn't make the account's
// payment terms overlap. Terms tied to the contract end at its stop, so
// staff may have scheduled other terms from then; moving the stop later,
// or no longer stopping, would leave both in effect at once.
func validateTiedTermsStillFit(ctx context.Context, c client.Reader, be *billingv1alpha1.BillingEntitlement,
	oldStop, newStop time.Time, termPath *field.Path) field.ErrorList {
	var list billingv1alpha1.BillingArrangementList
	if err := c.List(ctx, &list, client.InNamespace(be.Namespace)); err != nil {
		return field.ErrorList{field.InternalError(termPath, fmt.Errorf("listing billing arrangements: %w", err))}
	}
	var arrs []*billingv1alpha1.BillingArrangement
	for i := range list.Items {
		arr := &list.Items[i]
		if arr.DeletionTimestamp.IsZero() && arr.Spec.BillingAccountRef.Name == be.Spec.BillingAccountRef.Name {
			arrs = append(arrs, arr)
		}
	}
	overlap := func(a, b *billingv1alpha1.BillingArrangement, stop time.Time) bool {
		aStart, bStart := ArrangementStart(a, a.CreationTimestamp.Time), ArrangementStart(b, b.CreationTimestamp.Time)
		aEnd, bEnd := ArrangementEnd(a, stop), ArrangementEnd(b, stop)
		return !windowEmpty(aStart, aEnd) && !windowEmpty(bStart, bEnd) && windowsOverlap(aStart, aEnd, bStart, bEnd)
	}
	for _, tied := range arrs {
		if !tied.Spec.EndsWithContract {
			continue
		}
		for _, other := range arrs {
			if other == tied || !overlap(tied, other, newStop) || overlap(tied, other, oldStop) {
				continue
			}
			return field.ErrorList{field.Forbidden(termPath, fmt.Sprintf(
				"payment terms %q end with the contract, so they'd overlap payment terms %q, which start at %s: withdraw %q, or set an end date on %q first",
				tied.Name, other.Name, ArrangementStart(other, other.CreationTimestamp.Time).UTC().Format(time.RFC3339), other.Name, tied.Name))}
		}
	}
	return nil
}
