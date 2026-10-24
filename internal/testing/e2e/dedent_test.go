package e2e

import "testing"

const dedentProg = `
import "std/string";
function main(): i32 {
    if ("    hello\n      world".dedent() != "hello\n  world") { return 1; }
    if ("    a\n\n    b".dedent() != "a\n\nb") { return 2; }
    if ("    a\n  \n    b".dedent() != "a\n\nb") { return 3; }
    if ("a\n    b".dedent() != "a\n    b") { return 4; }
    if ("\ta\n  b".dedent() != "\ta\n  b") { return 5; }
    if ("foo\nbar".dedent() != "foo\nbar") { return 6; }
    if ("   solo".dedent() != "solo") { return 7; }
    if ("".dedent() != "") { return 8; }
    if ("\tx\n\ty".dedent() != "x\ny") { return 9; }
    if ("  a\n  b\n".dedent() != "a\nb\n") { return 10; }
    if ("x\ny".indent("    ").dedent() != "x\ny") { return 11; }
    return 42;
}
`

func TestDedentInterp(t *testing.T) {
	if got := runInterpExit(t, dedentProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestDedentX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, dedentProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestDedentWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, dedentProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestDedentArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, dedentProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
