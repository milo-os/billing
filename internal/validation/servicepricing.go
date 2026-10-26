// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"slices"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// serviceListGVK is the services.miloapis.com Service list. Billing reads it
// unstructured because service-catalog, which owns the type, imports billing.
var serviceListGVK = schema.GroupVersionKind{
	Group:   "services.miloapis.com",
	Version: "v1alpha1",
	Kind:    "ServiceList",
}

// ValidateServicePricingCreate validates a ServicePricing on creation. A
// Usage pricing is checked against the MeterDefinition it selects; a
// OneTime or Recurring pricing against the Service it names.
func ValidateServicePricingCreate(
	ctx context.Context,
	c client.Client,
	sp *billingv1alpha1.ServicePricing,
) field.ErrorList {
	fldPath := field.NewPath("spec")
	if allErrs := validateServicePricingSpec(&sp.Spec, fldPath); len(allErrs) > 0 {
		return allErrs
	}

	switch sp.Spec.ChargeType {
	case billingv1alpha1.ChargeTypeUsage:
		return validateServicePricingMeter(ctx, c, &sp.Spec, fldPath)
	default:
		return validateServicePricingService(ctx, c, &sp.Spec, fldPath)
	}
}

// ValidateServicePricingUpdate validates a ServicePricing on update. The
// spec can't change after create: a new price is a new ServicePricing, so a
// pricing's name always means one price to the Offers that snapshot it.
func ValidateServicePricingUpdate(oldSP, newSP *billingv1alpha1.ServicePricing) field.ErrorList {
	var allErrs field.ErrorList
	if !apiequality.Semantic.DeepEqual(oldSP.Spec, newSP.Spec) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec"),
			"ServicePricing spec is immutable; create a new ServicePricing for a new price",
		))
	}
	return allErrs
}

// validateServicePricingMeter checks a Usage pricing against the
// MeterDefinition whose spec.meterName is its metric: the meter exists and
// isn't Retired, every rate matches a dimension the meter declares, and
// serviceRef names the service that owns the meter.
func validateServicePricingMeter(
	ctx context.Context,
	c client.Client,
	spec *billingv1alpha1.ServicePricingSpec,
	fldPath *field.Path,
) field.ErrorList {
	var allErrs field.ErrorList
	metricPath := fldPath.Child("metric")

	var meters billingv1alpha1.MeterDefinitionList
	if err := c.List(ctx, &meters); err != nil {
		return append(allErrs, field.InternalError(metricPath, fmt.Errorf("listing MeterDefinitions: %w", err)))
	}
	idx := slices.IndexFunc(meters.Items, func(md billingv1alpha1.MeterDefinition) bool {
		return md.Spec.MeterName == spec.Metric
	})
	if idx < 0 {
		return append(allErrs, field.Invalid(
			metricPath,
			spec.Metric,
			"no MeterDefinition has this meterName",
		))
	}
	meter := &meters.Items[idx]

	if meter.Spec.Phase == billingv1alpha1.PhaseRetired {
		allErrs = append(allErrs, field.Invalid(
			metricPath,
			spec.Metric,
			fmt.Sprintf("MeterDefinition %q is Retired and no longer accepts usage", meter.Name),
		))
	}

	declared := meter.Spec.Measurement.Dimensions
	for i, rate := range spec.Rates {
		if rate.Match == nil {
			continue
		}
		dim := rate.Match.Dimension
		if dim == billingv1alpha1.SystemDimensionProjectName || slices.Contains(declared, dim) {
			continue
		}
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("rates").Index(i).Child("match", "dimension"),
			dim,
			fmt.Sprintf("meter %q doesn't declare this dimension (declared: %v)", spec.Metric, declared),
		))
	}

	owner, ok := meter.Labels[billingv1alpha1.MeterServiceLabel]
	switch {
	case !ok:
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("serviceRef"),
			spec.ServiceRef,
			fmt.Sprintf("MeterDefinition %q has no %s label, so its service can't be confirmed",
				meter.Name, billingv1alpha1.MeterServiceLabel),
		))
	case owner != spec.ServiceRef:
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("serviceRef"),
			spec.ServiceRef,
			fmt.Sprintf("meter %q belongs to service %q", spec.Metric, owner),
		))
	}

	return allErrs
}

// validateServicePricingService checks that a fixed charge's serviceRef
// names a Service by its spec.serviceName.
func validateServicePricingService(
	ctx context.Context,
	c client.Client,
	spec *billingv1alpha1.ServicePricingSpec,
	fldPath *field.Path,
) field.ErrorList {
	var allErrs field.ErrorList
	refPath := fldPath.Child("serviceRef")

	services := &unstructured.UnstructuredList{}
	services.SetGroupVersionKind(serviceListGVK)
	if err := c.List(ctx, services); err != nil {
		return append(allErrs, field.InternalError(refPath, fmt.Errorf("listing Services: %w", err)))
	}
	for _, svc := range services.Items {
		name, _, _ := unstructured.NestedString(svc.Object, "spec", "serviceName")
		if name == spec.ServiceRef {
			return allErrs
		}
	}
	return append(allErrs, field.Invalid(refPath, spec.ServiceRef, "no Service has this serviceName"))
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
	allErrs = append(allErrs, validatePricingRateMatches(rates, fldPath)...)
	return allErrs
}

// validatePricingRateMatches requires that matched rates share one
// dimension and match distinct values, and that at most one rate is an
// unmatched catch-all, so every usage record resolves to exactly one rate.
func validatePricingRateMatches(rates []billingv1alpha1.PricingRate, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	dimension := ""
	values := make(map[string]int, len(rates))
	catchAll := -1
	for i, rate := range rates {
		ratePath := fldPath.Index(i)
		if rate.Match == nil {
			if catchAll >= 0 {
				allErrs = append(allErrs, field.Required(
					ratePath.Child("match"),
					fmt.Sprintf("only one rate may omit match; rates[%d] already does", catchAll),
				))
				continue
			}
			catchAll = i
			continue
		}

		matchPath := ratePath.Child("match")
		if dimension == "" {
			dimension = rate.Match.Dimension
		} else if rate.Match.Dimension != dimension {
			allErrs = append(allErrs, field.Invalid(
				matchPath.Child("dimension"),
				rate.Match.Dimension,
				fmt.Sprintf("all rates must match the same dimension (%q)", dimension),
			))
			continue
		}

		if prev, ok := values[rate.Match.Value]; ok {
			allErrs = append(allErrs, field.Invalid(
				matchPath.Child("value"),
				rate.Match.Value,
				fmt.Sprintf("value is already matched by rates[%d]", prev),
			))
			continue
		}
		values[rate.Match.Value] = i
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
