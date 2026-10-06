package e2e

// Differential coverage for std/string `replacen` — `replace` capped at the
// first n non-overlapping occurrences (`"aaaa".replacen("a","b",2) == "bbaa"`);
// `n <= 0` returns the input, `n >= count` behaves as `replace`. Mirrors
// Rust's `str::replacen`. Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64.

import "testing"

const stringReplacenProg = `
import "std/string";
function main(): i32 {
    if ("aaaa".replacen("a", "b", 2) != "bbaa") { return 1; }
    if ("aaaa".replacen("a", "b", 0) != "aaaa") { return 2; }
    if ("aaaa".replacen("a", "b", 10) != "bbbb") { return 3; }
    if ("a.b.c".replacen(".", "-", 1) != "a-b.c") { return 4; }
    if ("hello".replacen("z", "x", 3) != "hello") { return 5; }
    if ("xx".replacen("x", "yy", 1) != "yyx") { return 6; }          // multi-char replacement
    if ("foofoofoo".replacen("foo", "bar", 2) != "barbarfoo") { return 7; }  // multi-char needle
    if ("".replacen("a", "b", 3) != "") { return 8; }
    if ("abc".replacen("", "x", 3) != "abc") { return 9; }           // empty needle -> unchanged
    if ("aaa".replacen("a", "", 2) != "a") { return 10; }            // delete first two
    return 42;
}
`

func TestStringReplacenInterp(t *testing.T) {
	if got := runInterpExit(t, stringReplacenProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringReplacenX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringReplacenProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringReplacenWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringReplacenProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringReplacenArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringReplacenProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
