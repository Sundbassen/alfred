package answer

import (
	"math"
	"testing"
)

func TestEvaluate(t *testing.T) {
	for expr, want := range map[string]float64{
		"15 * 23":         345,
		"(20/100)*80":     16,
		"sqrt(144)":       12,
		"(7 + (8 / 2))":   11,
		"7 + 8 / 2":       11,
		"2 ^ 3 ^ 2":       512,
		"-3 + 5":          2,
		"-2^2":            -4,
		"2^-1":            0.5,
		"10 % 4":          2,
		"1,000 * 3":       3000,
		"24 * 60 * 60":    86400,
		"sqrt(2)":         math.Sqrt2,
		"0.1 + 0.2":       0.30000000000000004,
		"((((((1))))))":   1,
		"2 * -(3 + 4)":    -14,
		"100 / 8":         12.5,
		" 1 + 1 ":         2,
		"3 × 4":           12,
		"sqrt (16) + 0.5": 4.5,
	} {
		if got, err := evaluate(expr); err != nil || got != want {
			t.Errorf("evaluate(%q) = %v, %v; want %v", expr, got, err, want)
		}
	}
	for _, expr := range []string{"", "1 +", "1 / 0", "5 % 0", "os.Exit(1)", "2 ** 3", "abc", "(1", "1)", "sqrt(-1)", "1e309 * 10"} {
		if v, err := evaluate(expr); err == nil {
			t.Errorf("evaluate(%q) = %v, want an error", expr, v)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	for v, want := range map[float64]string{
		345: "345", 12.5: "12.5", 453.59237: "453.59", -14: "minus 14", 0.1 + 0.2: "0.3", 0.0625: "0.0625",
		0.000012: "about 1.2 times 10 to the power of minus 5", 1e6: "1000000",
		1.07e301: "about 1.1 times 10 to the power of 301",
	} {
		if got := formatNumber(v); got != want {
			t.Errorf("formatNumber(%v) = %q, want %q", v, got, want)
		}
	}
}
