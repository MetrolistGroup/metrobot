package cmd

import "testing"

func TestCalculateExactArithmetic(t *testing.T) {
	for expression, want := range map[string]string{
		"(2 + 3) * 4": "20",
		"1 / 3":       "1/3",
		"2 + 3^2":     "11",
		"2^3^2":       "512",
		"-2^2":        "-4",
		"2^-3":        "1/8",
		"3³":          "27",
		"³3":          "27",
		"10 % 4":      "2",
	} {
		got, err := Calculate(expression)
		if err != nil || got != want {
			t.Errorf("Calculate(%q) = %q, %v; want %q", expression, got, err, want)
		}
	}
}

func TestCalculateRejectsUnsafeExpressions(t *testing.T) {
	for _, expression := range []string{"1 / 0", "sqrt(4)", "2 << 3"} {
		if result, err := Calculate(expression); err == nil {
			t.Errorf("Calculate(%q) = %q, want error", expression, result)
		}
	}
}

func TestCalculateRejectsOversizedIntermediates(t *testing.T) {
	for _, expression := range []string{
		"((2^1000)^1000)^10",
		"(2^1000)^1000",
		"1e1000000000",
		"1e-1000000000",
		"0x1p1000000000",
		"(2^1000)^16 * (2^1000)^16",
		"1/(2^1000)^17 + 1",
	} {
		if result, err := Calculate(expression); err == nil {
			t.Errorf("Calculate(%q) = %d chars, want error", expression, len(result))
		}
	}
	for expression, want := range map[string]string{
		"2^1000 / 2^999": "2",
		"1.5e3":          "1500",
		"2.5e-1":         "1/4",
		"0x10":           "16",
	} {
		got, err := Calculate(expression)
		if err != nil || got != want {
			t.Errorf("Calculate(%q) = %q, %v; want %q", expression, got, err, want)
		}
	}
}
