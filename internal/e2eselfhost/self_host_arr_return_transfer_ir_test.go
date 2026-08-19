package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArrReturnTransferIRX86_64 pins the array return-transfer retain
// (#4357 probe finding): `function id(a: i32[]): i32[] { return a; }` returned
// a bare PARAM array with NO retain, while the caller's binding slot is
// unconditionally exit-swept — so `var t = id(s)` over-released s's buffer
// (rc 1 → 0 while the owner still held it: a use-after-free surfaced by the
// underflow detector, values corrupted once the freed block was recycled).
// The StmtReturn lowering now emits the native-parity return-transfer
// __fern_rc_inc for a bare param-array return (mirroring the struct-field
// Perceus dup on the adjacent return paths), so the escaping return is a
// counted reference the owner's later dec frees exactly once. Fresh returns
// (literals, built-up locals via move-on-return) are unchanged — no new inc,
// still reclaimed flat.
func TestSelfHostArrReturnTransferIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = leak; 99 = over-release/UAF; 97 = value corrupted)", name, code, want)
		}
	}

	// ALIAS-RETURNING callee: pre-fix this under-flowed (99) — id hands back
	// s's buffer uncounted, t's sweep freed it, s's owner double-dec'd. With
	// the return-transfer inc every call is balanced and s stays live across
	// 4000 calls. Values checked, detector 0.
	run(t, `function id(a: i32[]): i32[] { return a; }
function f(s: i32[]): i32 { var t: i32[] = id(s); return t[0] + s[1]; }
function churn(m: i32): i32 { var s: i32[] = [1, 2, 3]; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var x: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } if (w != x) { return 97; } return 0; }`,
		"arr-return-transfer-alias-balanced", 0)

	// FRESH-RETURNING callee unchanged: the dead intermediate t (the #4357
	// shape) still reclaims flat — no new inc on literal returns, the second
	// churn re-serves everything from the freelist (< 256 B slack).
	run(t, `function mk(k: i32): i32[] { return [k, k + 1, k + 2]; }
function f(s: i32[]): i32 { var t: i32[] = mk(s[0]); var u: i32 = t[0] + s[1]; return u; }
function churn(m: i32): i32 { var s: i32[] = [1, 2, 3]; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"arr-return-transfer-fresh-flat", 0)

	// MIXED per-path returns: one path returns the param (inc'd), the other a
	// fresh literal (moved). Both callers' sweeps balance; flat + detector 0.
	run(t, `function pick(a: i32[], b: i32[], c: i32): i32[] { if (c > 0) { return a; } return [b[0], 9]; }
function f(s: i32[]): i32 { var t: i32[] = pick(s, s, 1); var u: i32[] = pick(s, s, 0); return t[0] + u[1] + s[1]; }
function churn(m: i32): i32 { var s: i32[] = [1, 2, 3]; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"arr-return-transfer-mixed-paths", 0)
}

// TestSelfHostArrReturnTransferWasmIR: the wasm sibling — same programs
// through the -ir driver under wasmtime.
func TestSelfHostArrReturnTransferWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arr return-transfer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"arr-return-transfer-alias-balanced-wasm", `function id(a: i32[]): i32[] { return a; }
function f(s: i32[]): i32 { var t: i32[] = id(s); return t[0] + s[1]; }
function churn(m: i32): i32 { var s: i32[] = [1, 2, 3]; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(1000); var x: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } if (w != x) { return 97; } return 0; }`, 0},
		{"arr-return-transfer-fresh-flat-wasm", `function mk(k: i32): i32[] { return [k, k + 1, k + 2]; }
function f(s: i32[]): i32 { var t: i32[] = mk(s[0]); var u: i32 = t[0] + s[1]; return u; }
function churn(m: i32): i32 { var s: i32[] = [1, 2, 3]; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(1000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(1000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
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
				t.Errorf("arr return-transfer wasm IR %q = %d, want %d (99 = over-release/UAF)", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostArrReturnTransferIRArm64: the arm64 sibling under qemu.
func TestSelfHostArrReturnTransferIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	prog := `function id(a: i32[]): i32[] { return a; }
function f(s: i32[]): i32 { var t: i32[] = id(s); return t[0] + s[1]; }
function churn(m: i32): i32 { var s: i32[] = [1, 2, 3]; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(1000); var x: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } if (w != x) { return 97; } return 0; }`
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "arr-return-transfer-alias-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arr-return-transfer-alias-arm64 exited %d, want 0 (99 = over-release/UAF)", code)
	}
}
