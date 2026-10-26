// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "billing.miloapis.com", Version: "v1alpha1"}

	// SchemeGroupVersion is an alias for GroupVersion, used by generated client code.
	SchemeGroupVersion = GroupVersion

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &schemeBuilder{groupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

type schemeBuilder struct {
	runtime.SchemeBuilder
	groupVersion schema.GroupVersion
}

func (b *schemeBuilder) Register(objs ...runtime.Object) *schemeBuilder {
	b.SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(b.groupVersion, objs...)
		metav1.AddToGroupVersion(s, b.groupVersion)
		return nil
	})
	return b
}
