package checker

import (
	"strings"
	"testing"
)

// A parameter or a local named print, write or eprint is called as itself,
// not as the builtin of that name. The builtin stays for every other call.
func TestPrintNamesShadowedByLocals(t *testing.T) {
	ok := []string{
		`function each(write: (u8[]) => boolean): boolean { let b: u8[] = [1 as u8]; return !write(b); } function main(): i32 { return 0; }`,
		`function main(): i32 { let print = (n: i32): i32 => n + 1; return print(1); }`,
		`function main(): i32 { let eprint = (b: boolean): boolean => !b; if (eprint(true)) { return 1; } return 0; }`,
		`function main(): i32 { print("still the builtin"); return 0; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s\nunexpected error: %v", src, err)
		}
	}
	err := checkSource(t, `function main(): i32 { let b: u8[] = [1 as u8]; write(b); return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "does not implement `Display`") {
		t.Errorf("the builtin write of u8[]: got %v, want the Display error", err)
	}
}
