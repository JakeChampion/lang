package e2ecompiler

import (
	"testing"
)

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

func TestSelfHostEnumCapturingPayloadIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	src := []byte(capturingEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src))

	if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
		t.Errorf("capturing-payload enum exited %d, want %d (interp oracle)\n%s", code, want, stderr)
	}
}
