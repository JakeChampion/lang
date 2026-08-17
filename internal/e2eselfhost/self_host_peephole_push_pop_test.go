package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPeepholePushPopX86_64 pins asm_ir.fern's peephole_push_pop.
//
// The stack-IR emitter pushes every op's result and pops every op's operand, so
// a value handed from one op to the next round-trips through memory even when
// both ends already sit in a register. The peephole rewrites an adjacent pair
// into the move it actually is (or nothing, when both name the same register) —
// worth ~20% of the emitted lines on the compiler's own source.
//
// Two things are checked per program, and they have to travel together: the
// asm must contain NO adjacent pushq/popq pair (the pass did its work), and the
// program must still exit with the value the unoptimised path produced (it did
// not break anything doing so). Either alone is easy to satisfy wrongly — an
// emitter that dropped both lines outright would pass the first.
//
// The no-adjacent-pair assertion is deliberately a property of the WHOLE
// emitted module, hand-written runtime blobs included, rather than a golden
// instruction sequence: it states the pass's postcondition directly, so it
// cannot rot as instruction selection changes around it.
func TestSelfHostPeepholePushPopX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return out
	}

	run := func(t *testing.T, name string, asm []byte) int {
		t.Helper()
		asmPath := filepath.Join(dir, name+".s")
		binPath := filepath.Join(dir, name)
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(binPath)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", name)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		// A chain of binary ops: every operand pair is a push/pop round-trip
		// before the fold, so this is the shape the pass exists for.
		{"arith", "function main(): i32 { return 2 + 3 * 4 - 1; }", 13},
		// Locals: `store_local` pops what the expression just pushed, the
		// pushq %rax / popq %rax case the fold erases outright.
		{"locals", "function main(): i32 { var x = 7; var y = x * 3; return y + 1; }", 22},
		// A loop body re-runs the folded sequence, and its compare feeds a
		// conditional branch — the fold must not disturb the flags-setting pair.
		{"loop", "function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + i * 2; i = i + 1; } return s; }", 90},
		// Calls: arguments are pushed as stack args and must NOT be folded away
		// (a `pushq` consumed by a `call`, not by a `popq`).
		{"call", "function add3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return add3(20, 15, 7); }", 42},
		// Division routes through the guarded non-trapping sequence, which has
		// its own labels — the fold must not reach across one.
		{"divmod", "function main(): i32 { var a = 100; var b = 7; return a / b + a % b; }", 16},
		// A discarded call result is the DEAD-push shape: the call's result is
		// pushed and the following `drop` lowers to `addq $8, %rsp`, so both
		// lines are removable. Statement-position expressions are where the IR
		// produces these, and it produces a great many.
		{"discarded-call", "function side(a: i32): i32 { return a + 1; } function main(): i32 { side(1); side(2); return 9; }", 9},
		// Several drops in a row, interleaved with live values, so the pass has
		// to remove exactly the dead ones and keep the rest.
		{"drops-and-live", "function side(a: i32): i32 { return a * 2; } function main(): i32 { var t = 0; side(1); t = t + side(3); side(2); return t; }", 6},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := emit(t, tc.src)
			if n, first := adjacentPushPop(string(asm)); n != 0 {
				t.Errorf("%d adjacent pushq/popq pair(s) survived the peephole; first at:\n%s", n, first)
			}
			// The dead-push postcondition travels with the round-trip one for the
			// same reason: a push whose very next line discards it is removable,
			// and stating it over the whole module cannot rot as instruction
			// selection moves around it.
			//
			// It doubles as the tripwire for the rule's one precondition. The fold
			// takes a register or immediate source only, because dropping a
			// memory-operand push would drop a LOAD; every dead push the emitter
			// makes today is `%rax`, `%rdx` or `$K`, so the precondition holds
			// everywhere and this assertion is fully exercised. If instruction
			// selection ever produces a memory-operand dead push, the pass will
			// leave it and THIS line fails — which is the notification wanted,
			// since whether that load can fault is a question for whoever
			// introduces it.
			if n, first := deadPushPairs(string(asm)); n != 0 {
				t.Errorf("%d dead pushq/addq pair(s) survived the peephole; first at:\n%s", n, first)
			}
			if got := run(t, tc.name, asm); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}

	// The fold is not allowed to eat a `pushq` that sets up a stack argument.
	// Those are consumed by a `call`, so they must survive — if the pass ever
	// grew a window that reached past intervening instructions, this is what
	// would catch it.
	t.Run("stack-args-survive", func(t *testing.T) {
		asm := string(emit(t, "function add3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return add3(1, 2, 3); }"))
		if !strings.Contains(asm, "pushq") {
			t.Errorf("no pushq left at all — the pass removed argument setup")
		}
	})
}

// deadPushPairs counts `pushq` lines immediately followed by `addq $8, %rsp` —
// a value pushed and discarded by the next instruction — returning the count and
// the first offending pair for the failure message.
func deadPushPairs(asm string) (int, string) {
	lines := strings.Split(asm, "\n")
	n := 0
	first := ""
	for i := 0; i+1 < len(lines); i++ {
		a := strings.TrimSpace(lines[i])
		b := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(a, "pushq ") && b == "addq $8, %rsp" {
			n++
			if first == "" {
				first = "  " + a + "\n  " + b
			}
		}
	}
	return n, first
}

// adjacentPushPop counts `pushq` lines immediately followed by a `popq` line,
// returning the count and the first offending pair for the failure message.
func adjacentPushPop(asm string) (int, string) {
	lines := strings.Split(asm, "\n")
	n := 0
	first := ""
	for i := 0; i+1 < len(lines); i++ {
		a := strings.TrimSpace(lines[i])
		b := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(a, "pushq ") && strings.HasPrefix(b, "popq ") {
			n++
			if first == "" {
				first = "  " + a + "\n  " + b
			}
		}
	}
	return n, first
}
