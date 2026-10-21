package main

import "testing"

func TestParseUnit(t *testing.T) {
	cases := map[string]meterUnit{
		"By":            {kind: unitBytes, scale: 1},
		"GiBy":          {kind: unitBytes, scale: 1 << 30},
		"s":             {kind: unitDuration, scale: 1},
		"h":             {kind: unitDuration, scale: 3600},
		"By.s":          {kind: unitByteTime, scale: 1},
		"{millicore}.s": {kind: unitCPUTime, scale: 1e-3},
		"{cpu}":         {kind: unitCPU, scale: 1},
		"{millicore}":   {kind: unitCPU, scale: 1e-3},
		"{token}":       {kind: unitCount, scale: 1},
		"{request}.s":   {kind: unitCount, scale: 1},
		"":              {kind: unitCount, scale: 1},
	}
	for unit, want := range cases {
		if got := parseUnit(unit); got != want {
			t.Errorf("parseUnit(%q) = %+v, want %+v", unit, got, want)
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
		if got := formatUsage(parseUnit(c.unit), c.value); got != c.want {
			t.Errorf("formatUsage(%q, %v) = %q, want %q", c.unit, c.value, got, c.want)
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
		if got := formatMoney(in[0], in[1]); got != want {
			t.Errorf("formatMoney(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestWithQualifier(t *testing.T) {
	if got := withQualifier("4 vCPU", "Latest"); got != "4 vCPU latest" {
		t.Errorf("got %q", got)
	}
	if got := withQualifier("12", "Sum"); got != "12" {
		t.Errorf("got %q", got)
	}
}
