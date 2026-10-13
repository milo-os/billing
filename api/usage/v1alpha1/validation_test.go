// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var start = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

// validQuery is the issue's org scenario: daily usage for the current cycle,
// with the total and two breakdowns.
func validQuery() *UsageQuery {
	return &UsageQuery{
		Spec: UsageQuerySpec{
			StartTime:   metav1.NewTime(start),
			EndTime:     metav1.NewTime(start.Add(8 * 24 * time.Hour)),
			Granularity: UsageGranularityDay,
			TimeZone:    "UTC",
			Groupings: []UsageGrouping{
				{},
				{GroupBy: []string{"project_name"}},
				{GroupBy: []string{"region"}},
			},
		},
	}
}

func TestSetDefaults(t *testing.T) {
	q := &UsageQuery{}
	q.SetDefaults()

	if q.Spec.TimeZone != "UTC" {
		t.Errorf("timeZone = %q, want UTC", q.Spec.TimeZone)
	}
	if len(q.Spec.Groupings) != 1 || len(q.Spec.Groupings[0].GroupBy) != 0 {
		t.Errorf("groupings = %+v, want a single total grouping", q.Spec.Groupings)
	}

	q = validQuery()
	q.Spec.TimeZone = "Europe/Madrid"
	q.SetDefaults()
	if q.Spec.TimeZone != "Europe/Madrid" || len(q.Spec.Groupings) != 3 {
		t.Errorf("SetDefaults changed fields that were set: %+v", q.Spec)
	}
}

func TestValidate(t *testing.T) {
	tooMany := func(n int, name func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = name(i)
		}
		return out
	}
	numbered := func(prefix string) func(int) string {
		return func(i int) string { return prefix + strings.Repeat("a", i+1) }
	}

	tests := []struct {
		name   string
		mutate func(q *UsageQuery)
		// wantFields lists the field paths that must be reported; empty
		// means the query is valid.
		wantFields []string
	}{
		{
			name:   "valid",
			mutate: func(q *UsageQuery) {},
		},
		{
			name: "valid project scenario with filters",
			mutate: func(q *UsageQuery) {
				q.Spec.Projects = []string{"web"}
				q.Spec.BillingAccounts = []BillingAccountReference{{Name: "acme-main"}}
				q.Spec.Meters = []string{"compute.miloapis.com-instance-cpu-hours"}
				q.Spec.Groupings = []UsageGrouping{{}, {GroupBy: []string{"region", "resource.type"}}}
			},
		},
		{
			name: "totals only, no granularity, long range",
			mutate: func(q *UsageQuery) {
				q.Spec.Granularity = ""
				q.Spec.EndTime = metav1.NewTime(start.Add(3 * 365 * 24 * time.Hour))
			},
		},
		{
			name: "missing times",
			mutate: func(q *UsageQuery) {
				q.Spec.StartTime = metav1.Time{}
				q.Spec.EndTime = metav1.Time{}
			},
			wantFields: []string{"spec.startTime", "spec.endTime"},
		},
		{
			name:       "end before start",
			mutate:     func(q *UsageQuery) { q.Spec.EndTime = metav1.NewTime(start.Add(-time.Hour)) },
			wantFields: []string{"spec.endTime"},
		},
		{
			name:       "end equal to start",
			mutate:     func(q *UsageQuery) { q.Spec.EndTime = q.Spec.StartTime },
			wantFields: []string{"spec.endTime"},
		},
		{
			name: "hourly range too long",
			mutate: func(q *UsageQuery) {
				q.Spec.Granularity = UsageGranularityHour
				q.Spec.EndTime = metav1.NewTime(start.Add(MaxHourlyRange + time.Hour))
			},
			wantFields: []string{"spec.endTime"},
		},
		{
			name: "hourly range at the limit",
			mutate: func(q *UsageQuery) {
				q.Spec.Granularity = UsageGranularityHour
				q.Spec.EndTime = metav1.NewTime(start.Add(MaxHourlyRange))
			},
		},
		{
			name:       "daily range too long",
			mutate:     func(q *UsageQuery) { q.Spec.EndTime = metav1.NewTime(start.Add(MaxDailyRange + time.Hour)) },
			wantFields: []string{"spec.endTime"},
		},
		{
			name:       "unknown granularity",
			mutate:     func(q *UsageQuery) { q.Spec.Granularity = "Minute" },
			wantFields: []string{"spec.granularity"},
		},
		{
			name:       "unknown time zone",
			mutate:     func(q *UsageQuery) { q.Spec.TimeZone = "Mars/Olympus" },
			wantFields: []string{"spec.timeZone"},
		},
		{
			name: "bad billing account references",
			mutate: func(q *UsageQuery) {
				q.Spec.BillingAccounts = []BillingAccountReference{
					{},
					{Namespace: "Not_A_Label", Name: "a"},
					{Name: "dup"},
					{Name: "dup"},
				}
			},
			wantFields: []string{
				"spec.billingAccounts[0].name",
				"spec.billingAccounts[1].namespace",
				"spec.billingAccounts[3]",
			},
		},
		{
			name: "same name in different namespaces is not a duplicate",
			mutate: func(q *UsageQuery) {
				q.Spec.BillingAccounts = []BillingAccountReference{
					{Namespace: "organization-a", Name: "main"},
					{Namespace: "organization-b", Name: "main"},
				}
			},
		},
		{
			name: "too many billing accounts",
			mutate: func(q *UsageQuery) {
				q.Spec.BillingAccounts = make([]BillingAccountReference, MaxUsageQueryBillingAccounts+1)
			},
			wantFields: []string{"spec.billingAccounts"},
		},
		{
			name:       "bad and duplicate projects",
			mutate:     func(q *UsageQuery) { q.Spec.Projects = []string{"", "Web!", "web", "web"} },
			wantFields: []string{"spec.projects[0]", "spec.projects[1]", "spec.projects[3]"},
		},
		{
			name:       "too many projects",
			mutate:     func(q *UsageQuery) { q.Spec.Projects = tooMany(MaxUsageQueryProjects+1, numbered("p")) },
			wantFields: []string{"spec.projects"},
		},
		{
			name:       "too many meters",
			mutate:     func(q *UsageQuery) { q.Spec.Meters = tooMany(MaxUsageQueryMeters+1, numbered("m")) },
			wantFields: []string{"spec.meters"},
		},
		{
			name: "bad groupBy dimensions",
			mutate: func(q *UsageQuery) {
				q.Spec.Groupings = []UsageGrouping{
					{GroupBy: []string{""}},
					{GroupBy: []string{strings.Repeat("d", MaxUsageDimensionLength+1)}},
					{GroupBy: []string{"region", "region"}},
					{GroupBy: []string{"a", "b", "c", "d"}},
				}
			},
			wantFields: []string{
				"spec.groupings[0].groupBy[0]",
				"spec.groupings[1].groupBy[0]",
				"spec.groupings[2].groupBy[1]",
				"spec.groupings[3].groupBy",
			},
		},
		{
			name: "same grouping in another order is a duplicate",
			mutate: func(q *UsageQuery) {
				q.Spec.Groupings = []UsageGrouping{
					{GroupBy: []string{"region", "model"}},
					{GroupBy: []string{"model", "region"}},
				}
			},
			wantFields: []string{"spec.groupings[1]"},
		},
		{
			name: "two totals are a duplicate",
			mutate: func(q *UsageQuery) {
				q.Spec.Groupings = []UsageGrouping{{}, {GroupBy: []string{}}}
			},
			wantFields: []string{"spec.groupings[1]"},
		},
		{
			name:       "too many groupings",
			mutate:     func(q *UsageQuery) { q.Spec.Groupings = make([]UsageGrouping, MaxUsageQueryGroupings+1) },
			wantFields: []string{"spec.groupings"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := validQuery()
			tt.mutate(q)

			errs := q.Validate()
			got := fieldSet(errs)

			if len(tt.wantFields) == 0 && len(errs) > 0 {
				t.Fatalf("expected no errors, got %v", errs)
			}
			for _, f := range tt.wantFields {
				if !got[f] {
					t.Errorf("expected an error on %s, got %v", f, errs)
				}
			}
		})
	}
}

func fieldSet(errs field.ErrorList) map[string]bool {
	out := map[string]bool{}
	for _, e := range errs {
		out[e.Field] = true
	}
	return out
}
