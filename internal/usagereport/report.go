// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"sort"
	"strings"
	"time"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ReportOptions shapes a Report.
type ReportOptions struct {
	// By adds a breakdown by this dimension; it must have been the query's
	// second grouping.
	By string
	// Daily keeps each series' daily points; the query must have had Day
	// granularity.
	Daily bool
	// IncludeIdle keeps published meters with no usage.
	IncludeIdle bool
	// Meter keeps only the meter whose MeterDefinition name, meter name or
	// display name matches (case-insensitive).
	Meter string
}

// Money is an amount in a currency, with its display form.
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Display  string `json:"display"`
}

// Point is one day's usage.
type Point struct {
	Date    string  `json:"date"`
	Value   float64 `json:"value"`
	Display string  `json:"display"`
}

// Group is a meter's usage for one value of the breakdown dimension.
type Group struct {
	Value   string  `json:"value"`
	Total   float64 `json:"total"`
	Display string  `json:"display"`
	Daily   []Point `json:"daily,omitempty"`
}

// Breakdown is a meter's usage split by one dimension.
type Breakdown struct {
	Dimension string  `json:"dimension"`
	Groups    []Group `json:"groups"`
}

// Meter is one meter's usage over the report's period.
type Meter struct {
	Service         string `json:"service"`
	Name            string `json:"name"`
	MeterDefinition string `json:"meterDefinition"`
	MeterName       string `json:"meterName,omitempty"`
	Unit            string `json:"unit,omitempty"`
	Aggregation     string `json:"aggregation,omitempty"`
	// Total uses the meter's aggregation, so for a gauge (Latest, Max) it is
	// a reading, not a running total; Qualifier says which.
	Total     float64 `json:"total"`
	Display   string  `json:"display"`
	Qualifier string  `json:"qualifier,omitempty"`
	// Spend is unset when no plan prices the meter.
	Spend            *Money     `json:"spend,omitempty"`
	SpendUnavailable bool       `json:"spendUnavailable,omitempty"`
	Deprecated       bool       `json:"deprecated,omitempty"`
	Daily            []Point    `json:"daily,omitempty"`
	Breakdown        *Breakdown `json:"breakdown,omitempty"`
}

// Period is the time range a report covers.
type Period struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Label is the whole period, e.g. "1 Oct - 1 Nov 2026", even when End
	// stops at now.
	Label string `json:"label"`
}

// Report is usage and spend for one scope and period, the way the portal
// and the CLI present it.
type Report struct {
	Scope  string `json:"scope"`
	Period Period `json:"period"`
	// NoBillingAccount is set when an organization has no billing account,
	// so there is no usage to report.
	NoBillingAccount bool    `json:"noBillingAccount,omitempty"`
	Meters           []Meter `json:"meters"`
	// Spend is the total across meters, before taxes, credits and
	// discounts. It is left out when ReportOptions.Meter narrows the report.
	Spend            *Money `json:"spend,omitempty"`
	SpendUnavailable bool   `json:"spendUnavailable,omitempty"`
	// MetersWithoutUsage counts published meters left out for having no
	// usage (see ReportOptions.IncludeIdle).
	MetersWithoutUsage int `json:"metersWithoutUsage,omitempty"`
}

type meterInfo struct {
	name       string
	service    string
	deprecated bool
}

// Build turns a UsageQuery into a Report. defs supplies display names; a
// meter missing from it is shown by its definition name.
func Build(s Scope, w Window, q *usagev1alpha1.UsageQuery, defs []billingv1alpha1.MeterDefinition, opts ReportOptions) Report {
	report := Report{
		Scope:  s.String(),
		Period: Period{Name: w.Name, Start: w.Start, End: w.End, Label: RangeLabel(w.Start, w.PeriodEnd)},
		Meters: []Meter{},
	}
	if s.Project == "" && len(q.Status.BillingAccounts) == 0 {
		report.NoBillingAccount = true
		return report
	}

	info := make(map[string]meterInfo, len(defs))
	for _, d := range defs {
		info[d.Name] = meterInfo{
			name:       d.Spec.DisplayName,
			service:    ServiceName(d.Spec.MeterName),
			deprecated: d.Spec.Phase == billingv1alpha1.PhaseDeprecated,
		}
	}

	for _, m := range q.Status.Meters {
		mi, ok := info[m.MeterDefinition]
		if !ok {
			mi = meterInfo{name: m.MeterDefinition, service: ServiceName(m.MeterName)}
		}
		if opts.Meter != "" && !matchesMeter(opts.Meter, m, mi) {
			continue
		}

		total := firstSeries(m.Groupings, 0)
		if total == nil || (ParseDecimal(total.Total) == 0 && len(total.Points) == 0) {
			// A deprecated meter without usage isn't worth listing; one that
			// still has usage stays so the meters add up to the spend.
			if mi.deprecated {
				continue
			}
			if !opts.IncludeIdle {
				report.MetersWithoutUsage++
				continue
			}
		}

		unit := ParseUnit(m.Unit)
		value := 0.0
		if total != nil {
			value = ParseDecimal(total.Total)
		}
		meter := Meter{
			Service:          mi.service,
			Name:             mi.name,
			MeterDefinition:  m.MeterDefinition,
			MeterName:        m.MeterName,
			Unit:             m.Unit,
			Aggregation:      m.Aggregation,
			Total:            value,
			Display:          WithQualifier(FormatUsage(unit, value), m.Aggregation),
			Qualifier:        aggregationQualifiers[m.Aggregation],
			SpendUnavailable: m.SpendError != "",
			Deprecated:       mi.deprecated,
		}
		if m.Spend != nil {
			meter.Spend = money(m.Spend.Amount, m.Spend.Currency)
		}
		if opts.Daily && total != nil {
			meter.Daily = points(unit, total.Points)
		}

		if opts.By != "" {
			var series []usagev1alpha1.UsageSeries
			if len(m.Groupings) > 1 {
				series = m.Groupings[1].Series
			}
			if len(series) == 0 && !opts.IncludeIdle {
				// The meter has no breakdown by this dimension.
				continue
			}
			breakdown := &Breakdown{Dimension: opts.By, Groups: []Group{}}
			for _, sr := range series {
				group := Group{
					Value: sr.Group[opts.By],
					Total: ParseDecimal(sr.Total),
				}
				if group.Value == "" {
					group.Value = "(none)"
				}
				group.Display = WithQualifier(FormatUsage(unit, group.Total), m.Aggregation)
				if opts.Daily {
					group.Daily = points(unit, sr.Points)
				}
				breakdown.Groups = append(breakdown.Groups, group)
			}
			sort.SliceStable(breakdown.Groups, func(i, j int) bool {
				return breakdown.Groups[i].Value < breakdown.Groups[j].Value
			})
			meter.Breakdown = breakdown
		}

		report.Meters = append(report.Meters, meter)
	}

	sort.SliceStable(report.Meters, func(i, j int) bool {
		a, b := report.Meters[i], report.Meters[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Name < b.Name
	})

	// The total covers every meter, so a report narrowed to one meter
	// leaves it out rather than let it read as that meter's spend.
	if opts.Meter == "" {
		switch {
		case q.Status.Spend != nil:
			report.Spend = money(q.Status.Spend.Amount, q.Status.Spend.Currency)
		case q.Status.SpendError != "":
			report.SpendUnavailable = true
		}
	}
	return report
}

func matchesMeter(want string, m usagev1alpha1.MeterUsage, mi meterInfo) bool {
	for _, candidate := range []string{m.MeterDefinition, m.MeterName, mi.name} {
		if strings.EqualFold(candidate, want) {
			return true
		}
	}
	return false
}

func money(amount, currency string) *Money {
	return &Money{Amount: amount, Currency: currency, Display: FormatMoney(amount, currency)}
}

func points(unit MeterUnit, in []usagev1alpha1.UsagePoint) []Point {
	out := make([]Point, 0, len(in))
	for _, p := range in {
		value := ParseDecimal(p.Value)
		out = append(out, Point{
			Date:    p.Start.UTC().Format(time.DateOnly),
			Value:   value,
			Display: FormatUsage(unit, value),
		})
	}
	return out
}

func firstSeries(groupings []usagev1alpha1.UsageGroupingResult, index int) *usagev1alpha1.UsageSeries {
	if len(groupings) <= index || len(groupings[index].Series) == 0 {
		return nil
	}
	return &groupings[index].Series[0]
}

// HasDimension reports whether any meter can be broken down by dimension.
func HasDimension(defs []billingv1alpha1.MeterDefinition, dimension string) bool {
	for _, d := range defs {
		for _, dims := range [][]string{d.Spec.Measurement.Dimensions, d.Status.SystemDimensions} {
			for _, dim := range dims {
				if dim == dimension {
					return true
				}
			}
		}
	}
	return false
}
