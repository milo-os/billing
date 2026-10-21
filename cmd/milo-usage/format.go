package main

import (
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// unitKind is the display family of a meter's UCUM unit.
type unitKind int

const (
	unitCount    unitKind = iota
	unitBytes             // bytes
	unitDuration          // seconds
	unitByteTime          // byte-seconds, e.g. memory over time
	unitCPUTime           // core-seconds
	unitCPU               // cores, e.g. allocated vCPU
)

// meterUnit is a display family plus the factor that converts a raw value
// into the family's base unit.
type meterUnit struct {
	kind  unitKind
	scale float64
}

var byteFactors = map[string]float64{
	"By": 1, "kBy": 1e3, "KiBy": 1024, "MBy": 1e6, "MiBy": 1 << 20,
	"GBy": 1e9, "GiBy": 1 << 30, "TBy": 1e12, "TiBy": 1 << 40,
}

var timeFactors = map[string]float64{"ms": 1e-3, "s": 1, "min": 60, "h": 3600, "d": 86400}

var cpuFactors = map[string]float64{
	"{cpu}": 1, "{core}": 1, "{vcpu}": 1, "{millicore}": 1e-3, "{millicpu}": 1e-3,
}

type unitFamily int

const (
	familyOther unitFamily = iota
	familyBytes
	familyTime
	familyCPU
)

func parseTerm(term string) (unitFamily, float64) {
	if f, ok := byteFactors[term]; ok {
		return familyBytes, f
	}
	if f, ok := timeFactors[term]; ok {
		return familyTime, f
	}
	if f, ok := cpuFactors[strings.ToLower(term)]; ok {
		return familyCPU, f
	}
	return familyOther, 1
}

// parseUnit maps a UCUM unit from a MeterDefinition onto a display family,
// matching the portal's usage formatter:
//
//	By, GiBy ...           bytes
//	s, ms, min, h, d       duration
//	By.s, GBy.h ...        byte-time (memory over time)
//	{millicore}.s ...      CPU time
//	{cpu}, {millicore}     CPU (cores)
//	everything else        count
func parseUnit(unit string) meterUnit {
	count := meterUnit{kind: unitCount, scale: 1}
	var terms []string
	for _, t := range strings.Split(strings.TrimSpace(unit), ".") {
		if t != "" {
			terms = append(terms, t)
		}
	}
	switch len(terms) {
	case 1:
		family, factor := parseTerm(terms[0])
		switch family {
		case familyBytes:
			return meterUnit{kind: unitBytes, scale: factor}
		case familyTime:
			return meterUnit{kind: unitDuration, scale: factor}
		case familyCPU:
			return meterUnit{kind: unitCPU, scale: factor}
		}
	case 2:
		fa, a := parseTerm(terms[0])
		fb, b := parseTerm(terms[1])
		if fb != familyTime {
			fa, a, fb, b = fb, b, fa, a
		}
		if fb != familyTime {
			return count
		}
		switch fa {
		case familyBytes:
			return meterUnit{kind: unitByteTime, scale: a * b}
		case familyCPU:
			return meterUnit{kind: unitCPUTime, scale: a * b}
		}
	}
	return count
}

// trimNumber keeps up to two meaningful decimals without trailing zeros.
func trimNumber(v float64) string {
	decimals := 2
	switch {
	case math.Abs(v) >= 100:
		decimals = 0
	case math.Abs(v) >= 10:
		decimals = 1
	}
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func formatBytes(b float64) string {
	if b < 1024 {
		return trimNumber(b) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v, i := b/1024, 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return trimNumber(v) + " " + units[i]
}

func formatDuration(s float64) string {
	switch {
	case s < 60:
		return trimNumber(s) + "s"
	case s < 3600:
		return trimNumber(s/60) + "m"
	case s < 86400:
		return trimNumber(s/3600) + "h"
	}
	return trimNumber(s/86400) + "d"
}

var printer = message.NewPrinter(language.English)

// formatUsage renders a raw meter value for its unit, e.g. "219 GB·h".
func formatUsage(u meterUnit, value float64) string {
	if value == 0 {
		return "0"
	}
	base := value * u.scale
	switch u.kind {
	case unitBytes:
		return formatBytes(base)
	case unitDuration:
		return formatDuration(base)
	case unitByteTime:
		return formatBytes(base/3600) + "·h"
	case unitCPUTime:
		switch {
		case base >= 3600:
			return trimNumber(base/3600) + " vCPU·h"
		case base >= 60:
			return trimNumber(base/60) + " vCPU·min"
		}
		return trimNumber(base) + " vCPU·s"
	case unitCPU:
		return trimNumber(base) + " vCPU"
	}
	if base == math.Trunc(base) {
		return printer.Sprintf("%d", int64(base))
	}
	return printer.Sprintf("%.2f", base)
}

var aggregationQualifiers = map[string]string{
	"Latest": "latest", "Max": "peak", "Min": "lowest", "Average": "average", "UniqueCount": "unique",
}

// withQualifier marks values that aren't running totals, e.g. "4 vCPU latest".
func withQualifier(value, aggregation string) string {
	if q := aggregationQualifiers[aggregation]; q != "" {
		return value + " " + q
	}
	return value
}

var currencySymbols = map[string]string{"USD": "$", "EUR": "€", "GBP": "£"}

// formatMoney renders a decimal amount string in its currency: cents as
// usual, up to four decimals below one unit ("$0.0042").
func formatMoney(amount, currency string) string {
	v, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return amount + " " + currency
	}
	digits := 2
	if v == 0 {
		digits = 0
	} else if math.Abs(v) < 1 {
		digits = 4
	}
	s := printer.Sprintf("%.*f", digits, v)
	if digits == 4 {
		// Keep cents, drop zeros past them ("0.0900" -> "0.09").
		s = strings.TrimRight(s, "0")
		if i := strings.Index(s, "."); i >= 0 && len(s)-i-1 < 2 {
			s += strings.Repeat("0", 2-(len(s)-i-1))
		}
	}
	if symbol, ok := currencySymbols[currency]; ok {
		return symbol + s
	}
	return s + " " + currency
}

func parseDecimal(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
