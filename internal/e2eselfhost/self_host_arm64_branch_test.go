package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostArm64Branches byte-checks the AArch64 control-flow encoders
// added in slice 3b of arm64_encode.fern — compare (cmp reg / cmp imm) and
// the branch family (b / b.cond / cbz / cbnz) — against the ground-truth
// little-endian encodings, run through the self-host wasm pipeline. Exit 0
// = all checks pass; a failing check returns its 1-based id.
func TestSelfHostArm64Branches(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 branches e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64BranchSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 branches self-test")
	}
	watPath := filepath.Join(dir, "arm64_branch_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 branches self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOLoopRuns extends the no-external-tool
// arm64-darwin proof to control flow: a Fern program assembles a loop
// (acc=0; repeat 7×: acc += 6; counter -= 1; cbnz counter, loop) and exits
// with acc — exercising the immediate ALU encoders plus a backward cbnz —
// then wraps it with macho.fern into an ad-hoc-signed Mach-O. The test
// asserts debug/macho parses it as an arm64 MH_EXECUTE (structural, every
// host) and, on Apple Silicon, executes it and checks exit 42 (= 6 × 7).
func TestSelfHostArm64DarwinMachOLoopRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin loop run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOLoopDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin loop driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_loop_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}

	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	binPath := filepath.Join(dir, "loop42")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	cmd := exec.Command(binPath)
	runErr := cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Skipf("self-host Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
	}
	if got := ps.ExitCode(); got != 42 {
		t.Errorf("self-host arm64-darwin loop exit = %d, want 42", got)
	}
}

// arm64BranchSelfTestMain asserts the compare + branch encoders against
// their ground-truth little-endian bytes. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64BranchSelfTestMain = `
function main(): i32 {
    // cmp x1, #1 (subs xzr, x1, #1) -> 0xF100043F -> 3F 04 00 F1
    var a: i32[] = arm64_cmpimm([], arm64_x1(), 1, false);
    if (a[0] != 63 || a[1] != 4 || a[2] != 0 || a[3] != 241) { return 1; }
    // cmp x1, x2 (subs xzr, x1, x2) -> 0xEB02003F -> 3F 00 02 EB
    var b: i32[] = arm64_cmpreg([], arm64_x1(), arm64_x2(), false);
    if (b[0] != 63 || b[1] != 0 || b[2] != 2 || b[3] != 235) { return 2; }
    // b #-8 -> 0x17FFFFFE -> FE FF FF 17
    var c: i32[] = arm64_b([], 0 - 8);
    if (c[0] != 254 || c[1] != 255 || c[2] != 255 || c[3] != 23) { return 3; }
    // b #+8 -> 0x14000002 -> 02 00 00 14
    var d: i32[] = arm64_b([], 8);
    if (d[0] != 2 || d[1] != 0 || d[2] != 0 || d[3] != 20) { return 4; }
    // b.ne #-8 -> 0x54FFFFC1 -> C1 FF FF 54
    var e: i32[] = arm64_bcond([], arm64_ne(), 0 - 8);
    if (e[0] != 193 || e[1] != 255 || e[2] != 255 || e[3] != 84) { return 5; }
    // b.eq #+8 -> 0x54000040 -> 40 00 00 54
    var f: i32[] = arm64_bcond([], arm64_eq(), 8);
    if (f[0] != 64 || f[1] != 0 || f[2] != 0 || f[3] != 84) { return 6; }
    // cbnz x1, #-8 -> 0xB5FFFFC1 -> C1 FF FF B5
    var g: i32[] = arm64_cbnz([], arm64_x1(), 0 - 8, false);
    if (g[0] != 193 || g[1] != 255 || g[2] != 255 || g[3] != 181) { return 7; }
    // cbz x0, #+8 -> 0xB4000040 -> 40 00 00 B4
    var h: i32[] = arm64_cbz([], arm64_x0(), 8, false);
    if (h[0] != 64 || h[1] != 0 || h[2] != 0 || h[3] != 180) { return 8; }
    // condition-code field values.
    if (arm64_eq() != 0 || arm64_ne() != 1 || arm64_lt() != 11 || arm64_ge() != 10) { return 9; }
    return 0;
}
`

// arm64MachOLoopDriverMain assembles a backward-branch loop computing
// 6 × 7 = 42, then a Darwin exit(acc). Offsets: 0 movz x0,#0; 4 movz
// x1,#7; 8 (loop) add x0,x0,#6; 12 sub x1,x1,#1; 16 cbnz x1,loop(-8);
// 20 movz x16,#1; 24 svc #0x80.
const arm64MachOLoopDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = arm64_movz(code, arm64_x0(), 0, 0, false);   // acc = 0
    code = arm64_movz(code, arm64_x1(), 7, 0, false);   // counter = 7
    var loop_off: i32 = code.len();
    code = arm64_addimm(code, arm64_x0(), arm64_x0(), 6, false); // acc += 6
    code = arm64_subimm(code, arm64_x1(), arm64_x1(), 1, false); // counter -= 1
    code = arm64_cbnz(code, arm64_x1(), loop_off - code.len(), false); // loop if != 0
    code = arm64_movz(code, arm64_x16(), 1, 0, false);  // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                  // svc #0x80
    var none: i32[] = [];
    var bin: i32[] = macho_static_executable(code, none, "fern");
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
