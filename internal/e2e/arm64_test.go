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

// arm64 arithmetic + locals + function calls. Exercises the
// per-op switch's coverage of OpAdd / OpSub / OpMul, OpLoadLocal
// / OpStoreLocal, OpCallDirect for user-defined functions, and
// the AAPCS64 prologue/epilogue with parameter spilling.
func TestArm64Arithmetic(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { return 2 + 3 * 4; }`, 14},
		{`function main(): i32 { return 100 - 7 * 8; }`, 44},
		{`function main(): i32 { return 100 / 7; }`, 14},
		{`function main(): i32 { return 100 % 7; }`, 2},
		{`function main(): i32 { var x: i32 = 5; var y: i32 = 7; return x * y; }`, 35},
		{`function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(20, 22); }`, 42},
		{`function fib(n: i32): i32 {
    if (n <= 1) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function main(): i32 { return fib(10); }`, 55},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 string literals + len(). String literals live in
// .rodata with a 4-byte little-endian length prefix; pointers
// the runtime carries are post-prefix (`.LStr_N` label points
// at .asciz data). `len(s)` reads `[ptr - 4]`.
func TestArm64StringLiteralLen(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { var s: string = "hello"; return len(s); }`, 5},
		{`function main(): i32 { return len(""); }`, 0},
		{`function main(): i32 { return len("hi\nthere"); }`, 8},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 string concat. Pulls in __lang_alloc + __lang_memcpy
// + __lang_strcat — the entire string-runtime stack on the
// arm64 target.
func TestArm64StringConcat(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var s: string = "hello, " + "world!";
    return len(s);
}`, 13},
		{`function main(): i32 {
    var greeting: string = "good ";
    var name: string = "morning";
    return len(greeting + name);
}`, 12},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 array literals + indexing. Pulls in __lang_alloc and
// the inline __arr_idx helper. Verifies the alloc + store
// + indexed read pipeline composes correctly under qemu.
func TestArm64ArrayLiteral(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    return xs[1];
}`, 20},
		{`function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    return len(xs);
}`, 5},
		{`function sum(xs: i32[]): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < len(xs)) {
        total = total + xs[i];
        i = i + 1;
    }
    return total;
}
function main(): i32 {
    return sum([1, 2, 3, 4, 5]);
}`, 15},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 Map runtime — exercises the codegen-alias rewrites
// (`map_new` → `map_new_impl`, `__method_Map_*` → `_impl`),
// the new `__store_i32` / `__load_i32` / `__memset` runtime
// helpers, and the lang prelude's open-addressing core. Same
// shape as TestArm32Map / TestWASMStateMapAcrossCalls.
func TestArm64Map(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m.set(1, 100);
    m.set(2, 200);
    return m.get_or(2, 0);
}`, 200},
		{`function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 8) {
        m.set(i, i * 10);
        i = i + 1;
    }
    if (m.len() != 8) { return 1; }
    if (m.get_or(7, -1) != 70) { return 2; }
    return 42;
}`, 42},
		{`function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("alpha", 1);
    m.set("beta", 2);
    m.set("gamma", 3);
    return m.get_or("beta", -1) + m.len();
}`, 5},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 control flow: while loop, if/else, comparison ops.
// Verifies OpBlock / OpLoop / OpIf / OpEnd / OpBr / OpBrIf
// scope tracking + the cbz / cbnz branch idioms.
func TestArm64ControlFlow(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var sum: i32 = 0;
    var i: i32 = 1;
    while (i <= 10) {
        sum = sum + i;
        i = i + 1;
    }
    return sum;
}`, 55},
		{`function classify(n: i32): i32 {
    if (n < 0) { return 1; }
    if (n == 0) { return 2; }
    return 3;
}
function main(): i32 {
    var a: i32 = classify(0 - 5);
    var b: i32 = classify(0);
    var c: i32 = classify(7);
    return a * 100 + b * 10 + c;
}`, 123},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
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
