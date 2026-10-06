package e2e

import "testing"

// Differential coverage for std/string.partition / rpartition — the Python-style
// three-way split (head, sep, tail) that KEEPS the separator (unlike
// split_once/rsplit_once, which drop it). partition splits at the FIRST
// occurrence and, when the sep is absent, returns (s, "", ""); rpartition
// splits at the LAST and, when absent, returns ("", "", s). The result is a
// (string, string, string) tuple. Returns 42 iff every check holds across interp
// / x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
const stringPartitionProg = `
import "std/string";
function chk(t: (string, string, string), a: string, b: string, c: string): boolean {
    return t.0 == a && t.1 == b && t.2 == c;
}
function main(): i32 {
    if (!chk("a.b.c".partition("."), "a", ".", "b.c")) { return 1; }
    if (!chk("abc".partition("."), "abc", "", "")) { return 2; }        // absent -> first slot
    if (!chk("a.b.c".rpartition("."), "a.b", ".", "c")) { return 3; }
    if (!chk("abc".rpartition("."), "", "", "abc")) { return 4; }       // absent -> last slot
    if (!chk("key=value".partition("="), "key", "=", "value")) { return 5; }
    if (!chk("=x".partition("="), "", "=", "x")) { return 6; }          // sep at start
    if (!chk("x=".partition("="), "x", "=", "")) { return 7; }          // sep at end
    if (!chk("a::b::c".partition("::"), "a", "::", "b::c")) { return 8; }// multi-char
    if (!chk("a::b::c".rpartition("::"), "a::b", "::", "c")) { return 9; }
    if (!chk("abc".partition(""), "abc", "", "")) { return 10; }        // empty sep
    if (!chk("abc".rpartition(""), "", "", "abc")) { return 11; }
    return 42;
}
`

func TestStringPartitionInterp(t *testing.T) {
	if got := runInterpExit(t, stringPartitionProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringPartitionX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringPartitionProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringPartitionWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringPartitionProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringPartitionArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringPartitionProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
