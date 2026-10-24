package e2ecompiler

import (
	"testing"
)

const futureEnumProgram = `enum Future { Ready(i32), Pending(i32, (i32) => Future) }

function step(x: i32): Future { return Ready(x + 1); }

@noinline
function pending(): Future { return Pending(41, step); }

function main(): i32 {
    let f: Future = pending();
    match (f) {
        Ready(v) => { return v; },
        Pending(tag, k) => {
            let r: Future = k(tag);
            match (r) {
                Ready(v2) => { return v2; },
                Pending(t2, k2) => { return 100; }
            }
        }
    }
    return 99;
}`

func TestSelfHostEnumFnPayloadIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	src := []byte(futureEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src))

	if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
		t.Errorf("Future enum exited %d, want %d (interp oracle)\n%s", code, want, stderr)
	}
}
