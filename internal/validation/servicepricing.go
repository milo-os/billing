// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateServicePricingCreate validates a ServicePricing on creation.
func ValidateServicePricingCreate(sp *billingv1alpha1.ServicePricing) field.ErrorList {
	return validateServicePricingSpec(&sp.Spec, field.NewPath("spec"))
}

// ValidateServicePricingUpdate validates a ServicePricing on update.
func ValidateServicePricingUpdate(_, newSP *billingv1alpha1.ServicePricing) field.ErrorList {
	return validateServicePricingSpec(&newSP.Spec, field.NewPath("spec"))
}

func validateServicePricingSpec(spec *billingv1alpha1.ServicePricingSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if spec.Currency != "" && spec.Currency != "USD" {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("currency"),
			spec.Currency,
			"currency must be USD",
		))
	}

	switch spec.ChargeType {
	case billingv1alpha1.ChargeTypeUsage:
		if spec.Metric == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("metric"), "metric is required when chargeType is Usage"))
		}
		if spec.PricingUnit == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("pricingUnit"), "pricingUnit is required when chargeType is Usage"))
		}
		if len(spec.Rates) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("rates"), "rates is required when chargeType is Usage"))
		}
		allErrs = append(allErrs, validatePricingRates(spec.Rates, fldPath.Child("rates"))...)
	case billingv1alpha1.ChargeTypeOneTime:
		if spec.Amount == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("amount"), "amount is required when chargeType is OneTime"))
		}
		if spec.Trigger == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("trigger"), "trigger is required when chargeType is OneTime"))
		}
	case billingv1alpha1.ChargeTypeRecurring:
		if spec.Amount == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("amount"), "amount is required when chargeType is Recurring"))
		}
		if spec.Interval == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("interval"), "interval is required when chargeType is Recurring"))
		}
	case "":
		allErrs = append(allErrs, field.Required(fldPath.Child("chargeType"), "chargeType is required"))
	default:
		allErrs = append(allErrs, field.NotSupported(
			fldPath.Child("chargeType"),
			spec.ChargeType,
			[]string{
				string(billingv1alpha1.ChargeTypeUsage),
				string(billingv1alpha1.ChargeTypeOneTime),
				string(billingv1alpha1.ChargeTypeRecurring),
			},
		))
	}

	return allErrs
}

func validatePricingRates(rates []billingv1alpha1.PricingRate, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	for i := range rates {
		allErrs = append(allErrs, validatePricingRate(&rates[i], fldPath.Index(i))...)
	}
	return allErrs
}

// validatePricingRate enforces exactly one of flat or tiered, and tier band shape.
func validatePricingRate(rate *billingv1alpha1.PricingRate, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	hasFlat := rate.Flat != ""
	hasTiered := len(rate.Tiered) > 0

	switch {
	case hasFlat && hasTiered:
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			rate,
			"exactly one of flat or tiered must be set",
		))
	case !hasFlat && !hasTiered:
		allErrs = append(allErrs, field.Required(
			fldPath,
			"exactly one of flat or tiered must be set",
		))
	case hasTiered:
		allErrs = append(allErrs, validatePricingTiers(rate.Tiered, fldPath.Child("tiered"))...)
	}

	return allErrs
}

// validatePricingTiers requires upTo on every band except the last, which may omit it.
func validatePricingTiers(tiers []billingv1alpha1.PricingTierBand, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if len(tiers) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, "tiered must contain at least one band"))
		return allErrs
	}

	last := len(tiers) - 1
	for i, band := range tiers {
		bandPath := fldPath.Index(i)
		if band.Rate == "" {
			allErrs = append(allErrs, field.Required(bandPath.Child("rate"), "rate is required"))
		}
		if i != last && band.UpTo == "" {
			allErrs = append(allErrs, field.Required(
				bandPath.Child("upTo"),
				fmt.Sprintf("upTo is required on all but the last tiered band (index %d)", i),
			))
		}
	}

	return allErrs
}
