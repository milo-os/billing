// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"testing"
)

func TestScopeFromUserExtra(t *testing.T) {
	parent := func(group, kind, name string) map[string][]string {
		return map[string][]string{
			ParentAPIGroupExtraKey: {group},
			ParentTypeExtraKey:     {kind},
			ParentNameExtraKey:     {name},
		}
	}

	tests := []struct {
		name    string
		extra   map[string][]string
		want    UsageScope
		wantErr bool
	}{
		{
			name:  "no parent is the platform",
			extra: nil,
			want:  UsageScope{Type: UsageScopePlatform},
		},
		{
			name:  "unrelated extras are the platform",
			extra: map[string][]string{"authentication.kubernetes.io/credential-id": {"x"}},
			want:  UsageScope{Type: UsageScopePlatform},
		},
		{
			name:  "organization",
			extra: parent("resourcemanager.miloapis.com", "Organization", "acme"),
			want:  UsageScope{Type: UsageScopeOrganization, Name: "acme"},
		},
		{
			name:  "project",
			extra: parent("resourcemanager.miloapis.com", "Project", "web"),
			want:  UsageScope{Type: UsageScopeProject, Name: "web"},
		},
		{
			name:    "user parent is rejected",
			extra:   parent("iam.miloapis.com", "User", "u-123"),
			wantErr: true,
		},
		{
			name:    "unknown kind is rejected",
			extra:   parent("resourcemanager.miloapis.com", "Folder", "f"),
			wantErr: true,
		},
		{
			name:    "organization from another API group is rejected",
			extra:   parent("example.com", "Organization", "acme"),
			wantErr: true,
		},
		{
			name: "missing API group is rejected",
			extra: map[string][]string{
				ParentTypeExtraKey: {"Organization"},
				ParentNameExtraKey: {"acme"},
			},
			wantErr: true,
		},
		{
			name: "missing name is rejected",
			extra: map[string][]string{
				ParentAPIGroupExtraKey: {"resourcemanager.miloapis.com"},
				ParentTypeExtraKey:     {"Organization"},
			},
			wantErr: true,
		},
		{
			name:    "empty name is rejected",
			extra:   parent("resourcemanager.miloapis.com", "Organization", ""),
			wantErr: true,
		},
		{
			name: "several names are rejected",
			extra: map[string][]string{
				ParentAPIGroupExtraKey: {"resourcemanager.miloapis.com"},
				ParentTypeExtraKey:     {"Organization"},
				ParentNameExtraKey:     {"acme", "other"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScopeFromUserExtra(tt.extra)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got scope %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOrganizationNamespace(t *testing.T) {
	if got := OrganizationNamespace("acme"); got != "organization-acme" {
		t.Errorf("got %q, want organization-acme", got)
	}
}
