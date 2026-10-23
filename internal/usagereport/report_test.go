// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func def(name, meterName, display string, phase billingv1alpha1.Phase) billingv1alpha1.MeterDefinition {
	d := billingv1alpha1.MeterDefinition{}
	d.Name = name
	d.Spec.MeterName = meterName
	d.Spec.DisplayName = display
	d.Spec.Phase = phase
	return d
}

func pt(day int, value string) usagev1alpha1.UsagePoint {
	start := time.Date(2026, time.October, day, 0, 0, 0, 0, time.UTC)
	return usagev1alpha1.UsagePoint{Start: metav1.NewTime(start), End: metav1.NewTime(start.AddDate(0, 0, 1)), Value: value}
}

func series(total string, points ...usagev1alpha1.UsagePoint) usagev1alpha1.UsageGroupingResult {
	return usagev1alpha1.UsageGroupingResult{Series: []usagev1alpha1.UsageSeries{{Total: total, Points: points}}}
}

// sample has egress with usage and a region breakdown, allocated CPU (a
// gauge) with usage, queries without usage, and a deprecated meter without
// usage.
func sample() (*usagev1alpha1.UsageQuery, []billingv1alpha1.MeterDefinition) {
	defs := []billingv1alpha1.MeterDefinition{
		def("egress", "networking.datumapis.com/gateway/egress-bytes", "ALB Egress", billingv1alpha1.PhasePublished),
		def("cpu", "compute.datumapis.com/instance/cpu-allocated", "CPU Allocated", billingv1alpha1.PhasePublished),
		def("queries", "dns.networking.miloapis.com/zone/queries", "DNS Queries", billingv1alpha1.PhasePublished),
		def("uptime", "compute.datumapis.com/instance/uptime-seconds", "Uptime", billingv1alpha1.PhaseDeprecated),
	}
	q := &usagev1alpha1.UsageQuery{Status: usagev1alpha1.UsageQueryStatus{
		BillingAccounts: []usagev1alpha1.BillingAccountReference{{Name: "acme-main"}},
		Spend:           &usagev1alpha1.UsageSpend{Amount: "0.05", Currency: "USD"},
		Meters: []usagev1alpha1.MeterUsage{
			{
				MeterDefinition: "egress", MeterName: "networking.datumapis.com/gateway/egress-bytes", Unit: "By", Aggregation: "Sum",
				Spend: &usagev1alpha1.UsageSpend{Amount: "0.05", Currency: "USD"},
				Groupings: []usagev1alpha1.UsageGroupingResult{
					series("283115520", pt(1, "104857600"), pt(2, "178257920")),
					{GroupBy: []string{"region"}, Series: []usagev1alpha1.UsageSeries{
						{Group: map[string]string{"region": "us-east-1"}, Total: "283115520", Points: []usagev1alpha1.UsagePoint{pt(1, "104857600"), pt(2, "178257920")}},
						{Group: map[string]string{"region": ""}, Total: "1024"},
					}},
				},
			},
			{
				MeterDefinition: "cpu", MeterName: "compute.datumapis.com/instance/cpu-allocated", Unit: "{cpu}", Aggregation: "Latest",
				Groupings: []usagev1alpha1.UsageGroupingResult{series("4", pt(2, "4"))},
			},
			{MeterDefinition: "queries", MeterName: "dns.networking.miloapis.com/zone/queries", Unit: "{query}", Aggregation: "Sum"},
			{MeterDefinition: "uptime", MeterName: "compute.datumapis.com/instance/uptime-seconds", Unit: "s", Aggregation: "Sum"},
		},
	}}
	return q, defs
}

var testWindow = Window{
	Name: "current billing cycle", Start: time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
	End: time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC),
}

func names(r Report) []string {
	out := []string{}
	for _, m := range r.Meters {
		out = append(out, m.Name)
	}
	return out
}

func TestBuildSummary(t *testing.T) {
	q, defs := sample()
	r := Build(Scope{Org: "acme"}, testWindow, q, defs, ReportOptions{})

	if got := names(r); len(got) != 2 || got[0] != "CPU Allocated" || got[1] != "ALB Egress" {
		t.Fatalf("meters = %v, want CPU Allocated then ALB Egress (by service)", got)
	}
	cpu, egress := r.Meters[0], r.Meters[1]
	if cpu.Display != "4 vCPU latest" || cpu.Qualifier != "latest" {
		t.Errorf("cpu = %q (%q)", cpu.Display, cpu.Qualifier)
	}
	if egress.Display != "270 MB" || egress.Spend == nil || egress.Spend.Display != "$0.05" {
		t.Errorf("egress = %+v", egress)
	}
	if r.Spend.Display != "$0.05" || r.Period.Label != "1 Oct - 1 Nov 2026" || r.Scope != "organization acme" {
		t.Errorf("report = %+v", r)
	}
	// DNS queries has no usage; the deprecated uptime meter isn't counted.
	if r.MetersWithoutUsage != 1 {
		t.Errorf("metersWithoutUsage = %d, want 1", r.MetersWithoutUsage)
	}
}

func TestBuildIncludeIdleSkipsDeprecated(t *testing.T) {
	q, defs := sample()
	got := names(Build(Scope{Org: "acme"}, testWindow, q, defs, ReportOptions{IncludeIdle: true}))
	if len(got) != 3 {
		t.Fatalf("meters = %v, want the published idle meter but not the deprecated one", got)
	}
}

func TestBuildBreakdownAndDaily(t *testing.T) {
	q, defs := sample()
	r := Build(Scope{Org: "acme"}, testWindow, q, defs, ReportOptions{By: "region", Daily: true})

	if got := names(r); len(got) != 1 || got[0] != "ALB Egress" {
		t.Fatalf("meters = %v, want only the meter with a region breakdown", got)
	}
	b := r.Meters[0].Breakdown
	if b == nil || len(b.Groups) != 2 || b.Groups[0].Value != "(none)" || b.Groups[1].Value != "us-east-1" {
		t.Fatalf("breakdown = %+v", b)
	}
	if daily := b.Groups[1].Daily; len(daily) != 2 || daily[1].Date != "2026-10-02" || daily[1].Display != "170 MB" {
		t.Errorf("daily = %+v", daily)
	}
}

func TestBuildMeterFilter(t *testing.T) {
	q, defs := sample()
	for _, want := range []string{"egress", "networking.datumapis.com/gateway/egress-bytes", "alb egress"} {
		r := Build(Scope{Org: "acme"}, testWindow, q, defs, ReportOptions{Meter: want})
		if got := names(r); len(got) != 1 {
			t.Errorf("Meter %q: got %v", want, got)
		}
		// The period total covers every meter, so it would read as this
		// meter's spend; the meter keeps its own.
		if r.Spend != nil || r.Meters[0].Spend == nil {
			t.Errorf("Meter %q: report spend = %v, meter spend = %v", want, r.Spend, r.Meters[0].Spend)
		}
	}
}

func TestBuildNoBillingAccount(t *testing.T) {
	q, defs := sample()
	q.Status.BillingAccounts = nil
	if r := Build(Scope{Org: "acme"}, testWindow, q, defs, ReportOptions{}); !r.NoBillingAccount || len(r.Meters) != 0 {
		t.Errorf("report = %+v", r)
	}
	// Project queries never list billing accounts.
	if r := Build(Scope{Project: "web"}, testWindow, q, defs, ReportOptions{}); r.NoBillingAccount {
		t.Errorf("a project report should not flag a missing billing account")
	}
}

func TestResolveWindow(t *testing.T) {
	now := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	noTerms := func() *billingv1alpha1.PaymentTerms { return nil }

	w, err := ResolveWindow(WindowOptions{}, noTerms, now)
	if err != nil || !w.Start.Equal(time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)) || !w.End.Equal(now) {
		t.Errorf("current = %+v, %v", w, err)
	}
	w, err = ResolveWindow(WindowOptions{From: "2026-10-01", To: "2026-10-15"}, func() *billingv1alpha1.PaymentTerms {
		t.Fatal("a custom range shouldn't read payment terms")
		return nil
	}, now)
	if err != nil || w.Name != "custom range" {
		t.Errorf("custom = %+v, %v", w, err)
	}
	for _, bad := range []WindowOptions{{Cycle: "next"}, {From: "soon"}, {From: "2026-10-15", To: "2026-10-01"}, {To: "2026-10-01"}} {
		if _, err := ResolveWindow(bad, noTerms, now); err == nil {
			t.Errorf("%+v: want an error", bad)
		} else if _, ok := err.(*InputError); !ok {
			t.Errorf("%+v: got %T, want *InputError", bad, err)
		}
	}
}
