package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4702 regression: a loop-local array buffer that ESCAPES via `out.append(grp)`
// must not have its buffer reused for the next iteration's `grp`. The self-host
// RC pass treated `grp` as dead after the owned append (arr_push_owned stores its
// pointer with no counted reference), so the `var grp = []` loop-rebind reinit-drop
// freed a buffer `out` still held — every appended group then aliased the last
// (`chunks2([1,2,3,4])` read back as [[3,4],[3,4]], so c[0][0] == 3 instead of 1).
// The fix retains a bare owned pointer-array element on append (the same Perceus
// dup the struct-field construction applies), so `out` holds a counted reference
// and grp's dec no longer frees it early. Native + interp were always correct;
// this pins the self-host backends to the same result.
const escapingAppendChunksProg = `function chunks2(xs: i32[]): i32[][] {
    var out: i32[][] = [];
    var i: i32 = 0;
    while (i < xs.len()) {
        var grp: i32[] = [];
        var j: i32 = 0;
        while (j < 2 && i + j < xs.len()) { grp = grp.append(xs[i + j]); j = j + 1; }
        out = out.append(grp);
        i = i + 2;
    }
    return out;
}
function main(): i32 {
    var c: i32[][] = chunks2([1, 2, 3, 4]);
    // Correct groups are [[1,2],[3,4]]. The pre-fix reuse bug makes every inner
    // array alias the LAST group's buffer, so c[0] reads back as [3,4]. Return 7
    // only when all four elements are exactly right.
    if (c[0][0] == 1 && c[0][1] == 2 && c[1][0] == 3 && c[1][1] == 4) { return 7; }
    return 1;
}`

// TestSelfHostEscapingAppendReuseIRX86_64 drives the #4702 repro through the
// self-host x86-64 IR driver (asm_run) and checks the read-back is correct (exit 7).
func TestSelfHostEscapingAppendReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(escapingAppendChunksProg+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "escaping_append", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("chunks2([1,2,3,4]) read back wrong (exit %d, want 7): the escaping loop-local buffer was reused, so groups alias the last (#4702)", code)
	}
}

// TestSelfHostEscapingAppendReuseWasm is the wasm32 mirror (same shared irlower.fern
// lowering, so the retain-on-append fix rides every backend).
func TestSelfHostEscapingAppendReuseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm escaping-append reuse e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	wat := runCapture(t, gcc, runner, driverBin, []byte(escapingAppendChunksProg+"\n"))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	watPath := filepath.Join(dir, "escaping_append.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
	_, _ = cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("chunks2([1,2,3,4]) read back wrong on wasm (exit %d, want 7): escaping loop-local buffer reused (#4702)\n--- WAT ---\n%s", code, strings.TrimSpace(string(wat)))
	}
}
