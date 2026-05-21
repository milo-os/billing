// SPDX-License-Identifier: AGPL-3.0-only

package featureflag

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(resourceGrantGVKList(), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(resourceGrantGVK, &unstructured.Unstructured{})
	return s
}

func newGrant(orgName, resourceType string, amount int64) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "quota.miloapis.com", Version: "v1alpha1", Kind: "ResourceGrant",
	})
	u.SetName(orgName + "-" + resourceType)
	u.Object["spec"] = map[string]interface{}{
		"consumerRef": map[string]interface{}{
			"kind": "Organization",
			"name": orgName,
		},
		"allowances": []interface{}{
			map[string]interface{}{
				"resourceType": resourceType,
				"buckets": []interface{}{
					map[string]interface{}{"amount": amount},
				},
			},
		},
	}
	return u
}

func TestOrgHasFeature(t *testing.T) {
	const feature = "billing.miloapis.com/multiple-billing-accounts"
	tests := []struct {
		name    string
		org     string
		feature string
		grants  []client.Object
		want    bool
	}{
		{name: "empty org returns false", org: "", feature: feature, want: false},
		{name: "empty feature returns false", org: "acme", feature: "", want: false},
		{name: "no grants returns false", org: "acme", feature: feature, want: false},
		{
			name:    "matching grant returns true",
			org:     "acme",
			feature: feature,
			grants:  []client.Object{newGrant("acme", feature, 1)},
			want:    true,
		},
		{
			name:    "grant for different org returns false",
			org:     "acme",
			feature: feature,
			grants:  []client.Object{newGrant("widgets", feature, 1)},
			want:    false,
		},
		{
			name:    "grant for different resourceType returns false",
			org:     "acme",
			feature: feature,
			grants:  []client.Object{newGrant("acme", "billing.miloapis.com/other-feature", 1)},
			want:    false,
		},
		{
			name:    "zero-amount grant returns false",
			org:     "acme",
			feature: feature,
			grants:  []client.Object{newGrant("acme", feature, 0)},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(tt.grants...).Build()
			got, err := OrgHasFeature(context.Background(), c, tt.org, tt.feature)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}
