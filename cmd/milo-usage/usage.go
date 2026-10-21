package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

type summaryOptions struct {
	cycle string
	from  string
	to    string
	by    string
	daily bool
	all   bool
}

func newRootCommand(out io.Writer, newAPI func() (api, error)) *cobra.Command {
	opts := summaryOptions{}
	root := plugin.NewRootCmd(pluginName, pluginDescription)
	root.Long = `Show metered usage and spend for the active organization or project.

Usage is read from Milo's UsageQuery API with your datumctl session. With a
project selected (in your context or with --project), the query only sees that
project's usage. Spend is priced the way the invoice prices it, before taxes,
credits and discounts.`
	root.Example = `  # Usage and spend for the current billing cycle
  datumctl usage

  # The previous billing cycle, for one project
  datumctl usage --cycle previous --project my-project

  # Break each meter down by project, or by any dimension the meter declares
  datumctl usage --by project_name
  datumctl usage --by region

  # Daily usage over a custom range, as JSON
  datumctl usage --daily --from 2026-10-01 --to 2026-10-15 -o json`
	root.Args = cobra.NoArgs
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetOut(out)

	f := root.Flags()
	f.StringVar(&opts.cycle, "cycle", "current", "Billing cycle to show: current or previous")
	f.StringVar(&opts.from, "from", "", "Start of a custom range (YYYY-MM-DD or RFC 3339), instead of a billing cycle")
	f.StringVar(&opts.to, "to", "", "End of a custom range, exclusive (default: now)")
	f.StringVar(&opts.by, "by", "", "Break usage down by a dimension, e.g. project_name or region")
	f.BoolVar(&opts.daily, "daily", false, "Show usage per day")
	f.BoolVar(&opts.all, "all", false, "Include meters with no usage")

	root.RunE = func(cmd *cobra.Command, _ []string) error {
		s, format, err := resolveCommon(cmd)
		if err != nil {
			return err
		}
		client, err := newAPI()
		if err != nil {
			return err
		}
		return runSummary(cmd.Context(), out, client, s, format, opts, time.Now())
	}

	root.AddCommand(newMetersCommand(out, newAPI))
	return root
}

// resolveCommon reads the --org, --project and --output flags that
// plugin.NewRootCmd defines and datumctl pre-fills from the active context.
func resolveCommon(cmd *cobra.Command) (scope, string, error) {
	org, _ := cmd.Flags().GetString("org")
	project, _ := cmd.Flags().GetString("project")
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case "table", "json", "yaml":
	default:
		return scope{}, "", usageErrorf("unknown output format %q; use table, json or yaml", format)
	}
	// datumctl pre-fills both flags from the active context. Naming another
	// organization without a project means that whole organization, not the
	// context's project, which may belong to a different one.
	if cmd.Flags().Changed("org") && !cmd.Flags().Changed("project") {
		project = ""
	}
	if org == "" && project == "" {
		return scope{}, "", usageErrorf("no organization selected; run `datumctl ctx` or pass --org")
	}
	return scope{org: org, project: project}, format, nil
}

func runSummary(ctx context.Context, out io.Writer, client api, s scope, format string, opts summaryOptions, now time.Time) error {
	w, err := resolveWindow(ctx, client, s, opts, now)
	if err != nil {
		return err
	}

	// The catalog adds display names and lets us catch a mistyped --by; the
	// usage itself stands without it.
	defs, _ := client.listMeterDefinitions(ctx)
	if opts.by != "" && len(defs) > 0 && !hasDimension(defs, opts.by) {
		return usageErrorf("no meter has a dimension named %q; run `datumctl usage meters` to see them", opts.by)
	}

	spec := usagev1alpha1.UsageQuerySpec{
		StartTime:    metav1.NewTime(w.start),
		EndTime:      metav1.NewTime(w.end),
		TimeZone:     "UTC",
		IncludeSpend: true,
		// Meters are left empty so the query reads every meter, and a
		// breakdown by a dimension only some meters have just has no series
		// for the others.
		Groupings: []usagev1alpha1.UsageGrouping{{}},
	}
	if opts.by != "" {
		spec.Groupings = append(spec.Groupings, usagev1alpha1.UsageGrouping{GroupBy: []string{opts.by}})
	}
	if opts.daily {
		spec.Granularity = usagev1alpha1.UsageGranularityDay
	}

	query, err := client.queryUsage(ctx, s, spec)
	if err != nil {
		return err
	}

	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(query)
	case "yaml":
		data, err := yaml.Marshal(query)
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	}

	return renderTable(out, s, w, query, catalog(defs), opts)
}

func resolveWindow(ctx context.Context, client api, s scope, opts summaryOptions, now time.Time) (window, error) {
	if opts.from != "" {
		start, err := parseTime(opts.from)
		if err != nil {
			return window{}, usageErrorf("invalid --from: %v", err)
		}
		end := now.UTC()
		if opts.to != "" {
			if end, err = parseTime(opts.to); err != nil {
				return window{}, usageErrorf("invalid --to: %v", err)
			}
		}
		if !end.After(start) {
			return window{}, usageErrorf("--to must be after --from")
		}
		return window{name: "custom range", start: start, end: end, periodEnd: end}, nil
	}
	if opts.to != "" {
		return window{}, usageErrorf("--to needs --from")
	}

	current, previous := billingCycles(client.paymentTerms(ctx, s), now)
	switch opts.cycle {
	case "current":
		return current, nil
	case "previous":
		return previous, nil
	}
	return window{}, usageErrorf("unknown --cycle %q; use current or previous", opts.cycle)
}

func hasDimension(defs []billingv1alpha1.MeterDefinition, dimension string) bool {
	for _, d := range defs {
		if slices.Contains(d.Spec.Measurement.Dimensions, dimension) ||
			slices.Contains(d.Status.SystemDimensions, dimension) {
			return true
		}
	}
	return false
}

func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date (YYYY-MM-DD) or RFC 3339 time", v)
	}
	return t.UTC(), nil
}

// meterInfo is what the table shows about a meter beyond its usage.
type meterInfo struct {
	displayName string
	service     string
	// deprecated meters are on their way out; they're shown only while
	// they still carry usage.
	deprecated bool
}

func catalog(defs []billingv1alpha1.MeterDefinition) map[string]meterInfo {
	out := make(map[string]meterInfo, len(defs))
	for _, d := range defs {
		out[d.Name] = meterInfo{
			displayName: d.Spec.DisplayName,
			service:     serviceName(d.Spec.MeterName),
			deprecated:  d.Spec.Phase == billingv1alpha1.PhaseDeprecated,
		}
	}
	return out
}

var serviceAcronyms = map[string]string{"dns": "DNS", "ai": "AI", "alb": "ALB", "iam": "IAM"}

// serviceName derives a service label from a meter name's domain, e.g.
// "compute.datumapis.com/instance/cpu-seconds" -> "Compute".
func serviceName(meterName string) string {
	domain, _, _ := strings.Cut(meterName, "/")
	label, _, _ := strings.Cut(domain, ".")
	if label == "" {
		return "Other"
	}
	if acronym, ok := serviceAcronyms[label]; ok {
		return acronym
	}
	words := strings.FieldsFunc(label, func(r rune) bool { return r == '-' || r == '_' })
	for i, word := range words {
		if acronym, ok := serviceAcronyms[word]; ok {
			words[i] = acronym
		} else {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

type tableRow struct {
	date    string
	service string
	meter   string
	group   string
	usage   string
	spend   string
	cost    float64
}

func renderTable(out io.Writer, s scope, w window, q *usagev1alpha1.UsageQuery, info map[string]meterInfo, opts summaryOptions) error {
	_, _ = fmt.Fprintf(out, "Usage for %s, %s (%s)\n\n", s, w.name, rangeLabel(w.start, w.periodEnd))

	if s.project == "" && len(q.Status.BillingAccounts) == 0 {
		_, _ = fmt.Fprintln(out, "This organization has no billing account, so there is no usage to show.")
		return nil
	}

	breakdown := opts.by != ""
	showSpend := !breakdown && !opts.daily
	var rows []tableRow
	hidden := 0

	for _, m := range q.Status.Meters {
		mi, ok := info[m.MeterDefinition]
		if !ok {
			mi = meterInfo{displayName: m.MeterDefinition, service: serviceName(m.MeterName)}
		}
		unit := parseUnit(m.Unit)
		total := firstSeries(m.Groupings, 0)

		if total == nil || (parseDecimal(total.Total) == 0 && len(total.Points) == 0) {
			// A deprecated meter without usage isn't worth listing, even with
			// --all; one that still has usage stays so the rows add up.
			if mi.deprecated {
				continue
			}
			if !opts.all {
				hidden++
				continue
			}
		}

		var series []usagev1alpha1.UsageSeries
		if breakdown {
			if len(m.Groupings) > 1 {
				series = m.Groupings[1].Series
			}
			if len(series) == 0 && !opts.all {
				continue
			}
		} else if total != nil {
			series = []usagev1alpha1.UsageSeries{*total}
		}
		if len(series) == 0 {
			series = []usagev1alpha1.UsageSeries{{Total: "0"}}
		}

		for _, sr := range series {
			group := ""
			if breakdown {
				group = sr.Group[opts.by]
				if group == "" {
					group = "(none)"
				}
			}
			if opts.daily {
				for _, p := range sr.Points {
					rows = append(rows, tableRow{
						date:    p.Start.UTC().Format(time.DateOnly),
						service: mi.service,
						meter:   mi.displayName,
						group:   group,
						usage:   formatUsage(unit, parseDecimal(p.Value)),
					})
				}
				continue
			}
			row := tableRow{
				service: mi.service,
				meter:   mi.displayName,
				group:   group,
				usage:   withQualifier(formatUsage(unit, parseDecimal(sr.Total)), m.Aggregation),
				spend:   "—",
			}
			if m.Spend != nil {
				row.spend = formatMoney(m.Spend.Amount, m.Spend.Currency)
				row.cost = parseDecimal(m.Spend.Amount)
			} else if m.SpendError != "" {
				row.spend = "unavailable"
			}
			rows = append(rows, row)
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.service != b.service {
			return a.service < b.service
		}
		if a.meter != b.meter {
			return a.meter < b.meter
		}
		if a.group != b.group {
			return a.group < b.group
		}
		return a.date < b.date
	})

	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No usage recorded in this period.")
	} else {
		tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		header := []string{}
		if opts.daily {
			header = append(header, "DATE")
		}
		header = append(header, "SERVICE", "METER")
		if breakdown {
			header = append(header, strings.ToUpper(opts.by))
		}
		header = append(header, "USAGE")
		if showSpend {
			header = append(header, "SPEND")
		}
		_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, r := range rows {
			cells := []string{}
			if opts.daily {
				cells = append(cells, r.date)
			}
			cells = append(cells, r.service, r.meter)
			if breakdown {
				cells = append(cells, r.group)
			}
			cells = append(cells, r.usage)
			if showSpend {
				cells = append(cells, r.spend)
			}
			_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintln(out)
	switch {
	case q.Status.Spend != nil:
		_, _ = fmt.Fprintf(out, "Spend this period: %s\n", formatMoney(q.Status.Spend.Amount, q.Status.Spend.Currency))
	case q.Status.SpendError != "":
		_, _ = fmt.Fprintln(out, "Spend this period: unavailable")
	}
	if hidden > 0 {
		noun := "meters"
		if hidden == 1 {
			noun = "meter"
		}
		_, _ = fmt.Fprintf(out, "%d %s with no usage hidden; use --all to show them.\n", hidden, noun)
	}
	return nil
}

func firstSeries(groupings []usagev1alpha1.UsageGroupingResult, index int) *usagev1alpha1.UsageSeries {
	if len(groupings) <= index || len(groupings[index].Series) == 0 {
		return nil
	}
	return &groupings[index].Series[0]
}
