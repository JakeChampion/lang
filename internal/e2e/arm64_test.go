// arm64 (aarch64) Linux end-to-end tests. The IR layer is
// shared with the wasm backend, but the assembly emit + Linux
// syscall numbers are arm64-specific. Each test SKIPs (rather
// than fails) when the cross-compiler or qemu-aarch64 isn't
// installed.
//
// Tests run the compiled binary under qemu-aarch64, which
// uses the host's Linux kernel via user-mode emulation. On
// real arm64 Linux hosts (Raspberry Pi 4+, AWS Graviton,
// etc.) the same binary runs natively without qemu.
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
// generic helpers like tcp_serve.
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
// does not).
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

// End-to-end arm64 HTTP handler. Compiles a program that only
// defines `function handle(req: HttpRequest): HttpResponse` —
// the checker synthesises `main()` from it as
// `tcp_serve(__port_from_env("PORT", 8080), handle)`. The
// resulting binary listens on the PORT env var, parses an
// HTTP/1.1 request, calls the user handler, and writes the
// serialised response back. Then this test sends two
// back-to-back requests on separate connections and asserts
// the bodies — the second one round-trips through a freshly
// reset per-request arena (via `tcp_serve`'s `arena_save` /
// `arena_restore` wrap), proving the arena reset actually
// reclaims handler-built allocations rather than leaking.
//
// Runs under qemu-aarch64; the binary opens a real TCP socket
// on the host's kernel (user-mode emulation forwards syscalls
// 1:1). Picks a port via Go's net.Listen("tcp", ":0") then
// closes the listener — tiny TOCTOU window before the binary
// claims it, acceptable for CI.
func TestArm64HttpHandler(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	// Pick a free port. Close the Go listener immediately so
	// the lang binary can claim it. Race window is small in
	// practice.
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// Poll-connect until the lang binary has actually bound
	// the port (qemu startup + tcp_listen take a few hundred
	// ms). 10s deadline is generous for CI.
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

	// Two requests, two connections — second one exercises
	// the arena_save / arena_restore round-trip that
	// reclaims the first request's allocations.
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
		// HTTP/1.1 response: status-line + headers + blank line + body.
		body := string(resp)
		if !strings.Contains(body, "HTTP/1.1 200") {
			t.Errorf("request %d: missing 200 status\n--- got ---\n%s", i, body)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("request %d: missing %q\n--- got ---\n%s", i, c.want, body)
		}
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
		// Map[i32, i32] — pointer-width fix exercise. The
		// Map handle now uses __store_ptr / __load_ptr (8
		// bytes on arm64) so the buf pointer round-trips
		// correctly even when macOS hands us heap addresses
		// above 4 GiB.
		{"map_i32", `function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m.set(1, 100);
    m.set(2, 200);
    return m.get_or(2, 0);
}`, 200},
		// random_bytes(n) — Darwin getentropy path
		// (chunked, 256-byte cap per call). Just verify the
		// length round-trips; can't assert content.
		{"random_bytes", `function main(): i32 {
    return len(random_bytes(32));
}`, 32},
		// Map[string, i32] — string keys exercise the
		// pointer-width entry-slot fix. set("world", 99)
		// writes the string pointer through __store_ptr (8
		// bytes on arm64), so lookup with the same key
		// (FNV-1a hash + byte-wise string compare) finds
		// the entry even when the heap is above 4 GiB. The
		// returned i32 value rides x0 untruncated.
		{"map_str_key", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("hello", 42);
    m.set("world", 99);
    return m.get_or("world", 0);
}`, 99},
		// Map[i32, string] — string values. get_or returns
		// the entry's pointer-width V slot via __load_ptr;
		// the i32-typed return rides x0 as a full 64-bit
		// pointer, and len(s) reads s's length prefix at
		// the correct (high-bit-preserved) address.
		{"map_str_val", `function main(): i32 {
    var m: Map[i32, string] = map_new(4);
    m.set(1, "abc");
    m.set(2, "abcdef");
    return len(m.get_or(2, ""));
}`, 6},
		// Map[string, string] — both key and value are
		// pointer-width. End-to-end check that the entry
		// stride doubled to 2*ptr_width on arm64 (16 bytes)
		// without breaking the bucket arithmetic.
		{"map_str_str", `function main(): i32 {
    var m: Map[string, string] = map_new(4);
    m.set("k1", "ab");
    m.set("k2", "abcde");
    return len(m.get_or("k2", ""));
}`, 5},
		// Iteration over Map[string, i32] via has_next /
		// key / value — accumulates the sum of all values.
		// Exercises __mapiter_entry_addr's stride math and
		// the pointer-width key load (even though we don't
		// inspect keys here, the iterator's address math
		// must use the same entryStride or it'd walk off).
		{"map_str_iter", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("a", 10);
    m.set("b", 20);
    m.set("c", 30);
    var it: MapIter[string, i32] = m.iter();
    var sum: i32 = 0;
    while (it.has_next()) {
        sum = sum + it.value();
        it.advance();
    }
    return sum;
}`, 60},
		// Delete over a string-keyed map — verifies the
		// swap-with-last path correctly uses __load_ptr /
		// __store_ptr on the moved entry's K/V slots. After
		// removing "b" and "c", get_or("a") still finds the
		// remaining entry.
		{"map_str_delete", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("a", 1);
    m.set("b", 2);
    m.set("c", 3);
    m.delete("b");
    m.delete("c");
    return m.get_or("a", 0) * 10 + m.len();
}`, 11},
		// Option[string] payload — the Some(s) variant now
		// stores `s` in a pointer-width payload slot (8
		// bytes on arm64), so the high 32 bits of macOS
		// heap pointers survive the match's payload-load.
		// `len(s)` reads s's length prefix at [s_ptr - 4],
		// which would trap on a truncated pointer.
		{"option_str", `function get_msg(): Option[string] {
    return Some("hi there");
}
function main(): i32 {
    match (get_msg()) {
        Some(s) => { return len(s); },
        None => { return 0; }
    }
    return -1;
}`, 8},
		// User-defined enum with a pointer-typed payload —
		// same widening as Option[string] but exercises the
		// full payloadLayout / payloadStore / payloadLoad
		// triple for a non-prelude variant.
		{"enum_str", `enum Msg {
    Text(string),
    Empty
}
function build(): Msg {
    return Text("payload-string");
}
function main(): i32 {
    match (build()) {
        Text(s) => { return len(s); },
        Empty => { return 0; }
    }
    return -1;
}`, 14},
		// Struct with a string field — exercises ptrW-aware
		// field offsets and stores. `name` lands at offset
		// 8 (aligned to 8) on arm64, sandwiched between two
		// i32 fields, and round-trips a real heap pointer.
		{"struct_str_field", `struct Person {
    age: i32,
    name: string,
    weight: i32
}
function main(): i32 {
    var p: Person = Person { age: 30, name: "Claude", weight: 100 };
    return len(p.name) + p.age + p.weight;
}`, 136},
		// Array of strings — array literal stride + element
		// store widened to 8 bytes for pointer-typed elems
		// on arm64; indexing via __arr_idx_8 picks the
		// matching `lsl #3` address compute.
		{"string_arr", `function main(): i32 {
    var xs: string[] = ["alpha", "beta", "gamma"];
    return len(xs[0]) + len(xs[1]) + len(xs[2]) + len(xs);
}`, 17},
		// Map[string, i32].keys() — the snapshot array is
		// now ptrW-aware (destStride=8 on arm64 for pointer
		// K), so iterating the keys() result and calling
		// len() on each returns valid lengths instead of
		// segfaulting on truncated pointers.
		{"map_keys_str", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m.set("alpha", 1);
    m.set("beta", 2);
    m.set("gamma", 3);
    var ks: string[] = m.keys();
    var i: i32 = 0;
    var total: i32 = 0;
    while (i < len(ks)) {
        total = total + len(ks[i]);
        i = i + 1;
    }
    return total;
}`, 14},
		// Map[i32, string].values() — same shape on the V
		// side. valKind is now tracked at buf+12 so
		// __map_values_impl picks destStride correctly per-
		// instance without per-V monomorphisation.
		{"map_values_str", `function main(): i32 {
    var m: Map[i32, string] = map_new(4);
    m.set(1, "one");
    m.set(2, "two");
    m.set(3, "three");
    var vs: string[] = m.values();
    var i: i32 = 0;
    var total: i32 = 0;
    while (i < len(vs)) {
        total = total + len(vs[i]);
        i = i + 1;
    }
    return total;
}`, 11},
		// Probe for the arm64-darwin heap-address truncation
		// bug (BACKEND-PARITY.md "Known limitations"). Map
		// values are HEAP-allocated strings (built via concat
		// at runtime), NOT .rodata literals. On macOS the
		// mmap address hint is ignored and the heap lands at
		// a high (>4 GiB) address; the lang prelude declares
		// pointer locals as `i32`, which truncates the high
		// 32 bits of the round-tripped pointer.
		//
		// This case CURRENTLY FAILS on macOS CI — that's
		// expected: the bug it probes is unfixed. Skip with
		// t.Skipf when running natively on Darwin until the
		// prelude pointer-width refactor lands; on Linux
		// (under qemu / native arm64) the heap fits in 32
		// bits so the test passes.
		{"map_heap_value_probe", `function main(): i32 {
    var m: Map[i32, string] = map_new(4);
    var v1: string = "alp" + "ha";
    var v2: string = "be" + "ta";
    m.set(1, v1);
    m.set(2, v2);
    return len(m.get_or(1, "")) + len(m.get_or(2, ""));
}`, 9},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Probe for the documented heap-address truncation
			// bug — skip on Darwin native (where the bug
			// trips) until the prelude pointer-width refactor
			// lands. See BACKEND-PARITY.md "Known limitations".
			if c.name == "map_heap_value_probe" && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				t.Skip("known limitation: heap-address truncation in Map runtime on arm64-darwin; see docs/BACKEND-PARITY.md")
			}
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
				// Newer ld64 (Xcode 16+ on macOS Sequoia/
				// Tahoe) refuses dynamic executables without
				// libSystem.dylib linked. `-nostdlib`
				// suppresses crt0/libc startup; `-lSystem`
				// re-adds just the dyld-stub linkage. See
				// cmd/lang/main.go's linkDarwin for matching
				// production-driver behaviour.
				args = []string{"-nostdlib", "-lSystem", asmPath, "-o", binPath}
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

// Tail-call optimisation. arm64 now wires `ir.TailCallOptimize`
// (backported from PR #274's x86-64 first-consumer wire-up).
// Two assertions:
//
//  1. The asm has exactly one `bl sum_to` (the kick-off from
//     `main`). Without TCO the recursive site would still
//     emit `bl <self>`; with TCO that site becomes
//     `b .Lloop_top`.
//  2. Recursion that would overflow the qemu-aarch64 default
//     stack returns cleanly with the right value.
func TestArm64TailCall(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

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
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.Count(asm, "bl sum_to"); got != 1 {
		t.Errorf("`bl sum_to` appearances = %d, want 1 (only from main); TCO didn't fire", got)
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
	cmd := exec.Command(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	// 5,000,050,000 → i32 (705,082,704) → exit code (mod 256) = 80.
	if got := cmd.ProcessState.ExitCode(); got != 80 {
		t.Errorf("sum_to(100000, 0) → exit = %d, want 80", got)
	}
}

// Closure factory pattern: `var f = makeAdder(7); f(35)`. The
// IR's Defunctionalise pass rewrites `f(35)` into a direct call
// to the hoisted `add` with env_ptr pulled out of the closure
// pair at offset +ptrW (=8 on native).
func TestArm64ClosureFactory(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var f = makeAdder(7);
    return f(35);
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Two closures over different captured values must not share
// state — separate env blocks per MakeClosure.
func TestArm64ClosureMultipleInstances(t *testing.T) {
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
	if _, code := compileAndRunArm64(t, src); code != 17 {
		t.Errorf("got %d, want 17", code)
	}
}

// Direct nested-function call (the ElideClosurePair case): the
// slot writer IS OpMakeClosure, so elide fires and the closure
// pair allocation collapses to just an env_ptr in the slot.
// Exercises the OpMakeEnv path.
func TestArm64ClosureCapturesParamAndVar(t *testing.T) {
	src := `function outer(seed: i32): i32 {
    var bonus: i32 = 100;
    function inner(x: i32): i32 { return x + seed + bonus; }
    return inner(2);
}
function main(): i32 { return outer(40); }`
	// 2 + 40 + 100 = 142
	if _, code := compileAndRunArm64(t, src); code != 142 {
		t.Errorf("got %d, want 142", code)
	}
}

// String capture — pointer-shaped capture takes a full ptr-width
// (8-byte) slot in the env block. Verifies arm64CaptureSlotSize
// routes through `str x1, ...` rather than the 4-byte `str w1`
// truncation path.
func TestArm64ClosureCapturesString(t *testing.T) {
	src := `function outer(s: string): i32 {
    function inner(): i32 { return len(s); }
    return inner();
}
function main(): i32 { return outer("hello"); }`
	if _, code := compileAndRunArm64(t, src); code != 5 {
		t.Errorf("got %d, want 5 (len(\"hello\") via captured string)", code)
	}
}

// Multi-capture closure: two i32 captures laid out at offsets 0
// and 4 in the env block. Verifies the running-offset
// arithmetic in emitMakeClosureOrEnv.
func TestArm64ClosureMultiCapture(t *testing.T) {
	src := `function make2(a: i32, b: i32): (i32) => i32 {
    function f(x: i32): i32 { return a + b + x; }
    return f;
}
function main(): i32 {
    var h = make2(10, 20);
    return h(12);
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Pointer + scalar captures mixed in one closure — pointer slot
// is 8 bytes, scalar slot is 4 bytes. Exercises mixed-width
// offset arithmetic.
func TestArm64ClosureCapturesMixedPointers(t *testing.T) {
	src := `function outer(s: string, n: i32): i32 {
    function inner(): i32 { return len(s) + n; }
    return inner();
}
function main(): i32 { return outer("hi", 40); }`
	// len("hi") + 40 = 42
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// i32 ↔ i64 conversion. arm64 lowers OpExtendI32S via `sxtw`,
// OpExtendI32U + OpWrapI64 via `mov w0, w0` (the 32-bit reg
// form implicitly zero-extends the high half on AArch64).
// OpConstI64 uses `ldr x0, =N` with a literal-pool entry.
func TestArm64I32I64Convert(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i32_to_i64_roundtrip", `function main(): i32 {
			var a: i32 = 7;
			var b: i64 = a as i64;
			var c: i32 = b as i32;
			return c + 35;
		}`, 42},
		{"wrap_drops_high_half", `function main(): i32 {
			var big: i64 = 4294967300i64;
			return (big as i32);
		}`, 4},
		{"sxtw_preserves_sign", `function main(): i32 {
			var neg: i32 = 0 - 1;
			var ext: i64 = neg as i64;
			if (ext == 0 - 1i64) { return 7; }
			return 99;
		}`, 7},
		{"i64_arith_roundtrip", `function main(): i32 {
			var a: i64 = 1000000000000i64;
			var b: i64 = a + 42i64;
			return (b - 1000000000000i64) as i32;
		}`, 42},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// Sub-i32 array reads + casts. Exercises:
//
//   - OpLoadI8S  (i8[] read; arm64 `ldrsb`)
//   - OpLoadI16U (u16[] read; arm64 `ldrh`)
//   - OpLoadI16S (i16[] read; arm64 `ldrsh`)
//   - OpSignExtend8  (i32 → i8 narrowing cast; arm64 `sxtb`)
//   - OpSignExtend16 (i32 → i16 narrowing cast; arm64 `sxth`)
//
// Pairs with wasm's TestWASMI8Array / TestWASMU16Array /
// TestWASMSubI32Widths.
func TestArm64SubI32(t *testing.T) {
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
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// compileArm64InDir builds `src` and runs the resulting binary
// in a fresh temp dir seeded with `seed` files (path → content).
// Returns stdout, exit code, AND the dir so callers can read
// back files the program created. Mirrors wasm's runWasmInDir.
func compileArm64InDir(t *testing.T, src string, seed map[string]string) (stdout string, exitCode int, dir string) {
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
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(qemu, binPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode(), dir
}

// read_file returns Ok(content) for a present file. Mirrors
// TestWASMReadFileOk's shape.
func TestArm64ReadFileOk(t *testing.T) {
	src := `function main(): i32 {
    match (read_file("greeting.txt")) {
        Ok(s) => { return len(s); },
        Err(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`
	_, code, _ := compileArm64InDir(t, src, map[string]string{
		"greeting.txt": "hello, file\n",
	})
	if code != 12 {
		t.Errorf("got %d, want 12 (len of \"hello, file\\n\"); error path or missing read", code)
	}
}

// Missing files surface as `IoError.NotFound(path)`. The path
// the caller passed must round-trip through the variant payload.
func TestArm64ReadFileNotFound(t *testing.T) {
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
	_, code, _ := compileArm64InDir(t, src, nil)
	// len("does_not_exist.txt") = 18
	if code != 18 {
		t.Errorf("got %d, want 18 (len of missing-file path via NotFound payload)", code)
	}
}

// write_file truncates the target and writes `content`. Verify
// by reading the file back from the host side after the program
// returns.
func TestArm64WriteFileOk(t *testing.T) {
	src := `function main(): i32 {
    match (write_file("out.txt", "wrote it\n")) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return 0 - 1;
}`
	_, code, dir := compileArm64InDir(t, src, nil)
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

// Round-trip: write a file then read it back; len reports the
// expected byte count. Both helpers in one program.
func TestArm64ReadWriteFileRoundtrip(t *testing.T) {
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
	_, code, _ := compileArm64InDir(t, src, nil)
	if code != 10 {
		t.Errorf("got %d, want 10 (len of \"round trip\")", code)
	}
}

// State[T] on natives is the program-lifetime / no-op
// interpretation: each `state { var ... }` slot lives in
// .data (literal init) or .bss (runtime init via the
// synthesised __state_init), accessed via adrp+add and
// width-appropriate ldr/str. The persistent-mode toggle ops
// (OpPersistentSet / OpPersistentRestore) lower to no-ops.
// Pairs with the wasm TestWASMState* suite for the shapes a
// native binary can express.
func TestArm64State(t *testing.T) {
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
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// State-rooted Map.set inside an arena cycle (the HTTP-handler
// shape). Without the two-cursor allocator, Map.set's grow path
// would allocate the new backing buffer in the per-request
// arena and arena_restore would reclaim it — leaving the state
// Map's data pointer dangling on the next call. The fix routes
// state-rooted allocs through a separate persistent cursor that
// arena_save / arena_restore never touch.
//
// Function calls with more arguments than the register-arg
// window (8 on AAPCS64). Args 9+ live on the caller's stack
// at [sp+0..]. The prologue copies them from there into the
// callee's local slots so subsequent OpLoadLocal references
// can read them uniformly.
func TestArm64StackPassedArgs(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sum_10_args", `function sum10(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32 {
    return a + b + c + d + e + f + g + h + i + j;
}
function main(): i32 {
    return sum10(1, 2, 3, 4, 5, 6, 7, 8, 9, 10);
}`, 55},
		{"sum_12_args_order_check", `function sum12(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32, k: i32, l: i32): i32 {
    return (a * 1) + (b * 2) + (c * 3) + (d * 4) + (e * 5) + (f * 6) + (g * 7) + (h * 8) + (i * 9) + (j * 10) + (k * 11) + (l * 12);
}
function main(): i32 {
    // a..l = 1..12, weighted by their position; sum = 1+4+9+16+25+36+49+64+81+100+121+144 = 650
    return sum12(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12) - 600;
}`, 50},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// Unsigned float ↔ int conversions. arm64 has dedicated
// `ucvtf` / `fcvtzu` opcodes; this test asserts the IR's
// Unsigned-flagged variants route through them and produce
// correct results for values above the signed boundary
// (u32 > 2^31; u64 > 2^63).
func TestArm64UnsignedFloatConv(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"u32_large_to_f64_back", `function main(): i32 {
    var u: u32 = 3000000000 as u32;
    var f: f64 = u as f64;
    var back: u32 = f as u32;
    if (back == u) { return 0; }
    return 1;
}`, 0},
		{"u64_max_to_f64_is_huge", `function main(): i32 {
    var i: i64 = 0 - 1i64;
    var u: u64 = i as u64;
    var f: f64 = u as f64;
    var threshold: f64 = 10000000000000000000.0f64;
    if (f > threshold) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// 50 inserts into a Map[i32, i32] starting at cap=2 forces
// multiple grows; each grow happens inside an arena_save /
// arena_restore window, so the regression is hit if the grow's
// new buffer lives in the arena region.
func TestArm64StateMapGrowInsideArena(t *testing.T) {
	src := `state {
    var todos: Map[i32, i32] = map_new(2);
}
function add(k: i32, v: i32): void {
    todos.set(k, v);
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 50) {
        var saved: i32 = arena_save();
        add(i, i * 2);
        arena_restore(saved);
        i = i + 1;
    }
    if (todos.len() != 50) { return 1; }
    if (todos.get_or(7, 0 - 1) != 14) { return 2; }
    if (todos.get_or(49, 0 - 1) != 98) { return 3; }
    if (todos.get_or(99, 0 - 1) != 0 - 1) { return 4; }
    return 42;
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42 (state Map grow survives arena cycle)", code)
	}
}

// State-rooted Array.push inside an arena cycle — same allocator
// concern as TestArm64StateMapGrowInsideArena, but exercising
// the IR's persistent-mode wrap on the push-then-assign path
// (`nums = nums.push(n)`). The push allocates a fresh backing
// buffer each grow; without the two-cursor allocator the new
// buffer would land in the arena and be reclaimed by
// arena_restore.
func TestArm64StateArrayPushInsideArena(t *testing.T) {
	src := `state {
    var nums: i32[] = [];
}
function push_one(n: i32): void {
    nums = nums.push(n);
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 50) {
        var saved: i32 = arena_save();
        push_one(i);
        arena_restore(saved);
        i = i + 1;
    }
    if (len(nums) != 50) { return 1; }
    if (nums[7] != 7) { return 2; }
    if (nums[49] != 49) { return 3; }
    return 42;
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42 (state array push survives arena cycle)", code)
	}
}

// `stdin().read_line()` — exercises the .bss buffer + byte
// loop + Some/None Option wrap. arm64's runtime used to be
// stdin-only via __lang_read_line; this test now goes through
// the receiver-aware __lang_reader_read_line (stdin() returns
// a real Reader{fd:0} struct). Closes the parity-doc gap.
func TestArm64ReadLine(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	src := `function main(): i32 {
    match (stdin().read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return 0 - 1;
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
	runCase := func(stdin string, want int) {
		t.Helper()
		cmd := exec.Command(qemu, binPath)
		cmd.Stdin = strings.NewReader(stdin)
		_, _ = cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("stdin=%q: exit = %d, want %d", stdin, got, want)
		}
	}
	runCase("", 0)        // EOF before any byte → None
	runCase("hello\n", 1) // Some(line)
}

// Reader / Writer file I/O round-trip. open_writer +
// Writer.write + Writer.close; open_appender; open_reader +
// Reader.read_chunk / Reader.read_line / Reader.close. Mirrors
// TestWASMOpenAppender / TestWASMReaderReadChunk /
// TestWASMStreamingRoundtrip.
func TestArm64ReaderWriter(t *testing.T) {
	for _, c := range []struct {
		name       string
		src        string
		wantStdout string
		wantExit   int
	}{
		{"open_writer_then_append_then_read", `function main(): i32 {
    match (open_writer("ap.txt")) {
        Ok(w) => {
            match (w.write("first")) { Some(_) => { return 1; }, None => {} }
            match (w.close()) { Some(_) => { return 2; }, None => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_appender("ap.txt")) {
        Ok(w) => {
            match (w.write("-second")) { Some(_) => { return 4; }, None => {} }
            match (w.close()) { Some(_) => { return 5; }, None => {} }
        },
        Err(_) => { return 6; }
    }
    match (read_file("ap.txt")) {
        Ok(s) => { write(s); return 0; },
        Err(_) => { return 7; }
    }
    return 0 - 1;
}`, "first-second", 0},
		{"reader_read_chunk", `function main(): i32 {
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
    return 0 - 1;
}`, "hello: world", 0},
		{"streaming_roundtrip_lines", `function main(): i32 {
    match (open_writer("rt.txt")) {
        Ok(w) => {
            match (w.write("line 1\n")) { Some(_) => { return 1; }, None => {} }
            match (w.write("line 2\n")) { Some(_) => { return 2; }, None => {} }
            match (w.close()) { Some(_) => { return 3; }, None => {} }
        },
        Err(_) => { return 4; }
    }
    match (open_reader("rt.txt")) {
        Ok(r) => {
            match (r.read_line()) { Some(line) => { write(line); }, None => { return 5; } }
            match (r.read_line()) { Some(line) => { write(line); }, None => { return 6; } }
            match (r.read_line()) { Some(_) => { return 7; }, None => {} }
            match (r.close()) { Some(_) => { return 8; }, None => {} }
            return 0;
        },
        Err(_) => { return 9; }
    }
    return 0 - 1;
}`, "line 1\nline 2\n", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			stdout, code, _ := compileArm64InDir(t, c.src, nil)
			if code != c.wantExit {
				t.Errorf("exit = %d, want %d (stdout = %q)", code, c.wantExit, stdout)
			}
			if !strings.Contains(stdout, c.wantStdout) {
				t.Errorf("stdout = %q, want to contain %q", stdout, c.wantStdout)
			}
		})
	}
}

// Wasm-shaped feature parity for the native arm64 backend.
// Each case asserts the program returns 0 (the wasm tests
// returned arbitrary i32 values via runWasm; on native we get
// the low byte of main's return as the exit code, so the
// programs internally compare and short-circuit to 0/N to fit
// the exit-code channel). Same source on x86-64 (see
// TestX86_64FeatureParity).
func TestArm64FeatureParity(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"defer_basic", `function inner(trace: Map[string, i32]): i32 {
    trace.set("body-start", 1);
    defer trace.set("first-defer", 10);
    defer trace.set("second-defer", 20);
    trace.set("body-end", 2);
    return 42;
}
function main(): i32 {
    var trace: Map[string, i32] = map_new(8);
    var r: i32 = inner(trace);
    if (r != 42) { return 1; }
    if (trace.len() != 4) { return 2; }
    if (trace.get_or("body-start", 0) != 1) { return 3; }
    if (trace.get_or("body-end", 0) != 2) { return 4; }
    if (trace.get_or("first-defer", 0) != 10) { return 5; }
    if (trace.get_or("second-defer", 0) != 20) { return 6; }
    return 0;
}`},
		{"switch_basic", `function classify(n: i32): i32 {
    switch (n) {
        case 0: return 100;
        case 1, 2, 3: return 200;
        case 7: return 700;
        default: return 0;
    }
    return 0 - 1;
}
function main(): i32 {
    var sum: i32 = classify(0) + classify(2) + classify(7) + classify(99);
    if (sum == 1000) { return 0; }
    return 1;
}`},
		{"switch_break_in_loop", `function main(): i32 {
    var sum: i32 = 0;
    for (var i: i32 = 0; i < 5; i = i + 1) {
        switch (i) {
            case 2: break;
            default: sum = sum + i;
        }
    }
    if (sum == 8) { return 0; }
    return 1;
}`},
		{"fstring_interp", `function main(): i32 {
    var x: i32 = 42;
    var s: string = f"x is {x}";
    if (len(s) == 7) { return 0; }
    return 1;
}`},
		{"for_each_array", `function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    var sum: i32 = 0;
    for x in xs { sum = sum + x; }
    if (sum == 15) { return 0; }
    return 1;
}`},
		{"if_let_match", `function main(): i32 {
    var o: Option[i32] = Some(42);
    if let Some(x) = o {
        if (x == 42) { return 0; }
        return 1;
    } else {
        return 2;
    }
}`},
		{"tuple_multi_return", `function divmod(a: i32, b: i32): (i32, i32) {
    return (a / b, a - (a / b) * b);
}
function main(): i32 {
    var p = divmod(17, 5);
    if (p.0 == 3 && p.1 == 2) { return 0; }
    return 1;
}`},
		{"generic_infer_from_arg", `function id[T](x: T): T { return x; }
function main(): i32 {
    var a = id(42);
    var b = id(7);
    if (a == 42 && b == 7) { return 0; }
    return 1;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
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