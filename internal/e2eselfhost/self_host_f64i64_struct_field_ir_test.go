package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostF64I64StructFieldIRX86_64 verifies that structs with f64[] and
// i64[] fields are admitted to the IR path (leak-safe array fields) and read with
// the correct 8-byte element width/type. use_v builds V{fs: f64[], is: i64[], n},
// compares f64 and i64 elements (proving the element typing flows through the
// struct-field index reads), and returns 1 + 10 + 7 = 18. v is reclaimable, so
// both arrays AND the box are deep-dropped: main has no arrays, so the count of
// __fern_arr_dec calls is 3 (fs + is + box), which also proves the module took
// the IR path (a bail would emit none).
func TestSelfHostF64I64StructFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	prog := `struct V { fs: f64[], is: i64[], n: i32 }
function use_v(): i32 {
    var v: V = V { fs: [1.5, 2.5], is: [10, 20], n: 7 };
    var sum: i32 = 0;
    if (v.fs[0] < v.fs[1]) { sum = sum + 1; }
    if (v.is[1] > v.is[0]) { sum = sum + 10; }
    return sum + v.n;
}
function main(): i32 { return use_v(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	if frees := bytes.Count(asm, []byte("call __fn___fern_arr_dec")); frees < 3 {
		t.Errorf("found %d __fern_arr_dec calls, want >= 3 (fs + is + box) — f64[]/i64[] struct fields not admitted/deep-dropped (module likely bailed to AST)", frees)
	}
	progBin := buildBin(t, gcc, dir, "f64i64_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 18 {
		t.Errorf("exit %d, want 18 (f64/i64 element comparisons + v.n)", code)
	}
}
