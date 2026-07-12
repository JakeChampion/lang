package e2e

import "testing"

// Differential coverage for the std/ansi 256-colour + truecolour helpers
// across backends: fg_256 / bg_256 and fg_rgb / bg_rgb must wrap their
// content in an SGR escape (so the wrapped string is longer than the
// content) and strip() must recover the original text, including when
// nested inside another style. Returns 42 iff every check holds. Each
// leg skips itself when its toolchain is absent.
const ansiColorProg = `
import "std/ansi" as ansi;
function main(): i32 {
    if (ansi.strip(ansi.fg_256(208, "hi")) != "hi") { return 1; }
    if (ansi.strip(ansi.bg_256(21, "x")) != "x") { return 2; }
    if (ansi.strip(ansi.fg_rgb(255, 136, 0, "orange")) != "orange") { return 3; }
    if (ansi.strip(ansi.bg_rgb(0, 0, 0, "y")) != "y") { return 4; }
    // Codes are actually emitted: the wrapped string is longer.
    if (ansi.fg_rgb(255, 136, 0, "z").len() <= 1) { return 5; }
    if (ansi.fg_256(208, "z").len() <= 1) { return 6; }
    // Nesting a truecolour inside bold strips cleanly.
    if (ansi.strip(ansi.bold(ansi.fg_rgb(10, 20, 30, "q"))) != "q") { return 7; }
    // strip is idempotent on already-plain output.
    if (ansi.strip(ansi.strip(ansi.bg_256(255, "dup"))) != "dup") { return 8; }
    return 42;
}
`

func TestAnsiColorInterp(t *testing.T) {
	if got := runInterpExit(t, ansiColorProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestAnsiColorX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, ansiColorProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestAnsiColorWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, ansiColorProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestAnsiColorArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, ansiColorProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
