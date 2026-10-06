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
//	J_nested_call_arg              49     0
//	K_two_calls_via_local          49     0
//	M_call_then_inline_append      49     0
//	N_param_read_inside_loop        0     0
//
// On the self-host the result of a call lent the parameter as the last reader
// of its box is a link of that parameter (ssarc.links_of): while it is still
// the parameter's box it gives the callee's retain back, so the next append
// finds the caller's count alone. L, J and K are one SSA graph, M appends to
// the same link inline, and N carries it through a loop phi. Native copies on
// J/K/M because the inner call's result is an argument temp with a count of
// its own (docs/rc-log/2026-09-02-consumed-array-arg-temp.md). Every row is
// at 0, so a row that moves is a regression.
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
	// handed back, so `b` is read exactly once and dies at that call.
	{"H_call_result_into_local", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return t; }`, 1, 0},

	// The argument-temp class, where native copies: the inner call's result is
	// a link of `b`, so the outer append finds the caller's count alone.
	{"J_nested_call_arg", `function g(b: i32[], v: i32): i32[] { return f(f(b, v), v + 1); }`, 2, 0},
	{"K_two_calls_via_local", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return f(t, v + 1); }`, 2, 0},
	{"M_call_then_inline_append", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return t.append(v + 1); }`, 2, 0},

	// LOOP negative: `b` is textually read once, but the read sits inside a loop, so
	// it is many DYNAMIC reads and the next iteration would observe the previous
	// one's in-place growth. The rule must decline here — without the exclusion it
	// degenerates into the textually-last-occurrence heuristic native's callArgDeaths
	// deliberately rejects. The 254/253 contents guards are what this row exists
	// for, because the wrong answer here is a wrong array, not a copy tally.
	{"N_param_read_inside_loop", `function g(b: i32[], v: i32): i32[] {
    let i: i32 = 0;
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
    let acc: i32[] = [];
    let i: i32 = 0;
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
	src, err := os.ReadFile("../../compiler/asm_run.fern")
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
				t.Errorf("%s: __arr_push_shared_count() = %d, want %d — the copy count moved",
					tc.name, got, tc.want)
			}
		})
	}
}
