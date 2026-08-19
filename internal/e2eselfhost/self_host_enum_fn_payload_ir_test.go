package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// futureEnumProgram is a Future-shaped user enum (the std/async core shape):
// a generic-style recursive enum whose Pending variant carries a FUNCTION-typed
// payload. main constructs Pending(41, step), matches it, and INDIRECT-calls the
// bound continuation k(41) -> step(41) -> Ready(42), so it exits 42.
//
// Before slice 5, the user-enum match path recovered the payload type but had no
// path to mark a function-typed field a closure local, so lower_func bailed and
// the module bailed (and the AST emitter it fell to could not emit it -> `call
// __fn_Pending`, a link failure). Now the function-typed payload is marked a
// closure local and the call dispatches via call_indirect, like Option/Result.
const futureEnumProgram = `enum Future { Ready(i32), Pending(i32, (i32) => Future) }

function step(x: i32): Future { return Ready(x + 1); }

function main(): i32 {
    var f: Future = Pending(41, step);
    match (f) {
        Ready(v) => { return v; },
        Pending(tag, k) => {
            var r: Future = k(tag);
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
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	src := []byte(futureEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src)) // 42

	path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	if path != "ir" {
		t.Fatalf("Future enum routed through %q path, want \"ir\" (function-typed payload bailed lower_func)", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "enum_fn_payload", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("Future enum exited %d, want %d (interp oracle)", code, want)
	}
}
