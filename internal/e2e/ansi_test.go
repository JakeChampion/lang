package e2e

import "testing"

// Differential coverage for std/ansi across backends: the SGR wrap
// primitive (sgr), representative fg / bright / bg colours and text
// styles, and strip() — which must remove SGR sequences while
// preserving surrounding text and multi-byte UTF-8 content. Every check
// is expressed through strip() (so it doesn't depend on the raw ESC
// byte in the Go source) plus a length check confirming codes are
// actually emitted. Returns 42 iff all hold. Each leg skips itself when
// its toolchain is absent.
const ansiProg = `
import "std/ansi" as ansi;
function main(): i32 {
    if (ansi.strip(ansi.red("hi")) != "hi") { return 1; }
    if (ansi.strip(ansi.bold(ansi.green("ok"))) != "ok") { return 2; }
    if (ansi.strip(ansi.bg_blue(ansi.bright_white("x"))) != "x") { return 3; }
    if (ansi.strip("a" + ansi.red("b") + "c") != "abc") { return 4; }
    if (ansi.strip("plain") != "plain") { return 5; }
    if (ansi.strip(ansi.cyan("héllo")) != "héllo") { return 6; }
    if (ansi.strip(ansi.sgr("38;5;208", "z")) != "z") { return 7; }
    if (ansi.red("hi").len() <= 2) { return 8; }
    if (ansi.strip(ansi.italic(ansi.underline(ansi.reverse(ansi.strikethrough("q"))))) != "q") { return 9; }
    // strip is idempotent and identity on already-plain text.
    if (ansi.strip(ansi.strip(ansi.yellow("dup"))) != "dup") { return 10; }
    return 42;
}
`

func TestAnsiInterp(t *testing.T) {
	if got := runInterpExit(t, ansiProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestAnsiX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, ansiProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestAnsiWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, ansiProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestAnsiArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, ansiProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
