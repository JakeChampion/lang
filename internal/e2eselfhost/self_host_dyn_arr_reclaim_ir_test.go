package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostDynArrReclaimIRX86_64 pins the #4351 slice-3 surface: a
// dyn-Trait ARRAY local built from an all-literal array literal
// (`var xs: dyn T[] = [41, "s", Dot{..}]`). Every element is an rc-headered
// box (a prim/string op_dyn_box cell — slice 2 — or a scalar-only leak-safe
// struct box), sole-owned by the buffer, so the exit sweep releases each
// element by its recorded kind ('s' first frees the sole-owned inner string
// box at cell@8) before the buffer dec — credited "DARR:<name>|<kinds>" by
// reclaimable_names_of under the darr_unsafe_for element-hazard walk
// (dispatch only in transient positions; element bindings, for-in, appends,
// stores, and escapes all exclude — those arrays keep today's sound leak).
func TestSelfHostDynArrReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = elements leaked; 99 = over-release/UAF; 97 = value corrupted)", name, code, want)
		}
	}

	// MIXED literal elements (prim + string + scalar-only struct + a variant
	// construction, #4351 slice 4), RECLAIM, BOUNDED HIGH-WATER: every element
	// box + the string's inner box + the buffer free per call — a second
	// 3000-iteration churn stays flat.
	run(t, `trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
enum Op { Add(i32), Neg }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
impl Show for Dot { function show(self: Self): i32 { return self.r * 2; } }
impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } }
function go(k: i32): i32 { var xs: dyn Show[] = [41, "hello", Dot { r: k }, Add(7)]; return xs[0].show() + xs[1].show() + xs[2].show() + xs[3].show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(3000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-arr-mixed-reclaim-flat", 0)

	// INDEXED-LOOP dispatch: `while (j < xs.len()) { acc + xs[j].show() }` —
	// the len() receiver and transient dispatches are admitted; still flat.
	// (Elements must be LITERALS — a param-derived element like `[k, 5]`
	// stays uncredited/sound-leak; the literal-only gate is the slice.)
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var xs: dyn Show[] = [4, 5, 7]; var t: i32 = 0; var j: i32 = 0; while (j < xs.len()) { t = t + xs[j].show(); j = j + 1; } return t + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(3000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-arr-idx-loop-reclaim-flat", 0)

	// ELEMENT BINDING excluded: `var e = xs[0]` is a lasting element alias —
	// darr_expr_unsafe rejects the candidate; values + detector stay clean
	// (the array leaks, sound).
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var xs: dyn Show[] = [k, 5]; var e = xs[0]; return e.show() + xs[1].show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-arr-elem-binding-excluded", 0)

	// FOR-IN excluded: the loop var is a lasting element binding this slice
	// doesn't track — candidate rejected, values + detector clean.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var xs: dyn Show[] = [k, 5]; var t: i32 = 0; for x in xs { t = t + x.show(); } return t; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-arr-forin-excluded", 0)

	// ALIASED excluded: `var ys = xs` — a bare-ident alias rejects the
	// candidate (both names see live elements through frame exit; the array
	// leaks, sound). Values + detector stay clean.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var xs: dyn Show[] = [41, 5]; var ys = xs; return ys[0].show() + xs[1].show() + k; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 51) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-arr-aliased-excluded", 0)

	// RETURNED `dyn T[]` dispatches in the caller (#4780): struct_ret_fns_of
	// now records the coarse "dyn <Trait>" ELEMENT type for a `dyn Trait[]`
	// return (exactly as the literal-binding and param paths tag it), so an
	// unannotated `var ys = mk()` — and the annotated form — recover the
	// trait and route `ys[i].m()` through op_dyn_dispatch. Pre-fix this
	// mis-dispatched (returned 74 for a want-10 program). The returned array
	// itself is escaping → excluded from the DARR sweep (leaks, sound):
	// values + detector pin correctness, not flatness.
	run(t, `trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
impl Show for Dot { function show(self: Self): i32 { return self.r * 2; } }
function mk(k: i32): dyn Show[] { var xs: dyn Show[] = [k, Dot { r: 5 }]; return xs; }
function go(k: i32): i32 { var ys: dyn Show[] = mk(k); var zs = mk(k + 1); return ys[0].show() + ys[1].show() + zs[0].show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 19) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-arr-returned-dispatch", 0)
}

// TestSelfHostDynArrReclaimWasmIR: the wasm sibling through the -ir driver.
func TestSelfHostDynArrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping dyn arr reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
		{"dyn-arr-mixed-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
impl Show for Dot { function show(self: Self): i32 { return self.r * 2; } }
function go(k: i32): i32 { var xs: dyn Show[] = [41, "hello", Dot { r: k }]; return xs[0].show() + xs[1].show() + xs[2].show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-arr-returned-dispatch-wasm", `trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
impl Show for Dot { function show(self: Self): i32 { return self.r * 2; } }
function mk(k: i32): dyn Show[] { var xs: dyn Show[] = [k, Dot { r: 5 }]; return xs; }
function go(k: i32): i32 { var ys: dyn Show[] = mk(k); var zs = mk(k + 1); return ys[0].show() + ys[1].show() + zs[0].show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 19) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`, 0},
		{"dyn-arr-forin-excluded-wasm", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var xs: dyn Show[] = [k, 5]; var t: i32 = 0; for x in xs { t = t + x.show(); } return t; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 10) { bad = 1; } i = i + 1; } return bad; }
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
				t.Errorf("dyn arr reclaim wasm IR %q = %d, want %d (98 = leaked; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostDynArrReclaimIRArm64: the arm64 sibling under qemu.
func TestSelfHostDynArrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	prog := `trait Show { function show(self: Self): i32; }
struct Dot { r: i32 }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
impl Show for Dot { function show(self: Self): i32 { return self.r * 2; } }
function go(k: i32): i32 { var xs: dyn Show[] = [41, "hello", Dot { r: k }]; return xs[0].show() + xs[1].show() + xs[2].show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "dyn-arr-mixed-reclaim-flat-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("dyn-arr-mixed-reclaim-flat-arm64 exited %d, want 0 (98 = leaked; 99 = over-release; 97 = corrupted)", code)
	}
}
