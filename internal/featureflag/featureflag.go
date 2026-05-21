// SPDX-License-Identifier: AGPL-3.0-only

// Package featureflag is a thin helper for checking whether an
// Organization has a granted quota.miloapis.com Feature.
//
// We use unstructured client access rather than importing the milo
// quota Go types because billing has no other reason to take a direct
// module dependency on go.miloapis.com/milo. The set of fields we read
// on ResourceGrant is small and stable; if billing ever gains other
// reasons to import milo, swap this for the typed client.
package featureflag

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// resourceGrantGVK is the GroupVersionKind of milo's ResourceGrant CRD.
var resourceGrantGVK = schema.GroupVersionKind{
	Group:   "quota.miloapis.com",
	Version: "v1alpha1",
	Kind:    "ResourceGrant",
}

// OrgHasFeature reports whether the given Organization has been
// granted the named feature resourceType. Returns (false, nil) when no
// matching grant exists; an error only on transport failures.
//
// A grant matches when its consumerRef.kind == "Organization",
// consumerRef.name == orgName, and at least one entry in
// spec.allowances has the requested resourceType with amount > 0.
//
// Callers that want to be permissive on transport failures (e.g. an
// admission webhook that should fail-open rather than block all
// BillingAccount creation when the quota API is unreachable) should
// log the error and proceed as if the feature were granted.
func OrgHasFeature(ctx context.Context, c client.Reader, orgName, resourceType string) (bool, error) {
	if orgName == "" || resourceType == "" {
		return false, nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(resourceGrantGVKList())
	if err := c.List(ctx, list); err != nil {
		return false, fmt.Errorf("listing ResourceGrants: %w", err)
	}
	for i := range list.Items {
		if grantMatches(&list.Items[i], orgName, resourceType) {
			return true, nil
		}
	}
	return false, nil
}

func resourceGrantGVKList() schema.GroupVersionKind {
	gvk := resourceGrantGVK
	gvk.Kind = "ResourceGrantList"
	return gvk
}

func grantMatches(grant *unstructured.Unstructured, orgName, resourceType string) bool {
	consumerKind, _, _ := unstructured.NestedString(grant.Object, "spec", "consumerRef", "kind")
	consumerName, _, _ := unstructured.NestedString(grant.Object, "spec", "consumerRef", "name")
	if consumerKind != "Organization" || consumerName != orgName {
		return false
	}
	allowances, _, _ := unstructured.NestedSlice(grant.Object, "spec", "allowances")
	for _, a := range allowances {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		rt, _, _ := unstructured.NestedString(m, "resourceType")
		if rt != resourceType {
			continue
		}
		// Sum the buckets; treat any positive amount as "granted".
		buckets, _, _ := unstructured.NestedSlice(m, "buckets")
		var total int64
		for _, b := range buckets {
			bm, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			if amt, found, _ := unstructured.NestedInt64(bm, "amount"); found && amt > 0 {
				total += amt
			}
		}
		if total > 0 {
			return true
		}
	}
	return false
}
