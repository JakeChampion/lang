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
	"runtime"
	"strings"
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

// arm64 f32 / f64 arithmetic + comparisons. Float values
// live as raw bit patterns on the operand stack; the codegen
// fmov's them into the V-register file (s0/s1 for f32,
// d0/d1 for f64), runs the op, and fmov's the result back.
func TestArm64Floats(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		// f32 arithmetic
		{`function main(): i32 {
    var a: f32 = 3.5;
    var b: f32 = 1.5;
    return (a + b) as i32;
}`, 5},
		{`function main(): i32 {
    var a: f32 = 10.0;
    var b: f32 = 3.0;
    return (a / b) as i32;
}`, 3},
		// f64 arithmetic + comparison
		{`function main(): i32 {
    var pi: f64 = 3.14f64;
    var two: f64 = 2.0f64;
    if (pi * two > 6.0f64) { return 42; }
    return 0;
}`, 42},
		// Mixed: i32 → f64 → i32 round trip.
		{`function main(): i32 {
    var n: i32 = 7;
    var f: f64 = (n as f64) * 1.5f64;
    return f as i32;
}`, 10},
		// Float negation.
		{`function main(): i32 {
    var x: f32 = 5.5;
    var y: f32 = 0.0 - x;
    if (y < 0.0) { return 1; }
    return 0;
}`, 1},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 indirect calls: OpConstFunc (function value
// materialisation via adrp + add :lo12:) + OpCallIndirect
// (blr xN). Lets handlers be passed as function values to
// generic helpers like tcp_serve. Same shape arm32 uses.
func TestArm64IndirectCall(t *testing.T) {
	_, code := compileAndRunArm64(t, `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
    var f: (i32, i32) => i32 = add;
    return f(20, 22);
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (indirect call through function value)", code)
	}
}

// arm64 print / write / putchar — stdout builtins lowered to
// direct write(2) syscalls. Verifies the asm wires the right
// fd, length, and newline behaviour (`print` adds one, `write`
// does not). Same shape arm32 uses under the hood, modulo the
// AAPCS64 reg names.
func TestArm64Print(t *testing.T) {
	out, code := compileAndRunArm64(t, `function main(): i32 {
    print("hello arm64");
    write("no-nl");
    putchar(10);
    putchar(65);
    putchar(10);
    return 0;
}`)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	want := "hello arm64\nno-nl\nA\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// arm64 args() — materialises argv as a length-prefixed
// string[]. compileAndRunArm64 doesn't pass extra args, so
// we drive qemu-aarch64 directly with a fixed argv list and
// check len + each entry. argv[0] is implementation-defined
// (the binary path under emulation, often `/tmp/...`); we
// just check that it ends with our binary name.
func TestArm64Args(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	src := `function main(): i32 {
    var a: string[] = args();
    var i: i32 = 0;
    while (i < len(a)) {
        print(a[i]);
        i = i + 1;
    }
    return len(a);
}`
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
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(qemu, binPath, "alpha", "beta", "gamma")
	out, _ := cmd.CombinedOutput()
	if got, want := cmd.ProcessState.ExitCode(), 4; got != want {
		t.Errorf("exit = %d (argc), want %d", got, want)
	}
	// argv[0] is the binary path; check just the user args.
	for _, want := range []string{"alpha\n", "beta\n", "gamma\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// arm64 arena_save / arena_restore — snapshot the bump cursor
// and rewind to discard everything allocated between the two
// calls. Verifies both helpers are wired up and that
// reclaim is observable as heap_ptr returning to its saved
// value.
func TestArm64Arena(t *testing.T) {
	_, code := compileAndRunArm64(t, `function main(): i32 {
    var s1: string = "hello, " + "world!"; // alloc
    var saved: i32 = arena_save();
    var s2: string = "throwaway-" + "junk"; // alloc, will be reclaimed
    arena_restore(saved);
    var s3: string = "after-" + "restore"; // alloc reuses s2's space
    return len(s1) + len(s3);
}`)
	if code != 13+13 {
		t.Errorf("exit = %d, want 26 (len(s1) + len(s3))", code)
	}
}

// arm64 random_bytes(n) — kernel CSPRNG fill. Verifies length
// matches and that the output isn't all zeros (extremely unlikely
// from getrandom + actual entropy).
func TestArm64RandomBytes(t *testing.T) {
	out, code := compileAndRunArm64(t, `function main(): i32 {
    var s: string = random_bytes(16);
    write(s);
    return len(s);
}`)
	if code != 16 {
		t.Errorf("exit = %d, want 16 (length of returned bytes)", code)
	}
	if len(out) != 16 {
		t.Errorf("stdout len = %d, want 16", len(out))
	}
	allZero := true
	for _, b := range []byte(out) {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Errorf("random_bytes returned all zeros — getrandom likely failed silently")
	}
}

// arm64 eprint + exit. eprint(s) writes to fd 2 (stderr); exit(code)
// is a direct exit syscall and skips main's normal return path.
// Combined: write to stderr, then bail with a specific code.
func TestArm64EprintExit(t *testing.T) {
	out, code := compileAndRunArm64(t, `function main(): i32 {
    eprint("oops");
    exit(7);
    return 99;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (exit(7) should not fall through to return 99)", code)
	}
	// compileAndRunArm64 captures CombinedOutput so stderr is folded in.
	want := "oops\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// arm64 TCP primitives: tcp_listen / tcp_close round-trip
// validates the socket / bind / listen / close syscall
// chain. Port 0 means "kernel-assigned ephemeral" — fast
// way to confirm the listener works without picking a free
// port. Full HTTP server e2e (handle() + auto-main +
// tcp_serve + parser/serializer composed) is a follow-up.
func TestArm64TcpListen(t *testing.T) {
	_, code := compileAndRunArm64(t, `function main(): i32 {
    var fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    tcp_close(fd);
    return 42;
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (tcp_listen + tcp_close on ephemeral port)", code)
	}
}

// arm64-darwin baseline: native Apple Silicon macOS Mach-O
// binaries. Compiles via clang --target=arm64-apple-darwin +
// lld's Mach-O backend; the resulting binary runs natively on
// Apple Silicon Macs (no Linux container needed). Tests
// can't execute the binary here (qemu-aarch64 only emulates
// Linux), so they assert the output is a valid Mach-O 64-bit
// arm64 executable.
//
// All three syscall surfaces the runtime needs are now
// Darwin-aware: SYS_exit (1), SYS_mmap (197) in __lang_alloc,
// and the TCP/IO family (socket=97, bind=104, listen=106,
// accept=30, read=3, write=4, close=6). Each emits via
// `svc #0x80` with x16=number, and TCP/IO normalises Darwin's
// C-flag error shape into Linux-style -errno in x0 so the
// existing callers' `cmp x0, #0; blt` checks work unchanged.
func TestArm64DarwinBuilds(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH; skipping arm64-darwin cross-compile e2e")
	}
	// lld is required for Mach-O cross-compilation from Linux,
	// but on a native macOS arm64 host clang ships with ld64
	// and we don't need (or want) lld. The macOS CI runner
	// hits this branch.
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	if !native {
		if _, err := exec.LookPath("ld.lld"); err != nil {
			t.Skip("lld not on PATH; skipping arm64-darwin cross-compile e2e")
		}
	}

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		// Plain return — exercises only SYS_exit.
		{"exit_42", `function main(): i32 { return 42; }`, 42},
		// String concat — exercises SYS_mmap via __lang_alloc.
		{"strconcat", `function main(): i32 {
    var s: string = "hello, " + "world!";
    return len(s);
}`, 13},
		// TCP listen + close — exercises socket/bind/listen/close
		// syscalls (Darwin numbers + svc #0x80 path).
		{"tcp", `function main(): i32 {
    var fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    tcp_close(fd);
    return 42;
}`, 42},
		// Array push — exercises the IR's emitArrayPush
		// inline lowering (alloc + memcpy + tail store).
		// push() returns a new array; lang uses value
		// semantics so the receiver must be reassigned.
		{"arrpush", `function main(): i32 {
    var xs: i32[] = [];
    xs = xs.push(7);
    xs = xs.push(35);
    return xs[0] + xs[1];
}`, 42},
		// Stdout builtins — print(s) lowers to two write(2)s
		// (string + newline), putchar(c) to a single 1-byte
		// write. Exercises Darwin write syscall + the
		// .LLangNewline rodata entry on Mach-O.
		{"print", `function main(): i32 {
    print("hi");
    putchar(33);
    putchar(10);
    return 0;
}`, 0},
		// exit(code) — direct exit syscall; bypasses main's
		// normal return path. Verifies the user-supplied code
		// makes it through Darwin's `mov x16, #1; svc #0x80`
		// flavour of exit.
		{"exit", `function main(): i32 {
    exit(7);
    return 99;
}`, 7},
		// args() — argv reader. With no extra args passed by
		// the harness, argv contains just the binary path, so
		// argc == 1. Verifies the start-runtime prologue
		// stashed argc/argv from the kernel-delivered stack.
		{"args", `function main(): i32 {
    return len(args());
}`, 1},
		// arena_save / arena_restore — snapshot + rewind the
		// bump cursor. Both leaf helpers (one ldr / one str
		// against __lang_heap_ptr).
		{"arena", `function main(): i32 {
    var s1: string = "hello, " + "world!";
    var saved: i32 = arena_save();
    var s2: string = "throwaway-" + "junk";
    arena_restore(saved);
    return len(s1);
}`, 13},
		// stdin().read_line() — exercises the .bss buffer +
		// byte-by-byte read syscall + Option[string] result.
		// CI runs the binary with no stdin attached, so the
		// first read returns 0 (EOF) and we get None.
		{"read_line", `function main(): i32 {
    match (stdin().read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return -1;
}`, 0},
		// random_bytes(n) — Darwin getentropy path
		// (chunked, 256-byte cap per call). Just verify the
		// length round-trips; can't assert content.
		{"random_bytes", `function main(): i32 {
    return len(random_bytes(32));
}`, 32},
		// Map is deliberately not exercised here yet — the
		// runtime round-trips heap pointers through 32-bit
		// storage slots (__store_i32 / __load_i32), and
		// macOS hands out high addresses that don't fit.
		// Needs the prelude widened to i64 pointer storage;
		// follow-up PR.
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, err := parser.Parse(c.src)
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
			asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
			if err != nil {
				t.Fatalf("emit: %v", err)
			}

			dir := t.TempDir()
			asmPath := filepath.Join(dir, "prog.s")
			binPath := filepath.Join(dir, "prog")
			if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			// On macOS arm64 native, the default clang IS the
			// arm64-apple-darwin clang and ld64 is its default
			// linker; the cross-compile flags would force an
			// unnecessary lld dependency. Cross from Linux
			// requires lld because the host's clang defaults
			// to ELF.
			var args []string
			if native {
				args = []string{"-nostdlib", asmPath, "-o", binPath}
			} else {
				args = []string{
					"--target=arm64-apple-darwin",
					"-fuse-ld=lld",
					"-nostdlib",
					"-Wl,-arch,arm64",
					asmPath,
					"-o", binPath,
				}
			}
			if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
				t.Fatalf("clang Mach-O: %v\n%s\n--- asm ---\n%s", err, out, asm)
			}
			out, _ := exec.Command("file", binPath).CombinedOutput()
			// Linux `file` reports "Mach-O 64-bit arm64 executable";
			// macOS `file` reports "Mach-O 64-bit executable arm64"
			// (word order differs). Both are fine — check the three
			// pieces separately.
			s := string(out)
			if !strings.Contains(s, "Mach-O 64-bit") || !strings.Contains(s, "arm64") || !strings.Contains(s, "executable") {
				t.Errorf("not a Mach-O arm64 executable: %s\n%s", out, asm)
			}
			// Cross-compilation hosts can't run the Mach-O —
			// qemu-aarch64 only speaks the Linux ABI. The
			// macos-14 CI runner hits this and verifies the
			// runtime actually behaves correctly.
			if native {
				cmd := exec.Command(binPath)
				_, _ = cmd.CombinedOutput()
				if got := cmd.ProcessState.ExitCode(); got != c.wantExit {
					t.Errorf("native exit = %d, want %d\n--- asm ---\n%s", got, c.wantExit, asm)
				}
			}
		})
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