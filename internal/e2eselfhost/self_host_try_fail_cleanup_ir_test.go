package e2eselfhost

import (
	"testing"
)

// tryFailCleanupIRCases pin the RC dec-sweep on the self-host `?` (try) FAILURE
// path (#4334). A failure-path early return with no cleanup leaks every owned
// array / string / struct / map / tuple local live at a `?` when the `?`
// short-circuits — the only uncleaned exit on the IR
// path (StmtReturn already swept). The fix routes the failure return through the
// same emit_dec_sweep_except a normal return runs, mirroring native's
// emitRcDecLocalsAtExit at the TryOp failure edge.
//
// The heap probe ISOLATES the owned-local reclaim from the unrelated Option-box
// safe-leak (an enum box + payload are never swept; ~16 B/iter here on every
// backend, #2704). It runs two 20000-iteration loops over functions that differ
// ONLY by an owned local (array / string) declared live across a FAILING `?`,
// and compares the bump high-water growth: if the owned local is reclaimed the
// delta is ~0 (both loops leak only their Option boxes), and if it leaks the
// owned loop grows by 20000 * ~20 B ≈ 400000. Expectations are the native
// result (native reclaims — validated exit 7). Without the fix the self-host
// owned loop leaks and returns 1.
var tryFailCleanupIRCases = []struct {
	name string
	main string
	want int
}{
	// Owned i32[] live across a failing `?` — reclaimed => extra growth ~0.
	{"try-fail-array-reclaimed",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { let x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { let owned: i32[] = [1, 2, 3, 4, 5]; let x: i32 = fails()?; return Some(x + owned[0]); }
function main(): i32 {
    let b0: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    let base: i32 = (__heap_bump_bytes() as i32) - b0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, 7},
	// Owned heap string live across a failing `?` — exercises __fern_str_free in
	// the sweep.
	{"try-fail-string-reclaimed",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { let x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { let owned: string = "abcdefghijklmnop" + "!"; let x: i32 = fails()?; return Some(x + owned.len()); }
function main(): i32 {
    let b0: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    let base: i32 = (__heap_bump_bytes() as i32) - b0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, 7},
	// The SUCCESS path is unchanged by the failure-path edit: the `?` still
	// yields the payload and the value is correct (the sweep is emitted only in
	// the failure block, under the return value already on the operand stack).
	{"try-success-value",
		`function ok(): Option[i32] { return Some(41); }
function step(): Option[i32] { let owned: i32[] = [1]; let x: i32 = ok()?; return Some(x + owned[0]); }
function main(): i32 { match (step()) { Some(v) => { return v - 35; }, None => { return 1; } } }`, 7},
}

// TestSelfHostTryFailCleanupIR runs each case through the self-host CLI on
// x86-64 and wasm against each row's expected exit code; the rows read the
// bump allocator, which the interpreter has no model of. Wasm's sweep uses
// __fern_rc_dec / __fern_str_free the same way.
func TestSelfHostTryFailCleanupIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tryFailCleanupIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
