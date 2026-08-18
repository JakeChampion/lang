package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostWasmUnsupportedOpRefused pins wasm_ir's refusal for an IR op with
// no instruction selection on that backend (#6917's wasm half, landed in #6981).
//
// Both binary selectors used to end in `return ";; ir: unsupported bin <name>"` —
// a WAT COMMENT where an instruction belongs — and the op dispatch's terminal
// `else` routes every unrecognised tag through them, so the op was silently
// DROPPED. What made that silent rather than merely broken is arity: a pops-1/
// pushes-1 op leaves the operand stack balanced, so wasmtime ACCEPTED the module
// and ran it to completion with the operation simply gone.
//
// `__heap_mark` is the reproducer. It is a self-host-only intrinsic that both
// register backends lower inline (#6728) and wasm does not model at all, and the
// drivers are bare emitters that run no capability pass — so the CLI's E066
// `arena` refusal (platforms.fern) does not fire here and the op reaches
// instruction selection. The raw-memory / syscall floor used to serve as the
// reproducer; it is now named by wasm_unsupported_builtin's pre-emit gate
// (#6946), which is the better diagnostic for a builtin a user can write, and
// leaves this refusal as the backstop it should be.
//
// Two halves, and they have to travel together: wasm refuses with exit 3 and
// names the op, and the SAME program still compiles on x86-64, which is what
// distinguishes "this backend cannot lower it" from "the program is invalid".
func TestSelfHostWasmUnsupportedOpRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	wasmDriver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")

	const src = `function main(): i32 { var m: i64 = __heap_mark(); __heap_release_to(m); return 0; }`

	t.Run("wasm-refuses", func(t *testing.T) {
		cmd := exec.Command(wasmDriver)
		cmd.Stdin = bytes.NewReader([]byte(src))
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("wasm driver did not exit normally")
		}
		if code := cmd.ProcessState.ExitCode(); code != 3 {
			t.Errorf("exit = %d, want 3 (the unsupported-op refusal); stderr:\n%s", code, errb.String())
		}
		// The diagnostic has to name the op — the whole point is that the old
		// failure named nothing at all.
		if !strings.Contains(errb.String(), "heap_mark") {
			t.Errorf("refusal does not name the op; stderr:\n%s", errb.String())
		}
		if !strings.Contains(errb.String(), "no instruction selection") {
			t.Errorf("refusal is not the instruction-selection diagnostic; stderr:\n%s", errb.String())
		}
		// And it must not have produced a module. A WAT body containing the op as
		// a comment is precisely the bug.
		if strings.Contains(out.String(), ";; ir: unsupported") {
			t.Errorf("emitted the op as a WAT comment instead of refusing:\n%s", out.String())
		}
	})

	// The control. Without this the test would also pass if wasm_ir_run had
	// simply started rejecting the program for an unrelated reason.
	t.Run("x86-64-still-lowers-it", func(t *testing.T) {
		asmDir := t.TempDir()
		copySelfHostDriver(t, asmDir, "asm_ir_run.fern")
		asmDriver := buildSelfHostBin(t, gcc, asmDir, "asm_ir_run.fern", "asmdriver")
		cmd := exec.Command(asmDriver)
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("x86-64 driver failed on a program it is expected to lower: %v", err)
		}
		if len(out) == 0 {
			t.Fatal("x86-64 driver emitted 0 bytes")
		}
		// No shape assertion on the instruction sequence — the checkpoint pair
		// lowers to plain reads and writes of the arena globals that ordinary
		// allocation also emits, so grepping for one would prove nothing. The
		// control's content is the asymmetry itself: same program, other
		// backend, no refusal.
		if strings.Contains(string(out), "# ir: unsupported") {
			t.Error("x86-64 emitted the op as a comment — its own #6917 refusal has regressed")
		}
	})
}
