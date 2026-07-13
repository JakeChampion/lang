package e2e

import "testing"

// Differential coverage for std/string.find_all — the start indices of every
// non-overlapping occurrence of a substring (the positions count() tallies).
// Exercises multiple matches, non-overlapping semantics ("aaaa".find_all("aa")
// == [0, 2]), no-match, empty-needle, and the length==count invariant. Returns
// 42 iff every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const stringFindAllProg = `
import "std/string";
function main(): i32 {
    var a: i32[] = "abcabcabc".find_all("bc");
    if (a.len() != 3) { return 1; }
    if (a[0] != 1 || a[1] != 4 || a[2] != 7) { return 2; }
    // non-overlapping: resume past each match
    var b: i32[] = "aaaa".find_all("aa");
    if (b.len() != 2 || b[0] != 0 || b[1] != 2) { return 3; }
    // no match -> empty
    if ("hello".find_all("z").len() != 0) { return 4; }
    // empty needle -> empty
    if ("hello".find_all("").len() != 0) { return 5; }
    // length always equals count
    if ("a.b.c.d".find_all(".").len() != "a.b.c.d".count(".")) { return 6; }
    // single leading match
    var c: i32[] = "hello".find_all("he");
    if (c.len() != 1 || c[0] != 0) { return 7; }
    // match at the very end
    var d: i32[] = "xyzend".find_all("end");
    if (d.len() != 1 || d[0] != 3) { return 8; }
    return 42;
}
`

func TestStringFindAllInterp(t *testing.T) {
	if got := runInterpExit(t, stringFindAllProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringFindAllX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringFindAllProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringFindAllWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringFindAllProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringFindAllArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringFindAllProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
