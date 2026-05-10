// arm64 (aarch64) Linux end-to-end tests. The arm64 backend
// is a parallel codegen target alongside arm32; the IR layer
// is shared but the assembly emit + Linux syscall numbers
// are arm64-specific. Each test SKIPs (rather than fails)
// when the cross-compiler or qemu-aarch64 isn't installed.
//
// Tests run the compiled binary under qemu-aarch64, which
// uses the host's Linux kernel via user-mode emulation. On
// real arm64 Linux hosts (Raspberry Pi 4+, AWS Graviton,
// etc.) the same binary runs natively without qemu.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/parser"
)

func arm64Tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	for _, c := range []string{"aarch64-linux-gnu-gcc", "aarch64-unknown-linux-gnu-gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if gcc == "" || qemu == "" {
		t.Skipf("aarch64 cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

func compileAndRunArm64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// First arm64 e2e: `function main(): i32 { return 42; }`
// validates the toolchain end-to-end. Compiles via the
// new arm64 backend, links a static -nostdlib ELF with
// aarch64-linux-gnu-gcc, runs under qemu-aarch64, and
// confirms the kernel propagates main's return value
// through `exit_group` to qemu's exit code.
func TestArm64ExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 42, 137, 250} {
		src := "function main(): i32 { return " + intToString(want) + "; }"
		_, code := compileAndRunArm64(t, src)
		if code != want {
			t.Errorf("return %d → exit = %d", want, code)
		}
	}
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
