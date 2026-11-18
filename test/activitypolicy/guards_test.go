// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

// Repository guards for billing's ActivityPolicies. What each rule matches and
// renders is tested with activitytool in test/activity-policies (task
// test:activity-policies); these tests cover what activitytool cannot see:
// rules added later, billing's Go constants, and the policies' kustomization.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const policiesGlob = "../../config/components/activity-policies/*-policy.yaml"

type rule struct {
	Name  string `json:"name"`
	Match string `json:"match"`
}

type activityPolicy struct {
	file     string
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		AuditRules []rule `json:"auditRules"`
		EventRules []rule `json:"eventRules"`
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
		pol.file = filepath.Base(p)
		policies = append(policies, pol)
	}
	return policies
}

var writeVerbRE = regexp.MustCompile(`audit\.verb\s*(==\s*'(create|update|patch|delete)'|in\s*\[[^\]]*'(create|update|patch|delete)')`)

// TestWriteRulesGateOnOutcome checks every write rule, including ones added
// later, for the two conditions the activity processor does not apply for
// it: the request succeeded (2xx) and was not a dry run. Without them a
// rejected or dry-run request shows up as a change that never happened.
func TestWriteRulesGateOnOutcome(t *testing.T) {
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			if !writeVerbRE.MatchString(r.Match) {
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

// TestEventRulesUseDefinedReasons ties each policy's event rules to the reason
// constants billing emits, so renaming a constant fails here instead of
// silently orphaning the rule.
func TestEventRulesUseDefinedReasons(t *testing.T) {
	byFile := map[string][]string{
		"invoice-policy.yaml": {
			billingv1alpha1.EventReasonInvoiceIssued,
			billingv1alpha1.EventReasonInvoicePaymentSucceeded,
			billingv1alpha1.EventReasonInvoicePaymentFailed,
			billingv1alpha1.EventReasonInvoicePaymentActionRequired,
			billingv1alpha1.EventReasonInvoicePartiallyPaid,
			billingv1alpha1.EventReasonInvoicePastDue,
			billingv1alpha1.EventReasonInvoiceVoided,
			billingv1alpha1.EventReasonInvoiceCurrencyMismatch,
		},
		"paymentmethod-policy.yaml": {
			billingv1alpha1.EventReasonPaymentMethodAttached,
			billingv1alpha1.EventReasonPaymentMethodFailed,
			billingv1alpha1.EventReasonPaymentMethodExpiringSoon,
			billingv1alpha1.EventReasonPaymentMethodExpired,
		},
		"billingaccount-policy.yaml": {
			billingv1alpha1.EventReasonDefaultPaymentMethodChanged,
			billingv1alpha1.EventReasonInvoicingPastDue,
			billingv1alpha1.EventReasonInvoicingCurrent,
		},
		"billingarrangement-policy.yaml": {
			billingv1alpha1.EventReasonPaymentTermsEnded,
		},
	}
	policies := map[string]activityPolicy{}
	for _, pol := range loadPolicies(t) {
		policies[pol.file] = pol
	}
	for file, reasons := range byFile {
		pol, ok := policies[file]
		if !ok {
			t.Errorf("%s: policy file not found", file)
			continue
		}
		for _, reason := range reasons {
			if !matchesReason(pol.Spec.EventRules, reason) {
				t.Errorf("%s: no event rule matches reason %q", file, reason)
			}
		}
	}
}

// matchesReason reports whether some rule's match names the reason as a
// quoted CEL string.
func matchesReason(rules []rule, reason string) bool {
	for _, r := range rules {
		if strings.Contains(r.Match, "'"+reason+"'") || strings.Contains(r.Match, `"`+reason+`"`) {
			return true
		}
	}
	return false
}
