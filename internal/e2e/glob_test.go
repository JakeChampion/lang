package e2e

import "testing"

// Differential coverage for std/glob across backends: `*` staying within
// a path segment, `?`, `**` crossing separators (with the zero-directory
// elision), and `[...]` classes with ranges + negation. Returns 42 iff
// every case holds. Each leg skips itself when its toolchain is absent.
const globProg = `
import "std/glob" as glob;
function y(p: string, t: string): boolean { return glob.glob_match(p, t); }
function main(): i32 {
    if (!y("*.fern", "a.fern") || y("*.fern", "src/a.fern")) { return 1; }
    if (!y("src/*.fern", "src/a.fern") || y("src/*.fern", "src/x/a.fern")) { return 2; }
    if (!y("a?c", "abc") || y("a?c", "a/c") || y("a?c", "ac")) { return 3; }
    if (!y("src/**", "src/a/b/c")) { return 4; }
    if (!y("**/*.fern", "a.fern") || !y("**/*.fern", "src/x/a.fern")) { return 5; }
    if (y("**/*.fern", "a.txt")) { return 6; }
    if (!y("[a-z]9", "m9") || y("[a-z]9", "M9")) { return 7; }
    if (!y("[!0-9]", "a") || y("[!0-9]", "5")) { return 8; }
    if (!y("file[0-9].log", "file7.log")) { return 9; }
    if (!y("ab*", "ab") || !y("", "") || y("", "x")) { return 10; }
    return 42;
}
`

func TestGlobInterp(t *testing.T) {
	if got := runInterpExit(t, globProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestGlobX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, globProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestGlobWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, globProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestGlobArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, globProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
