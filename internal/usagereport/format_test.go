// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import "testing"

func TestParseUnit(t *testing.T) {
	cases := map[string]MeterUnit{
		"By":            {Kind: UnitBytes, Scale: 1},
		"GiBy":          {Kind: UnitBytes, Scale: 1 << 30},
		"s":             {Kind: UnitDuration, Scale: 1},
		"h":             {Kind: UnitDuration, Scale: 3600},
		"By.s":          {Kind: UnitByteTime, Scale: 1},
		"{millicore}.s": {Kind: UnitCPUTime, Scale: 1e-3},
		"{cpu}":         {Kind: UnitCPU, Scale: 1},
		"{millicore}":   {Kind: UnitCPU, Scale: 1e-3},
		"{token}":       {Kind: UnitCount, Scale: 1},
		"{request}.s":   {Kind: UnitCount, Scale: 1},
		"":              {Kind: UnitCount, Scale: 1},
	}
	for unit, want := range cases {
		if got := ParseUnit(unit); got != want {
			t.Errorf("ParseUnit(%q) = %+v, want %+v", unit, got, want)
		}
	}
}

func TestFormatUsage(t *testing.T) {
	cases := []struct {
		unit  string
		value float64
		want  string
	}{
		{"By.s", float64(1<<30) * 36000, "10 GB·h"},
		{"{millicore}.s", 500 * 4 * 3600, "2 vCPU·h"},
		{"{millicore}.s", 1000 * 90, "1.5 vCPU·min"},
		{"{millicore}", 4000, "4 vCPU"},
		{"By", 1024 * 40, "40 KB"},
		{"By", 1024 * 1024 * 100, "100 MB"},
		{"s", 5400, "1.5h"},
		{"{token}", 3563586, "3,563,586"},
		{"By", 0, "0"},
	}
	for _, c := range cases {
		if got := FormatUsage(ParseUnit(c.unit), c.value); got != c.want {
			t.Errorf("FormatUsage(%q, %v) = %q, want %q", c.unit, c.value, got, c.want)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := map[[2]string]string{
		{"12.5", "USD"}:         "$12.50",
		{"843005372.07", "USD"}: "$843,005,372.07",
		{"0.09", "USD"}:         "$0.09",
		{"0.0042", "USD"}:       "$0.0042",
		{"0", "USD"}:            "$0",
		{"3", "CHF"}:            "3.00 CHF",
	}
	for in, want := range cases {
		if got := FormatMoney(in[0], in[1]); got != want {
			t.Errorf("FormatMoney(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestWithQualifier(t *testing.T) {
	if got := WithQualifier("4 vCPU", "Latest"); got != "4 vCPU latest" {
		t.Errorf("got %q", got)
	}
	if got := WithQualifier("12", "Sum"); got != "12" {
		t.Errorf("got %q", got)
	}
}

func TestServiceName(t *testing.T) {
	cases := map[string]string{
		"compute.datumapis.com/instance/cpu-seconds": "Compute",
		"dns.networking.miloapis.com/zone/queries":   "DNS",
		"ai-gateway.miloapis.com/requests":           "AI Gateway",
		"":                                           "Other",
	}
	for in, want := range cases {
		if got := ServiceName(in); got != want {
			t.Errorf("ServiceName(%q) = %q, want %q", in, got, want)
		}
	}
}
