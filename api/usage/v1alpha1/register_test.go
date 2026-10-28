// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestSchemeRoundTrip_BillingAccountBalance(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	gvk := SchemeGroupVersion.WithKind("BillingAccountBalance")
	if _, err := scheme.New(gvk); err != nil {
		t.Fatalf("%s is not registered: %v", gvk, err)
	}

	now := metav1.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	in := &BillingAccountBalance{
		Status: BillingAccountBalanceStatus{
			RetrievedAt: now,
			Currency:    "USD",
			Balance: AccountBalanceSummary{
				CreditRemaining: "130.00",
				AmountOwed:      "75.00",
			},
			Forecast: AccountBalanceForecast{
				PeriodStart:              now,
				PeriodEnd:                now,
				EstimatedSpend:           "60.00",
				EstimatedCreditUsed:      "60.00",
				EstimatedAmountDue:       "0.00",
				EstimatedCreditRemaining: "70.00",
			},
			Grants: []BillingAccountBalanceGrant{{
				Name:      "welcome-acme",
				Amount:    "500.00",
				Used:      "420.00",
				Remaining: "80.00",
				ExpiresAt: &now,
			}},
		},
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

	got, ok := out.(*BillingAccountBalance)
	if !ok {
		t.Fatalf("decoded %T, want *BillingAccountBalance", out)
	}
	got.TypeMeta = in.TypeMeta
	if !equality.Semantic.DeepEqual(in, got) {
		t.Errorf("round trip changed the object:\n in: %+v\nout: %+v", in, got)
	}
}
