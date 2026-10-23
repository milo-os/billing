// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/rest"
)

// fakeMilo stands in for the Milo API: it records UsageQuery requests and
// answers with a small catalog and one meter's usage.
type fakeMilo struct {
	mu           sync.Mutex
	queryPaths   []string
	queryAuth    []string
	querySpecs   []map[string]any
	forbidden    bool
	paymentTerms bool
	// unbound leaves the project without a billing account binding.
	unbound bool
}

func (f *fakeMilo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/apis/billing.miloapis.com/v1alpha1/meterdefinitions":
		_, _ = io.WriteString(w, `{"items":[
			{"metadata":{"name":"egress"},"spec":{"meterName":"networking.datumapis.com/gateway/egress-bytes","displayName":"ALB Egress","phase":"Published","measurement":{"unit":"By","aggregation":"Sum","dimensions":["region"]}},"status":{"systemDimensions":["project_name"]}},
			{"metadata":{"name":"old"},"spec":{"meterName":"compute.datumapis.com/instance/old","displayName":"Old","phase":"Deprecated","measurement":{"unit":"s","aggregation":"Sum"}}}
		]}`)
	case r.URL.Path == "/apis/resourcemanager.miloapis.com/v1alpha1/projects/web":
		_, _ = io.WriteString(w, `{"spec":{"ownerRef":{"kind":"Organization","name":"acme"}}}`)
	case strings.HasSuffix(r.URL.Path, "/billingaccountbindings"):
		if !f.paymentTerms {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"forbidden"}`)
			return
		}
		if f.unbound {
			_, _ = io.WriteString(w, `{"items":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"spec":{"projectRef":{"name":"web"},"billingAccountRef":{"name":"acme-main"}},"status":{"phase":"Active"}}]}`)
	case strings.HasSuffix(r.URL.Path, "/billingaccounts/acme-main"):
		_, _ = io.WriteString(w, `{"metadata":{"name":"acme-main","annotations":{"kubernetes.io/display-name":"Acme"}},
			"spec":{"currencyCode":"USD","defaultPaymentMethodRef":{"name":"pm-card"},"paymentTerms":{"invoiceFrequency":"Monthly","invoiceDayOfMonth":1,"netDays":30},
			        "contactInfo":{"email":"billing@acme.example","address":{"line1":"1 Main St"}}},
			"status":{"phase":"Ready","linkedProjectsCount":2,"conditions":[{"type":"Ready","status":"True","reason":"Ready","message":"Billing account is active."}]}}`)
	case strings.HasSuffix(r.URL.Path, "/paymentmethods"):
		_, _ = io.WriteString(w, `{"items":[
			{"metadata":{"name":"pm-card"},"spec":{"billingAccountRef":{"name":"acme-main"},"displayName":"Company card"},
			 "status":{"phase":"Active","details":{"type":"card","card":{"brand":"visa","last4":"4242","expiryMonth":10,"expiryYear":2030,"issuerIdentificationNumber":"424242","country":"US","billingAddress":{"line1":"1 Main St"}}}}},
			{"metadata":{"name":"pm-other-account"},"spec":{"billingAccountRef":{"name":"other"},"displayName":"Other"},"status":{"phase":"Active"}}
		]}`)
	case strings.HasSuffix(r.URL.Path, "/invoices"):
		_, _ = io.WriteString(w, `{"items":[
			{"metadata":{"name":"acme-main-2026-08"},"spec":{"billingAccountRef":{"name":"acme-main"},"period":{"start":"2026-08-01T00:00:00Z","end":"2026-09-01T00:00:00Z"}},
			 "status":{"phase":"PastDue","currencyCode":"USD","total":"99.00","amountDue":"99.00","documentUri":"payments/invoices/legacy"}},
			{"metadata":{"name":"acme-main-2026-09","annotations":{"openmeter.miloapis.com/invoiceId":"inv_9"}},"spec":{"billingAccountRef":{"name":"acme-main"},"period":{"start":"2026-09-01T00:00:00Z","end":"2026-10-01T00:00:00Z"}},
			 "status":{"phase":"Paid","currencyCode":"USD","total":"12.50","amountPaid":"12.50","amountDue":"0","paidAt":"2026-10-03T00:00:00Z","documentUri":"https://invoice.stripe.com/i/acct_1/test_9"}}
		]}`)
	case strings.HasSuffix(r.URL.Path, "/billingentitlements"):
		_, _ = io.WriteString(w, `{"items":[{"spec":{"billingAccountRef":{"name":"acme-main"},"offerRef":{"name":"payg-4"}}}]}`)
	case strings.HasSuffix(r.URL.Path, "/billingaccounts"):
		_, _ = io.WriteString(w, `{"items":[{"metadata":{"name":"acme-main"},"spec":{"paymentTerms":{"invoiceFrequency":"Monthly","invoiceDayOfMonth":15}}}]}`)
	case strings.HasSuffix(r.URL.Path, "/usagequeries"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.queryPaths = append(f.queryPaths, r.URL.Path)
		f.queryAuth = append(f.queryAuth, r.Header.Get("Authorization"))
		f.querySpecs = append(f.querySpecs, body["spec"].(map[string]any))
		f.mu.Unlock()
		if f.forbidden {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"usagequeries is forbidden"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":{"spend":{"amount":"0.05","currency":"USD"},"meters":[
			{"meterDefinition":"egress","meterName":"networking.datumapis.com/gateway/egress-bytes","unit":"By","aggregation":"Sum",
			 "spend":{"amount":"0.05","currency":"USD"},
			 "groupings":[{"series":[{"total":"283115520","points":[{"start":"2026-10-02T00:00:00Z","end":"2026-10-03T00:00:00Z","value":"283115520"}]}]},
			              {"groupBy":["region"],"series":[{"group":{"region":"us-east-1"},"total":"283115520"}]}]}
		]}}`)
	default:
		http.NotFound(w, r)
	}
}

// headerTransport adds the headers Patch sends with every MCP request.
type headerTransport struct {
	headers map[string]string
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func setup(t *testing.T, headers map[string]string) (*fakeMilo, *mcp.ClientSession, string) {
	t.Helper()
	milo := &fakeMilo{}
	miloServer := httptest.NewServer(milo)
	t.Cleanup(miloServer.Close)

	handler, err := NewHandler(Options{
		BaseConfig: &rest.Config{Host: miloServer.URL},
		Now:        func() time.Time { return time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   server.URL + MCPPath,
		HTTPClient: &http.Client{Transport: headerTransport{headers: headers}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return milo, session, server.URL
}

var asCaller = map[string]string{"Authorization": "Bearer caller-token", ProjectHeader: "web"}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func TestListsTheTools(t *testing.T) {
	_, session, _ := setup(t, asCaller)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s should be marked read-only", tool.Name)
		}
	}
	for _, want := range Tools {
		if !got[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}

func TestUsageSummaryReadsTheProjectAsTheCaller(t *testing.T) {
	milo, session, _ := setup(t, asCaller)
	text, isErr := call(t, session, ToolUsageSummary, nil)
	if isErr {
		t.Fatalf("error: %s", text)
	}

	if len(milo.queryPaths) != 1 || milo.queryPaths[0] != "/apis/resourcemanager.miloapis.com/v1alpha1/projects/web/control-plane/apis/usage.billing.miloapis.com/v1alpha1/usagequeries" {
		t.Errorf("query paths = %v", milo.queryPaths)
	}
	if milo.queryAuth[0] != "Bearer caller-token" {
		t.Errorf("query sent as %q, want the caller's token", milo.queryAuth[0])
	}
	if milo.querySpecs[0]["includeSpend"] != true {
		t.Errorf("spec = %v", milo.querySpecs[0])
	}

	var report toolReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("not a report: %v\n%s", err, text)
	}
	if report.Scope != "project web" || len(report.Meters) != 1 || report.Meters[0].Display != "270 MB" || report.Spend.Display != "$0.05" {
		t.Errorf("report = %+v", report)
	}
	// The binding couldn't be read, so the cycle falls back to calendar
	// months, and the notes say so.
	if report.Period.Label != "1 Oct - 1 Nov 2026" || len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "calendar months") {
		t.Errorf("period = %+v, notes = %v", report.Period, report.Notes)
	}
}

func TestUsageSummaryUsesTheBillingAccountsCycle(t *testing.T) {
	milo, session, _ := setup(t, asCaller)
	milo.paymentTerms = true
	text, _ := call(t, session, ToolUsageSummary, map[string]any{"cycle": "previous"})

	var report toolReport
	_ = json.Unmarshal([]byte(text), &report)
	if report.Period.Label != "15 Sep - 15 Oct 2026" || len(report.Notes) != 0 {
		t.Errorf("period = %+v, notes = %v", report.Period, report.Notes)
	}
}

func TestUsageBreakdownAndDaily(t *testing.T) {
	milo, session, _ := setup(t, asCaller)

	text, isErr := call(t, session, ToolUsageBreakdown, map[string]any{"dimension": "region"})
	if isErr || !strings.Contains(text, `"us-east-1"`) {
		t.Errorf("breakdown: %s", text)
	}
	if g := milo.querySpecs[0]["groupings"].([]any); len(g) != 2 {
		t.Errorf("groupings = %v", g)
	}

	text, isErr = call(t, session, ToolUsageDaily, map[string]any{"meter": "ALB Egress", "from": "2026-10-01", "to": "2026-10-05"})
	if isErr || !strings.Contains(text, `"2026-10-02"`) {
		t.Errorf("daily: %s", text)
	}
	if milo.querySpecs[1]["granularity"] != "Day" {
		t.Errorf("spec = %v", milo.querySpecs[1])
	}
}

func TestBadArgumentsAreToolErrors(t *testing.T) {
	_, session, _ := setup(t, asCaller)
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{ToolUsageBreakdown, map[string]any{"dimension": ""}, "dimension is required"},
		{ToolUsageBreakdown, map[string]any{"dimension": "regoin"}, `No meter has a dimension named "regoin"`},
		{ToolUsageSummary, map[string]any{"cycle": "next"}, "unknown cycle"},
		{ToolUsageSummary, map[string]any{"from": "2026-10-15", "to": "2026-10-01"}, "to must be after from"},
	}
	for _, c := range cases {
		text, isErr := call(t, session, c.tool, c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s %v: got %q (error=%v), want %q", c.tool, c.args, text, isErr, c.want)
		}
	}
}

func TestBreakdownRequiresADimension(t *testing.T) {
	_, session, _ := setup(t, asCaller)
	// The schema marks dimension required, so the SDK rejects the call.
	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolUsageBreakdown})
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Errorf("got %v", err)
	}
}

func TestForbiddenExplainsTheRole(t *testing.T) {
	milo, session, _ := setup(t, asCaller)
	milo.forbidden = true
	text, isErr := call(t, session, ToolUsageSummary, nil)
	if !isErr || !strings.Contains(text, "billing viewer role") {
		t.Errorf("got %q", text)
	}
}

func TestMissingCallerContextIsTheClientsFault(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"no token":        {ProjectHeader: "web"},
		"no project":      {"Authorization": "Bearer caller-token"},
		"path in project": {"Authorization": "Bearer caller-token", ProjectHeader: "web/../acme"},
	} {
		_, session, _ := setup(t, headers)
		text, isErr := call(t, session, ToolUsageSummary, nil)
		if !isErr || !strings.Contains(text, "configuration problem") {
			t.Errorf("%s: got %q", name, text)
		}
	}
}

func TestBillingAccountTools(t *testing.T) {
	milo, session, _ := setup(t, asCaller)
	milo.paymentTerms = true

	text, isErr := call(t, session, ToolGetBillingAccount, nil)
	if isErr {
		t.Fatalf("get_billing_account: %s", text)
	}
	for _, want := range []string{`"displayName": "Acme"`, `"plan": "payg-4"`, `"netDays": 30`, `"last4": "4242"`, `"default": true`} {
		if !strings.Contains(text, want) {
			t.Errorf("get_billing_account is missing %s:\n%s", want, text)
		}
	}
	// Contact details and card internals never reach the model.
	for _, leak := range []string{"billing@acme.example", "1 Main St", "424242", "issuerIdentificationNumber"} {
		if strings.Contains(text, leak) {
			t.Errorf("get_billing_account leaks %q:\n%s", leak, text)
		}
	}

	text, isErr = call(t, session, ToolListPaymentMethods, nil)
	if isErr || !strings.Contains(text, "pm-card") || strings.Contains(text, "pm-other-account") || strings.Contains(text, "1 Main St") {
		t.Errorf("list_payment_methods: %s", text)
	}

	text, isErr = call(t, session, ToolListInvoices, nil)
	if isErr || !strings.Contains(text, "acme-main-2026-09") || strings.Contains(text, "acme-main-2026-08") {
		t.Errorf("list_invoices should show only OpenMeter invoices: %s", text)
	}
	if !strings.Contains(text, "https://invoice.stripe.com/i/acct_1/test_9") || !strings.Contains(text, `"total": "$12.50"`) {
		t.Errorf("list_invoices: %s", text)
	}

	text, isErr = call(t, session, ToolListInvoices, map[string]any{"status": "Unpaid"})
	if !isErr || !strings.Contains(text, "Unknown status") {
		t.Errorf("bad status: %s", text)
	}
}

func TestBillingAccountToolsExplainMissingAccess(t *testing.T) {
	milo, session, _ := setup(t, asCaller)
	// The organization's billing resources are forbidden to this caller.
	text, isErr := call(t, session, ToolListInvoices, nil)
	if !isErr || !strings.Contains(text, "organization role") {
		t.Errorf("forbidden: %s", text)
	}

	milo.paymentTerms, milo.unbound = true, true
	text, isErr = call(t, session, ToolGetBillingAccount, nil)
	if !isErr || !strings.Contains(text, "isn't bound to a billing account") {
		t.Errorf("unbound: %s", text)
	}
}

func TestListMetersLeavesOutDeprecated(t *testing.T) {
	_, session, _ := setup(t, asCaller)
	text, isErr := call(t, session, ToolListMeters, nil)
	if isErr || !strings.Contains(text, "ALB Egress") || strings.Contains(text, `"Old"`) {
		t.Errorf("got %s", text)
	}
	if !strings.Contains(text, `"project_name"`) {
		t.Errorf("dimensions should include system dimensions: %s", text)
	}
}

func TestServesKnowledgeAndRunbooks(t *testing.T) {
	_, _, base := setup(t, asCaller)
	for path, want := range map[string]int{
		"/llms-full.txt":             http.StatusOK,
		"/runbooks/explain-spend.md": http.StatusOK,
		"/runbooks/usage-spike.md":   http.StatusOK,
		"/runbooks/missing.md":       http.StatusNotFound,
		HealthzPath:                  http.StatusOK,
	} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestCheckBaseConfig(t *testing.T) {
	for host, ok := range map[string]bool{
		"https://api.example.com":           true,
		"api.example.com":                   true,
		"":                                  false,
		"https://user:pw@api.example.com":   false,
		"https://api.example.com/some/path": false,
	} {
		err := CheckBaseConfig(&rest.Config{Host: host})
		if (err == nil) != ok {
			t.Errorf("%q: err = %v", host, err)
		}
	}
}
