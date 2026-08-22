package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// dynCallReclaimCases pin the #4351 slice that closes the three remaining
// NON-LITERAL-initialiser holes in dyn-payload reclaim:
//
//  1. A STRICT-FRESH CALL result (`var d: dyn T = mk(k)`, `= f.make(k)`) is
//     credited "DYN:<name>|<Concrete>" and released by the exit sweep, exactly
//     as a struct-LITERAL init already was. Only the literal / prim /
//     variant-ctor arms of collect_dyn_struct_in_stmt used to credit, so a call
//     result fell through and leaked the concrete's whole rc-headered box —
//     40 B/round on x86-64 for a scalar-only concrete, 88 with an rc field.
//  2. A COMPUTED non-string PRIMITIVE payload (`var d: dyn T = k * 2`). The
//     coercion COPIES the value into a fresh op_dyn_box cell, so the local owns
//     that cell however the value was produced — but only a prim LITERAL was
//     credited, and everything else leaked the 40-byte cell.
//  3. A dyn local declared inside an if / while / for / match BODY. The credit
//     is keyed by name and retire_locals renames a block-scoped slot to
//     "!retired!<name>" on block exit, so the sweep and the entry-zeroing both
//     missed it — a literal payload leaked there too.
//
// The flat cases RETURN THE MEASURED BYTES PER ROUND (clamped at 95) rather
// than a boolean verdict, so a regression reports its own size: pre-fix the
// headline case exits 40, post-fix 0. 99 = rc over-release, 97 = the two churns
// disagreed (a value was corrupted), 96 = wrong answer.
//
// The gate cases must NOT move: a bare-ident STRUCT alias, a non-strict-fresh
// (identity) callee, a PARAM receiver whose type the AST scan cannot resolve,
// an escaping `return d`, and a STRING payload behind a non-literal init (its
// cell holds a pointer to a box that may be borrowed) all stay uncredited — a
// sound leak. Each reads its aliased source AFTER the dyn local is done with
// it, so an over-admission surfaces as a wrong answer or an underflow tick
// rather than as silence.
//
// They run under FERN_STRICT_IR=1 (#6602): a per-function bail would reach the
// same exit code by the AST route on the commit the fix has not landed on.
var dynCallReclaimCases = []struct {
	name string
	src  string
	exit int
}{
	// The headline shape. `mk` is strict-fresh (its only return is a fresh
	// struct literal), so its result is this frame's rc==1 box — the same
	// standing the literal init has.
	{"call-result-scalar-concrete", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function mk(k: i32): Square { return Square { side: k }; }
function go(k: i32): i32 { var d: dyn Shape = mk(k); return d.area(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i64 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } var per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	// An rc-ARRAY field on the concrete: the sweep has to reach
	// __struct_drop_Circle for the tags buffer, not just dec the box.
	{"call-result-rc-field-concrete", `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32, tags: i32[] }
impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r + self.tags[0]; } }
function mk(k: i32): Circle { return Circle { r: k, tags: [7, 8] }; }
function go(k: i32): i32 { var d: dyn Shape = mk(k); return d.area(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i64 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } var per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	// A METHOD callee: the `dyn Shape` annotation cannot name the receiver, so
	// the key comes from the receiver LOCAL's own annotation.
	{"method-call-result", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
struct Factory { base: i32 }
impl Factory { function make(self: Self, k: i32): Square { return Square { side: k + self.base }; } }
function go(k: i32): i32 { var f: Factory = Factory { base: 1 }; var d: dyn Shape = f.make(k); return d.area(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i64 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } var per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	// GATE: a bare-ident init aliases a local that outlives the coercion. The
	// dyn local must stay uncredited or `s.side` reads freed memory.
	{"alias-init-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function mk(k: i32): Square { return Square { side: k }; }
function go(k: i32): i32 { var s: Square = mk(k); var d: dyn Shape = s; var a: i32 = d.area(); return a + s.side; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 12) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	// GATE: the callee hands back its PARAMETER, so it is not strict-fresh and
	// the result is the caller's box, not a fresh one.
	{"identity-callee-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function ident(s: Square): Square { return s; }
function go(k: i32): i32 { var live: Square = Square { side: k }; var d: dyn Shape = ident(live); var a: i32 = d.area(); return a + live.side; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 12) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	// GATE: a PARAM receiver carries no `var` annotation for the AST scan to
	// read, so the method key does not resolve and the box keeps today's leak.
	// The receiver is read after the dispatch, so a mistaken credit would show.
	{"param-receiver-method-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
struct Factory { base: i32 }
impl Factory { function make(self: Self, k: i32): Square { return Square { side: k + self.base }; } }
function go(f: Factory, k: i32): i32 { var d: dyn Shape = f.make(k); var a: i32 = d.area(); return a + f.base; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { var f: Factory = Factory { base: 1 }; if (go(f, 3) != 17) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	// A COMPUTED primitive payload: op_dyn_box copies the value into a fresh
	// cell, so the escape verdict is the whole admission.
	{"computed-prim-payload", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var d: dyn Show = k * 2; return d.show(); }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i64 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } var per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	// The value comes from a LOCAL rather than an expression — still a copy
	// into the cell, and the source stays readable afterwards.
	{"prim-payload-from-local", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function go(k: i32): i32 { var n: i32 = k * 3; var d: dyn Show = n; var a: i32 = d.show(); return a + n; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 19) { return 96; } acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); if (w == 96) { return 96; } var b1: i64 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } var per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	// BLOCK-SCOPED: the dyn local lives in an if body, so retire_locals renames
	// its slot before the sweep reads the credit.
	{"block-scoped-struct-payload", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function go(k: i32): i32 { var t: i32 = 0; if (k > 0) { var d: dyn Shape = Square { side: k }; t = d.area(); } else { t = 5; } return t; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(3)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i64 = __heap_bump_bytes(); var x: i32 = churn(2000); var b2: i64 = __heap_bump_bytes(); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } var per: i64 = (b2 - b1) / 2000; if (per > 95) { per = 95; } return (per as i32); }`, 0},

	// The UNTAKEN branch of the same shape: the slot is entry-zeroed by the
	// prologue and the sweep decs a null rather than stack garbage.
	{"block-scoped-untaken-branch", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function go(k: i32): i32 { var t: i32 = 0; if (k > 100) { var d: dyn Shape = Square { side: k }; t = d.area(); } else { t = 5; } return t; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 5) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	// GATE: a STRING payload from a non-literal init. Its cell is fresh, but
	// the box at cell@8 is the LOCAL's, and the "string" tag's sweep would free
	// it — so the whole binding stays uncredited and `s.len()` still reads.
	{"string-payload-alias-excluded", `trait Show { function show(self: Self): i32; }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function go(k: i32): i32 { var s: string = "abcd"; var d: dyn Show = s; var a: i32 = d.show(); return a + s.len() + k - k; }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 8) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	// GATE: `return d` escapes the box to the caller — body_unsafe_for refuses,
	// and the caller's dispatch has to still find a live payload.
	{"escaping-dyn-excluded", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function mk(k: i32): Square { return Square { side: k }; }
function mkd(k: i32): dyn Shape { var d: dyn Shape = mk(k); return d; }
function go(k: i32): i32 { var e = mkd(k); return e.area(); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 9) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},

	// GATE: the prim credit is otherwise unconditional, so the escape gates are
	// all that keep an escaping or reassigned cell out of the sweep.
	{"escaping-and-reassigned-prim-excluded", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self + 1; } }
function mkd(k: i32): dyn Show { var d: dyn Show = k * 2; return d; }
function reasg(k: i32): i32 { var d: dyn Show = k * 2; d = k * 3; return d.show(); }
function go(k: i32): i32 { return mkd(k).show() + reasg(k); }
function churn(m: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < m) { if (go(3) != 17) { bad = 96; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
}

// TestSelfHostDynCallReclaimIRX86_64 — the x86-64 IR path.
func TestSelfHostDynCallReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range dynCallReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (a small non-zero code is the leaked bytes per round; 99 = over-release, 97 = churns disagreed, 96 = wrong answer)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostDynCallReclaimIRArm64 — the arm64 IR path, under qemu.
func TestSelfHostDynCallReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range dynCallReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (a small non-zero code is the leaked bytes per round; 99 = over-release, 97 = churns disagreed, 96 = wrong answer)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostDynCallReclaimIRWasm — the wasm IR path.
func TestSelfHostDynCallReclaimIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn call-result reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range dynCallReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (a small non-zero code is the leaked bytes per round; 99 = over-release, 97 = churns disagreed, 96 = wrong answer)", tc.name, code, tc.exit)
			}
		})
	}
}
