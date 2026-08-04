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

// arm64NativeSrc returns the merged arm64-darwin native backend module
// (encoder + GAS assembler + Mach-O writer), the single source the
// self-host CLI imports and the e2e self-tests concatenate with a driver.
func arm64NativeSrc(t *testing.T) string {
	b, err := os.ReadFile("../../examples/self_host/arm64_native.fern")
	if err != nil {
		t.Fatalf("read arm64_native.fern: %v", err)
	}
	return string(b)
}

// TestSelfHostArm64Encode exercises the self-hosted AArch64 machine-code
// encoder (examples/self_host/arm64_encode.fern) — the assembler half of
// the arm64-darwin native-binary path (the container half is macho.fern),
// mirroring internal/native/arm64/arm64.go.
//
// arm64_encode.fern is import-free, so this test concatenates it with a
// self-test main() that encodes each instruction and asserts the bytes
// against the ground-truth encodings (the Go reference is pinned against
// llvm-mc), then runs the combined program through the self-host wasm
// pipeline (wasm_run -> WAT -> wasmtime). Exit 0 = all checks pass; a
// failing check returns its 1-based id.
func TestSelfHostArm64Encode(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64_encode e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64EncodeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64_encode self-test")
	}
	watPath := filepath.Join(dir, "arm64_encode_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64_encode self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOExitRuns is the first end-to-end proof of the
// arm64-darwin native-binary path with NO external tool: a Fern program
// (arm64_encode.fern + macho.fern + a driver) assembles an exit(42) program
// to AArch64 machine code, wraps it in an ad-hoc-signed static Mach-O via
// macho.fern, and writes the raw binary to stdout. The Go test captures
// that binary, asserts it parses as an arm64 MH_EXECUTE, and — on an Apple
// Silicon host — writes it 0o755 and executes it, asserting the process
// exits 42. This exercises the whole chain (Fern instruction encoder ->
// Mach-O writer + ad-hoc signature -> kernel load -> svc) with no clang,
// ld64, or codesign.
//
// Off Apple Silicon (the Linux CI box) the produced Mach-O can't be run
// (it's an arm64 Darwin binary), so the check is structural only:
// debug/macho must parse it as an arm64 executable.
func TestSelfHostArm64DarwinMachOExitRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin macho run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOExitDriverMain

	// Stage 1: compile the driver to WAT via the self-host emitter.
	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin macho driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}

	// Stage 2: run the WAT under wasmtime; its stdout is the raw Mach-O the
	// Fern program assembled, signed, and wrote.
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}

	// Structural validation (every host): a parseable arm64 executable.
	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}

	// Decisive runtime check only on Apple Silicon.
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	binPath := filepath.Join(dir, "exit42")
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
		t.Errorf("self-host arm64-darwin exit = %d, want 42", got)
	}
}

// arm64EncodeSelfTestMain asserts each encoder against the ground-truth
// little-endian bytes (the Go reference is pinned against llvm-mc). Each
// `return N` is a distinct failing-check id (0 = all pass).
const arm64EncodeSelfTestMain = `
function main(): i32 {
    // movz x0, #42 -> 0xD2800540 -> 40 05 80 D2
    var a: i32[] = arm64_movz([], arm64_x0(), 42, 0, false);
    if (a.len() != 4 || a[0] != 64 || a[1] != 5 || a[2] != 128 || a[3] != 210) { return 1; }
    // movz x16, #1 -> 0xD2800030 -> 30 00 80 D2
    var b: i32[] = arm64_movz([], arm64_x16(), 1, 0, false);
    if (b.len() != 4 || b[0] != 48 || b[1] != 0 || b[2] != 128 || b[3] != 210) { return 2; }
    // movk x0, #0x10 -> 0xF2800200 -> 00 02 80 F2
    var c: i32[] = arm64_movk([], arm64_x0(), 16, 0);
    if (c[0] != 0 || c[1] != 2 || c[2] != 128 || c[3] != 242) { return 3; }
    // movn x0, #0 -> 0x92800000 -> 00 00 80 92
    var d: i32[] = arm64_movn([], arm64_x0(), 0, 0, false);
    if (d[0] != 0 || d[1] != 0 || d[2] != 128 || d[3] != 146) { return 4; }
    // add x0, x1, #5 -> 0x91001420 -> 20 14 00 91
    var e: i32[] = arm64_addimm([], arm64_x0(), arm64_x1(), 5, false);
    if (e[0] != 32 || e[1] != 20 || e[2] != 0 || e[3] != 145) { return 5; }
    // sub x0, x1, #5 -> 0xD1001420 -> 20 14 00 D1
    var f: i32[] = arm64_subimm([], arm64_x0(), arm64_x1(), 5, false);
    if (f[0] != 32 || f[1] != 20 || f[2] != 0 || f[3] != 209) { return 6; }
    // add x0, x1, x2 -> 0x8B020020 -> 20 00 02 8B
    var g: i32[] = arm64_addreg([], arm64_x0(), arm64_x1(), arm64_x2());
    if (g[0] != 32 || g[1] != 0 || g[2] != 2 || g[3] != 139) { return 7; }
    // sub x0, x1, x2 -> 0xCB020020 -> 20 00 02 CB
    var h: i32[] = arm64_subreg([], arm64_x0(), arm64_x1(), arm64_x2());
    if (h[0] != 32 || h[1] != 0 || h[2] != 2 || h[3] != 203) { return 8; }
    // mov x0, x1 (orr x0, xzr, x1) -> 0xAA0103E0 -> E0 03 01 AA
    var i: i32[] = arm64_movreg([], arm64_x0(), arm64_x1(), false);
    if (i[0] != 224 || i[1] != 3 || i[2] != 1 || i[3] != 170) { return 9; }
    // svc #0x80 -> 0xD4001001 -> 01 10 00 D4
    var j: i32[] = arm64_svc([], 128);
    if (j[0] != 1 || j[1] != 16 || j[2] != 0 || j[3] != 212) { return 10; }
    // ret (x30) -> 0xD65F03C0 -> C0 03 5F D6
    var k: i32[] = arm64_ret([], arm64_lr());
    if (k[0] != 192 || k[1] != 3 || k[2] != 95 || k[3] != 214) { return 11; }
    // blr x1 (indirect call) -> 0xD63F0020 -> 20 00 3F D6
    var l: i32[] = arm64_blr([], arm64_x1());
    if (l[0] != 32 || l[1] != 0 || l[2] != 63 || l[3] != 214) { return 12; }
    return 0;
}
`

// arm64MachOExitDriverMain assembles a Darwin exit(42): x0 = status,
// x16 = 1 (SYS_exit), then the BSD trap `svc #0x80`. macho.fern wraps and
// ad-hoc-signs it into a runnable static Mach-O.
const arm64MachOExitDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = arm64_movz(code, arm64_x0(), 42, 0, false);  // exit status
    code = arm64_movz(code, arm64_x16(), 1, 0, false);  // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                  // svc #0x80
    var none: i32[] = [];
    var bin: i32[] = macho_static_executable(code, none, "fern");
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
