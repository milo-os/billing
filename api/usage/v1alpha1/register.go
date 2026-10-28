// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the API group served by the usage aggregated API server. It is
// separate from billing.miloapis.com because an APIService takes over a whole
// group/version, which would hide billing's CRDs.
const GroupName = "usage.billing.miloapis.com"

var (
	// SchemeGroupVersion is group version used to register these objects.
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

	// GroupVersion is an alias for SchemeGroupVersion, matching the billing
	// API package.
	GroupVersion = SchemeGroupVersion

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Resource takes an unqualified resource and returns a group-qualified
// GroupResource.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&UsageQuery{},
		&UsageQueryList{},
		&BillingAccountBalance{},
		&BillingAccountBalanceList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
