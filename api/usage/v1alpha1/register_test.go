// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func TestSchemeRoundTrip(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	gvk := SchemeGroupVersion.WithKind("UsageQuery")
	if _, err := scheme.New(gvk); err != nil {
		t.Fatalf("%s is not registered: %v", gvk, err)
	}

	in := validQuery()
	in.Spec.BillingAccounts = []BillingAccountReference{{Namespace: "organization-acme", Name: "main"}}
	in.Status = UsageQueryStatus{
		Scope:           UsageScope{Type: UsageScopeOrganization, Name: "acme"},
		BillingAccounts: in.Spec.BillingAccounts,
		Meters: []MeterUsage{{
			MeterDefinition: "compute.miloapis.com-instance-cpu-hours",
			Aggregation:     "Sum",
			Unit:            "h",
			Groupings: []UsageGroupingResult{{
				GroupBy: []string{"region"},
				Series: []UsageSeries{{
					Group: map[string]string{"region": "us-east4"},
					Total: "18.2",
					Points: []UsagePoint{{
						Start: in.Spec.StartTime,
						End:   in.Spec.EndTime,
						Value: "18.2",
					}},
				}},
			}},
		}},
	}

	codecs := serializer.NewCodecFactory(scheme)
	info, ok := runtime.SerializerInfoForMediaType(codecs.SupportedMediaTypes(), runtime.ContentTypeJSON)
	if !ok {
		t.Fatal("no JSON serializer")
	}
	codec := codecs.CodecForVersions(info.Serializer, codecs.UniversalDeserializer(), SchemeGroupVersion, SchemeGroupVersion)

	data, err := runtime.Encode(codec, in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := runtime.Decode(codec, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, ok := out.(*UsageQuery)
	if !ok {
		t.Fatalf("decoded %T, want *UsageQuery", out)
	}
	got.TypeMeta = in.TypeMeta
	if !equality.Semantic.DeepEqual(in, got) {
		t.Errorf("round trip changed the object:\n in: %+v\nout: %+v", in, got)
	}
}
