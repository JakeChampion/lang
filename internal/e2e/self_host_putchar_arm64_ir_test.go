package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostPutcharArm64IR is the arm64 leg of the self-host `putchar` IR
// support (issue #2839). The asm_arm64_ir_run driver (built via the x86 backend,
// running on the host) emits arm64 asm via the IR path's op_putchar, which calls
// the __fern_putchar runtime added to asm_arm64.emit_runtime (write(1,&byte,1)).
// The emitted program is assembled with the aarch64 toolchain, run under qemu,
// and the BYTES written to stdout are asserted ("Hi\n"), exit 0. The AST fallback
// is still missing putchar, so this exercises the IR path only.
func TestSelfHostPutcharArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")

	src := "function main(): i32 { putchar(72); putchar(105); putchar(10); return 0; }\n"
	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v (%d bytes asm)", err, len(asm))
	}
	bin := buildBinArm64(t, arm64gcc, dir, "putchar_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	var stdout bytes.Buffer
	run.Stdout = &stdout
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("arm64 putchar program did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("putchar program exited %d, want 0", code)
	}
	if got := stdout.String(); got != "Hi\n" {
		t.Errorf("putchar program wrote %q, want %q", got, "Hi\n")
	}
}
