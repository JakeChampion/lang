// x86-64 (amd64) Linux end-to-end tests. Third native backend
// alongside arm64 and the WASM family — emits System V AMD64
// assembly + Linux x86-64 syscall wiring.
//
// Tests run the compiled binary natively when the host is
// x86_64 Linux (no qemu needed); otherwise fall back to
// qemu-x86_64 in user-mode-emulation. Each test SKIPs (rather
// than fails) when the cross-compiler or qemu isn't installed,
// so CI on aarch64-only hosts doesn't hard-fail.
//
// First-PR scope (the "scaffolding + exit code" cut from the
// docs/LANGUAGE-DIRECTION.md x86-64 plan): exactly one
// shape — `function main(): i32 { return N; }`. Subsequent PRs
// add arithmetic / control flow, then strings + the alloc
// runtime, then composite types, then TCP + HTTP, then the
// parked `ir.TailCallOptimize` pass.
package e2e

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/parser"
)

// x86_64Tooling locates the gcc cross-compiler used to link
// the emitted asm. `qemu-x86_64` is optional — when the host
// is already x86_64 Linux the binary runs natively. Returns
// the binary executor command line (qemu prefix or empty).
func x86_64Tooling(t *testing.T) (gcc string, exec_ []string) {
	t.Helper()
	for _, c := range []string{"x86_64-linux-gnu-gcc", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	if gcc == "" {
		t.Skip("no x86_64-linux-gnu-gcc / gcc on PATH; skipping x86-64 e2e")
	}
	// Pick the runner. Native exec is preferred — no qemu
	// transition overhead — but we'll fall back to
	// qemu-x86_64 on non-x86_64 hosts so the same test suite
	// passes on aarch64 dev boxes.
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return gcc, nil
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return gcc, []string{p}
	}
	t.Skip("non-x86_64 host and no qemu-x86_64 on PATH; skipping x86-64 e2e")
	return "", nil
}

// compileAndRunX86_64 compiles `src`, links it as a static
// Linux x86-64 ELF, runs it, and returns (combined-output,
// exit-code). Mirrors the arm64 helper's shape so the tests
// look symmetric.
func compileAndRunX86_64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)

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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// `function main(): i32 { return N; }` is the smallest
// program shape that exercises the entire emit pipeline:
// _start prologue, main's prologue/epilogue, OpConstI32,
// OpReturn, the exit_group syscall wiring. If this passes
// the toolchain + test harness are wired correctly and
// follow-up PRs can layer on arithmetic / control flow.
func TestX86_64ExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 42, 255} {
		want := want
		t.Run("", func(t *testing.T) {
			src := "function main(): i32 { return " + intToString(want) + "; }"
			_, code := compileAndRunX86_64(t, src)
			if code != want {
				t.Errorf("return %d → exit = %d", want, code)
			}
		})
	}
}

// Arithmetic, locals, function calls (direct + recursive).
// Mirrors TestArm64Arithmetic's case table so the two
// backends stay observably equivalent — same source, same
// exit code.
func TestX86_64Arithmetic(t *testing.T) {
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
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// Control flow: while loop, if/else chain, comparison ops.
// Exercises OpBlock / OpLoop / OpIf / OpEnd / OpBr / OpBrIf
// scope tracking and the test+jz/jnz branch idioms. Mirrors
// TestArm64ControlFlow's case table.
func TestX86_64ControlFlow(t *testing.T) {
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
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// String literals + len(). Each literal lives in .rodata
// with a 4-byte little-endian length prefix at `[ptr - 4]`;
// `len(s)` lowers to `OpConstStr; OpConstI32 4; OpSub;
// OpLoad`. PR 3 wires the .rodata emission + the load width
// dispatch, so this is the smallest test that catches a
// broken length prefix or a broken `.LStr_N` address
// materialisation.
func TestX86_64StringLiteralLen(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { return len("hello"); }`, 5},
		{`function main(): i32 { return len("hello, world"); }`, 12},
		{`function main(): i32 { return len(""); }`, 0},
		{`function main(): i32 { return len("a") + len("bb") + len("ccc"); }`, 6},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// String concat via the runtime `__lang_strcat` helper.
// Exercises the alloc + memcpy + length-prefix path
// end-to-end: each `+` lowers to `OpCallDirect __lang_strcat`,
// which mmaps the heap on first call, copies both operands
// in, and returns a fresh data pointer.
func TestX86_64StringConcat(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { return len("hello, " + "world!"); }`, 13},
		{`function main(): i32 {
    var a: string = "foo";
    var b: string = "barbaz";
    return len(a + b);
}`, 9},
		// Triple-concat — each `+` is left-associative so this
		// flexes the strcat helper twice on the same arena.
		{`function main(): i32 { return len("aa" + "bb" + "cc"); }`, 6},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// Array literals + indexing. Pulls in __lang_alloc + the
// inline `lea rax, [base + idx*N]` per-stride index-helper
// path. Mirrors TestArm64ArrayLiteral so the two backends
// stay observably equivalent.
func TestX86_64ArrayLiteral(t *testing.T) {
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
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// Struct literals + field access. Verifies the existing
// `payloadStoreOp` + `structFieldLayout` IR-level lowering
// composes correctly on x86-64 — most of the work is
// IR-side; the backend just needs OpStore / OpLoad with the
// right widths (PR 3 wired both).
func TestX86_64Struct(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 10, y: 32 };
    return p.x + p.y;
}`, 42},
		// Struct with a string field — exercises the
		// ptr-width slot widening from PR #267.
		{`struct Person { age: i32, name: string }
function main(): i32 {
    var p: Person = Person { age: 25, name: "Claude" };
    return p.age + len(p.name);
}`, 31},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// f32 / f64 arithmetic, comparison, int <-> float
// conversions, negation. Mirrors TestArm64Floats's case
// table. Floats ride the operand stack as raw bit patterns
// and move into xmm0/xmm1 at op time — same shape as arm64
// uses for the V register file.
func TestX86_64Floats(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
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
		{`function main(): i32 {
    var pi: f64 = 3.14f64;
    var two: f64 = 2.0f64;
    if (pi * two > 6.0f64) { return 42; }
    return 0;
}`, 42},
		{`function main(): i32 {
    var n: i32 = 7;
    var f: f64 = (n as f64) * 1.5f64;
    return f as i32;
}`, 10},
		{`function main(): i32 {
    var x: f32 = 5.5;
    var y: f32 = 0.0 - x;
    if (y < 0.0) { return 1; }
    return 0;
}`, 1},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// End-to-end x86-64 HTTP handler. Same shape as
// `TestArm64HttpHandler` — compiles a tiny `handle` program
// (no manual main; the checker synthesises one calling
// `tcp_serve(__port_from_env("PORT", 8080), handle)`),
// spawns the resulting binary on a Go-picked free port,
// sends two requests on separate connections, asserts both
// bodies round-trip. The second request validates the
// `arena_save` / `arena_restore` cycle inside `tcp_serve` —
// a leak there would either OOM or scramble state between
// requests; both pass cleanly.
//
// Together with `TestArm64HttpHandler` this brings the two
// native backends to observable parity for the
// edge-handler use case the language is targeting.
func TestX86_64HttpHandler(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	src := `function handle(req: HttpRequest): HttpResponse {
    return HttpResponse {
        status: 200,
        body: "method=" + req.method + " path=" + req.path + " body-len=" + len(req.body).to_string()
    };
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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	var ready bool
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server never bound on %s within 10s", addr)
	}

	cases := []struct {
		req  string
		want string
	}{
		{"GET /first HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n", "method=GET path=/first body-len=0"},
		{"POST /second HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhello", "method=POST path=/second body-len=5"},
	}
	for i, c := range cases {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("request %d dial: %v", i, err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte(c.req)); err != nil {
			t.Fatalf("request %d write: %v", i, err)
		}
		resp, err := io.ReadAll(conn)
		conn.Close()
		if err != nil {
			t.Fatalf("request %d read: %v", i, err)
		}
		body := string(resp)
		if !strings.Contains(body, "HTTP/1.1 200") {
			t.Errorf("request %d: missing 200 status\n--- got ---\n%s", i, body)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("request %d: missing %q\n--- got ---\n%s", i, c.want, body)
		}
	}
}

// stdout builtins lowered to direct write(2) syscalls.
// Verifies the asm wires the right fd (1), length, and
// newline behaviour: `print` adds one, `write` doesn't,
// `putchar` writes a single byte.
func TestX86_64Print(t *testing.T) {
	out, code := compileAndRunX86_64(t, `function main(): i32 {
    print("hello x86-64");
    write("no-nl");
    putchar(10);
    putchar(65);
    putchar(10);
    return 0;
}`)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	want := "hello x86-64\nno-nl\nA\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

// argv capture. `args()` returns the program's argv as a
// `string[]` materialised by `__lang_args()`, populated
// from the argc / argv stashed at `_start`. Same shape as
// `TestArm64Args`: spawn the binary with three fixed user
// args, print each, check (a) len(args) reports the right
// count and (b) the user args show up on stdout. argv[0]
// is the binary path so we only check the trailing three.
func TestX86_64Args(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath, "alpha", "beta", "gamma")
	} else {
		args := append(append([]string{}, runner[1:]...), binPath, "alpha", "beta", "gamma")
		cmd = exec.Command(runner[0], args...)
	}
	out, _ := cmd.CombinedOutput()
	if got, want := cmd.ProcessState.ExitCode(), 4; got != want {
		t.Errorf("exit = %d (argc), want %d\n--- got ---\n%s", got, want, out)
	}
	for _, want := range []string{"alpha\n", "beta\n", "gamma\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// Tail-call optimisation. `ir.TailCallOptimize` rewrites
// every `OpCallDirect <self> ; OpReturn` pair in a function
// into a parameter rebind + `OpBr` back to a synthetic
// outer loop, so self-recursive functions run in O(1)
// stack depth. x86-64 is the first consumer of the pass —
// see the inline note in `EmitWithOptions`.
//
// Two assertions:
//
//  1. The asm has no `call <self>` instruction inside the
//     tail-recursive function. The only `call <self>`
//     left in the program is the kick-off from `main`.
//  2. A recursion depth that would overflow the kernel-
//     default 8 MiB stack (~10^5 frames * 16 bytes/frame
//     = 1.6 MiB) returns cleanly. Without TCO this would
//     segfault long before completing.
func TestX86_64TailCall(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	src := `function sum_to(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return sum_to(n - 1, acc + n);
}
function main(): i32 {
    return sum_to(100000, 0);
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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// Exactly one `call sum_to` survives — the one in
	// `main` — and the recursive site became `jmp <loop
	// top>`. If TCO didn't fire we'd see two.
	if got := strings.Count(asm, "call sum_to"); got != 1 {
		t.Errorf("`call sum_to` appearances = %d, want 1 (only from main); TCO didn't fire", got)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	// 100000 * 100001 / 2 = 5,000,050,000. As i32 (mod
	// 2^32) = 705_082_704. As an 8-bit exit code (mod
	// 256) = 80.
	if got := cmd.ProcessState.ExitCode(); got != 80 {
		t.Errorf("sum_to(100000, 0) → exit = %d, want 80", got)
	}
}

// random_bytes(n) — kernel CSPRNG fill via getrandom(2).
// Verifies length matches and that the output isn't all
// zeros (extremely unlikely from a working getrandom +
// actual entropy).
func TestX86_64RandomBytes(t *testing.T) {
	out, code := compileAndRunX86_64(t, `function main(): i32 {
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

// stdin().read_line() — exercises the 4 KiB .bss buffer +
// byte-by-byte read syscall + Option[string] result. Two
// runs:
//
//  1. Empty stdin → read(2) returns 0 before any byte →
//     None → exit 0.
//  2. Stdin = "hello\n" → first 6 bytes including the
//     newline land in the buffer → Some(line) → exit 1.
//
// Mirrors `TestArm64DarwinBuilds/read_line` for the same
// Option[string] payload-at-+8 layout.
func TestX86_64ReadLine(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	src := `function main(): i32 {
    match (stdin().read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return -1;
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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}

	runCase := func(stdin string, want int) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(binPath)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
		}
		cmd.Stdin = strings.NewReader(stdin)
		_, _ = cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("stdin=%q: exit = %d, want %d", stdin, got, want)
		}
	}
	runCase("", 0)            // EOF before any byte → None
	runCase("hello\n", 1)     // Some(line)
}

// Closure factory pattern: `var f = makeAdder(7); f(35)`. The
// IR's Defunctionalise pass rewrites `f(35)` into a direct call
// to the hoisted `add` with env_ptr pulled out of the closure
// pair at offset +ptrW (=8 on native; was hardcoded to 4 for
// wasm — see Defunctionalise's pairEnvOffset parameter). The
// pair allocation itself can't elide here because the slot's
// writer is OpCallDirect makeAdder, not a direct OpMakeClosure.
func TestX86_64ClosureFactory(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var f = makeAdder(7);
    return f(35);
}`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Two closures over different captured values must not share
// state — separate env blocks per MakeClosure.
func TestX86_64ClosureMultipleInstances(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var add5 = makeAdder(5);
    var add10 = makeAdder(10);
    return add5(1) + add10(1);
}`
	// (5+1) + (10+1) = 17
	if _, code := compileAndRunX86_64(t, src); code != 17 {
		t.Errorf("got %d, want 17", code)
	}
}

// Direct nested-function call (the ElideClosurePair case): the
// slot writer IS OpMakeClosure, so elide fires and the closure
// pair allocation collapses to just an env_ptr in the slot.
// Exercises the OpMakeEnv path.
func TestX86_64ClosureCapturesParamAndVar(t *testing.T) {
	src := `function outer(seed: i32): i32 {
    var bonus: i32 = 100;
    function inner(x: i32): i32 { return x + seed + bonus; }
    return inner(2);
}
function main(): i32 { return outer(40); }`
	// 2 + 40 + 100 = 142
	if _, code := compileAndRunX86_64(t, src); code != 142 {
		t.Errorf("got %d, want 142", code)
	}
}

// String capture — pointer-shaped capture takes a full ptr-width
// slot in the env block. Verifies captureSlotSize routes through
// the 8-byte store path (`mov [r12], rax`) rather than the 4-byte
// `mov [r12], eax` truncation.
func TestX86_64ClosureCapturesString(t *testing.T) {
	src := `function outer(s: string): i32 {
    function inner(): i32 { return len(s); }
    return inner();
}
function main(): i32 { return outer("hello"); }`
	if _, code := compileAndRunX86_64(t, src); code != 5 {
		t.Errorf("got %d, want 5 (len(\"hello\") via captured string)", code)
	}
}

// Multi-capture closure: two i32 captures laid out at offsets 0
// and 4 in the env block. Verifies the running-offset
// arithmetic in emitMakeClosureOrEnv.
func TestX86_64ClosureMultiCapture(t *testing.T) {
	src := `function make2(a: i32, b: i32): (i32) => i32 {
    function f(x: i32): i32 { return a + b + x; }
    return f;
}
function main(): i32 {
    var h = make2(10, 20);
    return h(12);
}`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Pointer + scalar captures mixed in one closure — pointer slot
// is 8 bytes, scalar slot is 4 bytes, total env = 12 bytes (rounded
// to 16 by the bump allocator). Exercises mixed-width offset
// arithmetic.
func TestX86_64ClosureCapturesMixedPointers(t *testing.T) {
	src := `function outer(s: string, n: i32): i32 {
    function inner(): i32 { return len(s) + n; }
    return inner();
}
function main(): i32 { return outer("hi", 40); }`
	// len("hi") + 40 = 42
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Map runtime — exercises the codegen-alias rewrites (`map_new`
// → `map_new_impl`, `__method_Map_*` → `_impl`), the new
// `__store_i32` / `__load_i32` / `__store_ptr` / `__load_ptr` /
// `__ptr_width` / `__memset` runtime helpers, and the lang
// prelude's open-addressing core. Mirrors TestArm64Map and the
// wasm StateMap* tests.
func TestX86_64Map(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"basic_set_get", `function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m.set(1, 100);
    m.set(2, 200);
    return m.get_or(2, 0);
}`, 200},
		{"grow_past_capacity", `function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 8) {
        m.set(i, i * 10);
        i = i + 1;
    }
    if (m.len() != 8) { return 1; }
    if (m.get_or(7, 0 - 1) != 70) { return 2; }
    return 42;
}`, 42},
		{"string_keys", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("alpha", 1);
    m.set("beta", 2);
    m.set("gamma", 3);
    return m.get_or("beta", 0 - 1) + m.len();
}`, 5},
		{"iter_after_delete", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("a", 10);
    m.set("b", 20);
    m.set("c", 30);
    m.delete("b");
    if (m.has("b")) { return 1; }
    var total: i32 = 0;
    var it = m.iter();
    while (it.has_next()) {
        total = total + it.value();
        it.advance();
    }
    return total;
}`, 40},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// Sub-i32 array reads + casts. Exercises:
//
//   - OpLoadI8S  (i8[] read; x86-64 `movsx eax, byte ptr [rax]`)
//   - OpLoadI16U (u16[] read; x86-64 `movzx eax, word ptr [rax]`)
//   - OpLoadI16S (i16[] read; x86-64 `movsx eax, word ptr [rax]`)
//   - OpSignExtend8  (i32 → i8 narrowing cast; x86-64 `movsx eax, al`)
//   - OpSignExtend16 (i32 → i16 narrowing cast; x86-64 `movsx eax, ax`)
//
// Pairs with wasm's TestWASMI8Array / TestWASMU16Array /
// TestWASMSubI32Widths.
func TestX86_64SubI32(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i8_array_signed_sum", `function main(): i32 {
    var xs: i8[] = [1 as i8, 2 as i8, 3 as i8, 0 - 1 as i8];
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < len(xs)) {
        sum = sum + (xs[i] as i32);
        i = i + 1;
    }
    return sum;
}`, 5},
		{"i16_array_signed_sum", `function main(): i32 {
    var xs: i16[] = [100 as i16, 200 as i16, 0 - 300 as i16];
    return (xs[0] as i32) + (xs[1] as i32) + (xs[2] as i32);
}`, 0},
		{"u16_array_zero_extends", `function main(): i32 {
    var xs: u16[] = [40000 as u16, 1 as u16];
    if ((xs[0] as i32) != 40000) { return 1; }
    if ((xs[1] as i32) != 1) { return 2; }
    return 7;
}`, 7},
		{"i32_to_i8_sign_preserved", `function main(): i32 {
    var v: i32 = 200;
    var b: i8 = v as i8;
    if ((b as i32) < 0) { return 7; }
    return 1;
}`, 7},
		{"i32_to_i16_sign_preserved", `function main(): i32 {
    var v: i32 = 40000;
    var s: i16 = v as i16;
    if ((s as i32) < 0) { return 7; }
    return 1;
}`, 7},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// compileX86_64InDir builds `src` and runs the resulting binary
// in a fresh temp dir seeded with `seed` files (path → content).
// Returns stdout, exit code, AND the dir so callers can read
// back files the program created. Mirrors wasm's runWasmInDir +
// arm64's compileArm64InDir.
func compileX86_64InDir(t *testing.T, src string, seed map[string]string) (stdout string, exitCode int, dir string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir = t.TempDir()
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode(), dir
}

// read_file returns Ok(content) for a present file.
func TestX86_64ReadFileOk(t *testing.T) {
	src := `function main(): i32 {
    match (read_file("greeting.txt")) {
        Ok(s) => { return len(s); },
        Err(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`
	_, code, _ := compileX86_64InDir(t, src, map[string]string{
		"greeting.txt": "hello, file\n",
	})
	if code != 12 {
		t.Errorf("got %d, want 12 (len of \"hello, file\\n\")", code)
	}
}

// Missing files surface as `IoError.NotFound(path)`. The path
// the caller passed must round-trip through the variant payload.
func TestX86_64ReadFileNotFound(t *testing.T) {
	src := `function main(): i32 {
    match (read_file("does_not_exist.txt")) {
        Ok(_) => { return 0; },
        Err(err) => {
            match (err) {
                NotFound(p) => { return len(p); },
                _ => { return 99; }
            }
        }
    }
    return 0 - 1;
}`
	_, code, _ := compileX86_64InDir(t, src, nil)
	// len("does_not_exist.txt") = 18
	if code != 18 {
		t.Errorf("got %d, want 18 (len of missing-file path via NotFound payload)", code)
	}
}

// write_file truncates the target and writes `content`. Verify
// by reading the file back from the host side after the program
// returns.
func TestX86_64WriteFileOk(t *testing.T) {
	src := `function main(): i32 {
    match (write_file("out.txt", "wrote it\n")) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return 0 - 1;
}`
	_, code, dir := compileX86_64InDir(t, src, nil)
	if code != 0 {
		t.Errorf("write_file exit = %d, want 0 (None path)", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "wrote it\n" {
		t.Errorf("got %q, want %q", got, "wrote it\n")
	}
}

// Round-trip: write then read; len reports the expected byte
// count. Both helpers in one program.
func TestX86_64ReadWriteFileRoundtrip(t *testing.T) {
	src := `function main(): i32 {
    match (write_file("rt.txt", "round trip")) {
        Some(_) => { return 1; },
        None => {}
    }
    match (read_file("rt.txt")) {
        Ok(s) => { return len(s); },
        Err(_) => { return 2; }
    }
    return 0 - 1;
}`
	_, code, _ := compileX86_64InDir(t, src, nil)
	if code != 10 {
		t.Errorf("got %d, want 10 (len of \"round trip\")", code)
	}
}

// Function-value-in-var: `var f: (i32, i32) => i32 = add; f(20, 22)`
// — exercises OpConstFunc + OpCallIndirect on x86-64. Mirrors
// TestArm64IndirectCall. The codegen has been in place since PR 2;
// this test closes the no-coverage gap flagged in
// BACKEND-PARITY.md.
func TestX86_64IndirectCall(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
    var f: (i32, i32) => i32 = add;
    return f(20, 22);
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (indirect call through function value)", code)
	}
}

// Arena scope: arena_save() snapshots the bump cursor and
// arena_restore(saved) rewinds it. Allocations after the
// restore reuse the space the freed allocations consumed.
// Mirrors TestArm64Arena.
func TestX86_64Arena(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function main(): i32 {
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

// eprint(s) routes a string + newline to stderr (fd 2); exit(N)
// terminates immediately via the exit_group syscall. Verifies the
// post-exit `return 99` is unreachable. Mirrors TestArm64EprintExit.
func TestX86_64EprintExit(t *testing.T) {
	out, code := compileAndRunX86_64(t, `function main(): i32 {
    eprint("oops");
    exit(7);
    return 99;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (exit(7) should not fall through to return 99)", code)
	}
	// compileAndRunX86_64 captures CombinedOutput so stderr is
	// folded in.
	if out != "oops\n" {
		t.Errorf("output = %q, want %q", out, "oops\n")
	}
}

// State[T] on natives is the program-lifetime / no-op
// interpretation. Mirrors TestArm64State; same source on both
// backends so they stay observably equivalent.
func TestX86_64State(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"scalar_counter", `state {
    var counter: i32 = 41;
}
function main(): i32 {
    counter = counter + 1;
    return counter;
}`, 42},
		{"scalar_i64", `state {
    var big: i64 = 100i64;
}
function main(): i32 {
    big = big + 1i64;
    return big as i32;
}`, 101},
		{"computed_init", `state {
    var x: i32 = 1 + 2 * 3;
}
function main(): i32 { return x; }`, 7},
		{"f64_and_bool", `state {
    var pi: f64 = 3.14;
    var maybe: boolean = true;
}
function main(): i32 {
    if (maybe) { return (pi * 10.0f64) as i32; }
    return 0;
}`, 31},
		{"string_init", `state {
    var greeting: string = "hello, " + "world!";
}
function main(): i32 { return len(greeting); }`, 13},
		{"map_across_calls", `state {
    var todos: Map[i32, string] = map_new(4);
}
function add(id: i32, text: string): void {
    todos.set(id, text);
}
function get(id: i32): string {
    return todos.get_or(id, "?");
}
function main(): i32 {
    add(1, "buy milk");
    add(2, "feed cat");
    add(3, "walk dog");
    var got: string = get(2);
    if (got == "feed cat") { return 42; }
    return 0;
}`, 42},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}
