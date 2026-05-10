// Package e2e exercises the full pipeline: compile a .lang source string to
// ARM32 assembly, link it with the system's ARM cross-compiler, and run the
// resulting binary under qemu-arm.
//
// Each test SKIPS (rather than fails) when the cross-compiler or qemu-arm
// is not installed, so `go test ./...` stays green on machines without an
// ARM toolchain. CI installs both, so coverage is exercised there.
package e2e

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/parser"
)

func tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	for _, c := range []string{"arm-linux-gnueabihf-gcc", "arm-linux-gnueabi-gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	for _, c := range []string{"qemu-arm", "qemu-arm-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if gcc == "" || qemu == "" {
		t.Skipf("ARM cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

func compileAndRun(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
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

// compileMultiFileAndRun writes each entry in `files` (path → src)
// into a temp dir, loads the named entry through modload, runs the
// rest of the pipeline, and exec's the resulting ARM binary under
// qemu. Used by the cross-module e2e tests.
func compileMultiFileAndRun(t *testing.T, entry string, files map[string]string) (stdout string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

	dir := t.TempDir()
	for path, contents := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, entry))
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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

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

func TestExitCode(t *testing.T) {
	_, code := compileAndRun(t, `function main(): i32 { return 42; }`)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// state{} block on arm32: each var lowers to a `.data` label
// pre-baked with the literal init value, accessed via
// `ldr =state_<NAME>` + LDR / STR through that pointer. The
// loader maps `.data` into memory with the literal already in
// place, so no runtime init code is needed for the simple
// scalar shapes the first PR ships.
func TestArm32StateScalarCounter(t *testing.T) {
	_, code := compileAndRun(t, `state {
    var counter: i32 = 41;
}

function main(): i32 {
    counter = counter + 1;
    return counter;
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (state counter survives init -> main)", code)
	}
}

// arm32 HTTP server end-to-end. Compiles a tcp_serve-based
// program, runs it under qemu-arm (which uses the host's
// network stack for TCP), and drives it from the test
// process via Go's net/http client. Verifies the same
// shape that wasi-http components target — `function
// handle(req): HttpResponse` semantics — works on a real
// native target with real kernel sockets.
//
// Skips when the cross-compiler or qemu aren't installed.
// Picks a kernel-assigned ephemeral port to dodge
// EADDRINUSE under parallel test runs.
func TestArm32HttpServer(t *testing.T) {
	gcc, qemu := tooling(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	src := `function handle(req: HttpRequest): HttpResponse {
    if (req.path == "/hello") {
        return HttpResponse { status: 200, body: req.method + " hello!" };
    }
    if (req.method == "POST") {
        return HttpResponse { status: 200, body: "echo: " + req.body };
    }
    return HttpResponse { status: 404, body: "not found\n" };
}

function main(): i32 {
    return tcp_serve(` + itoa(port) + `, handle);
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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "server.s")
	binPath := filepath.Join(dir, "server")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}

	// Run the arm32 server under qemu. qemu-arm in user-mode
	// uses the host's network stack so plain TCP just works.
	cmd := exec.Command(qemu, binPath)
	var srvOut bytes.Buffer
	cmd.Stdout = &srvOut
	cmd.Stderr = &srvOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("qemu start: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	})

	// Wait for the server to bind. tcp_listen returns the
	// listener fd as soon as bind+listen succeed; the first
	// accept-able socket appears almost immediately, but
	// qemu translation adds a small startup delay so we poll.
	addr := "127.0.0.1:" + itoa(port)
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v\nserver out:\n%s", addr, err, srvOut.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// GET /hello → 200 "GET hello!"
	resp, err := client.Get("http://" + addr + "/hello")
	if err != nil {
		t.Fatalf("GET /hello: %v\nserver out:\n%s", err, srvOut.String())
	}
	if resp.StatusCode != 200 {
		t.Errorf("GET /hello status = %d; want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "GET hello!" {
		t.Errorf("GET /hello body = %q; want %q", string(body), "GET hello!")
	}

	// POST /anywhere with body
	want := "round trip"
	resp, err = client.Post("http://"+addr+"/echo", "text/plain", strings.NewReader(want))
	if err != nil {
		t.Fatalf("POST /echo: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("POST status = %d; want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "echo: "+want {
		t.Errorf("POST body = %q; want %q", string(body), "echo: "+want)
	}

	// 404 path
	resp, err = client.Get("http://" + addr + "/missing")
	if err != nil {
		t.Fatalf("GET /missing: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("GET /missing status = %d; want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// String slicing on arm32: `s[a:b]` lowers to a __str_slice
// runtime call. Arm32 didn't ship the helper until the
// HTTP-prelude work surfaced the gap (slicing inside
// __http_parse_request_line, __http_content_length, etc).
func TestArm32StringSlice(t *testing.T) {
	_, code := compileAndRun(t, `function main(): i32 {
    var s: string = "hello, world!";
    var sub: string = s[7:12];
    return len(sub);
}`)
	if code != 5 {
		t.Errorf("exit = %d, want 5 (len('world') after slice)", code)
	}
}

// String slicing trap path: slicing past the end with `high >
// src_len` traps via the `unreachable` path. Verifies the
// arm32 trap handler exits with code 134 (matches the wasm
// `unreachable` SIGILL behaviour).
func TestArm32StringSliceTraps(t *testing.T) {
	_, code := compileAndRun(t, `function main(): i32 {
    var s: string = "abc";
    var sub: string = s[0:99];
    return len(sub);
}`)
	if code != 134 {
		t.Errorf("exit = %d, want 134 (out-of-bounds slice traps)", code)
	}
}

// HTTP request parser end-to-end on arm32. Drives the lang-
// prelude `http_parse_request` against a complete request
// buffer and asserts the parsed method's length. Same
// behavioural shape as the wasm e2e tests
// (TestWASMHttpParseRequest etc); confirms the new arm32
// string-runtime helpers (`__str_slice`, `__alloc_u8`,
// `string_from_bytes`) compose correctly under qemu.
func TestArm32HttpParseRequest(t *testing.T) {
	_, code := compileAndRun(t, `function main(): i32 {
    var raw: string = "POST /todos HTTP/1.1\r\nHost: localhost\r\nContent-Length: 13\r\n\r\nhello, world!";
    match (http_parse_request(raw)) {
        Some(req) => {
            if (req.method != "POST") { return 1; }
            if (req.path != "/todos") { return 2; }
            if (req.body != "hello, world!") { return 3; }
            return 42;
        },
        None => { return 99; }
    }
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (POST /todos with 13-byte body parses on arm32)", code)
	}
}

func TestArm32HttpSerializeResponse(t *testing.T) {
	_, code := compileAndRun(t, `function main(): i32 {
    var resp: HttpResponse = HttpResponse { status: 200, body: "hi" };
    var wire: string = http_serialize_response(resp);
    if (!wire.starts_with("HTTP/1.1 200 OK\r\n")) { return 1; }
    if (!wire.contains("Content-Length: 2\r\n")) { return 2; }
    if (!wire.ends_with("hi")) { return 3; }
    return 42;
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (serialised response wire shape on arm32)", code)
	}
}

// State string on arm32: a non-literal init expression
// (`"hello, " + "world"`) routes through the synthesised
// `__state_init` start function called from `_start` before
// `main`. The string allocation lives in the bump heap below
// any per-request `arena_save` point — same lifetime story as
// scalar state.
func TestArm32StateStringConcat(t *testing.T) {
	_, code := compileAndRun(t, `state {
    var greeting: string = "hello, " + "world";
}

function main(): i32 {
    return len(greeting);
}`)
	if code != 12 {
		t.Errorf("exit = %d, want 12 (state string init runs `+` concat at _start time)", code)
	}
}

// State scalar with computed init: arm32 stores the LITERAL
// value (zero / null when the init is non-literal) in `.data`,
// then `__state_init` overwrites it with the computed value at
// startup. Verifies the result lands and is visible from main.
func TestArm32StateComputedScalarInit(t *testing.T) {
	_, code := compileAndRun(t, `state {
    var precomputed: i32 = 1 + 2 * 3;
}

function main(): i32 {
    return precomputed;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (computed init 1 + 2*3 = 7)", code)
	}
}

// Multi-var state on arm32: confirms each var gets its own
// .data label and the LDR / STR codegen addresses them
// independently (no aliasing between adjacent state slots).
func TestArm32StateMultipleVars(t *testing.T) {
	_, code := compileAndRun(t, `state {
    var a: i32 = 10;
    var b: i32 = 30;
}

function main(): i32 {
    a = a + 1;
    b = b + 1;
    return a + b;
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (a=11, b=31, sum=42)", code)
	}
}

// arena_save / arena_restore expose the bump-allocator cursor.
// After save → alloc → restore, the cursor must come back to
// the saved value, so a follow-up alloc returns the same
// address as the first one. We compare data-pointers via a
// helper that turns the pointer into an integer (the runtime
// uses raw integers anyway, but the language has no
// reinterpret cast — we approximate by comparing two array
// allocations' return values via len, then via mutating /
// re-reading the heap). For this test, we check the cursor
// directly: arena_save before and after a no-op restore must
// return the same value.
func TestArenaResetReclaimsAllocations(t *testing.T) {
	src := `function main(): i32 {
		var saved: i32 = arena_save();
		// Allocate something — this advances the heap cursor.
		var a: i32[] = [1, 2, 3, 4, 5];
		var afterAlloc: i32 = arena_save();
		// Restore to the saved cursor.
		arena_restore(saved);
		var afterRestore: i32 = arena_save();
		// The cursor must have advanced after the alloc...
		if (afterAlloc <= saved) { return 1; }
		// ...and come back to the saved value after restore.
		if (afterRestore != saved) { return 2; }
		// Sanity-check the array we allocated still has the right length.
		if (len(a) != 5) { return 3; }
		return 0;
	}`
	_, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("arena_save/restore: exit = %d, want 0", code)
	}
}

// random_bytes(n) returns a fresh string of n cryptographic-
// quality random bytes (via getrandom(2) on arm32). We can't
// assert specific values, but length and basic shape checks
// catch regressions.
func TestRandomBytesArm(t *testing.T) {
	src := `function main(): i32 {
		var a: string = random_bytes(16);
		var b: string = random_bytes(16);
		// Two calls must produce strings of the requested length.
		if (len(a) != 16) { return 1; }
		if (len(b) != 16) { return 2; }
		// And those strings must (almost certainly) differ —
		// 16 bytes of CSPRNG output collide with probability 2^-128.
		if (a == b) { return 3; }
		return 0;
	}`
	_, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("random_bytes: exit = %d, want 0", code)
	}
}

// A leaf function with several params exercises the register-pinned
// prologue: the body still produces the right answer despite never
// touching the stack to read a/b/c/d.
func TestLeafFunctionAllRegArgs(t *testing.T) {
	src := `
		function leaf(a: i32, b: i32, c: i32, d: i32): i32 {
			return (a + b) * (c + d);
		}
		function main(): i32 { return leaf(2, 3, 4, 5); }`
	_, code := compileAndRun(t, src)
	if code != 45 {
		t.Errorf("exit = %d, want 45 ((2+3)*(4+5))", code)
	}
}

// Function-type syntax in parameter declarations lets a function
// accept another function as a value and call it indirectly.
func TestFunctionTypeAsParameter(t *testing.T) {
	src := `
		function add(a: i32, b: i32): i32 { return a + b; }
		function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
			return f(a, b);
		}
		function main(): i32 { return apply(add, 40, 2); }`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// Deeply self-recursive function: the test would blow the call stack
// without tail-call optimization. The tail call rewrites to a branch
// so we can iterate millions of times in O(1) frames.
func TestDeepTailRecursionDoesNotOverflowStack(t *testing.T) {
	src := `
		function countdown(n: i32, acc: i32): i32 {
			if (n == 0) { return acc; }
			return countdown(n - 1, acc + 1);
		}
		function main(): i32 { return countdown(100000, 0); }`
	// 100000 mod 256 = 160 (the i32 result wraps when used as an
	// 8-bit exit code).
	_, code := compileAndRun(t, src)
	if code != 160 {
		t.Errorf("exit = %d, want 160 (= 100000 mod 256)", code)
	}
}

// Storing a function name in a var and calling through that var
// goes through the indirect-call path (blx r12).
func TestFunctionValueIndirectCall(t *testing.T) {
	src := `
		function add(a: i32, b: i32): i32 { return a + b; }
		function main(): i32 {
			var f = add;
			return f(40, 2);
		}`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestArithmeticAndCalls(t *testing.T) {
	src := `
		function add(a: i32, b: i32): i32 { return a + b; }
		function main(): i32 { return add(40, 2); }`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestFactorialRecursion(t *testing.T) {
	src := `
		function fact(n: i32): i32 {
			if (n == 0) { return 1; }
			return n * fact(n - 1);
		}
		function main(): i32 { return fact(5); }`
	_, code := compileAndRun(t, src)
	if code != 120 {
		t.Errorf("exit = %d, want 120", code)
	}
}

func TestWhileLoop(t *testing.T) {
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			var i: i32 = 1;
			while (i <= 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 55 {
		t.Errorf("exit = %d, want 55 (1+2+...+10)", code)
	}
}

func TestDivision(t *testing.T) {
	src := `function main(): i32 { return 100 / 7; }`
	_, code := compileAndRun(t, src)
	if code != 14 {
		t.Errorf("exit = %d, want 14", code)
	}
}

func TestComparisonsAndShortCircuit(t *testing.T) {
	src := `
		function inRange(x: i32, lo: i32, hi: i32): boolean {
			return lo <= x && x <= hi;
		}
		function main(): i32 {
			if (inRange(5, 1, 10) && !inRange(20, 1, 10)) { return 1; }
			return 0;
		}`
	_, code := compileAndRun(t, src)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestPutcharOutput(t *testing.T) {
	src := `
		function main(): i32 {
			putchar(72);  // H
			putchar(73);  // I
			putchar(10);  // \n
			return 0;
		}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if out != "HI\n" {
		t.Errorf("output = %q, want \"HI\\n\"", out)
	}
}

func TestBreakInWhile(t *testing.T) {
	src := `
		function main(): i32 {
			var i: i32 = 0;
			while (true) {
				if (i == 7) { break; }
				i = i + 1;
			}
			return i;
		}`
	_, code := compileAndRun(t, src)
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

func TestContinueInForRunsStep(t *testing.T) {
	// Sum 5..9 (skip i < 5) = 5+6+7+8+9 = 35.
	// `continue` must still run the step, otherwise we'd loop forever.
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			for (var i: i32 = 0; i < 10; i = i + 1) {
				if (i < 5) { continue; }
				sum = sum + i;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 35 {
		t.Errorf("exit = %d, want 35", code)
	}
}

func TestForLoop(t *testing.T) {
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			for (var i: i32 = 1; i <= 10; i = i + 1) {
				sum = sum + i;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 55 {
		t.Errorf("exit = %d, want 55", code)
	}
}

func TestSixArgFunction(t *testing.T) {
	src := `
		function sum6(a: i32, b: i32, c: i32,
		              d: i32, e: i32, f: i32): i32 {
			return a + b + c + d + e + f;
		}
		function main(): i32 { return sum6(1, 2, 4, 8, 16, 32); }`
	_, code := compileAndRun(t, src)
	if code != 63 {
		t.Errorf("exit = %d, want 63", code)
	}
}

func TestModulo(t *testing.T) {
	src := `function main(): i32 { return 17 % 5; }`
	_, code := compileAndRun(t, src)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestBitwiseAndOr(t *testing.T) {
	// (12 & 10) | 1 = 8 | 1 = 9
	src := `function main(): i32 { return (12 & 10) | 1; }`
	_, code := compileAndRun(t, src)
	if code != 9 {
		t.Errorf("exit = %d, want 9", code)
	}
}

func TestShifts(t *testing.T) {
	// (1 << 5) >> 2 = 32 >> 2 = 8
	src := `function main(): i32 { return (1 << 5) >> 2; }`
	_, code := compileAndRun(t, src)
	if code != 8 {
		t.Errorf("exit = %d, want 8", code)
	}
}

func TestStringConcat(t *testing.T) {
	src := `function main(): i32 {
		var s: string = "Hello, " + "world!";
		print(s);
		return 0;
	}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if out != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", out, "Hello, world!\n")
	}
}

func TestStringConcatChained(t *testing.T) {
	// Three-part concat exercises a nested concat inside the helper —
	// `(a + b) + c` allocates twice.
	src := `function main(): i32 {
		print("foo" + "-" + "bar");
		return 0;
	}`
	out, _ := compileAndRun(t, src)
	if out != "foo-bar\n" {
		t.Errorf("output = %q", out)
	}
}

func TestStringPrint(t *testing.T) {
	src := `function main(): i32 {
		print("Hello, world!");
		return 0;
	}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// `print` lowers to puts, which appends a newline.
	if out != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", out, "Hello, world!\n")
	}
}

func TestStringEscapes(t *testing.T) {
	src := `function main(): i32 {
		print("tab:\there\nnext line");
		return 0;
	}`
	out, _ := compileAndRun(t, src)
	if out != "tab:\there\nnext line\n" {
		t.Errorf("output = %q", out)
	}
}

// `for x in arr { ... }` desugars to an index loop; e2e check that
// the desugared form actually iterates correctly under qemu-arm.
// Cross-module struct types end-to-end: an entry file declares
// `var p: point.Point = point.make(3, 4);`, the loader rewrites
// the qualified type to the mangled `point__Point`, the checker
// validates the field access, and the linked binary returns 7.
func TestCrossModuleStructTypeArm(t *testing.T) {
	_, code := compileMultiFileAndRun(t, "main.lang", map[string]string{
		"point.lang": `pub struct Point { x: i32, y: i32 }
pub function make(x: i32, y: i32): Point {
	return Point { x: x, y: y };
}`,
		"main.lang": `import "./point";
function main(): i32 {
	var p: point.Point = point.make(3, 4);
	return p.x + p.y;
}`,
	})
	if code != 7 {
		t.Errorf("exit = %d, want 7 (3 + 4)", code)
	}
}

// Top-level consts end-to-end: integer + arithmetic over an
// earlier const folds at compile time, the IR pipeline sees only
// literals, and the resulting binary returns the resolved value.
func TestConstFoldedIntoArm(t *testing.T) {
	src := `
		const BASE: i32 = 10;
		const TWICE: i32 = BASE * 2;
		function main(): i32 { return TWICE + BASE; }`
	_, code := compileAndRun(t, src)
	if code != 30 {
		t.Errorf("exit = %d, want 30 (10*2 + 10)", code)
	}
}

// Cross-module `pub const` reaches the binary intact: the entry
// imports a module that exports a i32-typed const, and the
// folded literal travels through the rewriter and the rest of the
// pipeline without surprises.
func TestPubConstAcrossModulesArm(t *testing.T) {
	_, code := compileMultiFileAndRun(t, "main.lang", map[string]string{
		"limits.lang": `pub const MAX: i32 = 42;`,
		"main.lang": `import "./limits";
function main(): i32 { return limits.MAX; }`,
	})
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// compileAndCaptureStreams is compileAndRun split-streams. The
// `eprint` and `write` e2e tests need to know which file
// descriptor each builtin lands on, which compileAndRun's
// CombinedOutput collapses. qemu passes stdin/stdout/stderr
// straight through, so the parent's split capture is faithful.
func compileAndCaptureStreams(t *testing.T, src string) (stdout, stderr string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
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
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String(), cmd.ProcessState.ExitCode()
}

// compileAndRunWithArgs is compileAndRun plus extra positional argv
// for the qemu-launched binary. argv[0] is the binary path injected
// by qemu (argv[0] is whatever execve sets). Used by the args()
// e2e tests to feed scripted argv into the running program.
func compileAndRunWithArgs(t *testing.T, src string, extraArgs ...string) int {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
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
	cmdArgs := append([]string{binPath}, extraArgs...)
	cmd := exec.Command(qemu, cmdArgs...)
	out, _ := cmd.CombinedOutput()
	if testing.Verbose() {
		t.Logf("output: %s", string(out))
	}
	return cmd.ProcessState.ExitCode()
}

// args() under qemu-arm: argv[0] is the binary path that qemu
// passes through, plus whatever extra args we supplied — so
// `len(args())` should match the count we set up.
func TestArgsBuiltinArm(t *testing.T) {
	src := `function main(): i32 {
		var a: string[] = args();
		return len(a);
	}`
	code := compileAndRunWithArgs(t, src, "alpha", "beta")
	if code != 3 {
		t.Errorf("got %d, want 3 (binary path + alpha + beta)", code)
	}
}

// Reading individual args produces the expected string contents —
// the runtime helper must scan each NUL-terminated argv entry,
// allocate a length-prefixed copy, and the language's `print`
// must then handle it like any other string.
func TestArgsBuiltinReadsValueArm(t *testing.T) {
	src := `function main(): i32 {
		var a: string[] = args();
		print(a[1]);
		return 0;
	}`
	gcc, qemu := tooling(t)
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
	asm, err := codegen.Emit(prog, info)
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
	cmd := exec.Command(qemu, binPath, "hello")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "hello") {
		t.Errorf("expected output to contain `hello`, got %q", string(out))
	}
}

// runWithStdinEnv builds the program, then runs it under qemu
// with scripted stdin + extra env vars, returning stdout, stderr,
// and exit code separately. Used by the read_line / env / exit
// builtins' e2e tests where any one of those streams is the
// signal under test.
func runWithStdinEnv(t *testing.T, src, stdin string, extraEnv []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
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
	cmd.Stdin = strings.NewReader(stdin)
	if len(extraEnv) > 0 {
		// qemu-arm forwards parent env to the guest by default;
		// append our overrides so they win the lookup.
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	_ = cmd.Run()
	return soBuf.String(), seBuf.String(), cmd.ProcessState.ExitCode()
}

// `read_line()` returns `Some(line)` for a present line and
// `None` at EOF — the post-Phase-3 typed shape replaces the
// empty-string sentinel from earlier PRs.
func TestReadLineBuiltinArm(t *testing.T) {
	src := `function main(): i32 {
		match (stdin().read_line()) {
			Some(line) => { write(line); return len(line); },
			None => { return -1; }
		}
		return -2;
	}`
	stdout, _, code := runWithStdinEnv(t, src, "hello\n", nil)
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
	if code != 6 {
		t.Errorf("exit = %d, want 6 (len of \"hello\\n\")", code)
	}
}

// EOF routes to the `None` arm. A non-empty line (even just
// `"\n"`) routes to `Some`. The match arms encode the
// distinction; callers no longer have to special-case
// `len(line) == 0`.
func TestReadLineBuiltinEOFArm(t *testing.T) {
	src := `function main(): i32 {
		match (stdin().read_line()) {
			Some(line) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	_, _, code := runWithStdinEnv(t, src, "", nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (EOF routes to None arm)", code)
	}
}

// `env(name)` returns `Some(value)` when the key is set,
// `None` when it isn't.
func TestEnvBuiltinArm(t *testing.T) {
	src := `function main(): i32 {
		match (env("LANG_TEST_VAR")) {
			Some(v) => { write(v); return len(v); },
			None => { return -1; }
		}
		return -2;
	}`
	stdout, _, code := runWithStdinEnv(t, src, "", []string{"LANG_TEST_VAR=hi"})
	if stdout != "hi" {
		t.Errorf("stdout = %q, want %q", stdout, "hi")
	}
	if code != 2 {
		t.Errorf("exit = %d, want 2 (len of \"hi\")", code)
	}
}

// Missing env routes to `None`, distinguishable from a present-
// but-empty value (which would be `Some("")`).
func TestEnvBuiltinMissingArm(t *testing.T) {
	src := `function main(): i32 {
		match (env("LANG_TEST_DEFINITELY_NOT_SET_XYZ")) {
			Some(v) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	_, _, code := runWithStdinEnv(t, src, "", nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (missing env routes to None)", code)
	}
}

// `exit(code)` short-circuits whatever main was about to return.
// Pairing it with `eprint` is the canonical "fatal error" shape.
func TestExitBuiltinArm(t *testing.T) {
	src := `function main(): i32 {
		eprint("boom");
		exit(7);
		return 0;
	}`
	_, stderr, code := runWithStdinEnv(t, src, "", nil)
	if stderr != "boom\n" {
		t.Errorf("stderr = %q, want %q", stderr, "boom\n")
	}
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

// `write` is `print` minus the newline. Three calls back-to-back
// land on stdout as one continuous run with no separators; the
// final `print` then closes the line.
func TestWriteBuiltinArm(t *testing.T) {
	src := `function main(): i32 {
		write("a");
		write("b");
		print("c");
		return 0;
	}`
	stdout, _, _ := compileAndCaptureStreams(t, src)
	if stdout != "ab" + "c\n" {
		t.Errorf("stdout = %q, want %q", stdout, "abc\n")
	}
}

// `eprint` writes to stderr with a trailing newline. Pairing it
// with `print` on stdout in the same program proves the two
// builtins land on different file descriptors and don't
// interfere with each other.
func TestEprintBuiltinArm(t *testing.T) {
	src := `function main(): i32 {
		print("hi");
		eprint("err");
		return 0;
	}`
	stdout, stderr, _ := compileAndCaptureStreams(t, src)
	if stdout != "hi\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hi\n")
	}
	if stderr != "err\n" {
		t.Errorf("stderr = %q, want %q", stderr, "err\n")
	}
}

func TestForEachOverArray(t *testing.T) {
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			for x in [10, 20, 30] {
				sum = sum + x;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 60 {
		t.Errorf("exit = %d, want 60 (10+20+30)", code)
	}
}

// break and continue inside a foreach body target the surrounding
// loop just like in a hand-written `for`.
func TestForEachBreakContinue(t *testing.T) {
	src := `
		function main(): i32 {
			var sum: i32 = 0;
			for x in [1, 2, 3, 4, 5] {
				if (x == 2) { continue; }
				if (x == 5) { break; }
				sum = sum + x;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 8 {
		t.Errorf("exit = %d, want 8 (1+3+4)", code)
	}
}

func TestArraySumAndMutation(t *testing.T) {
	src := `
		function main(): i32 {
			var a: i32[] = [10, 20, 30, 40];
			a[2] = 100;
			return a[0] + a[1] + a[2] + a[3];
		}`
	_, code := compileAndRun(t, src)
	if code != 170 {
		t.Errorf("exit = %d, want 170 (10+20+100+40)", code)
	}
}

// Sum types end-to-end on arm32: a payload-carrying variant
// is constructed, matched, and the bound payloads flow into
// the result.
func TestEnumMatchPayloadArm(t *testing.T) {
	src := `enum Pair { Two(i32, i32) }
		function main(): i32 {
			var p: Pair = Two(7, 5);
			match (p) {
				Two(a, b) => { return a + b; }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 12 {
		t.Errorf("exit = %d, want 12 (7 + 5)", code)
	}
}

// Multi-variant dispatch on arm32 — each arm is reachable, the
// payload-less Ok constructor and the string-carrying Err
// constructor both work.
func TestEnumMatchDispatchArm(t *testing.T) {
	// Use distinct variant names so this test doesn't collide
	// with the auto-injected `Result[T, E]` (which owns Ok/Err).
	src := `enum Status { Good, Bad(string) }
		function status(): Status { return Bad("boom"); }
		function main(): i32 {
			match (status()) {
				Good => { return 0; },
				Bad(msg) => { return len(msg); }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 4 {
		t.Errorf("exit = %d, want 4 (len of \"boom\")", code)
	}
}

// Generic enum Option end-to-end on arm32 — same scenarios as
// the WASM tests, but compiled and executed under qemu so the
// type-erased lowering is exercised on a non-WASM backend too.
func TestGenericOptionArm(t *testing.T) {
	src := `enum Option[T] { Some(T), None }
		function find(): Option[i32] { return Some(42); }
		function main(): i32 {
			match (find()) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 99;
		}`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// Generic Result[T, E] on arm32 — both type parameters route
// through the heap at runtime. The error arm carries a string
// payload that flows through `len` on extraction.
func TestGenericResultArm(t *testing.T) {
	// Use the auto-injected `Result[T, E]` instead of redeclaring
	// it — Phase 3 makes Result a built-in.
	src := `function check(b: boolean): Result[i32, string] {
			if (b) { return Ok(7); }
			return Err("oops");
		}
		function main(): i32 {
			match (check(false)) {
				Ok(v) => { return v; },
				Err(msg) => { return len(msg); }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 4 {
		t.Errorf("exit = %d, want 4 (len of \"oops\")", code)
	}
}

// Float arithmetic on arm32 lowers through VFPv2: f32 bit
// patterns flow through r0..r3 like ints, and the `OpF*` ops
// `vmov` them into s-registers for the actual instruction. This
// test exercises the full chain — add, sub, mul, div, unary
// negate — against expected exit codes computed by the harness.
func TestArmFloatArithmetic(t *testing.T) {
	src := `function main(): i32 {
		var a: float = 2.5;
		var b: float = 4.0;
		var sum: float = a + b;       // 6.5
		var diff: float = b - a;      // 1.5
		var prod: float = a * b;      // 10.0
		var quot: float = b / a;      // 1.6
		var neg: float = -a;          // -2.5
		if (sum > 6.0) {
			if (diff < 2.0) {
				if (prod >= 9.5) {
					if (quot <= 2.0) {
						if (neg < 0.0) {
							return 7;
						}
					}
				}
			}
		}
		return 0;
	}`
	_, code := compileAndRun(t, src)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (all five float predicates true)", code)
	}
}

// Float comparison ops on arm32 use `vcmp.f32` + `vmrs APSR_nzcv,
// FPSCR` to pull VFP flags into the integer condition register,
// then the standard movCC pattern. This test pinpoints each of
// the four ordered comparisons in isolation.
func TestArmFloatComparisons(t *testing.T) {
	cases := []struct {
		op   string
		want int
	}{
		{"<", 1}, {"<=", 1}, {">", 0}, {">=", 0},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			src := `function main(): i32 {
				var a: float = 1.0;
				var b: float = 2.0;
				if (a ` + tc.op + ` b) { return 1; }
				return 0;
			}`
			_, code := compileAndRun(t, src)
			if code != tc.want {
				t.Errorf("a %s b: exit = %d, want %d", tc.op, code, tc.want)
			}
		})
	}
}

// Generics with float payload on arm32: same shape as
// TestWASMOptionFloatPayload, but exercising the VFP path
// through the IR's OpFStore / OpFLoad helpers used when a
// generic `T` is substituted with `float`.
func TestArmGenericOptionFloatPayload(t *testing.T) {
	src := `function pick(): Option[float] { return Some(3.14); }
		function main(): i32 {
			match (pick()) {
				Some(v) => { if (v > 3.0) { return 1; } return 2; },
				None => { return 0; }
			}
			return -1;
		}`
	_, code := compileAndRun(t, src)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (Some(3.14) > 3.0)", code)
	}
}

// runArmInDir is the arm32 analogue of runWasmInDir. It
// compiles the program, drops it in a temp dir alongside any
// seed files, runs it under qemu-arm with the temp dir as the
// process's cwd (so libc open() resolves relative paths
// against it), and returns stdout, exit code, and the dir for
// post-run inspection.
func runArmInDir(t *testing.T, src string, seed map[string]string) (stdout string, code int, dir string) {
	t.Helper()
	gcc, qemu := tooling(t)

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
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir = t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	cmd := exec.Command(qemu, binPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode(), dir
}

// `read_file` round-trips a string through libc open/read/close
// on the arm32 runtime helper.
func TestReadFileOkArm(t *testing.T) {
	src := `function main(): i32 {
		match (read_file("greeting.txt")) {
			Ok(s) => { write(s); return len(s); },
			Err(_) => { return -1; }
		}
		return -2;
	}`
	stdout, code, _ := runArmInDir(t, src, map[string]string{
		"greeting.txt": "hello, file",
	})
	if stdout != "hello, file" {
		t.Errorf("stdout = %q, want %q", stdout, "hello, file")
	}
	if code != 11 {
		t.Errorf("exit = %d, want 11 (len of \"hello, file\")", code)
	}
}

// Missing files surface as `IoError.NotFound(path)`. The path
// is carried in the variant payload so callers don't need
// secondary context plumbing.
func TestReadFileNotFoundArm(t *testing.T) {
	src := `function main(): i32 {
		match (read_file("does_not_exist.txt")) {
			Ok(_) => { return 0; },
			Err(err) => {
				match (err) {
					NotFound(p) => { write(p); return 1; },
					_ => { return 99; }
				}
			}
		}
		return -1;
	}`
	stdout, code, _ := runArmInDir(t, src, nil)
	if !strings.Contains(stdout, "does_not_exist.txt") {
		t.Errorf("stdout should echo the path; got %q", stdout)
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (NotFound arm)", code)
	}
}

// `write_file` truncates the target and writes the content via
// libc open(O_CREAT|O_TRUNC|O_WRONLY) + write + close.
func TestWriteFileOkArm(t *testing.T) {
	src := `function main(): i32 {
		match (write_file("out.txt", "from arm")) {
			Some(_) => { return 1; },
			None => { return 0; }
		}
		return -1;
	}`
	_, code, dir := runArmInDir(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (None / success)", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "from arm" {
		t.Errorf("got %q, want %q", got, "from arm")
	}
}

// arm32 streaming round-trip — same shape as the WASM
// version but compiled to native arm assembly and run under
// qemu. open_writer + Writer.write + Writer.close +
// open_reader + Reader.read_line + Reader.close.
func TestStreamingRoundtripArm(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("out.txt")) {
			Ok(w) => {
				match (w.write("line 1\n")) { Some(_) => { return 1; }, None => {} }
				match (w.write("line 2\n")) { Some(_) => { return 2; }, None => {} }
				match (w.close()) { Some(_) => { return 3; }, None => {} }
			},
			Err(_) => { return 4; }
		}
		match (open_reader("out.txt")) {
			Ok(r) => {
				match (r.read_line()) { Some(line) => { write(line); }, None => { return 5; } }
				match (r.read_line()) { Some(line) => { write(line); }, None => { return 6; } }
				match (r.read_line()) { Some(_) => { return 7; }, None => {} }
				match (r.close()) { Some(_) => { return 8; }, None => {} }
				return 0;
			},
			Err(_) => { return 9; }
		}
		return -1;
	}`
	stdout, code, _ := runArmInDir(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "line 1\n") || !strings.Contains(stdout, "line 2\n") {
		t.Errorf("stdout missing both lines; got %q", stdout)
	}
}

func TestReaderReadChunkArm(t *testing.T) {
	src := `function main(): i32 {
		match (open_writer("rc.txt")) {
			Ok(w) => {
				match (w.write("hello world")) { Some(_) => { return 1; }, None => {} }
				match (w.close()) { Some(_) => { return 2; }, None => {} }
			},
			Err(_) => { return 3; }
		}
		match (open_reader("rc.txt")) {
			Ok(r) => {
				match (r.read_chunk(5)) { Some(s) => { write(s); write(":"); }, None => { return 4; } }
				match (r.read_chunk(20)) { Some(s) => { write(s); }, None => { return 5; } }
				match (r.read_chunk(20)) { Some(_) => { return 6; }, None => { return 0; } }
			},
			Err(_) => { return 7; }
		}
		return -1;
	}`
	stdout, code, _ := runArmInDir(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "hello: world") {
		t.Errorf("stdout should contain `hello: world`; got %q", stdout)
	}
}
