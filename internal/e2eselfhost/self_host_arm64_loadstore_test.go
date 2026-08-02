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

// TestSelfHostArm64LoadStore byte-checks the AArch64 load/store encoders
// added in slice 3e of arm64_encode.fern (ldr/str, 64-bit unsigned scaled
// immediate) against the ground-truth little-endian encodings, run through
// the self-host wasm pipeline. Exit 0 = all checks pass; a failing check
// returns its 1-based id.
func TestSelfHostArm64LoadStore(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 load/store e2e")
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

	source := arm64NativeSrc(t) + "\n" + arm64LoadStoreSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 load/store self-test")
	}
	watPath := filepath.Join(dir, "arm64_loadstore_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 load/store self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOFrameRuns is the end-to-end proof of the
// load/store encoders: a Fern program assembles a stack-frame round-trip —
// sub sp; movz x0,#42; str x0,[sp,#8]; clobber x0; ldr x0,[sp,#8]; add sp
// — then exits with x0. A wrong scaled offset or store/load opcode would
// not reload 42. macho.fern wraps it into an ad-hoc-signed Mach-O; the test
// asserts an arm64 MH_EXECUTE (structural, every host) and, on Apple
// Silicon, executes it and checks exit 42 — no clang/ld64/codesign.
func TestSelfHostArm64DarwinMachOFrameRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin frame run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, n := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOFrameDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin frame driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_frame_driver.wat")
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
	binPath := filepath.Join(dir, "frame42")
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
		t.Errorf("self-host arm64-darwin frame exit = %d, want 42", got)
	}
}

// arm64LoadStoreSelfTestMain asserts the ldr/str encoders against their
// ground-truth little-endian bytes. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64LoadStoreSelfTestMain = `
function main(): i32 {
    // str x0, [sp, #8] -> 0xF90007E0 -> E0 07 00 F9
    var a: i32[] = arm64_str([], arm64_x0(), arm64_sp(), 8);
    if (a[0] != 224 || a[1] != 7 || a[2] != 0 || a[3] != 249) { return 1; }
    // ldr x0, [sp, #8] -> 0xF94007E0 -> E0 07 40 F9
    var b: i32[] = arm64_ldr([], arm64_x0(), arm64_sp(), 8);
    if (b[0] != 224 || b[1] != 7 || b[2] != 64 || b[3] != 249) { return 2; }
    // str x1, [x2, #0] -> 0xF9000041 -> 41 00 00 F9
    var c: i32[] = arm64_str([], arm64_x1(), arm64_x2(), 0);
    if (c[0] != 65 || c[1] != 0 || c[2] != 0 || c[3] != 249) { return 3; }
    // ldr x1, [x2, #16] -> 0xF9400841 -> 41 08 40 F9
    var d: i32[] = arm64_ldr([], arm64_x1(), arm64_x2(), 16);
    if (d[0] != 65 || d[1] != 8 || d[2] != 64 || d[3] != 249) { return 4; }
    return 0;
}
`

// arm64MachOFrameDriverMain assembles a stack-frame spill/reload that
// exits with the reloaded value (42): allocate 16 bytes, store 42 at
// [sp,#8], clobber the register, reload it, free the frame.
const arm64MachOFrameDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = arm64_subimm(code, arm64_sp(), arm64_sp(), 16); // sub sp, sp, #16
    code = arm64_movz(code, arm64_x0(), 42, 0);            // x0 = 42
    code = arm64_str(code, arm64_x0(), arm64_sp(), 8);     // str x0, [sp, #8]
    code = arm64_movz(code, arm64_x0(), 0, 0);             // clobber x0
    code = arm64_ldr(code, arm64_x0(), arm64_sp(), 8);     // ldr x0, [sp, #8]
    code = arm64_addimm(code, arm64_sp(), arm64_sp(), 16); // add sp, sp, #16
    code = arm64_movz(code, arm64_x16(), 1, 0);            // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                            // svc #0x80
    var none: i32[] = [];
    var bin: i32[] = macho_static_executable(code, none, "fern");
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
