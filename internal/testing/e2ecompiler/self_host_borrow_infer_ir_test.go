package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostBorrowInferInterprocX86_64(t *testing.T) {
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

	const cycle = `struct Node { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function walk_a(n: Node, d: i32): i32 { if (d <= 0) { return n.items[0]; } return walk_b(n, d - 1); }
function walk_b(n: Node, d: i32): i32 { if (d <= 0) { return n.items[0]; } return walk_a(n, d - 1); }
`

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
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, cycle+`function once(): i32 {
    let nd: Node = Node { items: id([5, 6, 7]) };
    return walk_a(nd, 4);
}
function main(): i32 {
    let s: i32 = 0;
    let f: i32 = 0;
    while (f < 200000000) { s = s + once(); f = f + 1; }
    return s - s;
}`, "borrow_infer_cycle_churn", 0)

	run(t, cycle+`function once(): i32 {
    let nd: Node = Node { items: id([5, 6, 7]) };
    return walk_a(nd, 4);
}
function main(): i32 {
    let s: i32 = 0;
    let f: i32 = 0;
    while (f < 1000) { s = s + once(); f = f + 1; }
    return (s - 5000) + __rc_underflow_count();
}`, "borrow_infer_cycle_sound", 0)
}
