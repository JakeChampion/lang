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

// TestArm64NativePrintRunsUnderQemu confirms string printing works end
// to end through the native backend: rodata string + the write-syscall
// runtime. stdout must be exactly the printed text.
func TestArm64NativePrintRunsUnderQemu(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"string", `function main(): i32 { print("hello native"); return 0; }`, "hello native\n"},
		{"int", `import "std/i32"; function main(): i32 { print((42).to_string()); return 0; }`, "42\n"},
		{"negint", `import "std/i32"; function main(): i32 { print((0 - 42).to_string()); return 0; }`, "-42\n"},
		{"concat", `import "std/i32"; function main(): i32 { print("x=" + (42).to_string()); return 0; }`, "x=42\n"},
		{"loopsum", `import "std/i32"; function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 9) { s = s + i; i = i + 1; } print(s.to_string()); return 0; }`, "36\n"},
		{"float", `import "std/float"; function main(): i32 { print((3.5).to_string()); return 0; }`, "3.5\n"},
		{"negfloat", `import "std/float"; function main(): i32 { print((0.0 - 2.25).to_string()); return 0; }`, "-2.25\n"},
		{"wholefloat", `import "std/float"; function main(): i32 { print((42.0).to_string()); return 0; }`, "42\n"},
		{"floatarith", `import "std/float"; function main(): i32 { var x: f64 = 1.5; var y: f64 = 2.0; print((x * y).to_string()); return 0; }`, "3\n"},
		{"slashstring", `function main(): i32 { print("x: i32 = 1; // comment"); return 0; }`, "x: i32 = 1; // comment\n"},
		{"strarr_iter", `function main(): i32 { var a: string[] = ["a", "bb", "ccc"]; for s in a { print(s); } return 0; }`, "a\nbb\nccc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToArm64Asm(t, c.src)
			text, data, err := na.AssembleProgram(asm, elf.TextVAddr)
			if err != nil {
				t.Fatalf("AssembleProgram: %v", err)
			}
			path := filepath.Join(t.TempDir(), "prog")
			if err := os.WriteFile(path, elf.StaticExecutableData(text, data), 0o755); err != nil {
				t.Fatal(err)
			}
			out, err := runArm64Bin(qemu, path).Output()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := string(out); got != c.want {
				t.Fatalf("stdout = %q, want %q", got, c.want)
			}
		})
	}
}

// TestArm64NativeBackendRunsUnderQemu is the integration gate for the
// Go-side native backend: compile a Fern program to assembly with the
// real arm64 code generator, then assemble + link it entirely
// in-process (internal/native/arm64 + internal/native/elf) — no gcc,
// no external assembler — and run the produced ELF (natively on an arm64
// host, else under qemu-aarch64). main()'s
// return value becomes the process exit code (via _start's exit_group),
// so the exit code is the assertion.
//
// This exercises the whole native pipeline on REAL backend output
// (runtime prologue, frame setup, .rodata, .note.GNU-stack, etc.), not
// hand-written snippets. Programs whose emitted assembly uses an
// instruction the assembler doesn't cover yet would fail to assemble —
// surfacing the next gap to fill.
func TestArm64NativeBackendRunsUnderQemu(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)

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
		{"bitand", "function main(): i32 { var x: i32 = 250; return x & 42; }", 42},
		{"bitor", "function main(): i32 { var x: i32 = 40; return x | 2; }", 42},
		// Array element addressing past [0] needs the scaled/extended add
		// forms (lsl #N for the element-size stride, uxtw to widen the
		// 32-bit index), or element [1]+ is corrupted.
		{"i32arr_index", "function main(): i32 { var a: i32[] = [10, 20, 12]; return a[1] + a[2]; }", 32},
		{"i32arr_iter", "function main(): i32 { var a: i32[] = [1, 2, 3, 36]; var s: i32 = 0; for x in a { s = s + x; } return s; }", 42},
		{"structarr_index", "struct P { x: i32, y: i32 } function main(): i32 { var ps: P[] = [P{x:1,y:2}, P{x:40,y:2}]; return ps[1].x + ps[1].y; }", 42},
		{"structarr_iter", "struct P { v: i32 } function main(): i32 { var ps: P[] = [P{v:20}, P{v:22}]; var s: i32 = 0; for p in ps { s = s + p.v; } return s; }", 42},
		// Cheap f64 math intrinsics lower to single FP instructions
		// (fabs/fsqrt/frintm/frintp/frintz/frinta) — no libm.
		{"abs_f64", "function main(): i32 { return __abs_f64(0.0 - 42.0) as i32; }", 42},
		{"sqrt_f64", "function main(): i32 { return __sqrt_f64(1764.0) as i32; }", 42},
		{"floor_f64", "function main(): i32 { return __floor_f64(42.9) as i32; }", 42},
		{"ceil_f64", "function main(): i32 { return __ceil_f64(41.1) as i32; }", 42},
		{"trunc_f64", "function main(): i32 { return __trunc_f64(42.9) as i32; }", 42},
		{"round_f64", "function main(): i32 { return __round_f64(41.5) as i32; }", 42},
		// f64 transcendentals via polynomial-approximation runtime
		// helpers (arm64 has no hardware sin/cos/exp/log). Tolerance
		// contract; exit codes pin the integer-truncated result.
		{"exp_f64", "function main(): i32 { return __exp_f64(2.0) as i32; }", 7},
		{"log_f64", "function main(): i32 { return __log_f64(10.0) as i32; }", 2},
		{"sin_f64", "function main(): i32 { var r: f64 = __sin_f64(1.5707963267948966); if (r > 0.999 && r < 1.001) { return 42; } return 0; }", 42},
		{"cos_f64", "function main(): i32 { return __cos_f64(0.0) as i32; }", 1},
		{"pow_f64", "function main(): i32 { return __pow_f64(3.0, 2.0) as i32; }", 9},
		{"exp_log_roundtrip_f64", "function main(): i32 { var r: f64 = __log_f64(__exp_f64(3.0)); if (r > 2.999 && r < 3.001) { return 42; } return 0; }", 42},
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
			if err := runArm64Bin(qemu, path).Run(); err != nil {
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
// the actual fern CLI: `fern -target arm64-linux -native -o out src.fern`
// must produce a static ELF with no external toolchain, which then
// exits with main()'s return value (run natively on arm64, else under qemu).
func TestCmdFernNativeArm64(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "n.fern")
	if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "n.out")
	cmd := exec.Command("go", "run", "./cmd/fern", "-target", "arm64-linux", "-native", "-o", outPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -native failed: %v\n%s", err, out)
	}
	got := 0
	if err := runArm64Bin(qemu, outPath).Run(); err != nil {
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
	if err := constfold.Fold(prog, nil); err != nil {
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
