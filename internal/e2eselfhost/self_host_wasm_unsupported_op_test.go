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
// DROPPED. `__raw_alloc` is the reproducer: it is one of the raw-memory
// intrinsics the register backends lower and wasm_ir does not, and it is NOT
// classified in platforms.fern, so it escapes the E066 capability gate and
// reaches instruction selection.
//
// What made it silent rather than merely broken is the arity. raw_alloc is
// pops-1/pushes-1, so omitting it leaves the operand stack balanced: before the
// fix this program compiled clean, wasmtime ACCEPTED the module, and it ran to
// completion with the allocation simply gone. A binary op at least unbalances the
// stack and gets rejected, with a message naming neither op nor function.
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

	// __raw_alloc reaches instruction selection on every target: it is not
	// capability-gated, so nothing declines it earlier.
	const src = `function main(): i32 { var p: i32 = __raw_alloc(64); return 0; }`

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
		if !strings.Contains(errb.String(), "raw_alloc") {
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
		// No shape assertion on the instruction sequence — raw_alloc lowers to a
		// `call __fern_alloc` that ordinary allocation also emits, so grepping for
		// it would prove nothing. The control's content is the asymmetry itself:
		// same program, other backend, no refusal.
		if strings.Contains(string(out), "# ir: unsupported") {
			t.Error("x86-64 emitted the op as a comment — its own #6917 refusal has regressed")
		}
	})
}
