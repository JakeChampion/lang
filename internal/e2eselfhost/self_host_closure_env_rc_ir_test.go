package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostClosureEnvRcIRX86_64 pins the #4354 closure-env capture-RC slice
// on the IR path. A closure local bound once from a LITERAL lambda, never
// reassigned, non-escaping (body_unsafe_for-clean) is approved for capture RC:
// the env build retains each classifiable capture ('s' string / 'a' scalar
// array — read back from the env box, __fern_rc_inc'd), and the exit sweep
// releases them (rc==1-gated walk: __fern_str_free / __fern_rc_dec) before the
// env box dec. The SAME kinds string drives both sides, so incs and drops land
// together (the #4354 invariant). A captured FRESH string re-enters the STR:
// sweep (the capture is now a counted reference), closing the leak; anything
// unclassified or escaping keeps today's sound leak.
func TestSelfHostClosureEnvRcIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = capture leaked; 99 = over-release/UAF; 97 = value corrupted)", name, code, want)
		}
	}

	// STRING capture, reclaimed: nm (a fresh concat) is captured, the closure
	// called, both die at exit. Pre-slice nm leaked every call (excluded from
	// the STR: sweep by the lambda escape); now build-site inc (rc 2) + env
	// release (→1) + string sweep (→0) free it exactly once. After a
	// 3000-iteration warmup a second churn stays flat (< 256 B slack).
	run(t, `function go(pre: string): i32 { var nm: string = pre + "xyz"; var c = () => nm.len(); return c(); }
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"closure-string-capture-flat", 0)

	// SCALAR-ARRAY capture, balanced: xs is swept by its own slot AND released
	// by the env — the build-site inc makes that two decs against rc 2, freed
	// exactly once (underflow 0), flat across the second churn.
	run(t, `function go(k: i32): i32 { var xs: i32[] = [k, k + 1, k + 2]; var c = () => xs[0] + xs[2]; return c(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(i)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"closure-array-capture-flat", 0)

	// CAPTURE USED AFTER the closure: nm read directly after c() — the release
	// only runs at exit (after all uses), and the ordering (env release before
	// the string sweep) frees nm exactly once. Values + detector checked.
	run(t, `function go(pre: string): i32 { var nm: string = pre + "xy"; var c = () => nm.len(); var r: i32 = c(); return r + nm.len(); }
function churn(m: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(pre) != 8) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"closure-capture-used-after-balanced", 0)

	// PARAM-STRING capture, balanced: pre belongs to the caller — the inc/release
	// pair nets zero and the owner frees it, so nothing double-frees. Detector 0.
	run(t, `function go(pre: string): i32 { var c = () => pre.len(); return c(); }
function churn(m: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(pre) != 2) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"closure-param-capture-balanced", 0)
}

// TestSelfHostClosureEnvRcWasmIR: the wasm sibling through the -ir driver.
func TestSelfHostClosureEnvRcWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping closure-env RC wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"closure-string-capture-flat-wasm", `function go(pre: string): i32 { var nm: string = pre + "xyz"; var c = () => nm.len(); return c(); }
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"closure-array-capture-flat-wasm", `function go(k: i32): i32 { var xs: i32[] = [k, k + 1, k + 2]; var c = () => xs[0] + xs[2]; return c(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(i)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("closure-env RC wasm IR %q = %d, want %d (98 = capture leaked; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostClosureEnvRcIRArm64: the arm64 sibling under qemu.
func TestSelfHostClosureEnvRcIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	prog := `function go(pre: string): i32 { var nm: string = pre + "xyz"; var c = () => nm.len(); return c(); }
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "closure-string-capture-flat-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("closure-string-capture-flat-arm64 exited %d, want 0 (98 = capture leaked; 99 = over-release)", code)
	}
}
