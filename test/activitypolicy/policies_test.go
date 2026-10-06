// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types/ref"
	"sigs.k8s.io/yaml"
)

// These tests guard the ActivityPolicy audit rules shipped from this repository.
// The activity processor applies no global filter, so each write rule must:
//
//   - match only requests that succeeded (2xx); a rejected request otherwise
//     shows up as a change that never happened
//   - skip dry runs (?dryRun=All), which succeed but persist nothing
//   - evaluate without error for every request shape the API server records,
//     because the processor stops at the first rule that errors and sends the
//     event to the dead-letter queue instead of the feed
const policiesGlob = "../../config/components/activity-policies/*-policy.yaml"

type auditRule struct {
	Name    string `json:"name"`
	Match   string `json:"match"`
	Summary string `json:"summary"`
}

type activityPolicy struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		AuditRules []auditRule `json:"auditRules"`
	} `json:"spec"`
}

func loadPolicies(t *testing.T) []activityPolicy {
	t.Helper()
	paths, err := filepath.Glob(policiesGlob)
	if err != nil {
		t.Fatalf("glob %q: %v", policiesGlob, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no policy files matched %q", policiesGlob)
	}
	policies := make([]activityPolicy, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var pol activityPolicy
		if err := yaml.Unmarshal(b, &pol); err != nil {
			t.Fatalf("unmarshal %s: %v", p, err)
		}
		policies = append(policies, pol)
	}
	return policies
}

var writeVerbRE = regexp.MustCompile(`audit\.verb\s*(==\s*'(create|update|patch|delete)'|in\s*\[[^\]]*'(create|update|patch|delete)')`)

// verbOf returns the first write verb a rule's match targets, or "".
func verbOf(match string) string {
	m := writeVerbRE.FindStringSubmatch(match)
	if m == nil {
		return ""
	}
	if m[2] != "" {
		return m[2]
	}
	return m[3]
}

// newEnv mirrors the activity processor's audit environment.
func newEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("audit", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	return env
}

func evalMatch(t *testing.T, env *cel.Env, r auditRule, audit map[string]any) bool {
	t.Helper()
	ast, iss := env.Compile(r.Match)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile %s: %v", r.Name, iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program %s: %v", r.Name, err)
	}
	out, _, err := prg.Eval(map[string]any{"audit": withDefaults(audit)})
	if err != nil {
		t.Errorf("%s errored (the event would go to the DLQ): %v", r.Name, err)
		return false
	}
	b, ok := out.Value().(bool)
	if !ok {
		t.Fatalf("%s did not evaluate to bool, got %T", r.Name, out.Value())
	}
	return b
}

// withDefaults mirrors the processor's BuildAuditVars, which sets absent
// top-level objects to empty maps before evaluating a rule.
func withDefaults(audit map[string]any) map[string]any {
	out := make(map[string]any, len(audit))
	for k, v := range audit {
		out[k] = v
	}
	for _, field := range []string{"objectRef", "user", "responseStatus", "responseObject", "requestObject"} {
		if _, ok := out[field]; !ok {
			out[field] = map[string]any{}
		}
	}
	return out
}

func auditEvent(verb string, code int, uri string, request any) map[string]any {
	audit := map[string]any{
		"verb":           verb,
		"user":           map[string]any{"username": "alice@example.com"},
		"objectRef":      map[string]any{"name": "obj-1", "namespace": "default"},
		"requestURI":     uri,
		"responseStatus": map[string]any{"code": code},
	}
	if request != nil {
		audit["requestObject"] = request
	}
	if code >= 200 && code < 300 {
		audit["responseObject"] = map[string]any{
			"metadata": map[string]any{"name": "obj-1", "namespace": "default"},
			"spec":     map[string]any{},
			"status":   map[string]any{},
		}
	} else {
		audit["responseObject"] = map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": code}
	}
	return audit
}

const objectURI = "/apis/example.miloapis.com/v1alpha1/namespaces/default/objects/obj-1"

func successCode(verb string) int {
	if verb == "create" {
		return 201
	}
	return 200
}

// Structural guard: every write rule names the 2xx and dry-run conditions.
func TestWriteRulesGateOnOutcome(t *testing.T) {
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			if verbOf(r.Match) == "" {
				continue
			}
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				if !strings.Contains(r.Match, "has(audit.responseStatus.code) && audit.responseStatus.code >= 200") || !strings.Contains(r.Match, "audit.responseStatus.code < 300") {
					t.Errorf("not gated on a 2xx response:\n  %s", r.Match)
				}
				if !strings.Contains(r.Match, "audit.requestURI.contains('dryRun=')") {
					t.Errorf("does not skip dry-run requests:\n  %s", r.Match)
				}
			})
		}
	}
}

// Semantic guard: no write rule matches a rejected or dry-run request.
func TestWriteRulesIgnoreFailedAndDryRunRequests(t *testing.T) {
	env := newEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			verb := verbOf(r.Match)
			if verb == "" {
				continue
			}
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				request := map[string]any{"spec": map[string]any{}}
				for _, code := range []int{400, 403, 404, 409, 422, 500} {
					if evalMatch(t, env, r, auditEvent(verb, code, objectURI, request)) {
						t.Errorf("matched a rejected %s (code %d)", verb, code)
					}
				}
				if evalMatch(t, env, r, auditEvent(verb, successCode(verb), objectURI+"?dryRun=All", request)) {
					t.Errorf("matched a dry-run %s", verb)
				}
			})
		}
	}
}

// Shape guard: no rule errors on the request bodies the API server records for
// a successful write: JSON Patch arrays, DeleteOptions, Status delete responses,
// and Metadata-level events with no bodies at all.
func TestAuditRulesTolerateRequestShapes(t *testing.T) {
	env := newEnv(t)
	shapes := []struct {
		name, verb string
		request    any
		response   any
		noResponse bool
	}{
		{name: "JSON Patch", verb: "patch", request: []any{map[string]any{"op": "replace", "path": "/spec/x", "value": 1}}},
		{name: "JSON Patch on metadata", verb: "patch", request: []any{map[string]any{"op": "add", "path": "/metadata/labels/a", "value": "b"}}},
		{name: "metadata-level patch", verb: "patch", noResponse: true},
		{name: "metadata-level update", verb: "update", noResponse: true},
		{name: "delete with DeleteOptions", verb: "delete", request: map[string]any{"kind": "DeleteOptions", "apiVersion": "v1"}},
		{name: "delete returning Status", verb: "delete", response: map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Success", "details": map[string]any{"name": "obj-1"}}},
		{name: "metadata-level delete", verb: "delete", noResponse: true},
	}
	for _, pol := range loadPolicies(t) {
		for _, s := range shapes {
			t.Run(pol.Metadata.Name+"/"+s.name, func(t *testing.T) {
				audit := auditEvent(s.verb, successCode(s.verb), objectURI, s.request)
				switch {
				case s.noResponse:
					delete(audit, "responseObject")
				case s.response != nil:
					audit["responseObject"] = s.response
				}
				// The processor stops at the first match or error; mirror that.
				for _, r := range pol.Spec.AuditRules {
					if evalMatch(t, env, r, audit) {
						return
					}
				}
			})
		}
	}
}

// summaryBlockRE mirrors the processor's {{ expression }} template matcher.
var summaryBlockRE = regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`)

// newSummaryEnv mirrors the processor's audit environment for summaries,
// including the link() function, which renders as its display text.
func newSummaryEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(
		cel.Variable("audit", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("actor", cel.StringType),
		cel.Variable("actorRef", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("kind", cel.StringType),
		cel.Function("link",
			cel.Overload("link_string_dyn",
				[]*cel.Type{cel.StringType, cel.DynType},
				cel.StringType,
				cel.BinaryBinding(func(text, _ ref.Val) ref.Val { return text }),
			),
		),
	)
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	return env
}

// renderSummary renders a summary template the way the processor does. Any
// block that fails to compile or evaluate fails the test: in production
// that sends the event to the dead-letter queue.
func renderSummary(t *testing.T, env *cel.Env, r auditRule, audit map[string]any) string {
	t.Helper()
	vars := map[string]any{
		"audit":    withDefaults(audit),
		"actor":    "alice@example.com",
		"actorRef": map[string]any{"type": "user", "name": "alice@example.com"},
		"kind":     "",
	}
	return summaryBlockRE.ReplaceAllStringFunc(r.Summary, func(block string) string {
		expr := summaryBlockRE.FindStringSubmatch(block)[1]
		ast, iss := env.Compile(expr)
		if iss != nil && iss.Err() != nil {
			t.Fatalf("%s: compile {{ %s }}: %v", r.Name, expr, iss.Err())
		}
		prg, err := env.Program(ast)
		if err != nil {
			t.Fatalf("%s: program: %v", r.Name, err)
		}
		out, _, err := prg.Eval(vars)
		if err != nil {
			t.Fatalf("%s: eval {{ %s }}: %v", r.Name, expr, err)
		}
		return fmt.Sprintf("%v", out.Value())
	})
}

// Every summary block must compile and return a string (or dyn), which is
// what the activity apiserver checks when the policy is applied.
func TestSummariesCompile(t *testing.T) {
	env := newSummaryEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				if strings.Count(r.Summary, "{{") != strings.Count(r.Summary, "}}") {
					t.Fatalf("unbalanced template delimiters")
				}
				for _, m := range summaryBlockRE.FindAllStringSubmatch(r.Summary, -1) {
					ast, iss := env.Compile(m[1])
					if iss != nil && iss.Err() != nil {
						t.Fatalf("compile {{ %s }}: %v", m[1], iss.Err())
					}
					if !ast.OutputType().IsExactType(cel.StringType) && !ast.OutputType().IsExactType(cel.DynType) {
						t.Errorf("{{ %s }} returns %v, want string", m[1], ast.OutputType())
					}
				}
			})
		}
	}
}

func TestBillingArrangementSummaries(t *testing.T) {
	var policy activityPolicy
	for _, pol := range loadPolicies(t) {
		if pol.Metadata.Name == "billing.miloapis.com-billingarrangement" {
			policy = pol
		}
	}
	if len(policy.Spec.AuditRules) == 0 {
		t.Fatal("billingarrangement policy not found")
	}

	stored := func(spec map[string]any) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"name": "acme-terms", "namespace": "organization-acme"},
			"spec":     spec,
		}
	}
	invoiceSpec := func(extra map[string]any) map[string]any {
		spec := map[string]any{
			"billingAccountRef": map[string]any{"name": "acme"},
			"type":              "Invoice",
			// JSON numbers decode as float64, as they do in the processor.
			"invoice":  map[string]any{"netDays": float64(30), "creditLimit": "10000", "purchaseOrderNumber": "PO-4471"},
			"startsAt": "2026-10-06T10:00:00Z",
			"reason":   "Internal note that must never appear",
		}
		for k, v := range extra {
			spec[k] = v
		}
		return spec
	}
	event := func(verb string, request any, response map[string]any) map[string]any {
		audit := auditEvent(verb, successCode(verb), "/apis/billing.miloapis.com/v1alpha1/namespaces/organization-acme/billingarrangements/acme-terms", request)
		audit["objectRef"] = map[string]any{"name": "acme-terms", "namespace": "organization-acme", "apiGroup": "billing.miloapis.com", "resource": "billingarrangements"}
		audit["requestReceivedTimestamp"] = "2026-10-06T10:00:00.123456Z"
		if response != nil {
			audit["responseObject"] = response
		}
		return audit
	}

	tests := []struct {
		name  string
		audit map[string]any
		want  string
	}{
		{
			name:  "grant open-ended terms",
			audit: event("create", stored(invoiceSpec(nil)), stored(invoiceSpec(nil))),
			want:  "alice@example.com granted invoice terms to billing account acme: net 30, credit limit 10000, PO PO-4471",
		},
		{
			name: "grant scheduled terms with an end date",
			audit: event("create", nil, stored(invoiceSpec(map[string]any{
				"startsAt": "2026-11-01T00:00:00Z",
				"endsAt":   "2027-09-30T00:00:00Z",
			}))),
			want: "alice@example.com granted invoice terms to billing account acme: net 30, credit limit 10000, PO PO-4471, starting 1 November 2026, until 30 September 2027",
		},
		{
			name:  "end terms now",
			audit: event("patch", map[string]any{"spec": map[string]any{"endsAt": "2026-10-06T10:00:00Z"}}, stored(invoiceSpec(nil))),
			want:  "alice@example.com ended invoice terms for billing account acme",
		},
		{
			name:  "set a future end date",
			audit: event("patch", map[string]any{"spec": map[string]any{"endsAt": "2027-03-31T00:00:00Z"}}, stored(invoiceSpec(nil))),
			want:  "alice@example.com set invoice terms for billing account acme to end on 31 March 2027",
		},
		{
			name: "change the credit limit",
			audit: event("patch", map[string]any{"spec": map[string]any{"invoice": map[string]any{"creditLimit": "20000"}}},
				stored(invoiceSpec(map[string]any{
					"invoice": map[string]any{"netDays": float64(45), "creditLimit": "20000"},
					"endsAt":  "2027-09-30T00:00:00Z",
				}))),
			want: "alice@example.com updated invoice terms for billing account acme: net 45, credit limit 20000, until 30 September 2027",
		},
		{
			name:  "clear the end date",
			audit: event("patch", map[string]any{"spec": map[string]any{"endsAt": nil}}, stored(invoiceSpec(nil))),
			want:  "alice@example.com updated invoice terms for billing account acme: net 30, credit limit 10000, PO PO-4471",
		},
		{
			name:  "JSON Patch edit",
			audit: event("patch", []any{map[string]any{"op": "replace", "path": "/spec/endsAt", "value": "2027-01-01T00:00:00Z"}}, stored(invoiceSpec(nil))),
			want:  "alice@example.com updated invoice terms for billing account acme: net 30, credit limit 10000, PO PO-4471",
		},
		{
			name:  "delete returning the object",
			audit: event("delete", nil, stored(invoiceSpec(nil))),
			want:  "alice@example.com deleted the payment terms record acme-terms for billing account acme",
		},
		{
			name:  "delete returning Status",
			audit: event("delete", nil, map[string]any{"kind": "Status", "status": "Success"}),
			want:  "alice@example.com deleted the payment terms record acme-terms",
		},
	}

	matchEnv := newEnv(t)
	summaryEnv := newSummaryEnv(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, r := range policy.Spec.AuditRules {
				if !evalMatch(t, matchEnv, r, tt.audit) {
					continue
				}
				got := renderSummary(t, summaryEnv, r, tt.audit)
				if got != tt.want {
					t.Errorf("rule %s rendered\n  %q\nwant\n  %q", r.Name, got, tt.want)
				}
				if strings.Contains(got, "Internal note") {
					t.Errorf("summary leaks spec.reason: %q", got)
				}
				return
			}
			t.Fatal("no rule matched")
		})
	}
}
