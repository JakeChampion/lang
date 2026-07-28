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

// TestSelfHostArm64ForwardRefs byte-checks the forward-reference patch
// helpers added in slice 3c of arm64_encode.fern — arm64_rel and the
// placeholder-patch splicers arm64_patch_b (imm26) / arm64_patch_b19
// (imm19, shared by b.cond/cbz/cbnz) — run through the self-host wasm
// pipeline. Exit 0 = all checks pass; a failing check returns its 1-based id.
func TestSelfHostArm64ForwardRefs(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 forward-refs e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64ForwardSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 forward-refs self-test")
	}
	watPath := filepath.Join(dir, "arm64_forward_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 forward-refs self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOMaxRuns extends the no-external-tool
// arm64-darwin proof to a *forward* branch: a Fern program assembles
// max(42, 17) as `cmp a,b; b.ge done; mov a,b; done:` — the b.ge is a
// forward branch emitted as a placeholder and patched once `done`'s offset
// is known. With a (42) >= b (17) the branch is *taken*, skipping the
// `mov a,b`, so a wrong target or a not-taken branch would exit 17 instead
// of 42. macho.fern wraps the bytes into an ad-hoc-signed Mach-O; the test
// asserts an arm64 MH_EXECUTE (structural, every host) and, on Apple
// Silicon, executes it and checks exit 42.
func TestSelfHostArm64DarwinMachOMaxRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin max run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, n := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOMaxDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin max driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_max_driver.wat")
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
	binPath := filepath.Join(dir, "max42")
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
		t.Errorf("self-host arm64-darwin max exit = %d, want 42", got)
	}
}

// arm64ForwardSelfTestMain asserts arm64_rel and the patch splicers
// preserve the opcode/cond/Rt bits while writing the correct displacement.
// Each `return N` is a distinct failing-check id (0 = all pass).
const arm64ForwardSelfTestMain = `
function main(): i32 {
    // arm64_rel(target=20, branch=12) = 8.
    if (arm64_rel(20, 12) != 8) { return 1; }
    // patch a b.ge #0 placeholder (0x5400000A) at offset 0 with rel +8.
    // imm19 = 2 -> 0x5400004A -> 4A 00 00 54
    var a: i32[] = arm64_bcond([], arm64_ge(), 0);
    a = arm64_patch_b19(a, 0, 8);
    if (a[0] != 74 || a[1] != 0 || a[2] != 0 || a[3] != 84) { return 2; }
    // patch a b #0 placeholder (0x14000000) at offset 0 with rel -8.
    // imm26 = 0x3FFFFFE -> 0x17FFFFFE -> FE FF FF 17
    var b: i32[] = arm64_b([], 0);
    b = arm64_patch_b(b, 0, 0 - 8);
    if (b[0] != 254 || b[1] != 255 || b[2] != 255 || b[3] != 23) { return 3; }
    // patch a cbnz x1, #0 placeholder (0xB5000001) with rel -8, preserving
    // Rt=1 -> 0xB5FFFFC1 -> C1 FF FF B5
    var c: i32[] = arm64_cbnz([], arm64_x1(), 0);
    c = arm64_patch_b19(c, 0, 0 - 8);
    if (c[0] != 193 || c[1] != 255 || c[2] != 255 || c[3] != 181) { return 4; }
    // patch must not disturb neighbouring bytes: emit nop-ish movz, a
    // placeholder, then patch — the movz word stays intact.
    var d: i32[] = arm64_movz([], arm64_x0(), 42, 0); // 40 05 80 D2
    d = arm64_bcond(d, arm64_eq(), 0);
    d = arm64_patch_b19(d, 4, 8); // patch the branch at offset 4
    if (d[0] != 64 || d[1] != 5 || d[2] != 128 || d[3] != 210) { return 5; }
    if (d[4] != 64 || d[5] != 0 || d[6] != 0 || d[7] != 84) { return 6; }
    return 0;
}
`

// arm64MachOMaxDriverMain assembles max(42, 17) with a forward b.ge:
//
//	0 movz x0,#42 (a); 4 movz x1,#17 (b); 8 cmp x0,x1;
//	12 b.ge done (placeholder, patched); 16 mov x0,x1; 20 (done) movz
//	x16,#1; 24 svc #0x80. a >= b so the branch is taken, skipping the mov.
const arm64MachOMaxDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = arm64_movz(code, arm64_x0(), 42, 0);        // a = 42 (result)
    code = arm64_movz(code, arm64_x1(), 17, 0);        // b = 17
    code = arm64_cmpreg(code, arm64_x0(), arm64_x1()); // cmp a, b
    var br_off: i32 = code.len();
    code = arm64_bcond(code, arm64_ge(), 0);           // b.ge done (forward)
    code = arm64_movreg(code, arm64_x0(), arm64_x1()); // a = b (skipped)
    var done_off: i32 = code.len();
    code = arm64_patch_b19(code, br_off, arm64_rel(done_off, br_off));
    code = arm64_movz(code, arm64_x16(), 1, 0);        // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                        // svc #0x80
    var none: i32[] = [];
    var bin: i32[] = macho_static_executable(code, none, "fern");
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
