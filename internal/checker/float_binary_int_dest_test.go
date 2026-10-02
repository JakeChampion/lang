package checker

import (
	"strings"
	"testing"
)

// A float binary of unsettled literals is a float: an integer destination
// reports the mismatch rather than settling the binary to its width (#10609).
func TestFloatLiteralBinaryIsNotSettledToInt(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"return-i64", `function f(): i64 { return 3.0 * 2.0; }`, "return type mismatch"},
		{"return-i32", `function f(): i32 { return 1.5 + 2.5; }`, "return type mismatch"},
		{"var-i64", `function f(): i64 { let y: i64 = 3.0 * 2.0; return y; }`, "cannot assign f64 to variable of type i64"},
		{"cast-ok", `function f(): i64 { return (3.0 * 2.0) as i64; }`, ""},
		{"return-f64-ok", `function f(): f64 { return 3.0 * 2.0; }`, ""},
		{"int-literals-ok", `function f(): i64 { return 3 * 2; }`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSrc(t, tc.src)
			if tc.want == "" && got != "" {
				t.Fatalf("want no diagnostic, got %s", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("want %s, got %q", tc.want, got)
			}
		})
	}
}
