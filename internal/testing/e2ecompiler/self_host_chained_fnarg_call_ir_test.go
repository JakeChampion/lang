package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var chainedFnArgCallCases = []struct {
	name string
	src  string
	want int
}{
	{"chained-len-generic", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    let out: T[] = [];
    let i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    let c: i32[] = [1, 2, 3];
    return take_while(c, lt5).len();
}`, 3},
	{"chained-len-nongeneric", `function take_while_i(xs: i32[], p: (i32) => boolean): i32[] {
    let out: i32[] = [];
    let i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    let c: i32[] = [1, 2, 3];
    return take_while_i(c, lt5).len();
}`, 3},
	{"chained-len-lambda-arg", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    let out: T[] = [];
    let i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function main(): i32 {
    let c: i32[] = [1, 2, 3];
    return take_while(c, (x: i32) => x < 2).len();
}`, 1},
	{"chained-len-nested", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    let out: T[] = [];
    let i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    let c: i32[] = [1, 2, 3];
    return take_while(take_while(c, lt5), (x: i32) => x < 3).len();
}`, 2},
	{"chained-index", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    let out: T[] = [];
    let i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    let c: i32[] = [1, 2, 3];
    return take_while(c, lt5)[1];
}`, 2},
}

func TestSelfHostChainedFnArgCallIRX86_64(t *testing.T) {
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

	for _, tc := range chainedFnArgCallCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host x86-64 = %d, want %d (-1 = signal crash, the #4767 shape)", tc.name, code, tc.want)
			}
		})
	}
}
