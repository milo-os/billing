// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"fmt"
)

// The user extras Milo sets on a request made through an organization's or a
// project's control plane, and forwards to aggregated API servers. They match
// go.miloapis.com/milo/pkg/apis/iam/v1alpha1.
const (
	ParentAPIGroupExtraKey = "iam.miloapis.com/parent-api-group"
	ParentTypeExtraKey     = "iam.miloapis.com/parent-type"
	ParentNameExtraKey     = "iam.miloapis.com/parent-name"
)

// resourceManagerGroup is the API group of Milo's Organization and Project.
const resourceManagerGroup = "resourcemanager.miloapis.com"

// organizationNamespacePrefix is how Milo names an organization's namespace:
// organization-<organization name>.
const organizationNamespacePrefix = "organization-"

// OrganizationNamespace returns the namespace that holds an organization's
// billing accounts.
func OrganizationNamespace(organization string) string {
	return organizationNamespacePrefix + organization
}

// ScopeFromUserExtra returns the scope a request was made in, from the
// caller's user extras (user.Info.GetExtra()).
//
// A request without parent extras is a Platform request: Milo only
// authorizes it for callers with platform-wide permission, which is staff.
// An Organization or Project parent from resourcemanager.miloapis.com scopes
// the request to it. Anything else, such as a User parent or partial or
// repeated extras, is an error: the server must reject the request rather
// than guess.
func ScopeFromUserExtra(extra map[string][]string) (UsageScope, error) {
	groups, hasGroup := extra[ParentAPIGroupExtraKey]
	types, hasType := extra[ParentTypeExtraKey]
	names, hasName := extra[ParentNameExtraKey]

	if !hasGroup && !hasType && !hasName {
		return UsageScope{Type: UsageScopePlatform}, nil
	}

	if len(groups) != 1 || len(types) != 1 || len(names) != 1 || types[0] == "" || names[0] == "" {
		return UsageScope{}, fmt.Errorf("request has an incomplete parent context")
	}
	if groups[0] != resourceManagerGroup {
		return UsageScope{}, fmt.Errorf("usage cannot be queried in a %s parent from API group %s", types[0], groups[0])
	}

	switch UsageScopeType(types[0]) {
	case UsageScopeOrganization, UsageScopeProject:
		return UsageScope{Type: UsageScopeType(types[0]), Name: names[0]}, nil
	default:
		return UsageScope{}, fmt.Errorf("usage cannot be queried in a %s parent", types[0])
	}
}
