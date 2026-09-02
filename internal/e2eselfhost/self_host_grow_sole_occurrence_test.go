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
// Measured on the self-host, x86-64, before and after the port:
//
//	A_inline_append_tail        0 -> 0    control
//	C_call_tail                 0 -> 0    control
//	H_call_result_into_local   45 -> 0    the shape this issue is about
//	I_call_result_into_param    0 -> 0    already exempt (self-reassign shape)
//	J_nested_call_arg          44 -> 0    (44 again under the counted identity return, below)
//	K_two_calls_via_local        0 -> 0    (44 under the counted identity return)
//	L_two_calls_via_param        0 -> 0    (44 under the counted identity return)
//	M_call_then_inline_append  44 -> 0    (44 again under the counted identity return)
//
// TWO THINGS DIVERGED FROM THE ISSUE'S EXPECTATION when the port landed, and
// both were recorded here rather than smoothed over: the issue's "J/K/M stay at
// 49" is the NATIVE corpus, where the self-host read 44/0/44 before the port and
// 0 after it; and native kept reading 49 for those three, so the self-host did
// FEWER copies than native there.
//
// That gap closed from the other side. Since the counted identity return
// (docs/rc-log/2026-09-02-identity-return-counted.md) an in-place push on a
// borrowed parameter's buffer hands its result back with a count of its own, so
// `x = f(x)` can release the superseded reference whether or not the pointer
// moved. A result bound to a NEW name, nested as an argument, or returned
// through a second call therefore arrives at rc 2 — the caller's reference and
// the result's — and the next push on it takes the copy path once per call:
// J/K/L/M read 44 (native: 49). The dead-argument caller-side release that would
// take them back to 0 is the argument-temp slice that rc-log entry names as
// open; the numbers here pin the convention until it lands, and I/L2-style
// self-reassign chains stay at 0.
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
	{"L_two_calls_via_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }`, 2, 44},

	// The shape #6048 is about: the call result is materialised into a local and
	// handed back, so `b` is read exactly once and dies at that call. 45 -> 0.
	{"H_call_result_into_local", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return t; }`, 1, 0},

	// Also reached by the same rule on the self-host (44 -> 0 each). Native still
	// reads 49 for these; see the divergence note above.
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
