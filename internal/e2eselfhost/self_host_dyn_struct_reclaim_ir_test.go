package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostDynStructReclaimIRX86_64 pins the #4351 slice: a dyn-Trait local
// holding a statically-known STRUCT payload (`var d: dyn T = C { ... }` — the
// struct flows UNBOXED behind the coercion, so the local holds the concrete's
// rc-headered box) is credited "DYN:<name>|<Concrete>" by reclaimable_names_of
// and released by the exit sweep: __struct_drop_<Concrete> deep-drops the
// concrete's rc fields (only when it has any), then the box is dec'd. Gates
// mirror the enum struct-payload path: fresh leak-safe struct LITERAL init,
// single-bind, never reassigned, non-escaping. Escaping/reassigned dyn locals
// and primitive/string dyn payloads (a separate HEADERLESS box cell) keep
// today's sound leak.
func TestSelfHostDynStructReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = dyn box leaked; 99 = over-release/UAF; 97 = value corrupted)", name, code, want)
		}
	}

	// RECLAIM, BOUNDED HIGH-WATER: the Circle behind the dyn (with an rc array
	// field) is built and dropped every call. Pre-slice the box + its tags
	// buffer leaked per call (the coarse "dyn Show" slot type kept it out of
	// every reclaim class); now the sweep deep-drops tags then frees the box —
	// a second 3000-iteration churn stays flat.
	run(t, `trait Show { function show(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Show for Circle { function show(self: Self): i32 { return self.r * self.r + self.tags[0]; } }
function go(k: i32): i32 { var d: dyn Show = Circle { r: k, tags: [7, 8] }; return d.show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(3000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-struct-reclaim-flat", 0)

	// SCALAR-ONLY concrete: no deep-drop emitted (no reclaimable field), the
	// box alone is freed — still flat, no underflow.
	run(t, `trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
impl Show for Dot { function show(self: Self): i32 { return self.r + 1; } }
function go(k: i32): i32 { var d: dyn Show = Dot { r: k }; return d.show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(3000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-scalar-struct-reclaim-flat", 0)

	// ESCAPING dyn excluded: `return d` — body_unsafe_for rejects the
	// candidate, the caller's use stays valid, detector 0.
	run(t, `trait Show { function show(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Show for Circle { function show(self: Self): i32 { return self.r + self.tags[0]; } }
function mk(k: i32): dyn Show { var d: dyn Show = Circle { r: k, tags: [5, 6] }; return d; }
function go(k: i32): i32 { var e = mk(k); return e.show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 8) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-escaping-excluded", 0)

	// REASSIGNED dyn excluded: `d = C{..}` rebind — the concretes could
	// differ, so the candidate is rejected (both boxes leak, sound). Values +
	// detector checked.
	run(t, `trait Show { function show(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Show for Circle { function show(self: Self): i32 { return self.r; } }
function go(k: i32): i32 { var d: dyn Show = Circle { r: k, tags: [1] }; d = Circle { r: k + 1, tags: [2] }; return d.show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 4) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-reassigned-excluded", 0)
}

// TestSelfHostDynStructReclaimWasmIR: the wasm sibling through the -ir driver.
func TestSelfHostDynStructReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping dyn struct reclaim wasm IR e2e")
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
		{"dyn-struct-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Show for Circle { function show(self: Self): i32 { return self.r * self.r + self.tags[0]; } }
function go(k: i32): i32 { var d: dyn Show = Circle { r: k, tags: [7, 8] }; return d.show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-escaping-excluded-wasm", `trait Show { function show(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Show for Circle { function show(self: Self): i32 { return self.r + self.tags[0]; } }
function mk(k: i32): dyn Show { var d: dyn Show = Circle { r: k, tags: [5, 6] }; return d; }
function go(k: i32): i32 { var e = mk(k); return e.show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 8) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`, 0},
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
				t.Errorf("dyn struct reclaim wasm IR %q = %d, want %d (98 = leaked; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostDynStructReclaimIRArm64: the arm64 sibling under qemu.
func TestSelfHostDynStructReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	prog := `trait Show { function show(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Show for Circle { function show(self: Self): i32 { return self.r * self.r + self.tags[0]; } }
function go(k: i32): i32 { var d: dyn Show = Circle { r: k, tags: [7, 8] }; return d.show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "dyn-struct-reclaim-flat-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("dyn-struct-reclaim-flat-arm64 exited %d, want 0 (98 = leaked; 99 = over-release)", code)
	}
}
