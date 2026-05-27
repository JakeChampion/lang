// x86-64 native-backend end-to-end tests: compile a Fern program with
// the x86-64 code generator, assemble + link it with the pure-Go native
// backend (internal/native/x86_64 + internal/native/elf) — no external
// assembler or linker — then run the static ELF and check its behaviour.
// Mirrors the arm64 native path. On amd64 hosts the binary runs directly;
// elsewhere it runs under qemu-x86_64 (SKIP if absent).
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	x86codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

func x86NativeRunner(t *testing.T) []string {
	t.Helper()
	if runtime.GOARCH == "amd64" {
		return nil
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}
	}
	t.Skip("no qemu-x86_64 to run x86-64 binaries")
	return nil
}

func compileAndRunX86Native(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	runner := x86NativeRunner(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("x86_64 emit: %v", err)
	}
	text, rodata, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("NATIVE-ASM-FAIL: %v\n--- asm ---\n%s", err, asm)
	}
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(binPath, nativeelf.StaticExecutableDataX86(text, rodata), 0o755); err != nil {
		t.Fatalf("write native bin: %v", err)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], binPath)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// First x86-64 native milestone: main()'s return value reaches the
// process exit code through the kernel, end to end, with no gcc.
func TestX86_64NativeExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 42, 120, 208, 250} {
		src := "function main(): i32 { return " + intToString(want) + "; }"
		if _, code := compileAndRunX86Native(t, src); code != want {
			t.Errorf("return %d → exit = %d", want, code)
		}
	}
}

// Recursion + multiply + compare/branch + function calls with arguments,
// exercising push/pop, frame setup, cmp/sete/test/jz, imul and call/ret.
func TestX86_64NativeFactorial(t *testing.T) {
	src := `function factorial(n: i32): i32 {
  if (n == 0) { return 1; }
  return n * factorial(n - 1);
}
function main(): i32 { return factorial(5); }`
	if _, code := compileAndRunX86Native(t, src); code != 120 {
		t.Errorf("factorial(5) → exit = %d, want 120", code)
	}
}

// Integer arithmetic and comparison operators across the phase-1
// instruction surface (add/sub/imul/idiv + cmp/setcc + branches).
func TestX86_64NativeArithmetic(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"function main(): i32 { return 6 * 7; }", 42},
		{"function main(): i32 { return 100 - 58; }", 42},
		{"function main(): i32 { return 84 / 2; }", 42},
		{"function main(): i32 { return 85 % 43; }", 42},
		{"function main(): i32 { return 40 + 2; }", 42},
		{"function main(): i32 { var x: i32 = 10; if (x > 5) { return 42; } return 0; }", 42},
		{"function main(): i32 { var x: i32 = 3; if (x < 5) { return 42; } return 0; }", 42},
		{"function main(): i32 { var n: i32 = 0; var i: i32 = 0; while (i < 42) { n = n + 1; i = i + 1; } return n; }", 42},
	}
	for _, c := range cases {
		if _, code := compileAndRunX86Native(t, c.src); code != c.want {
			t.Errorf("%q → exit = %d, want %d", c.src, code, c.want)
		}
	}
}
