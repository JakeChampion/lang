package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostDynPrimReclaimIRX86_64 pins the #4351 slice-2 surface: a
// dyn-Trait local holding a PRIMITIVE/STRING literal payload
// (`var d: dyn T = 41` / `= "lit"`). The payload is heap-boxed into an
// op_dyn_box cell — now rc-HEADERED via __fern_arr_box(cap=2) instead of a
// raw headerless __fern_alloc(16), so the exit sweep can free it. The
// binding is credited "DYN:<name>|<prim>" by reclaimable_names_of (fresh
// LITERAL init, single-bind, never reassigned, non-escaping) and released
// by the exit sweep: a "string" tag first frees the sole-owned inner
// string box at cell@8 (rc==1-gated via __fern_rc_is_unique, which doubles
// as the null guard), then every prim tag decs the cell. Escaping /
// reassigned / non-literal dyn locals keep today's sound leak.
func TestSelfHostDynPrimReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = dyn cell leaked; 99 = over-release/UAF; 97 = value corrupted)", name, code, want)
		}
	}

	// PRIM i32 payload, RECLAIM, BOUNDED HIGH-WATER: the rc-headered
	// op_dyn_box cell frees per call — a second 3000-iteration churn stays
	// flat. Pre-slice the headerless cell leaked 16 bytes per call.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var d: dyn Show = 41; return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-prim-i32-reclaim-flat", 0)

	// STRING literal payload: the sweep frees the inner string box
	// (value@8) then decs the cell — both per-iteration allocations
	// reclaim, flat churn, no underflow.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { var d: dyn Show = "hello"; return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-prim-str-reclaim-flat", 0)

	// ESCAPING dyn excluded: `return d` — body_unsafe_for rejects the
	// candidate (bare-ident return), the caller's dispatch stays valid,
	// detector 0. The cell leaks — sound.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function mk(k: i32): dyn Show { var d: dyn Show = 41; return d; }
function go(k: i32): i32 { var e = mk(k); return e.show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 42) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-prim-escaping-excluded", 0)

	// REASSIGNED dyn excluded: `d = 41` rebind — the reassigned-names gate
	// rejects the candidate (both cells leak, sound). Values + detector
	// checked.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var d: dyn Show = 10; d = 41; return d.show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 42) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-prim-reassigned-excluded", 0)

	// ENUM payload (#4351 slice 4): `var d: dyn T = V(<args>)` — the variant
	// construction is a fresh rc-headered enum box, credited with the ENUM
	// name as its tag; the sweep's shallow dec frees it (pointer payloads
	// would leak with it — safe). Flat churn, no underflow. (An enum LOCAL
	// coerced to dyn — `var e: Op = Add(k); var d: dyn Show = e;` — currently
	// mis-dispatches on the IR path, a pre-existing gap tracked separately,
	// so only the direct-construction shape is pinned here.)
	run(t, `trait Show { function show(self: Self): i32; }
enum Op { Add(i32), Neg }
impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } }
function go(k: i32): i32 { var d: dyn Show = Add(41); return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"dyn-enum-payload-reclaim-flat", 0)

	// ALIASED STRING payload excluded: `var d: dyn Show = s` where s is a
	// string local — a non-literal init is never credited, so the inner
	// free can't fire on a box someone else owns. Values + detector.
	run(t, `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { var s: string = "world"; var d: dyn Show = s; return d.show() + s.len(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"dyn-prim-aliased-str-excluded", 0)
}

// TestSelfHostDynPrimReclaimWasmIR: the wasm sibling through the -ir
// driver. The wasm op_dyn_box cell was already rc-headered
// ($__fern_str_box), so this pins the crediting + sweep on that backend.
func TestSelfHostDynPrimReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping dyn prim reclaim wasm IR e2e")
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
		{"dyn-prim-i32-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var d: dyn Show = 41; return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-prim-str-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { var d: dyn Show = "hello"; return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-enum-payload-reclaim-flat-wasm", `trait Show { function show(self: Self): i32; }
enum Op { Add(i32), Neg }
impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } }
function go(k: i32): i32 { var d: dyn Show = Add(41); return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"dyn-prim-escaping-excluded-wasm", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function mk(k: i32): dyn Show { var d: dyn Show = 41; return d; }
function go(k: i32): i32 { var e = mk(k); return e.show(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 42) { bad = 1; } i = i + 1; } return bad; }
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
				t.Errorf("dyn prim reclaim wasm IR %q = %d, want %d (98 = leaked; 99 = over-release)", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostDynPrimReclaimIRArm64: the arm64 sibling under qemu — the
// i32 and string payloads through the arm64 IR backend's rc-headered
// __fern_arr_box dyn cell.
func TestSelfHostDynPrimReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		prog string
	}{
		{"dyn-prim-i32-reclaim-flat-arm64", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var d: dyn Show = 41; return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`},
		{"dyn-prim-str-reclaim-flat-arm64", `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { var d: dyn Show = "hello"; return d.show() + k; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`},
	} {
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("%s exited %d, want 0 (98 = leaked; 99 = over-release; 97 = corrupted)", tc.name, code)
		}
	}
}
