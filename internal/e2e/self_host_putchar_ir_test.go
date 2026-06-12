package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostPutcharIRPathX86_64 covers the self-host `putchar` builtin on the
// IR code path (issue #2839). `putchar(c)` writes c's low byte to stdout; it
// works on the native compiler but the self-host compiler used to lower it to
// an undefined `__fn_putchar` call (link failure). The fix adds a dedicated
// `putchar` IR op (irlower → op_putchar) lowered by every IR backend to the
// `__fern_putchar` runtime (write(1,&byte,1)). This drives the program through
// the asm_ir_run driver's `-ir` flag and asserts the BYTES written to stdout —
// the AST fallback is still missing putchar, so we exercise the IR path only.
func TestSelfHostPutcharIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// The issue's reproducer: print "Hi\n" via three putchar calls, exit 0.
	src := "function main(): i32 { putchar(72); putchar(105); putchar(10); return 0; }\n"

	// Emit via the IR path (-ir). The program is a pure-i32 single function, so
	// it is IR-eligible and the putchar op goes through the IR backend.
	var drv *exec.Cmd
	if len(runner) == 0 {
		drv = exec.Command(driverBin, "-ir")
	} else {
		drv = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	drv.Stdin = bytes.NewReader([]byte(src))
	asm, err := drv.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v (%d bytes asm)", err, len(asm))
	}
	innerBin := buildBin(t, gcc, dir, "putchar_prog", string(asm))

	var inner *exec.Cmd
	if len(runner) == 0 {
		inner = exec.Command(innerBin)
	} else {
		inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
	}
	var stdout bytes.Buffer
	inner.Stdout = &stdout
	_ = inner.Run()
	if inner.ProcessState == nil || !inner.ProcessState.Exited() {
		t.Fatal("inner program did not exit normally")
	}
	if code := inner.ProcessState.ExitCode(); code != 0 {
		t.Errorf("putchar program exited %d, want 0", code)
	}
	if got := stdout.String(); got != "Hi\n" {
		t.Errorf("putchar program wrote %q, want %q", got, "Hi\n")
	}
}
