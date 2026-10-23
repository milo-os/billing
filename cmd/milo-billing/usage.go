// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/usagereport"
)

type summaryOptions struct {
	cycle string
	from  string
	to    string
	by    string
	daily bool
	all   bool
}

func newUsageCommand(out io.Writer, newAPI func() (api, error)) *cobra.Command {
	opts := summaryOptions{}
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show metered usage and spend for your organization or project",
		Long: `Show metered usage and spend for the active organization or project.

Usage is read from Milo's UsageQuery API with your datumctl session. With a
project selected (in your context or with --project), the query only sees that
project's usage. Spend is priced the way the invoice prices it, before taxes,
credits and discounts.`,
		Example: `  # Usage and spend for the current billing cycle
  datumctl billing usage

  # The previous billing cycle, for one project
  datumctl billing usage --cycle previous --project my-project

  # Break each meter down by project, or by any dimension the meter declares
  datumctl billing usage --by project_name
  datumctl billing usage --by region

  # Daily usage over a custom range, as JSON
  datumctl billing usage --daily --from 2026-10-01 --to 2026-10-15 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, format, err := resolveCommon(cmd)
			if err != nil {
				return err
			}
			client, err := newAPI()
			if err != nil {
				return err
			}
			return runSummary(cmd.Context(), out, client, s, format, opts, time.Now())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.cycle, "cycle", "current", "Billing cycle to show: current or previous")
	f.StringVar(&opts.from, "from", "", "Start of a custom range (YYYY-MM-DD or RFC 3339), instead of a billing cycle")
	f.StringVar(&opts.to, "to", "", "End of a custom range, exclusive (default: now)")
	f.StringVar(&opts.by, "by", "", "Break usage down by a dimension, e.g. project_name or region")
	f.BoolVar(&opts.daily, "daily", false, "Show usage per day")
	f.BoolVar(&opts.all, "all", false, "Include meters with no usage")
	return cmd
}

// resolveCommon reads the --org, --project and --output flags that
// plugin.NewRootCmd defines and datumctl pre-fills from the active context.
func resolveCommon(cmd *cobra.Command) (usagereport.Scope, string, error) {
	org, _ := cmd.Flags().GetString("org")
	project, _ := cmd.Flags().GetString("project")
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case "table", "json", "yaml":
	default:
		return usagereport.Scope{}, "", usageErrorf("unknown output format %q; use table, json or yaml", format)
	}
	// datumctl pre-fills both flags from the active context. Naming another
	// organization without a project means that whole organization, not the
	// context's project, which may belong to a different one.
	if cmd.Flags().Changed("org") && !cmd.Flags().Changed("project") {
		project = ""
	}
	if org == "" && project == "" {
		return usagereport.Scope{}, "", usageErrorf("no organization selected; run `datumctl ctx` or pass --org")
	}
	return usagereport.Scope{Org: org, Project: project}, format, nil
}

func runSummary(ctx context.Context, out io.Writer, client api, s usagereport.Scope, format string, opts summaryOptions, now time.Time) error {
	w, err := usagereport.ResolveWindow(
		usagereport.WindowOptions{Cycle: opts.cycle, From: opts.from, To: opts.to},
		func() *billingv1alpha1.PaymentTerms { return client.paymentTerms(ctx, s) },
		now,
	)
	var inputErr *usagereport.InputError
	if errors.As(err, &inputErr) {
		return usageErrorf("%s", flagNames.Replace(inputErr.Msg))
	} else if err != nil {
		return err
	}

	// The catalog adds display names and lets us catch a mistyped --by; the
	// usage itself stands without it.
	defs, _ := client.listMeterDefinitions(ctx)
	if opts.by != "" && len(defs) > 0 && !usagereport.HasDimension(defs, opts.by) {
		return usageErrorf("no meter has a dimension named %q; run `datumctl billing meters` to see them", opts.by)
	}

	spec := usagev1alpha1.UsageQuerySpec{
		StartTime:    metav1.NewTime(w.Start),
		EndTime:      metav1.NewTime(w.End),
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

	report := usagereport.Build(s, w, query, defs, usagereport.ReportOptions{
		By:          opts.by,
		Daily:       opts.daily,
		IncludeIdle: opts.all,
	})
	return renderTable(out, report, opts)
}

// flagNames turns the shared window errors' field names into this
// command's flags.
var flagNames = strings.NewReplacer("invalid from", "invalid --from", "invalid to", "invalid --to",
	"to must be after from", "--to must be after --from", "to needs from", "--to needs --from",
	"unknown cycle", "unknown --cycle")

type tableRow struct {
	date, service, meter, group, usage, spend string
}

func renderTable(out io.Writer, r usagereport.Report, opts summaryOptions) error {
	_, _ = fmt.Fprintf(out, "Usage for %s, %s (%s)\n\n", r.Scope, r.Period.Name, r.Period.Label)

	if r.NoBillingAccount {
		_, _ = fmt.Fprintln(out, "This organization has no billing account, so there is no usage to show.")
		return nil
	}

	breakdown := opts.by != ""
	// Spend is per meter, so it only fits a row per meter.
	showSpend := !breakdown && !opts.daily

	var rows []tableRow
	for _, m := range r.Meters {
		row := tableRow{service: m.Service, meter: m.Name, usage: m.Display, spend: "—"}
		switch {
		case m.Spend != nil:
			row.spend = m.Spend.Display
		case m.SpendUnavailable:
			row.spend = "unavailable"
		}

		switch {
		case breakdown && m.Breakdown != nil:
			for _, g := range m.Breakdown.Groups {
				if !opts.daily {
					rows = append(rows, tableRow{service: m.Service, meter: m.Name, group: g.Value, usage: g.Display})
					continue
				}
				for _, p := range g.Daily {
					rows = append(rows, tableRow{date: p.Date, service: m.Service, meter: m.Name, group: g.Value, usage: p.Display})
				}
			}
		case opts.daily:
			for _, p := range m.Daily {
				rows = append(rows, tableRow{date: p.Date, service: m.Service, meter: m.Name, usage: p.Display})
			}
		default:
			rows = append(rows, row)
		}
	}

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
		for _, row := range rows {
			cells := []string{}
			if opts.daily {
				cells = append(cells, row.date)
			}
			cells = append(cells, row.service, row.meter)
			if breakdown {
				cells = append(cells, row.group)
			}
			cells = append(cells, row.usage)
			if showSpend {
				cells = append(cells, row.spend)
			}
			_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintln(out)
	switch {
	case r.Spend != nil:
		_, _ = fmt.Fprintf(out, "Spend this period: %s\n", r.Spend.Display)
	case r.SpendUnavailable:
		_, _ = fmt.Fprintln(out, "Spend this period: unavailable")
	}
	if n := r.MetersWithoutUsage; n > 0 {
		noun := "meters"
		if n == 1 {
			noun = "meter"
		}
		_, _ = fmt.Fprintf(out, "%d %s with no usage hidden; use --all to show them.\n", n, noun)
	}
	return nil
}
