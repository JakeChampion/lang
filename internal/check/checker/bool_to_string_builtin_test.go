package checker

import (
	"strings"
	"testing"
)

// A boolean's `to_string` is a builtin that needs no import, and a declared
// one takes over from it (#12657). Before, the f-string desugar `b.to_string()`
// was E043 "field access on non-struct value of type boolean" without
// `import "core/cmp"`.
func TestBoolToStringBuiltin(t *testing.T) {
	ok := []string{
		`function main(): i32 { let b = true; write(f"<{b}>"); return 0; }`,
		`function main(): i32 { let s: string = (1 == 2).to_string(); return s.len(); }`,
		`function main(): i32 { print(false); return 0; }`,
		`function (b: boolean) to_string(): string { return "y"; } function main(): i32 { write(f"{true}"); return 0; }`,
		// The declaration takes the builtin's signature over too, rather than
		// being refused as a redeclaration of it.
		`function (b: boolean) to_string(): i32 { return 1; } function main(): i32 { let n: i32 = true.to_string(); return n; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s\nunexpected error: %v", src, err)
		}
	}
	bad := []struct{ src, want string }{
		{`function main(): i32 { let n: i32 = true.to_string(); return n; }`, "string"},
		{`function main(): i32 { let b = true; return b.nope(); }`, "field access on non-struct value of type boolean"},
		{`function (b: boolean) to_string(): string { return "y"; } function (b: boolean) to_string(): string { return "z"; } function main(): i32 { return 0; }`, "redeclared"},
	}
	for _, tc := range bad {
		err := checkSource(t, tc.src)
		if err == nil {
			t.Errorf("%s\nexpected an error naming %q, got none", tc.src, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s\nerror %q does not name %q", tc.src, err, tc.want)
		}
	}
}

// Unary minus on an unsigned integer is accepted at the operand's type; it
// wraps (docs/INTEGER-SEMANTICS.md).
func TestUnsignedNegationChecks(t *testing.T) {
	for _, ty := range []string{"u8", "u32", "u64", "usize"} {
		src := `function main(): i32 { let x: ` + ty + ` = 5; let y: ` + ty + ` = -x; return 0; }`
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s\nunexpected error: %v", src, err)
		}
	}
}
