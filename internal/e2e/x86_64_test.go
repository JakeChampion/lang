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
