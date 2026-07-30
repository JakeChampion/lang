package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostBorrowInferInterprocX86_64 covers Perceus slice 2: the
// inter-procedural borrow inference (`borrowable_params_interproc`) is now a
// GREATEST-fixpoint from above (native `inferParamEscapes`), not the old
// least-fixpoint from below. The from-below pass started with an empty registry
// and grew it, so it could never bootstrap a MUTUALLY RECURSIVE borrow — a param
// only became borrowable once its callee already was, and in a cycle neither
// could go first. From-above starts every param optimistically borrowable and
// only flips one off when an ACTUAL escape (return-of-derived / alias / container
// store / slice) is proven, so a param that is merely threaded around a recursive
// cycle (read-only) stays borrowable.
//
// The observable effect is downstream reclaim: a non-escaping struct LOCAL passed
// into a mutually recursive borrow-only cycle is recognised as not-escaping, so it
// is reclaimed at the caller's exit (__struct_drop). Under the old least-fixpoint
// the cycle's params looked escaping, so the local "escaped" and leaked.
//
// The leak/reclaim signal is heap exhaustion: a long churn that leaks one box +
// buffer per iteration exhausts the bump heap and is SIGKILLed (exit 137); with
// the local reclaimed each iteration the freed blocks recycle through the
// size-class freelist and the churn stays bounded (exit 0). This is the same
// heap-exhaustion differential the field-reclaim IR test uses.
func TestSelfHostBorrowInferInterprocX86_64(t *testing.T) {
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

	// The mutually recursive borrow-only cycle: walk_a calls walk_b calls walk_a,
	// each only READING the Node param (n.items[0]) and forwarding it. Under the
	// greatest-fixpoint both params are borrowable; under the old least-fixpoint
	// neither was (the cycle couldn't bootstrap).
	const cycle = `struct Node { items: i32[] }
function walk_a(n: Node, d: i32): i32 { if (d <= 0) { return n.items[0]; } return walk_b(n, d - 1); }
function walk_b(n: Node, d: i32): i32 { if (d <= 0) { return n.items[0]; } return walk_a(n, d - 1); }
`

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted asm missing %q — the mutual-recursive borrow was not recognised (regressed to the least-fixpoint?)", name, wantAsmSubstr)
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
    var nd: Node = Node { items: [5, 6, 7] };
    return walk_a(nd, 4);
}
function main(): i32 {
    var s: i32 = 0;
    var f: i32 = 0;
    while (f < 200000000) { s = s + once(); f = f + 1; }
    return s - s;
}`, "borrow_infer_cycle_churn", 0, "__fn___struct_drop_Node")

	// VALUE + OVER-RELEASE: the reclaim must be SOUND — `nd` is genuinely dead at
	// `once`'s exit (only ever read inside the cycle), so freeing it must not
	// double-free. once() returns nd.items[0] == 5; 1000 calls sum to 5000, and the
	// over-release detector must stay 0. A wrong free of a live buffer would corrupt
	// the value or tick the detector.
	run(t, cycle+`function once(): i32 {
    var nd: Node = Node { items: [5, 6, 7] };
    return walk_a(nd, 4);
}
function main(): i32 {
    var s: i32 = 0;
    var f: i32 = 0;
    while (f < 1000) { s = s + once(); f = f + 1; }
    return (s - 5000) + __rc_underflow();
}`, "borrow_infer_cycle_sound", 0, "")
}
