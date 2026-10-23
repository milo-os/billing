// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/usagereport"
)

// Tool names. Patch offers them as "billing__<name>".
const (
	ToolUsageSummary   = "usage_summary"
	ToolUsageBreakdown = "usage_breakdown"
	ToolUsageDaily     = "usage_daily"
	ToolListMeters     = "list_meters"
)

// Tools is every tool this server offers.
var Tools = []string{
	ToolUsageSummary, ToolUsageBreakdown, ToolUsageDaily, ToolListMeters,
	ToolGetBillingAccount, ToolListPaymentMethods, ToolListInvoices,
}

// ToolCallTimeout bounds one tool call end to end. It sits below the 60
// second budget Patch gives a tool call, so a slow query fails as a tool
// error rather than being cut off.
const ToolCallTimeout = 50 * time.Second

// Period selects the time range a tool reads.
type Period struct {
	Cycle string `json:"cycle,omitempty" jsonschema:"Billing cycle to read: current (the default) or previous. Ignored when from is set."`
	From  string `json:"from,omitempty" jsonschema:"Start of a custom range, as a date (YYYY-MM-DD) or RFC 3339 time, instead of a billing cycle."`
	To    string `json:"to,omitempty" jsonschema:"End of a custom range, exclusive. Defaults to now."`
}

// UsageSummaryArgs are usage_summary's arguments.
type UsageSummaryArgs struct {
	Period
	IncludeUnused bool `json:"includeUnused,omitempty" jsonschema:"Also list meters with no usage in the period."`
}

// UsageBreakdownArgs are usage_breakdown's arguments.
type UsageBreakdownArgs struct {
	Period
	Dimension string `json:"dimension" jsonschema:"Dimension to break usage down by, e.g. region, model or httproute_name. list_meters shows each meter's dimensions."`
	Meter     string `json:"meter,omitempty" jsonschema:"Only this meter: its display name, meter name or MeterDefinition name. Omit for every meter."`
}

// UsageDailyArgs are usage_daily's arguments.
type UsageDailyArgs struct {
	Period
	Meter string `json:"meter,omitempty" jsonschema:"Only this meter: its display name, meter name or MeterDefinition name. Omit for every meter with usage."`
}

// ListMetersArgs are list_meters' arguments; it takes none.
type ListMetersArgs struct{}

// toolReport is a report plus anything the model should know about how it
// was produced.
type toolReport struct {
	usagereport.Report
	Notes []string `json:"notes,omitempty"`
}

type tools struct {
	resolve resolver
	now     func() time.Time
}

func readOnly() *mcp.ToolAnnotations { return &mcp.ToolAnnotations{ReadOnlyHint: true} }

func addTool[In any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, any]) {
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, ToolCallTimeout)
		defer cancel()
		return handler(ctx, req, args)
	})
}

func registerTools(server *mcp.Server, t *tools) {
	addTool(server, &mcp.Tool{
		Name: ToolUsageSummary,
		Description: "Usage and spend for this project over a billing cycle or date range: each meter's usage " +
			"(e.g. requests, egress bytes, tokens, vCPU time) with what it costs, and the total spend. " +
			"Start here for questions about usage, cost, spend or the bill. Spend is before taxes, credits and discounts.",
		Annotations: readOnly(),
	}, t.usageSummary)

	addTool(server, &mcp.Tool{
		Name: ToolUsageBreakdown,
		Description: "Break usage down by one dimension, e.g. region, model or httproute_name, to see what is " +
			"driving it. Optionally for a single meter. Usage only: spend is per meter, see usage_summary.",
		Annotations: readOnly(),
	}, t.usageBreakdown)

	addTool(server, &mcp.Tool{
		Name: ToolUsageDaily,
		Description: "Usage per day over a billing cycle or date range, to find when usage changed or spiked. " +
			"Optionally for a single meter. Days without usage are zero.",
		Annotations: readOnly(),
	}, t.usageDaily)

	addTool(server, &mcp.Tool{
		Name: ToolListMeters,
		Description: "The meters usage is reported for: what each measures, its unit, how it aggregates, and " +
			"the dimensions usage_breakdown can split it by.",
		Annotations: readOnly(),
	}, t.listMeters)

	registerAccountTools(server, t)
}

func (t *tools) usageSummary(ctx context.Context, _ *mcp.CallToolRequest, args UsageSummaryArgs) (*mcp.CallToolResult, any, error) {
	return t.report(ctx, args.Period, usagereport.ReportOptions{IncludeIdle: args.IncludeUnused})
}

func (t *tools) usageBreakdown(ctx context.Context, _ *mcp.CallToolRequest, args UsageBreakdownArgs) (*mcp.CallToolResult, any, error) {
	if args.Dimension == "" {
		return errorResult("dimension is required, e.g. region or model; list_meters shows each meter's dimensions."), nil, nil
	}
	return t.report(ctx, args.Period, usagereport.ReportOptions{By: args.Dimension, Meter: args.Meter})
}

func (t *tools) usageDaily(ctx context.Context, _ *mcp.CallToolRequest, args UsageDailyArgs) (*mcp.CallToolResult, any, error) {
	return t.report(ctx, args.Period, usagereport.ReportOptions{Daily: true, Meter: args.Meter})
}

func (t *tools) report(ctx context.Context, period Period, opts usagereport.ReportOptions) (*mcp.CallToolResult, any, error) {
	c, err := t.resolve(ctx)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	scope := usagereport.Scope{Project: c.project}

	var notes []string
	w, err := usagereport.ResolveWindow(usagereport.WindowOptions(period), func() *billingv1alpha1.PaymentTerms {
		// The cycle comes from the project's billing account, which lives in
		// its organization; a project-only member may not be able to read it.
		if org, err := c.client.ProjectOrganization(ctx, c.project); err == nil {
			if terms := c.client.PaymentTerms(ctx, usagereport.Scope{Org: org, Project: c.project}); terms != nil {
				return terms
			}
		}
		notes = append(notes, "The billing account's payment terms couldn't be read, so billing cycles are assumed to be calendar months.")
		return nil
	}, t.now())
	var inputErr *usagereport.InputError
	if errors.As(err, &inputErr) {
		return errorResult(inputErr.Msg), nil, nil
	} else if err != nil {
		return errorResult(err.Error()), nil, nil
	}

	defs, err := c.client.ListMeterDefinitions(ctx)
	if err != nil {
		notes = append(notes, "The meter catalog couldn't be read, so meters are named by their definition.")
	}
	if opts.By != "" && len(defs) > 0 && !usagereport.HasDimension(defs, opts.By) {
		return errorResult(fmt.Sprintf("No meter has a dimension named %q; list_meters shows each meter's dimensions.", opts.By)), nil, nil
	}

	spec := usagev1alpha1.UsageQuerySpec{
		StartTime:    metav1.NewTime(w.Start),
		EndTime:      metav1.NewTime(w.End),
		TimeZone:     "UTC",
		IncludeSpend: true,
		Groupings:    []usagev1alpha1.UsageGrouping{{}},
	}
	if opts.By != "" {
		spec.Groupings = append(spec.Groupings, usagev1alpha1.UsageGrouping{GroupBy: []string{opts.By}})
	}
	if opts.Daily {
		spec.Granularity = usagev1alpha1.UsageGranularityDay
	}

	query, err := c.client.QueryUsage(ctx, scope, spec)
	if err != nil {
		return errorResult(describeError(err, c.project)), nil, nil
	}

	report := usagereport.Build(scope, w, query, defs, opts)
	if opts.Meter != "" && len(report.Meters) == 0 {
		notes = append(notes, fmt.Sprintf("No meter named %q had usage in this period; list_meters shows the meter names.", opts.Meter))
	}
	return jsonResult(toolReport{Report: report, Notes: notes})
}

// meterSummary is one meter as list_meters describes it.
type meterSummary struct {
	Service         string   `json:"service"`
	Name            string   `json:"name"`
	MeterDefinition string   `json:"meterDefinition"`
	MeterName       string   `json:"meterName"`
	Description     string   `json:"description,omitempty"`
	Unit            string   `json:"unit"`
	Aggregation     string   `json:"aggregation"`
	Dimensions      []string `json:"dimensions,omitempty"`
}

func (t *tools) listMeters(ctx context.Context, _ *mcp.CallToolRequest, _ ListMetersArgs) (*mcp.CallToolResult, any, error) {
	c, err := t.resolve(ctx)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	defs, err := c.client.ListMeterDefinitions(ctx)
	if err != nil {
		return errorResult(describeError(err, c.project)), nil, nil
	}

	meters := []meterSummary{}
	for _, d := range defs {
		if d.Spec.Phase != billingv1alpha1.PhasePublished {
			continue
		}
		dims := append(append([]string{}, d.Spec.Measurement.Dimensions...), d.Status.SystemDimensions...)
		meters = append(meters, meterSummary{
			Service:         usagereport.ServiceName(d.Spec.MeterName),
			Name:            d.Spec.DisplayName,
			MeterDefinition: d.Name,
			MeterName:       d.Spec.MeterName,
			Description:     d.Spec.Description,
			Unit:            d.Spec.Measurement.Unit,
			Aggregation:     string(d.Spec.Measurement.Aggregation),
			Dimensions:      unique(dims),
		})
	}
	sort.SliceStable(meters, func(i, j int) bool { return meters[i].MeterName < meters[j].MeterName })
	return jsonResult(map[string]any{"meters": meters})
}

// describeError explains an API failure in terms the assistant can relay.
func describeError(err error, project string) string {
	var apiErr *usagereport.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Sprintf("Reading usage failed: %v", err)
	}
	switch apiErr.Status {
	case http.StatusUnauthorized:
		return "The person's session has expired; they need to sign in again."
	case http.StatusForbidden:
		return fmt.Sprintf("The person asking doesn't have access to usage in project %s. Usage needs the "+
			"billing viewer role, which the organization's Viewer, Editor and Owner roles include.", project)
	case http.StatusServiceUnavailable:
		return "The usage service is temporarily unavailable; try again shortly."
	}
	return fmt.Sprintf("Reading usage failed: %s", apiErr.Error())
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func errorResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

func jsonResult(output any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to format results: %v", err)), nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}

// recoverMiddleware turns a panic in an MCP handler into an error instead
// of a crash: the SDK runs handlers on its own goroutines without a recover,
// so one bad record could otherwise take down every caller's request.
func recoverMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
		defer func() {
			if r := recover(); r != nil {
				klog.ErrorS(fmt.Errorf("panic: %v", r), "MCP handler panicked", "method", method, "stack", string(debug.Stack()))
				if method == "tools/call" {
					result, err = errorResult("Internal error while running this tool. The failure has been logged."), nil
					return
				}
				result, err = nil, fmt.Errorf("internal error handling %s", method)
			}
		}()
		return next(ctx, method, req)
	}
}
