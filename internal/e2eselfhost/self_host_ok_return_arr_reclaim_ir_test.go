package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// okReturnArrProg exercises a Perceus move-on-return gap: `return Ok(a)` /
// `return Some(a)` over an owned array LOCAL. The Result/Option box stores the
// payload pointer with NO alias-inc and is leak-mode (never deep-dropped), so
// the local is MOVED into the box — yet returned_moved_arr_slots only recognised
// USER enum-variant constructors, not the built-in Ok/Err/Some. The exit
// dec-sweep therefore freed the array while the returned box still referenced it
// (rc 1 -> 0). Harmless for a grown array (its buffer lands in a different
// freelist size-class) but a use-after-free for an EMPTY array (`[]`, freelist
// class 3) which the caller's next 24-byte string box recycles — so `names`
// aliases `s` and `names.len()` reads `s`'s length. Surfaced end-to-end by the
// read_dir->Fern migration (#2649/#5290): `__fern_read_dir` on an empty
// directory returns `Ok([])`, whose block the caller's next string allocation
// stomped (the filesystem_ops / string_count_and_dir_listing std/test gates).
//
// probe returns 0*100 + 10 = 10 when sound; the UAF makes names.len() read the
// recycled string box, so probe != 10 (exit 97/96); a double-free trips the
// underflow detector (exit 99).
const okReturnArrProg = `function mk_ok(): Result[string[], i32] { var names: string[] = []; return Ok(names); }
function mk_some(): Option[string[]] { var names: string[] = []; return Some(names); }
function probe_ok(): i32 {
    match (mk_ok()) {
        Ok(names) => { var s: string = "0123456789"; return names.len() * 100 + s.len(); },
        Err(_) => { return -1; }
    }
}
function probe_some(): i32 {
    match (mk_some()) {
        Some(names) => { var s: string = "0123456789"; return names.len() * 100 + s.len(); },
        None => { return -1; }
    }
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 500) {
        if (probe_ok() != 10) { return 97; }
        if (probe_some() != 10) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`

// TestSelfHostOkReturnArrReclaimIRX86_64 pins the move-on-return of an owned
// array local wrapped in Ok(...) / Some(...) on the x86-64 IR path.
func TestSelfHostOkReturnArrReclaimIRX86_64(t *testing.T) {
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

	asm := runCapture(t, gcc, runner, driverBin, []byte(okReturnArrProg))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "ok-return-arr-reclaim", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ok-return-arr-reclaim exited %d, want 0 (97/96 = UAF value corruption; 99 = over-release)", code)
	}
}

// TestSelfHostOkReturnArrReclaimWasmIR is the wasm sibling under wasmtime.
func TestSelfHostOkReturnArrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Ok-return array reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(okReturnArrProg))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "ok-return-arr-reclaim.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != 0 {
		t.Errorf("ok-return-arr-reclaim wasm IR = %d, want 0 (97/96 = UAF value corruption; 99 = over-release)", got)
	}
}

// TestSelfHostOkReturnArrReclaimIRArm64 is the arm64 sibling under qemu.
func TestSelfHostOkReturnArrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(okReturnArrProg), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "ok-return-arr-reclaim-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ok-return-arr-reclaim-arm64 exited %d, want 0 (97/96 = UAF value corruption; 99 = over-release)", code)
	}
}
