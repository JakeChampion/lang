package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// growSoleCases pin #6048: the #6043 sole-occurrence death, ported to the self-host
// grow bracket. A PARAM read exactly once in the whole body, at a call no loop or
// lambda encloses, is dead at that call — so the #4873 containment bracket need not
// force a full-buffer copy on it.
//
// Oracle is `__arr_push_shared_count()`: the number of appends that took the copy
// path because the buffer had more than one reference. The driver checks the
// accumulator's length and endpoints BEFORE reading the counter, so a miscompile
// reports 254/253 rather than a plausible count — a count is only meaningful on a
// program that computed the right answer.
//
// Measured on the self-host, x86-64, against NATIVE on the identical corpus:
//
//	                           native   self-host
//	A_inline_append_tail            0     0    control
//	C_call_tail                     0     0    control
//	H_call_result_into_local        0     0    the shape this issue is about
//	I_call_result_into_param        0     0    already exempt (self-reassign shape)
//	L_two_calls_via_param           0     0
//	J_nested_call_arg              49    44
//	K_two_calls_via_local          49    44
//	M_call_then_inline_append      49    44
//
// J/K/M COPY, AND ARE PINNED THAT WAY DELIBERATELY. They read 0 between the #6048
// port and the counted identity return
// (docs/rc-log/2026-09-02-identity-return-counted.md), which made every
// array-producing call result an owned reference. That convention is what a
// sole-occurrence exemption cannot survive on these three: the inner call's result
// is an argument temp with a count of its own, and nothing releases it before the
// outer append reads the buffer as shared. Native copies on all three for its own
// reasons, so the two compilers now AGREE that a copy happens and differ only in
// the tally — where before the self-host was doing fewer copies than native and
// this comment recorded that as a divergence to watch.
//
// Closing the remainder is the argument-temp slice
// (docs/rc-log/2026-09-02-consumed-array-arg-temp.md), not this rule. When it lands
// these three go to 0 and the pins move with it; a row that moves on its own is a
// regression.
//
// L IS NOT ONE OF THEM, and was briefly pinned at 44 as though it were. Native
// reads 0 for L and always has, so 44 there was a self-host regression wearing the
// argument-temp class's clothes: the frame owns `b` after `b = f(b, v)`,
// `return f(b, v + 1)` hands back a result carrying its own count, and
// emit_consumed_param_exit skipped the release because `b` was NAMED in the
// returned expression. rc stayed at 2 with one owner and the caller's next append
// copied. Fixed in docs/rc-log/2026-09-02-consumed-param-returned-call-release.md;
// L is back to 0 and pinned there.
//
// The loop/lambda exclusion and the params-only restriction are both carried over
// from native and are load-bearing; `grow_sole_exempt_names_of` says why.
type growSoleCase struct {
	name    string
	g       string
	appends int
	want    int
}

var growSoleCases = []growSoleCase{
	// Controls: clean before and after, so a regression in the shared machinery
	// shows up here rather than being read as part of the intended change.
	{"A_inline_append_tail", `function g(a: i32[], v: i32): i32[] { return a.append(v); }`, 1, 0},
	{"C_call_tail", `function g(b: i32[], v: i32): i32[] { return f(b, v); }`, 1, 0},
	{"I_call_result_into_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return b; }`, 1, 0},
	{"L_two_calls_via_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }`, 2, 0},

	// The shape #6048 is about: the call result is materialised into a local and
	// handed back, so `b` is read exactly once and dies at that call. 45 -> 0.
	{"H_call_result_into_local", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return t; }`, 1, 0},

	// The argument-temp class: the inner call's result is a counted temp nothing
	// releases before the outer append, so the buffer reads as shared and copies.
	// Native copies here too (49). Pinned at what both compilers now do — see the
	// header for why 0 is not reachable until the argument-temp slice lands.
	{"J_nested_call_arg", `function g(b: i32[], v: i32): i32[] { return f(f(b, v), v + 1); }`, 2, 44},
	{"K_two_calls_via_local", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return f(t, v + 1); }`, 2, 44},
	{"M_call_then_inline_append", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return t.append(v + 1); }`, 2, 44},

	// LOOP negative: `b` is textually read once, but the read sits inside a loop, so
	// it is many DYNAMIC reads and the next iteration would observe the previous
	// one's in-place growth. The rule must decline here — without the exclusion it
	// degenerates into the textually-last-occurrence heuristic native's callArgDeaths
	// deliberately rejects. Asserted on CONTENTS (via the 254/253 guards) rather than
	// on a count, because the wrong answer here is a wrong array, not a copy tally.
	{"N_param_read_inside_loop", `function g(b: i32[], v: i32): i32[] {
    var i: i32 = 0;
    while (i < 1) { b = f(b, v); i = i + 1; }
    return b;
}`, 1, 0},
}

func (c growSoleCase) src() string {
	wantLen := 50 * c.appends
	wantLast := 49 + (c.appends - 1)
	return fmt.Sprintf(`function f(b: i32[], v: i32): i32[] { return b.append(v); }
%s
function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 50) { acc = g(acc, i); i = i + 1; }
    if (acc.len() != %d) { return 254; }
    if (acc[0] != 0 || acc[%d] != %d) { return 253; }
    return __arr_push_shared_count();
}`, c.g, wantLen, wantLen-1, wantLast)
}

// TestSelfHostGrowSoleOccurrenceX86_64 drives the corpus through the self-hosted
// x86-64 compiler (asm_run).
func TestSelfHostGrowSoleOccurrenceX86_64(t *testing.T) {
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

	for _, tc := range growSoleCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src()+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			got := cmd.ProcessState.ExitCode()
			switch got {
			case 254, 253:
				t.Errorf("%s: driver returned %d — the accumulator computed the WRONG contents; "+
					"the copy count below it is meaningless until that is fixed", tc.name, got)
			case tc.want:
			default:
				t.Errorf("%s: __arr_push_shared_count() = %d, want %d — the grow bracket's copy count moved",
					tc.name, got, tc.want)
			}
		})
	}
}
