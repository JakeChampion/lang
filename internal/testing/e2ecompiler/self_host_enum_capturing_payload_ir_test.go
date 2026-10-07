package e2ecompiler

import (
	"testing"
)

// capturingEnumProgram constructs a user enum whose variant carries a CAPTURING
// closure as its function-typed payload (`Wrap((x) => { x + base })` captures
// base), matches it, and indirect-calls the bound continuation. make(40) builds
// Wrap(λx. x+40); k(2) -> 42. `make` is `@noinline` so main cannot see the
// closure and call its body directly. A payload that lost its capture on the
// way through construction, match and call would not answer 42.
const capturingEnumProgram = `enum Box { Wrap((i32) => i32), Empty }

@noinline
function make(base: i32): Box {
    return Wrap((x: i32): i32 => { return x + base; });
}

function main(): i32 {
    match (make(40)) {
        Wrap(k) => { return k(2); },
        Empty => { return 0; }
    }
    return 99;
}`

// TestSelfHostEnumCapturingPayloadIRX86_64 (slice 5b): a user enum constructed
// with a capturing-closure payload now routes the IR path on x86-64 and runs
// (exit 42 = the interp oracle).
func TestSelfHostEnumCapturingPayloadIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	src := []byte(capturingEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src)) // 42

	if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
		t.Errorf("capturing-payload enum exited %d, want %d (interp oracle)\n%s", code, want, stderr)
	}
}
