package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

var testNow = time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)

type fakeAPI struct {
	scope  scope
	spec   usagev1alpha1.UsageQuerySpec
	status usagev1alpha1.UsageQueryStatus
	defs   []billingv1alpha1.MeterDefinition
	terms  *billingv1alpha1.PaymentTerms
	err    error
}

func (f *fakeAPI) queryUsage(_ context.Context, s scope, spec usagev1alpha1.UsageQuerySpec) (*usagev1alpha1.UsageQuery, error) {
	f.scope, f.spec = s, spec
	if f.err != nil {
		return nil, f.err
	}
	return &usagev1alpha1.UsageQuery{Spec: spec, Status: f.status}, nil
}

func (f *fakeAPI) listMeterDefinitions(context.Context) ([]billingv1alpha1.MeterDefinition, error) {
	return f.defs, nil
}

func (f *fakeAPI) paymentTerms(context.Context, scope) *billingv1alpha1.PaymentTerms { return f.terms }

func meterDef(name, meterName, display, unit, aggregation string, dims ...string) billingv1alpha1.MeterDefinition {
	d := billingv1alpha1.MeterDefinition{}
	d.Name = name
	d.Spec.MeterName = meterName
	d.Spec.DisplayName = display
	d.Spec.Phase = billingv1alpha1.PhasePublished
	d.Spec.Measurement.Unit = unit
	d.Spec.Measurement.Aggregation = billingv1alpha1.MeterAggregation(aggregation)
	d.Spec.Measurement.Dimensions = dims
	return d
}

func point(day int, value string) usagev1alpha1.UsagePoint {
	start := time.Date(2026, time.October, day, 0, 0, 0, 0, time.UTC)
	return usagev1alpha1.UsagePoint{Start: metav1.NewTime(start), End: metav1.NewTime(start.AddDate(0, 0, 1)), Value: value}
}

// sampleAPI has three meters: memory with usage and spend, egress with a
// region breakdown, and DNS queries with no usage.
func sampleAPI() *fakeAPI {
	return &fakeAPI{
		defs: []billingv1alpha1.MeterDefinition{
			meterDef("memory", "compute.datumapis.com/instance/memory-seconds", "Memory Seconds", "By.s", "Sum"),
			meterDef("egress", "networking.datumapis.com/gateway/egress-bytes", "ALB Egress", "By", "Sum", "region"),
			meterDef("queries", "dns.networking.miloapis.com/zone/queries", "DNS Queries", "{query}", "Sum"),
		},
		status: usagev1alpha1.UsageQueryStatus{
			BillingAccounts: []usagev1alpha1.BillingAccountReference{{Name: "acme-main"}},
			Spend:           &usagev1alpha1.UsageSpend{Amount: "1.75", Currency: "USD"},
			Meters: []usagev1alpha1.MeterUsage{
				{
					MeterDefinition: "memory", MeterName: "compute.datumapis.com/instance/memory-seconds",
					Unit: "By.s", Aggregation: "Sum",
					Spend: &usagev1alpha1.UsageSpend{Amount: "1.70", Currency: "USD"},
					Groupings: []usagev1alpha1.UsageGroupingResult{
						{Series: []usagev1alpha1.UsageSeries{{Total: "1352914698240000", Points: []usagev1alpha1.UsagePoint{point(1, "1352914698240000")}}}},
					},
				},
				{
					MeterDefinition: "egress", MeterName: "networking.datumapis.com/gateway/egress-bytes",
					Unit: "By", Aggregation: "Sum",
					Spend: &usagev1alpha1.UsageSpend{Amount: "0.05", Currency: "USD"},
					Groupings: []usagev1alpha1.UsageGroupingResult{
						{Series: []usagev1alpha1.UsageSeries{{Total: "283115520", Points: []usagev1alpha1.UsagePoint{point(1, "104857600"), point(2, "178257920")}}}},
						{GroupBy: []string{"region"}, Series: []usagev1alpha1.UsageSeries{
							{Group: map[string]string{"region": "us-east-1"}, Total: "283115520", Points: []usagev1alpha1.UsagePoint{point(1, "104857600"), point(2, "178257920")}},
						}},
					},
				},
				{
					MeterDefinition: "queries", MeterName: "dns.networking.miloapis.com/zone/queries",
					Unit: "{query}", Aggregation: "Sum",
					Groupings: []usagev1alpha1.UsageGroupingResult{{}, {GroupBy: []string{"region"}}},
				},
			},
		},
	}
}

func run(t *testing.T, client *fakeAPI, s scope, format string, opts summaryOptions) string {
	t.Helper()
	if opts.cycle == "" {
		opts.cycle = "current"
	}
	var out bytes.Buffer
	if err := runSummary(context.Background(), &out, client, s, format, opts, testNow); err != nil {
		t.Fatalf("runSummary: %v", err)
	}
	return out.String()
}

func TestSummaryQueriesTheCurrentCycleWithSpend(t *testing.T) {
	client := sampleAPI()
	run(t, client, scope{org: "acme"}, "table", summaryOptions{})

	if !client.spec.StartTime.Equal(&metav1.Time{Time: date(2026, time.October, 1)}) {
		t.Errorf("start = %v", client.spec.StartTime)
	}
	if !client.spec.EndTime.Time.Equal(testNow) {
		t.Errorf("end = %v, want now", client.spec.EndTime)
	}
	if !client.spec.IncludeSpend || len(client.spec.Meters) != 0 || len(client.spec.Groupings) != 1 {
		t.Errorf("spec = %+v", client.spec)
	}
	if client.spec.Granularity != "" {
		t.Errorf("a summary needs no buckets, got %q", client.spec.Granularity)
	}
}

func TestSummaryTable(t *testing.T) {
	out := run(t, sampleAPI(), scope{org: "acme"}, "table", summaryOptions{})

	for _, want := range []string{
		"Usage for organization acme, current billing cycle (1 Oct - 1 Nov 2026)",
		"SERVICE", "SPEND",
		"Compute", "Memory Seconds", "350 GB·h", "$1.70",
		"Networking", "ALB Egress", "270 MB", "$0.05",
		"Spend this period: $1.75",
		"1 meter with no usage hidden; use --all to show them.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DNS Queries") {
		t.Errorf("a meter with no usage should be hidden:\n%s", out)
	}
}

func TestSummaryAllShowsIdleMeters(t *testing.T) {
	out := run(t, sampleAPI(), scope{org: "acme"}, "table", summaryOptions{all: true})
	if !strings.Contains(out, "DNS Queries") || strings.Contains(out, "hidden") {
		t.Errorf("--all should list every meter:\n%s", out)
	}
}

func TestSummaryBreakdown(t *testing.T) {
	client := sampleAPI()
	client.defs[0].Status.SystemDimensions = []string{"project_name"}
	out := run(t, client, scope{org: "acme"}, "table", summaryOptions{by: "region"})

	if got := client.spec.Groupings; len(got) != 2 || got[1].GroupBy[0] != "region" {
		t.Errorf("groupings = %+v", got)
	}
	if !strings.Contains(out, "REGION") || !strings.Contains(out, "us-east-1") {
		t.Errorf("missing the region breakdown:\n%s", out)
	}
	if strings.Contains(out, "Memory Seconds") {
		t.Errorf("a meter without the dimension has no rows:\n%s", out)
	}
	if strings.Contains(out, "SPEND\n") || strings.Contains(out, "$0.05") {
		t.Errorf("spend is per meter, so a breakdown leaves it out:\n%s", out)
	}
}

func TestSummaryDaily(t *testing.T) {
	client := sampleAPI()
	out := run(t, client, scope{org: "acme"}, "table", summaryOptions{daily: true})

	if client.spec.Granularity != usagev1alpha1.UsageGranularityDay {
		t.Errorf("granularity = %q", client.spec.Granularity)
	}
	for _, want := range []string{"DATE", "2026-10-01", "2026-10-02", "100 MB", "170 MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestSummaryProjectScope(t *testing.T) {
	client := sampleAPI()
	client.status.BillingAccounts = nil // project queries never list them
	out := run(t, client, scope{org: "acme", project: "web"}, "table", summaryOptions{})

	if client.scope.project != "web" {
		t.Errorf("scope = %+v", client.scope)
	}
	if !strings.Contains(out, "Usage for project web") || !strings.Contains(out, "Memory Seconds") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSummaryNoBillingAccount(t *testing.T) {
	client := sampleAPI()
	client.status.BillingAccounts = nil
	out := run(t, client, scope{org: "acme"}, "table", summaryOptions{})
	if !strings.Contains(out, "no billing account") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSummaryJSONIsTheUsageQuery(t *testing.T) {
	out := run(t, sampleAPI(), scope{org: "acme"}, "json", summaryOptions{})
	var q usagev1alpha1.UsageQuery
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatalf("not a UsageQuery: %v\n%s", err, out)
	}
	if len(q.Status.Meters) != 3 || q.Status.Spend.Amount != "1.75" {
		t.Errorf("unexpected query: %+v", q.Status)
	}
}

func TestPreviousCycleUsesPaymentTerms(t *testing.T) {
	client := sampleAPI()
	client.terms = &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Monthly", InvoiceDayOfMonth: 15}
	run(t, client, scope{org: "acme"}, "table", summaryOptions{cycle: "previous"})

	if !client.spec.StartTime.Time.Equal(date(2026, time.September, 15)) || !client.spec.EndTime.Time.Equal(date(2026, time.October, 15)) {
		t.Errorf("window = %v - %v", client.spec.StartTime, client.spec.EndTime)
	}
}

func TestCustomRange(t *testing.T) {
	client := sampleAPI()
	run(t, client, scope{org: "acme"}, "table", summaryOptions{from: "2026-10-01", to: "2026-10-15"})
	if !client.spec.StartTime.Time.Equal(date(2026, time.October, 1)) || !client.spec.EndTime.Time.Equal(date(2026, time.October, 15)) {
		t.Errorf("window = %v - %v", client.spec.StartTime, client.spec.EndTime)
	}
}

func TestInvalidOptionsAreUsageErrors(t *testing.T) {
	cases := []summaryOptions{
		{cycle: "next"},
		{from: "yesterday"},
		{from: "2026-10-15", to: "2026-10-01"},
		{cycle: "current", to: "2026-10-01"},
		{cycle: "current", by: "regoin"},
	}
	for _, opts := range cases {
		err := runSummary(context.Background(), &bytes.Buffer{}, sampleAPI(), scope{org: "acme"}, "table", opts, testNow)
		var usageErr *usageError
		if !errors.As(err, &usageErr) {
			t.Errorf("%+v: got %v, want a usage error", opts, err)
		}
	}
}

func TestRootCommandNeedsAnOrganization(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, func() (api, error) { return sampleAPI(), nil })
	root.SetArgs([]string{"--org", "", "--project", ""})
	err := root.Execute()
	var usageErr *usageError
	if !errors.As(err, &usageErr) || !strings.Contains(err.Error(), "no organization selected") {
		t.Errorf("got %v", err)
	}
}

func TestServiceName(t *testing.T) {
	cases := map[string]string{
		"compute.datumapis.com/instance/cpu-seconds": "Compute",
		"dns.networking.miloapis.com/zone/queries":   "DNS",
		"ai-gateway.miloapis.com/requests":           "AI Gateway",
		"":                                           "Other",
	}
	for in, want := range cases {
		if got := serviceName(in); got != want {
			t.Errorf("serviceName(%q) = %q, want %q", in, got, want)
		}
	}
}
