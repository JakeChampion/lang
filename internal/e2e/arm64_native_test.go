package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	na "github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/elf"
)

// TestArm64NativeBackendRunsUnderQemu is the integration gate for the
// Go-side native backend: compile a Fern program to assembly with the
// real arm64 code generator, then assemble + link it entirely
// in-process (internal/native/arm64 + internal/native/elf) — no gcc,
// no external assembler — and run the produced ELF under qemu. main()'s
// return value becomes the process exit code (via _start's exit_group),
// so the exit code is the assertion.
//
// This exercises the whole native pipeline on REAL backend output
// (runtime prologue, frame setup, .rodata, .note.GNU-stack, etc.), not
// hand-written snippets. Programs whose emitted assembly uses an
// instruction the assembler doesn't cover yet would fail to assemble —
// surfacing the next gap to fill.
func TestArm64NativeBackendRunsUnderQemu(t *testing.T) {
	qemu := ""
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if qemu == "" {
		t.Skip("qemu-aarch64 not on PATH")
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		{"add", "function main(): i32 { return 40 + 2; }", 42},
		{"sub", "function main(): i32 { return 50 - 8; }", 42},
		{"mul", "function main(): i32 { return 6 * 7; }", 42},
		{"div", "function main(): i32 { return 84 / 2; }", 42},
		{"locals", "function main(): i32 { var x: i32 = 40; var y: i32 = 2; return x + y; }", 42},
		{"ifelse", "function main(): i32 { if (3 > 2) { return 42; } return 0; }", 42},
		{"while", "function main(): i32 { var i: i32 = 0; var s: i32 = 0; while (i < 42) { s = s + 1; i = i + 1; } return s; }", 42},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }", 42},
		{"recur", "function f(n: i32): i32 { if (n <= 1) { return 1; } return n * f(n - 1); } function main(): i32 { return f(5) / 3; }", 40},
		{"i64", "function main(): i32 { var x: i64 = 40; var y: i64 = 2; return (x + y) as i32; }", 42},
		{"f64", "function main(): i32 { var x: f64 = 21.0; return (x * 2.0) as i32; }", 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToArm64Asm(t, c.src)
			text, rodata, err := na.AssembleProgram(asm, elf.TextVAddr)
			if err != nil {
				t.Fatalf("AssembleProgram: %v\n--- asm ---\n%s", err, asm)
			}
			bin := elf.StaticExecutableData(text, rodata)
			path := filepath.Join(t.TempDir(), "prog")
			if err := os.WriteFile(path, bin, 0o755); err != nil {
				t.Fatal(err)
			}
			got := 0
			if err := exec.Command(qemu, path).Run(); err != nil {
				ee, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run failed: %v", err)
				}
				got = ee.ExitCode()
			}
			if got != c.want {
				t.Fatalf("exit code = %d, want %d", got, c.want)
			}
		})
	}
}

// TestCmdFernNativeArm64 drives the user-facing `-native` flag through
// the actual fern CLI: `fern -target arm64 -native -o out src.fern`
// must produce a static ELF with no external toolchain, which then
// exits with main()'s return value under qemu.
func TestCmdFernNativeArm64(t *testing.T) {
	qemu := ""
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if qemu == "" {
		t.Skip("qemu-aarch64 not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "n.fern")
	if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "n.out")
	cmd := exec.Command("go", "run", "./cmd/fern", "-target", "arm64", "-native", "-o", outPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -native failed: %v\n%s", err, out)
	}
	got := 0
	if err := exec.Command(qemu, outPath).Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run failed: %v", err)
		}
		got = ee.ExitCode()
	}
	if got != 42 {
		t.Fatalf("exit code = %d, want 42", got)
	}
}

// compileToArm64Asm runs the front-end pipeline + arm64 code generator,
// matching cmd/fern, and returns the emitted assembly text.
func compileToArm64Asm(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
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
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}
