package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostBorrowInferInterprocX86_64 covers inter-procedural borrow
// inference over a MUTUALLY RECURSIVE cycle. A param that is merely threaded
// around the cycle (read-only) must stay borrowable even though neither callee
// can be proven borrowable before the other; only an ACTUAL escape (return of
// a derived value / alias / container store / slice) makes it escaping. So a
// non-escaping struct LOCAL passed into the cycle is reclaimed at the caller's
// exit.
//
// The leak/reclaim signal is heap exhaustion: a long churn that leaks one box +
// buffer per iteration exhausts the bump heap and is SIGKILLed (exit 137); with
// the local reclaimed each iteration the freed blocks recycle through the
// size-class freelist and the churn stays bounded (exit 0). This is the same
// heap-exhaustion differential the field-reclaim IR test uses. items goes through
// id so the Node is built on the heap rather than placed as a constant.
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

	// The mutually recursive borrow-only cycle: walk_a calls walk_b calls walk_a,
	// each only READING the Node param (n.items[0]) and forwarding it. Under the
	// greatest-fixpoint both params are borrowable; under the old least-fixpoint
	// neither was (the cycle couldn't bootstrap).
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

	// CAPABILITY + CHURN: `nd` is a fresh non-escaping struct LOCAL passed borrowed
	// into the recursive cycle. The greatest-fixpoint recognises walk_a's Node param
	// as borrowable, so `nd` does not escape `once` and is reclaimed at its exit
	// (the emitted asm must therefore call __fn___struct_drop_Node inside `once`).
	// Across 200M iterations the reclaimed buffers recycle → bounded → exit 0; under
	// the least-fixpoint `nd` leaked every call → heap exhausted → SIGKILL (137).
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

	// VALUE + OVER-RELEASE: the reclaim must be SOUND — `nd` is genuinely dead at
	// `once`'s exit (only ever read inside the cycle), so freeing it must not
	// double-free. once() returns nd.items[0] == 5; 1000 calls sum to 5000, and the
	// over-release detector must stay 0. A wrong free of a live buffer would corrupt
	// the value or tick the detector.
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
