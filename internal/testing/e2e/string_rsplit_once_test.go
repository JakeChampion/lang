package e2e

import "testing"

// Differential coverage for std/string.rsplit_once — split at the LAST
// occurrence of the separator (the mirror of split_once, which splits at the
// first). Exercises multi-occurrence (last wins), no-match, empty-sep,
// separator at the very start / very end, and a multi-char separator. Returns
// 42 iff every check holds across interp / x86-64 / wasm / arm64. The result is
// Option[(string, string)] (string payloads, not i64), so the wasmbin enum-i64
// gap doesn't apply — split_once uses the same shape.
const stringRsplitOnceProg = `
import "std/string";
function chk(o: Option[(string, string)], h: string, t: string): boolean {
    match (o) { Some(p) => { return p.0 == h && p.1 == t; }, None => { return false; } }
}
function non(o: Option[(string, string)]): boolean {
    match (o) { Some(p) => { return false; }, None => { return true; } }
}
function main(): i32 {
    if (!chk("a.b.c".rsplit_once("."), "a.b", "c")) { return 1; }
    if (!chk("file.tar.gz".rsplit_once("."), "file.tar", "gz")) { return 2; }
    if (!chk("a/b/c".rsplit_once("/"), "a/b", "c")) { return 3; }
    if (!non("abc".rsplit_once("."))) { return 4; }   // no separator
    if (!non("abc".rsplit_once(""))) { return 5; }    // empty separator
    if (!chk("key=value=x".rsplit_once("="), "key=value", "x")) { return 6; }
    if (!chk(".hidden".rsplit_once("."), "", "hidden")) { return 7; }   // sep at start
    if (!chk("trailing.".rsplit_once("."), "trailing", "")) { return 8; } // sep at end
    if (!chk("a::b::c".rsplit_once("::"), "a::b", "c")) { return 9; }     // multi-char
    // rsplit_once vs split_once differ on multi-occurrence:
    match ("a.b.c".split_once(".")) {
        Some(p) => { if (p.0 != "a" || p.1 != "b.c") { return 10; } },
        None => { return 11; }
    }
    return 42;
}
`

func TestStringRsplitOnceInterp(t *testing.T) {
	if got := runInterpExit(t, stringRsplitOnceProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringRsplitOnceX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringRsplitOnceProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringRsplitOnceWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringRsplitOnceProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringRsplitOnceArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringRsplitOnceProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
