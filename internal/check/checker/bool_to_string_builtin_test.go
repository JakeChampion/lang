package checker

import (
	"strings"
	"testing"
)

func TestBoolToStringBuiltin(t *testing.T) {
	ok := []string{
		`function main(): i32 { let b = true; write(f"<{b}>"); return 0; }`,
		`function main(): i32 { let s: string = (1 == 2).to_string(); return s.len(); }`,
		`function main(): i32 { print(false); return 0; }`,
		`function (b: boolean) to_string(): string { return "y"; } function main(): i32 { write(f"{true}"); return 0; }`,
		`function (b: boolean) to_string(): i32 { return 1; } function main(): i32 { let n: i32 = true.to_string(); return n; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s\nunexpected error: %v", src, err)
		}
	}
	bad := []struct{ src, want string }{
		{`function main(): i32 { let n: i32 = true.to_string(); return n; }`, "string"},
		{`function main(): i32 { let b = true; return b.nope(); }`, `no method "nope" on boolean, and no stdlib module declares one`},
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

func TestUnsignedNegationChecks(t *testing.T) {
	for _, ty := range []string{"u8", "u32", "u64", "usize"} {
		src := `function main(): i32 { let x: ` + ty + ` = 5; let y: ` + ty + ` = -x; return 0; }`
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s\nunexpected error: %v", src, err)
		}
	}
}
