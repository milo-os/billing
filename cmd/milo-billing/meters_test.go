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
	deprecated := meterDef("uptime", "compute.datumapis.com/instance/uptime-seconds", "Uptime", "s", "Sum")
	deprecated.Spec.Phase = billingv1alpha1.PhaseDeprecated
	defs = append(defs, retired, deprecated)
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
	if strings.Contains(got, "Old") || strings.Contains(got, "Uptime") {
		t.Errorf("only published meters are listed:\n%s", got)
	}
}
