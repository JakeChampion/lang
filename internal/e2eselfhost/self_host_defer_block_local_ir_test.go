package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deferBlockLocalCases exercise a `defer` whose action names a local declared
// INSIDE the block the defer sits in (#6821). lower_defers_func replays each
// action wherever the function can exit, the function TAIL included, and by then
// irlower has retired the declaring block's locals out of name resolution — so
// the replay lowered `msg` as a function VALUE and the whole module bailed
// ("references unknown function value"). A defer over a block-scoped local is
// ordinary code that native compiles, so the bail was a pure self-host gap.
//
// Every case encodes its whole contract in main's exit code, through a
// Cell[i32] the deferred action mutates, so the same source serves the x86-64
// and wasm legs unchanged. `want` is what the NATIVE arm64 backend produces for
// the same program — the compiled native path is the spec here, and it is what
// each of these was read off. Values stay under 126: wasmtime rejects an exit
// status outside [0..126), which is a trap rather than a wrong answer and so
// would fail the test for the wrong reason.
var deferBlockLocalCases = []struct {
	name string
	main string
	want int
}{
	// The reported shape: a local declared in an `if` body, read by that body's
	// defer. Cell = 7 (the defer ran), f returned 2.
	{"if_block_local", `function f(a: Cell[i32]): i32 { var n: i32 = 1; if (n > 0) { var k: i32 = 7; defer a.set(a.get() + k); n = n + 1; } return n; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 72},
	// The same gap in each other block-scoping statement. A defer inside a loop
	// body runs at the end of the iteration that executed it (#6379/#6836), so
	// each iteration's action reads that iteration's `k`: 1+2+3 = 6, and 0+2+4 = 6.
	{"while_block_local", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 3) { var k: i32 = i + 1; defer a.set(a.get() + k); i = i + 1; } return i; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 63},
	{"for_block_local", `function f(a: Cell[i32]): i32 { for i in 0..3 { var k: i32 = i * 2; defer a.set(a.get() + k); } return 9; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 69},
	{"match_arm_block_local", `function f(a: Cell[i32], o: Option[i32]): i32 { match (o) { Some(v) => { var k: i32 = v + 1; defer a.set(a.get() + k); }, None => {} } return 1; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a, Some(4)); return a.get() * 10 + r; }`, 51},
	// Two SIBLING blocks declaring one name must resolve to two slots. Both run,
	// LIFO, so the cell walks 0 -> 5 -> 23 and main reports 92. Sharing a single
	// slot makes both replays read the second `k` (0 -> 5 -> 25, main 100), which
	// is the wrong answer this case exists to separate from the right one.
	{"sibling_blocks_same_name", `function f(a: Cell[i32]): i32 { var n: i32 = 1; if (n > 0) { var k: i32 = 3; defer a.set(a.get() * 4 + k); } if (n > 0) { var k: i32 = 5; defer a.set(a.get() * 4 + k); } return 0; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 4 + r; }`, 92},
	// A block that never runs never armed its flag, so its action must not run —
	// the cell stays 0 even though the slot it would have read exists.
	{"block_never_entered", `function f(a: Cell[i32], c: boolean): i32 { if (c) { var k: i32 = 7; defer a.set(a.get() + k); } return 1; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a, false); return a.get() * 10 + r; }`, 1},
	// Control: a nested defer naming NO block local already worked, and still does.
	{"nested_defer_no_local", `function f(a: Cell[i32]): i32 { var n: i32 = 1; if (n > 0) { defer a.set(4); } return 1; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 41},
	// A block local SHADOWING a function-level one: the replay must read the
	// block's slot (8), not the outer name that is still findable (which would
	// leave the cell at 1 and main at 11).
	{"shadows_outer_local", `function f(a: Cell[i32]): i32 { var k: i32 = 1; if (k > 0) { var k: i32 = 8; defer a.set(a.get() + k); } return k; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 81},
	// The action reads the local at EXIT, not at the `defer` — assigning after the
	// defer changes what runs (9, not 2). A capture-by-copy at the defer site
	// would report 21 here.
	{"reads_value_at_exit", `function f(a: Cell[i32]): i32 { var n: i32 = 1; if (n > 0) { var k: i32 = 2; defer a.set(a.get() + k); k = 9; } return 1; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 91},
	// The cleanup is also replayed at every `return`, including one in a LATER
	// block — past the point the declaring block retired its locals.
	{"return_in_later_block", `function f(a: Cell[i32], c: boolean): i32 { var n: i32 = 1; if (n > 0) { var k: i32 = 6; defer a.set(a.get() + k); } if (c) { return 3; } return 4; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a, true); return a.get() * 10 + r; }`, 63},
	// The defer and the local it names need not share a block: an inner block's
	// defer over an OUTER block's local outlives both scopes.
	{"inner_defer_outer_block_local", `function f(a: Cell[i32]): i32 { var n: i32 = 1; if (n > 0) { var k: i32 = 5; if (n > 0) { defer a.set(a.get() + k); } } return 1; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 51},
	// A REFERENCE-COUNTED block local, to show the slot carries its type metadata
	// (m is a string, so `.len()` dispatches str_len) and not just an i32.
	{"string_block_local", `function f(a: Cell[i32], s: string): i32 { if (s.len() > 0) { var m: string = s + "!"; defer a.set(a.get() + m.len()); } return 1; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a, "abc"); return a.get() * 10 + r; }`, 41},
	// The `?` failure edge is a THIRD replay site, reached from lower_try rather
	// than the tail or a rewritten `return` — and it replays the cleanup once
	// past the declaring block and once from inside it, which resolve by
	// different routes (the record, and ordinary scope).
	{"try_edge_after_block", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { var n: i32 = 1; if (n > 0) { var k: i32 = 7; defer a.set(a.get() + k); } var v: i32 = g(x)?; return Some(v + 1); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = 0; match (f(a, 0 - 5)) { Some(v) => { r = v; }, None => { r = 9; } } return a.get() * 10 + r; }`, 79},
	{"try_edge_inside_block", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { var n: i32 = 1; if (n > 0) { var k: i32 = 4; defer a.set(a.get() + k); var v: i32 = g(x)?; return Some(v + n); } return Some(0); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = 0; match (f(a, 0 - 5)) { Some(v) => { r = v; }, None => { r = 9; } } return a.get() * 10 + r; }`, 49},
	// `errdefer` over a block local rides the second cleanup list, so it needs
	// the record on the error path and must stay silent on the Ok path.
	{"errdefer_block_local", `function f(a: Cell[i32], x: i32): Result[i32, i32] { var n: i32 = 1; if (n > 0) { var k: i32 = 6; errdefer a.set(a.get() + k); } if (x < 0) { return Err(1); } return Ok(x); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = 0; match (f(a, 0 - 1)) { Ok(v) => { r = v; }, Err(e) => { r = e; } } return a.get() * 10 + r; }`, 61},
	{"errdefer_block_local_ok_path", `function f(a: Cell[i32], x: i32): Result[i32, i32] { var n: i32 = 1; if (n > 0) { var k: i32 = 6; errdefer a.set(a.get() + k); } if (x < 0) { return Err(1); } return Ok(x); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = 0; match (f(a, 3)) { Ok(v) => { r = v; }, Err(e) => { r = e; } } return a.get() * 10 + r; }`, 3},
	// An edge AHEAD of the defer in the body (#6860). Nothing can be pending
	// there — every edge out of the body clears the iteration's flags — and the
	// action names a local the edge has not reached, so replaying it there both
	// runs nothing and refuses to lower. `for_block_local` above is this shape
	// too: the range desugar opens every loop it builds with `if (i >= $hi)
	// break;`, ahead of the whole body.
	// Two iterations reach the defer, each reading its own `k`: 2 + 4 = 6.
	{"break_before_defer_block_local", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (true) { i = i + 1; if (i >= 3) { break; } var k: i32 = i * 2; defer a.set(a.get() + k); } return 9; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 69},
	// The same for `continue`, and the accumulator is order-sensitive so a run
	// on the skipping iteration would show: 0 -> 1 -> 5 -> 14.
	{"continue_before_defer_block_local", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 4) { i = i + 1; if (i == 2) { continue; } var k: i32 = i; defer a.set(a.get() * 2 + k); } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 14},
	// A labelled edge reaches the outer body from inside a NESTED loop, and this
	// one is ahead of the outer defer as well. Only the first iteration reaches
	// it, so the cell holds one run (3) and f returns 7.
	{"labelled_break_before_defer", `function f(a: Cell[i32]): i32 { var i: i32 = 0; outer: while (i < 3) { var j: i32 = 0; while (j < 2) { if (i == 1) { break outer; } j = j + 1; } var k: i32 = i + 3; defer a.set(a.get() * 4 + k); i = i + 1; } return 7; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 37},
	// The counterpart: an edge BEHIND the defer still replays it, and in a `for`
	// body, where it shares the loop with the desugar's own leading edge.
	// 0 -> 1 (tail) -> 5 (the `break`), and f returns 8.
	{"for_break_after_defer_block_local", `function f(a: Cell[i32]): i32 { for i in 0..5 { var k: i32 = i + 1; defer a.set(a.get() * 3 + k); if (i == 1) { break; } } return 8; }
function main(): i32 { var a: Cell[i32] = cell_new(0); var r: i32 = f(a); return a.get() * 10 + r; }`, 58},
}

// TestSelfHostDeferBlockLocalIRX86_64 runs the cases through the self-hosted
// x86-64 backend, asserting first that the module routes the IR path at all
// (the bail is what #6821 reported) and then that it computes the native answer.
func TestSelfHostDeferBlockLocalIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range deferBlockLocalCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "deferblocklocal_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostDeferBlockLocalIRWasm runs the same cases through the self-hosted
// wasm backend. The resolution lives in irlower, which every backend shares, so
// this leg is what proves the fix is not x86-specific — and it is the leg that
// runs on a host with wasmtime but no x86-64 runner.
func TestSelfHostDeferBlockLocalIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host defer-block-local wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range deferBlockLocalCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = strings.NewReader(tc.main + "\n")
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "dbl_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("defer-block-local wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
