// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// Limits on a single UsageQuery. They bound how much work one request can ask
// of the metering backend: every meter × grouping is a backend query.
const (
	MaxUsageQueryBillingAccounts = 50
	MaxUsageQueryProjects        = 50
	MaxUsageQueryMeters          = 100
	MaxUsageQueryGroupings       = 10
	MaxUsageQueryGroupByPerGroup = 3
	MaxUsageDimensionLength      = 64

	// MaxHourlyRange caps hourly queries at 31 days (744 buckets per series).
	MaxHourlyRange = 31 * 24 * time.Hour
	// MaxDailyRange caps daily queries at 400 days, a little over a year.
	MaxDailyRange = 400 * 24 * time.Hour
	// MaxRange caps monthly queries and totals-only queries at about 5 years.
	MaxRange = 5 * 366 * 24 * time.Hour
)

// DefaultTimeZone is the time zone used when spec.timeZone is empty.
const DefaultTimeZone = "UTC"

// SetDefaults fills in the defaults of a UsageQuery's spec: UTC, and a single
// grouping with no breakdown (the meter's total) when no grouping is asked
// for.
func (q *UsageQuery) SetDefaults() {
	if q.Spec.TimeZone == "" {
		q.Spec.TimeZone = DefaultTimeZone
	}
	if len(q.Spec.Groupings) == 0 {
		q.Spec.Groupings = []UsageGrouping{{}}
	}
}

// Validate checks a UsageQuery's spec on its own. It does not know the
// caller's scope or which meters and dimensions exist; the server checks
// those against the request's parent context and the MeterDefinitions.
func (q *UsageQuery) Validate() field.ErrorList {
	return validateSpec(&q.Spec, field.NewPath("spec"))
}

func validateSpec(spec *UsageQuerySpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	errs = append(errs, validateTimeRange(spec, path)...)

	if spec.TimeZone != "" {
		if _, err := time.LoadLocation(spec.TimeZone); err != nil {
			errs = append(errs, field.Invalid(path.Child("timeZone"), spec.TimeZone, "must be an IANA time zone name, such as UTC or Europe/Madrid"))
		}
	}

	errs = append(errs, validateBillingAccounts(spec.BillingAccounts, path.Child("billingAccounts"))...)
	errs = append(errs, validateNames(spec.Projects, MaxUsageQueryProjects, path.Child("projects"))...)
	errs = append(errs, validateNames(spec.Meters, MaxUsageQueryMeters, path.Child("meters"))...)
	errs = append(errs, validateGroupings(spec.Groupings, path.Child("groupings"))...)

	return errs
}

func validateTimeRange(spec *UsageQuerySpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	if spec.StartTime.IsZero() {
		errs = append(errs, field.Required(path.Child("startTime"), ""))
	}
	if spec.EndTime.IsZero() {
		errs = append(errs, field.Required(path.Child("endTime"), ""))
	}

	maxRange := MaxRange
	switch spec.Granularity {
	case "", UsageGranularityMonth:
	case UsageGranularityDay:
		maxRange = MaxDailyRange
	case UsageGranularityHour:
		maxRange = MaxHourlyRange
	default:
		errs = append(errs, field.NotSupported(path.Child("granularity"), spec.Granularity,
			[]UsageGranularity{UsageGranularityHour, UsageGranularityDay, UsageGranularityMonth}))
	}

	if len(errs) > 0 {
		return errs
	}

	span := spec.EndTime.Sub(spec.StartTime.Time)
	switch {
	case span <= 0:
		errs = append(errs, field.Invalid(path.Child("endTime"), spec.EndTime, "must be after startTime"))
	case span > maxRange:
		errs = append(errs, field.Invalid(path.Child("endTime"), spec.EndTime,
			fmt.Sprintf("time range must be at most %s for this granularity", formatRange(maxRange))))
	}

	return errs
}

func formatRange(d time.Duration) string {
	return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
}

func validateBillingAccounts(refs []BillingAccountReference, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	if len(refs) > MaxUsageQueryBillingAccounts {
		errs = append(errs, field.TooMany(path, len(refs), MaxUsageQueryBillingAccounts))
	}

	seen := map[BillingAccountReference]bool{}
	for i, ref := range refs {
		p := path.Index(i)
		if ref.Name == "" {
			errs = append(errs, field.Required(p.Child("name"), ""))
		} else {
			for _, msg := range validation.IsDNS1123Subdomain(ref.Name) {
				errs = append(errs, field.Invalid(p.Child("name"), ref.Name, msg))
			}
		}
		if ref.Namespace != "" {
			for _, msg := range validation.IsDNS1123Label(ref.Namespace) {
				errs = append(errs, field.Invalid(p.Child("namespace"), ref.Namespace, msg))
			}
		}
		if seen[ref] {
			errs = append(errs, field.Duplicate(p, ref))
		}
		seen[ref] = true
	}

	return errs
}

// validateNames checks a list of object names: projects or MeterDefinitions.
func validateNames(names []string, maxItems int, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	if len(names) > maxItems {
		errs = append(errs, field.TooMany(path, len(names), maxItems))
	}

	seen := map[string]bool{}
	for i, name := range names {
		p := path.Index(i)
		if name == "" {
			errs = append(errs, field.Required(p, ""))
			continue
		}
		for _, msg := range validation.IsDNS1123Subdomain(name) {
			errs = append(errs, field.Invalid(p, name, msg))
		}
		if seen[name] {
			errs = append(errs, field.Duplicate(p, name))
		}
		seen[name] = true
	}

	return errs
}

func validateGroupings(groupings []UsageGrouping, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	if len(groupings) > MaxUsageQueryGroupings {
		errs = append(errs, field.TooMany(path, len(groupings), MaxUsageQueryGroupings))
	}

	var seen [][]string
	for i, g := range groupings {
		p := path.Index(i).Child("groupBy")

		if len(g.GroupBy) > MaxUsageQueryGroupByPerGroup {
			errs = append(errs, field.TooMany(p, len(g.GroupBy), MaxUsageQueryGroupByPerGroup))
		}

		dims := map[string]bool{}
		for j, dim := range g.GroupBy {
			switch {
			case dim == "":
				errs = append(errs, field.Required(p.Index(j), ""))
			case len(dim) > MaxUsageDimensionLength:
				errs = append(errs, field.TooLong(p.Index(j), dim, MaxUsageDimensionLength))
			case dims[dim]:
				errs = append(errs, field.Duplicate(p.Index(j), dim))
			}
			dims[dim] = true
		}

		// The same dimensions in another order are the same breakdown.
		key := slices.Sorted(slices.Values(g.GroupBy))
		if slices.ContainsFunc(seen, func(s []string) bool { return slices.Equal(s, key) }) {
			errs = append(errs, field.Duplicate(path.Index(i), strings.Join(g.GroupBy, ",")))
		}
		seen = append(seen, key)
	}

	return errs
}
