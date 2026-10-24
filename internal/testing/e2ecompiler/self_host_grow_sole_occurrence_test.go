package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type growSoleCase struct {
	name    string
	g       string
	appends int
	want    int
}

var growSoleCases = []growSoleCase{
	{"A_inline_append_tail", `function g(a: i32[], v: i32): i32[] { return a.append(v); }`, 1, 0},
	{"C_call_tail", `function g(b: i32[], v: i32): i32[] { return f(b, v); }`, 1, 0},
	{"I_call_result_into_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return b; }`, 1, 0},
	{"L_two_calls_via_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }`, 2, 0},

	{"H_call_result_into_local", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return t; }`, 1, 0},

	{"J_nested_call_arg", `function g(b: i32[], v: i32): i32[] { return f(f(b, v), v + 1); }`, 2, 0},
	{"K_two_calls_via_local", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return f(t, v + 1); }`, 2, 0},
	{"M_call_then_inline_append", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return t.append(v + 1); }`, 2, 0},

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

func TestSelfHostGrowSoleOccurrenceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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
