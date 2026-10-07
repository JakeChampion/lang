package e2ecompiler

import (
	"testing"
)

// futureEnumProgram is a Future-shaped user enum (the std/async core shape):
// a generic-style recursive enum whose Pending variant carries a FUNCTION-typed
// payload. `pending()` builds Pending(41, step); main matches it and
// INDIRECT-calls the bound continuation k(41) -> step(41) -> Ready(42), so it
// exits 42. `pending` is `@noinline` so main cannot see that k is step and call
// it directly.
//
// The user-enum match marks the function-typed payload binding a closure local,
// so the call dispatches via call_indirect, like Option/Result.
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

// TestSelfHostEnumFnPayloadIRX86_64 is slice 5 of docs/ASYNC-SELFHOST-IR.md
// (Blocker 2): a user enum with a function-typed payload now routes the IR path
// on the self-host x86-64 backend and runs (exit 42 = the interp oracle).
func TestSelfHostEnumFnPayloadIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	src := []byte(futureEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src)) // 42

	if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
		t.Errorf("Future enum exited %d, want %d (interp oracle)\n%s", code, want, stderr)
	}
}
