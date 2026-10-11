package util

import "testing"

func TestUTF16Len(t *testing.T) {
	for _, test := range []struct {
		text string
		want int
	}{{"", 0}, {"a\r\nb", 4}, {"你好é", 3}, {"𝄞界\n", 4}} {
		if got := UTF16Len(test.text); got != test.want {
			t.Errorf("UTF16Len(%q) = %d, want %d", test.text, got, test.want)
		}
	}
}
