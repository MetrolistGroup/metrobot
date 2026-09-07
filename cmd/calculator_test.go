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
