package util

import "testing"

func TestValidJSONUnicode(t *testing.T) {
	for _, s := range []string{`{"content":"hello"}`, `{"content":"\ud83d\ude00"}`, `{"content":"\\ud800"}`, `{"content":"�"}`} {
		if !ValidJSONUnicode([]byte(s)) {
			t.Fatalf("rejected valid Unicode: %s", s)
		}
	}
	for _, s := range []string{`"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, `"\ud800x"`, string([]byte{0xff})} {
		if ValidJSONUnicode([]byte(s)) {
			t.Fatalf("accepted lossy Unicode: %q", s)
		}
	}
}
