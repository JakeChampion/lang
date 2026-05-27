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

// Strings: rodata (.asciz + 4-byte length prefix), rip-relative lea to a
// data symbol, and the write(2) syscall path through __fern_puts.
func TestX86_64NativeStrings(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`import "core/no_prelude";
function main(): i32 { print("Hello, world!"); return 0; }`, "Hello, world!\n"},
		{`import "core/no_prelude";
function main(): i32 { print("one"); print("two"); return 0; }`, "one\ntwo\n"},
		{`import "core/no_prelude";
import "std/string";
function main(): i32 {
  var a: string = "foo";
  print(a + "bar");
  print("foobar".replace("o", "0"));
  return 0;
}`, "foobar\nf00bar\n"},
	}
	for _, c := range cases {
		if out, code := compileAndRunX86Native(t, c.src); out != c.want || code != 0 {
			t.Errorf("%q → out=%q exit=%d, want out=%q exit=0", c.src, out, code, c.want)
		}
	}
}

// Closures and higher-order functions: rip-relative lea to a function
// body, function-pointer tables (.quad <symbol>), and indirect call/jmp
// through a register.
func TestX86_64NativeClosures(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{`function makeAdder(n: i32): (i32) => i32 {
  function add(x: i32): i32 { return x + n; }
  return add;
}
function main(): i32 { var add5 = makeAdder(5); return add5(37); }`, 42},
		{`function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return apply(dbl, 21); }`, 42},
	}
	for _, c := range cases {
		if _, code := compileAndRunX86Native(t, c.src); code != c.want {
			t.Errorf("%q → exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// Maps exercise heap allocation, hashing, base+index addressing, and
// Option-returning get via match.
func TestX86_64NativeMap(t *testing.T) {
	src := `import "core/no_prelude";
import "core/map";
function main(): i32 {
  var m: Map[i32, i32] = map_new(8);
  m.set(7, 40);
  m.set(11, 99);
  m.set(7, 42);
  if (m.len() != 2) { return 1; }
  if (!m.has(11)) { return 2; }
  match (m.get(7)) {
    Some(v) => { return v; },
    None => { return 3; }
  }
}`
	if _, code := compileAndRunX86Native(t, src); code != 42 {
		t.Errorf("map get → exit = %d, want 42", code)
	}
}

// SSE scalar f64: arithmetic (movq GPR<->xmm, mulsd/addsd/subsd/divsd),
// ordered compare (ucomisd), and int<->float conversion (cvtsi2sd /
// cvttsd2si via the `as` casts).
func TestX86_64NativeFloat(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{`import "core/no_prelude";
function main(): i32 {
  var a: f64 = 3.0; var b: f64 = 4.0;
  return ((a * a + b * b) as i32);
}`, 25},
		{`import "core/no_prelude";
function main(): i32 {
  var a: f64 = 10.0; var b: f64 = 4.0;
  return ((a - b) as i32);
}`, 6},
		{`import "core/no_prelude";
function main(): i32 {
  var a: f64 = 84.0; var b: f64 = 2.0;
  return ((a / b) as i32);
}`, 42},
		{`import "core/no_prelude";
function main(): i32 {
  var a: f64 = 1.5; var b: f64 = 2.5;
  if (a < b) { return 7; }
  return 0;
}`, 7},
	}
	for _, c := range cases {
		if _, code := compileAndRunX86Native(t, c.src); code != c.want {
			t.Errorf("%q → exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// x87 FPU transcendentals: __sin/__cos/__exp/__log/__pow_f64 lower to
// fsin/fcos/fyl2x/f2xm1/fscale/frndint and the x87 stack ops.
func TestX86_64NativeTranscendentals(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"function main(): i32 { return __pow_f64(2.0, 5.0) as i32; }", 32},
		{"function main(): i32 { return __pow_f64(3.0, 2.0) as i32; }", 9},
		{"function main(): i32 { return __exp_f64(0.0) as i32; }", 1},
		{"function main(): i32 { return __exp_f64(2.0) as i32; }", 7},
		{"function main(): i32 { return __log_f64(10.0) as i32; }", 2},
		{"function main(): i32 { var r: f64 = __exp_f64(1.0); if (r > 2.71 && r < 2.72) { return 7; } return 0; }", 7},
		{"function main(): i32 { var r: f64 = __log_f64(2.0); if (r > 0.69 && r < 0.70) { return 7; } return 0; }", 7},
		{"function main(): i32 { var r: f64 = __sin_f64(0.0); if (r > -0.01 && r < 0.01) { return 7; } return 0; }", 7},
		{"function main(): i32 { var r: f64 = __cos_f64(0.0); if (r > 0.99 && r < 1.01) { return 7; } return 0; }", 7},
	}
	for _, c := range cases {
		if _, code := compileAndRunX86Native(t, c.src); code != c.want {
			t.Errorf("%q → exit = %d, want %d", c.src, code, c.want)
		}
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
