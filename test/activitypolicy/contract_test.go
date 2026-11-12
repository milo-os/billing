// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import "testing"

// summarizeAudit returns the first matching rule's name and summary, as the
// activity processor picks it.
func summarizeAudit(t *testing.T, policyName string, audit map[string]any) (string, string) {
	t.Helper()
	var policy activityPolicy
	for _, pol := range loadPolicies(t) {
		if pol.Metadata.Name == policyName {
			policy = pol
		}
	}
	if len(policy.Spec.AuditRules) == 0 {
		t.Fatalf("policy %s not found", policyName)
	}
	env, summaryEnv := newEnv(t), newSummaryEnv(t)
	for _, r := range policy.Spec.AuditRules {
		if evalMatch(t, env, r, audit) {
			return r.Name, renderSummary(t, summaryEnv, r, audit)
		}
	}
	return "", ""
}

// The organization's timeline: an account set up on a contract, its fees
// changed, switched to another offer, and removed.
func TestBillingEntitlementSummaries(t *testing.T) {
	stored := func(spec map[string]any) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"name": "acme-contract", "namespace": "organization-acme-corp"},
			"spec":     spec,
		}
	}
	plain := map[string]any{
		"billingAccountRef": map[string]any{"name": "acme"},
		"offerRef":          map[string]any{"name": "payg-v6"},
	}
	withContract := func(offer string) map[string]any {
		return map[string]any{
			"billingAccountRef": map[string]any{"name": "acme"},
			"offerRef":          map[string]any{"name": offer},
			"contract":          map[string]any{"startsAt": "2026-11-24T00:00:00Z"},
		}
	}
	event := func(verb string, request any, response map[string]any) map[string]any {
		audit := auditEvent(verb, successCode(verb), "/apis/billing.miloapis.com/v1alpha1/namespaces/organization-acme-corp/billingentitlements/acme-contract", request)
		audit["objectRef"] = map[string]any{"name": "acme-contract", "namespace": "organization-acme-corp", "apiGroup": "billing.miloapis.com", "resource": "billingentitlements"}
		audit["responseObject"] = response
		return audit
	}

	tests := []struct {
		name, rule, want string
		audit            map[string]any
	}{
		{
			name:  "set up a contract on an account without an entitlement",
			audit: event("create", stored(withContract("contract-acme-1")), stored(withContract("contract-acme-1"))),
			rule:  "create-contract",
			want:  "alice@example.com set up a contract for billing account acme on offer contract-acme-1, starting 24 November 2026",
		},
		{
			name: "move an account onto a contract",
			audit: event("patch",
				map[string]any{"spec": map[string]any{"offerRef": map[string]any{"name": "contract-acme-1"}, "contract": map[string]any{"startsAt": "2026-11-24T00:00:00Z"}}},
				stored(withContract("contract-acme-1"))),
			rule: "add-contract",
			want: "alice@example.com set up a contract for billing account acme on offer contract-acme-1, starting 24 November 2026",
		},
		{
			name: "change a contract's fees",
			audit: event("patch",
				map[string]any{"spec": map[string]any{"offerRef": map[string]any{"name": "contract-acme-2"}}},
				stored(withContract("contract-acme-2"))),
			rule: "change-contract-fees",
			want: "alice@example.com changed the contract fees for billing account acme: now on offer contract-acme-2",
		},
		{
			name: "switch an account without a contract to another offer",
			audit: event("patch",
				map[string]any{"spec": map[string]any{"offerRef": map[string]any{"name": "payg-v6"}}},
				stored(plain)),
			rule: "switch-offer",
			want: "alice@example.com switched billing account acme to offer payg-v6",
		},
		{
			name:  "put an account on an offer",
			audit: event("create", stored(plain), stored(plain)),
			rule:  "create",
			want:  "alice@example.com put billing account acme on offer payg-v6",
		},
		{
			name:  "remove a contract",
			audit: event("delete", nil, stored(withContract("contract-acme-1"))),
			rule:  "delete-contract",
			want:  "alice@example.com removed the contract and offer contract-acme-1 from billing account acme",
		},
		{
			name:  "remove an offer",
			audit: event("delete", nil, stored(plain)),
			rule:  "delete",
			want:  "alice@example.com removed offer payg-v6 from billing account acme",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, got := summarizeAudit(t, "billing.miloapis.com-billingentitlement", tc.audit)
			if rule != tc.rule || got != tc.want {
				t.Errorf("got %s: %q\nwant %s: %q", rule, got, tc.rule, tc.want)
			}
		})
	}
}

// Controllers' writes, such as service-catalog seeding the default offer,
// stay off the timeline.
func TestBillingEntitlementIgnoresControllers(t *testing.T) {
	audit := auditEvent("create", 201, objectURI, nil)
	audit["user"] = map[string]any{"username": "system:serviceaccount:service-catalog-system:service-catalog"}
	if rule, _ := summarizeAudit(t, "billing.miloapis.com-billingentitlement", audit); rule != "" {
		t.Errorf("matched %s, want no rule", rule)
	}
}

// The platform timeline: draft contracts saved, edited and removed, and the
// contract offers made for them.
func TestOfferSummaries(t *testing.T) {
	contractOffer := func(stage string, prices int, startsAt string) map[string]any {
		refs := make([]any, prices)
		for i := range refs {
			refs[i] = map[string]any{"name": "fee", "namespace": "milo-system"}
		}
		metadata := map[string]any{
			"name": "contract-acme-1",
			"labels": map[string]any{
				"billing.miloapis.com/contract-billing-account": "acme",
				"billing.miloapis.com/contract-organization":    "acme-corp",
			},
		}
		if startsAt != "" {
			metadata["annotations"] = map[string]any{"billing.miloapis.com/contract-starts-at": startsAt}
		}
		return map[string]any{
			"metadata": metadata,
			"spec":     map[string]any{"launchStage": stage, "servicePricingRefs": refs},
		}
	}
	event := func(verb string, request any, response map[string]any) map[string]any {
		audit := auditEvent(verb, successCode(verb), "/apis/billing.miloapis.com/v1alpha1/offers/contract-acme-1", request)
		audit["objectRef"] = map[string]any{"name": "contract-acme-1", "apiGroup": "billing.miloapis.com", "resource": "offers"}
		audit["responseObject"] = response
		return audit
	}
	platform := map[string]any{"metadata": map[string]any{"name": "payg-v7"}, "spec": map[string]any{"launchStage": "Draft"}}

	tests := []struct {
		name, rule, want string
		audit            map[string]any
	}{
		{
			name:  "save a draft contract with a start date",
			audit: event("create", nil, contractOffer("Draft", 5, "2026-11-24T00:00:00Z")),
			rule:  "create-contract-draft",
			want:  "alice@example.com saved a draft contract for billing account acme in organization acme-corp: 5 prices, starting 24 November 2026",
		},
		{
			name:  "save a draft fee change with one price",
			audit: event("create", nil, contractOffer("Draft", 1, "")),
			rule:  "create-contract-draft",
			want:  "alice@example.com saved a draft contract for billing account acme in organization acme-corp: 1 price",
		},
		{
			name:  "edit a draft contract",
			audit: event("patch", map[string]any{"spec": map[string]any{}}, contractOffer("Draft", 6, "")),
			rule:  "update-contract-draft",
			want:  "alice@example.com updated the draft contract for billing account acme in organization acme-corp: 6 prices",
		},
		{
			name:  "remove a draft contract",
			audit: event("delete", nil, contractOffer("Draft", 6, "")),
			rule:  "delete-contract-draft",
			want:  "alice@example.com removed the draft contract for billing account acme in organization acme-corp",
		},
		{
			name:  "create a contract offer",
			audit: event("create", nil, contractOffer("GA", 5, "")),
			rule:  "create-contract",
			want:  "alice@example.com created the contract offer contract-acme-1 for billing account acme in organization acme-corp: 5 prices",
		},
		{
			name:  "publish a platform offer",
			audit: event("patch", map[string]any{"spec": map[string]any{"launchStage": "GA"}}, platform),
			rule:  "publish",
			want:  "alice@example.com published offer contract-acme-1",
		},
		{
			name:  "create a platform offer",
			audit: event("create", nil, platform),
			rule:  "create",
			want:  "alice@example.com created offer contract-acme-1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, got := summarizeAudit(t, "billing.miloapis.com-offer", tc.audit)
			if rule != tc.rule || got != tc.want {
				t.Errorf("got %s: %q\nwant %s: %q", rule, got, tc.rule, tc.want)
			}
		})
	}
}
