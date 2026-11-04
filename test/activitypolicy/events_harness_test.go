// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

// This harness evaluates the eventRules of billing's ActivityPolicies the
// way the Activity service does: rules in order, first match wins, and
// summaries are templates of {{ CEL }} expressions over the Event. Billing's
// e2e cluster doesn't run the Activity service, so these tests are how a
// policy is checked against sample events before it ships.

import (
	"fmt"
	"os"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"sigs.k8s.io/yaml"
)

const eventPolicyDir = "../../config/components/activity-policies/"

type eventRule struct {
	Name    string `json:"name"`
	Match   string `json:"match"`
	Summary string `json:"summary"`
}

type eventPolicy struct {
	Spec struct {
		Resource struct {
			APIGroup string `json:"apiGroup"`
			Kind     string `json:"kind"`
		} `json:"resource"`
		EventRules []eventRule `json:"eventRules"`
	} `json:"spec"`
}

func loadEventPolicy(t *testing.T, name string) eventPolicy {
	t.Helper()
	raw, err := os.ReadFile(eventPolicyDir + name)
	if err != nil {
		t.Fatal(err)
	}
	var p eventPolicy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// link records what link() was called with, as the portal would render it.
type link struct {
	text     string
	resource map[string]any
}

// linkFunc renders link() as its display text and, like the Activity
// service, records the resource it points at.
func linkFunc(links *[]link) cel.EnvOption {
	return cel.Function("link",
		cel.Overload("link_string_dyn", []*cel.Type{cel.StringType, cel.DynType}, cel.StringType,
			cel.BinaryBinding(func(text, resource ref.Val) ref.Val {
				s := fmt.Sprintf("%v", text.Value())
				if links != nil {
					*links = append(*links, link{text: s, resource: linkResource(resource.Value())})
				}
				return types.String(s)
			}),
		),
	)
}

func linkResource(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[ref.Val]ref.Val:
		out := make(map[string]any, len(m))
		for k, v := range m {
			if key, ok := k.Value().(string); ok {
				out[key] = v.Value()
			}
		}
		return out
	}
	return nil
}

func eventEnv(t *testing.T, links *[]link) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("actor", cel.StringType),
		cel.Variable("actorRef", cel.MapType(cel.StringType, cel.DynType)),
		linkFunc(links),
	)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func eval(t *testing.T, env *cel.Env, expr string, vars map[string]any) (any, error) {
	t.Helper()
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile %q: %v", expr, iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program %q: %v", expr, err)
	}
	out, _, err := prg.Eval(vars)
	if err != nil {
		return nil, err
	}
	return out.Value(), nil
}

// firstMatch returns the name and rendered summary of the first rule that
// matches. A match or summary that errors fails the test, because the
// Activity service sends that message to the dead letter queue instead of
// retrying it.
func firstMatch(t *testing.T, env *cel.Env, rules []eventRule, vars map[string]any) (string, string) {
	t.Helper()
	for _, r := range rules {
		matched, err := eval(t, env, r.Match, vars)
		if err != nil {
			t.Fatalf("rule %s: match: %v", r.Name, err)
		}
		if matched != true {
			continue
		}
		var failed error
		summary := summaryBlockRE.ReplaceAllStringFunc(r.Summary, func(m string) string {
			v, err := eval(t, env, summaryBlockRE.FindStringSubmatch(m)[1], vars)
			if err != nil {
				failed = err
			}
			return fmt.Sprintf("%v", v)
		})
		if failed != nil {
			t.Fatalf("rule %s: summary: %v", r.Name, failed)
		}
		return r.Name, summary
	}
	return "", ""
}

// summarizeEvent evaluates the policy's event rules against an event and
// returns the matched rule, the summary and the resources it links to.
func summarizeEvent(t *testing.T, p eventPolicy, event map[string]any) (string, string, []link) {
	t.Helper()
	vars := map[string]any{
		"event":    event,
		"actor":    "billing-controller",
		"actorRef": map[string]any{"type": "controller", "name": "billing-controller"},
	}
	var links []link
	rule, summary := firstMatch(t, eventEnv(t, &links), p.Spec.EventRules, vars)
	return rule, summary, links
}

// billingEvent builds a core/v1 Event about a billing resource as the
// Activity service sees it: the recorder's involvedObject, also exposed as
// regarding, and the detail in annotations. Annotation keys are given
// without the event.billing.miloapis.com/ prefix.
func billingEvent(kind, name, reason string, detail map[string]string) map[string]any {
	annotations := map[string]any{
		"platform.miloapis.com/scope.type": "Organization",
		"platform.miloapis.com/scope.name": "acme",
	}
	for k, v := range detail {
		annotations["event.billing.miloapis.com/"+k] = v
	}
	object := map[string]any{
		"apiVersion": "billing.miloapis.com/v1alpha1",
		"kind":       kind,
		"name":       name,
		"namespace":  "organization-acme",
		"uid":        "3f0c6a1e-0000-4000-8000-000000000001",
	}
	return map[string]any{
		"metadata":       map[string]any{"name": name + ".18a1", "namespace": "organization-acme", "annotations": annotations},
		"involvedObject": object,
		"regarding":      object,
		"reason":         reason,
		"type":           "Normal",
		"message":        "fallback message",
		"source":         map[string]any{"component": "billing-controller"},
	}
}

// withoutAnnotations drops the event's annotations entirely, as an Event
// from an emitter that sets none arrives.
func withoutAnnotations(event map[string]any) map[string]any {
	delete(event["metadata"].(map[string]any), "annotations")
	return event
}

func checkEventSummary(t *testing.T, p eventPolicy, event map[string]any, wantRule, wantSummary string) []link {
	t.Helper()
	gotRule, gotSummary, links := summarizeEvent(t, p, event)
	if gotRule != wantRule {
		t.Errorf("rule = %q, want %q", gotRule, wantRule)
	}
	if gotSummary != wantSummary {
		t.Errorf("summary =\n  %q\nwant\n  %q", gotSummary, wantSummary)
	}
	return links
}

func checkLinks(t *testing.T, links []link, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, l := range links {
		got[l.text] = fmt.Sprintf("%v/%v/%v", l.resource["resource"], l.resource["namespace"], l.resource["name"])
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("links = %v, want %v", got, want)
	}
}
