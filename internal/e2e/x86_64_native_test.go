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
		{`
function main(): i32 { print("Hello, world!"); return 0; }`, "Hello, world!\n"},
		{`
function main(): i32 { print("one"); print("two"); return 0; }`, "one\ntwo\n"},
		{`
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
	src := `
import "core/map";
function main(): i32 {
  var m: Map[i32, i32] = map_new(8);
  m = m.insert(7, 40);
  m = m.insert(11, 99);
  m = m.insert(7, 42);
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

// Issue #2763: a value-type struct with a Map field, reconstructed
// through a method (`return IntSet { m: s.m.insert(...) }`), used to
// SIGSEGV on x86-64 (and corrupt on arm64) — the COW mutator returns the
// borrowed receiver's handle in place, so the new struct aliased the
// caller's map and dropping the old struct freed it out from under the
// new one. The StructLit lowering now clones a Map field initialised by a
// COW mutator result, giving the new container its own buffer. main()
// returns the set size (2), which doubles as a no-crash check.
func TestX86_64NativeMapFieldStructRebind(t *testing.T) {
	src := `
import "core/map";
struct IntSet { m: Map[i32, i32] }
function (s: IntSet) insert(x: i32): IntSet { return IntSet { m: s.m.insert(x, 1) }; }
function (s: IntSet) len(): i32 { return s.m.len(); }
function main(): i32 {
    var m0: Map[i32, i32] = map_new(4);
    var s: IntSet = IntSet { m: m0 };
    s = s.insert(10);
    s = s.insert(20);
    s = s.insert(10);   // duplicate — set size stays 2
    return s.len();
}`
	if _, code := compileAndRunX86Native(t, src); code != 2 {
		t.Errorf("IntSet rebind → exit = %d, want 2 (issue #2763)", code)
	}
}

// Issue #4871: the #2763 clone (above) fires only when the Map field value is
// DIRECTLY a COW-mutator call. One `var` removed — `var m = s.m.insert(...);
// return ISet { m: m }` — the field value is a plain ident, the clone was
// missed, and the new struct aliased the borrowed receiver's in-place buffer:
// dropping the old struct on `s = iset_add(s, ...)` freed it, so the SECOND
// wrap-insert hung on the corrupted (open-addressing) map header on x86-64
// (interp was correct). borrowedMapFieldResults now flags a Map local bound to
// a mutator with a field-access receiver so the StructLit clones it too. main()
// returns the set size (2); a hang or a wrong count both fail.
func TestX86_64NativeMapFieldStructRebindIndirect(t *testing.T) {
	src := `
import "core/map";
struct ISet { m: Map[i32, i32] }
function iset_add(s: ISet, x: i32): ISet {
    var m: Map[i32, i32] = s.m.insert(x, 1);
    return ISet { m: m };
}
function main(): i32 {
    var m0: Map[i32, i32] = map_new(4);
    var s: ISet = ISet { m: m0 };
    s = iset_add(s, 10);   // one wrap-insert (was: emptied the map, returned 1→0)
    s = iset_add(s, 20);   // second wrap-insert (was: hung on the freed header)
    s = iset_add(s, 10);   // duplicate — set size stays 2
    return s.m.len();
}`
	if _, code := compileAndRunX86Native(t, src); code != 2 {
		t.Errorf("indirect IntSet rebind → exit = %d, want 2 (issue #4871)", code)
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
		{`
function main(): i32 {
  var a: f64 = 3.0; var b: f64 = 4.0;
  return ((a * a + b * b) as i32);
}`, 25},
		{`
function main(): i32 {
  var a: f64 = 10.0; var b: f64 = 4.0;
  return ((a - b) as i32);
}`, 6},
		{`
function main(): i32 {
  var a: f64 = 84.0; var b: f64 = 2.0;
  return ((a / b) as i32);
}`, 42},
		{`
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

// The transcendentals through the in-process assembler: __sin/__cos/__exp/
// __log/__pow_f64 lower to calls into the SSE fdlibm bundle, so what this
// pins is that assembler's coverage of it (roundsd, cvttsd2si, the movsd
// rip-relative constant loads) — the x87 group it was written for is gone.
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

// putchar writes a single byte to stdout. The runtime stashes its
// argument on the stack via `mov [rsp], dil` and write(1, &slot, 1).
// `dil` (low 8 of rdi) is one of the registers that can ONLY be
// addressed with a REX prefix present; the native assembler used to
// emit it REX-less, which silently decodes as `mov [rsp], bh` — so
// every putchar wrote a stray 0 byte instead of its argument. The
// gcc-linked e2e path (TestX86_64Print) never caught this because GNU
// as encodes it correctly; only the in-process native assembler was
// affected. Regression guard: assert the exact bytes reach stdout.
func TestX86_64NativePutchar(t *testing.T) {
	src := `function main(): i32 {
    putchar(65);
    putchar(66);
    putchar(233);
    putchar(10);
    return 0;
}`
	out, code := compileAndRunX86Native(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// 233 is the raw byte the program asked for — putchar is a 1-byte
	// write, not a UTF-8 codepoint encoder, so it stays a lone 0xE9.
	want := "AB\xe9\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
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
