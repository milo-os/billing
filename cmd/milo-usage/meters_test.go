package main

import (
	"bytes"
	"strings"
	"testing"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestMetersTable(t *testing.T) {
	defs := sampleAPI().defs
	retired := meterDef("old", "compute.datumapis.com/instance/old", "Old", "s", "Sum")
	retired.Spec.Phase = billingv1alpha1.PhaseRetired
	defs = append(defs, retired)
	defs[0].Status.SystemDimensions = []string{"project_name"}

	var out bytes.Buffer
	if err := renderMeters(&out, defs, "table"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"SERVICE", "DIMENSIONS", "Memory Seconds", "project_name", "region", "By.s"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Old") {
		t.Errorf("retired meters can't be queried, so they're left out:\n%s", got)
	}
}
