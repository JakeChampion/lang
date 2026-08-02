package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// capturingEnumProgram constructs a user enum whose variant carries a CAPTURING
// closure as its function-typed payload (`Wrap(function(x){ x + base })` captures
// base), matches it, and indirect-calls the bound continuation. make(40) builds
// Wrap(λx. x+40); k(2) -> 42.
//
// Slice 5 made the match/read side mark a function-typed payload a closure local,
// but constructing a variant with a CAPTURING payload still bailed to AST: the
// pre-lowering lift only env-boxed fn args of module functions / Option-Result,
// not user-enum variant constructors, so the capturing lambda stayed raw. Slice
// 5b env-boxes user-enum fn payloads too (try_fn_field_value, the Option/Result
// mechanism) — capturing -> [funcval, caps…], non-capturing/bare -> a $wrap
// trampoline box — and marks the match bind a closure local (ordered before the
// enum/struct branch, since is_enum_like_name("fn") is otherwise true). The whole
// path now routes IR and dispatches env-first.
const capturingEnumProgram = `enum Box { Wrap((i32) => i32), Empty }

function make(base: i32): Box {
    return Wrap(function(x: i32): i32 { return x + base; });
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
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	src := []byte(capturingEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src)) // 42

	path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	if path != "ir" {
		t.Fatalf("capturing-payload enum routed through %q path, want \"ir\" (construction-side closure box bailed lower_func)", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "enum_capturing_payload", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("capturing-payload enum exited %d, want %d (interp oracle)", code, want)
	}
}
