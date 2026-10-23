// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/usagereport"
)

func newMetersCommand(out io.Writer, newAPI func() (api, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "meters",
		Short: "List the meters usage is reported for, with their units and dimensions",
		Long: `List the published meters, with the unit and aggregation each reports in
and the dimensions you can break it down by (--by).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Flags().GetString("output")
			client, err := newAPI()
			if err != nil {
				return err
			}
			defs, err := client.listMeterDefinitions(cmd.Context())
			if err != nil {
				return err
			}
			return renderMeters(out, defs, format)
		},
	}
}

func renderMeters(out io.Writer, defs []billingv1alpha1.MeterDefinition, format string) error {
	var visible []billingv1alpha1.MeterDefinition
	for _, d := range defs {
		if d.Spec.Phase == billingv1alpha1.PhasePublished {
			visible = append(visible, d)
		}
	}
	sort.SliceStable(visible, func(i, j int) bool { return visible[i].Spec.MeterName < visible[j].Spec.MeterName })

	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(visible)
	case "yaml":
		data, err := yaml.Marshal(visible)
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	case "table":
	default:
		return usageErrorf("unknown output format %q; use table, json or yaml", format)
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SERVICE\tMETER\tNAME\tUNIT\tAGGREGATION\tDIMENSIONS")
	for _, d := range visible {
		dims := append(append([]string{}, d.Spec.Measurement.Dimensions...), d.Status.SystemDimensions...)
		dims = unique(dims)
		dimensions := strings.Join(dims, ", ")
		if dimensions == "" {
			dimensions = "—"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			usagereport.ServiceName(d.Spec.MeterName), d.Spec.DisplayName, d.Name,
			d.Spec.Measurement.Unit, d.Spec.Measurement.Aggregation, dimensions)
	}
	return tw.Flush()
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
