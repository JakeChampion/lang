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
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

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
		{`function main(): i32 { return ("hello").len(); }`, 5},
		{`function main(): i32 { return ("hello, world").len(); }`, 12},
		{`function main(): i32 { return ("").len(); }`, 0},
		{`function main(): i32 { return ("a").len() + ("bb").len() + ("ccc").len(); }`, 6},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// String concat via the runtime `__fern_strcat` helper.
// Exercises the alloc + memcpy + length-prefix path
// end-to-end: each `+` lowers to `OpCallDirect __fern_strcat`,
// which mmaps the heap on first call, copies both operands
// in, and returns a fresh data pointer.
// string.repeat_char — a std/string FREE function, reachable now that
// the parser accepts the keyword-named `string` module qualifier
// (docs/POST-PRELUDE-CLEANUP.md item 4). Pins parse + check + codegen +
// runtime end-to-end.
// Import aliases run end-to-end: `import "std/string" as s;` lets a
// keyword-basename module's free function be reached via the alias
// qualifier, alongside an aliased receiver-method module.
func TestX86_64ImportAlias(t *testing.T) {
	src := `
import "std/string" as s;
import "std/i32" as nums;
function main(): i32 {
    if (s.repeat_char(120, 3) != "xxx") { return 1; }
    if ((0 - 5).abs() != 5) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("import alias program: got exit %d, want 0", code)
	}
}

func TestX86_64StringRepeatChar(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    if (string.repeat_char(120, 4) != "xxxx") { return 1; }
    if (string.repeat_char(45, 3) != "---") { return 2; }
    if (string.repeat_char(120, 0) != "") { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("string.repeat_char: got exit %d, want 0", code)
	}
}

func TestX86_64StringConcat(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { return ("hello, " + "world!").len(); }`, 13},
		{`function main(): i32 {
    var a: string = "foo";
    var b: string = "barbaz";
    return (a + b).len();
}`, 9},
		// Triple-concat — each `+` is left-associative so this
		// flexes the strcat helper twice on the same arena.
		{`function main(): i32 { return ("aa" + "bb" + "cc").len(); }`, 6},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// `type X = A | B | C;` unions on x86-64 — third-backend
// cross-check for the union-types desugaring. See the arm64
// + wasm counterparts for the same source.
func TestX86_64Unions(t *testing.T) {
	src := `struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }

type Expr = Add | Mul | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var lhs: Expr = Add(Add { l: 2, r: 3 });
    var rhs: Expr = Lit(Lit { v: 4 });
    var prod: Expr = Mul(Mul { l: eval(lhs), r: eval(rhs) });
    return eval(prod);
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 20 {
		t.Errorf("got %d, want 20 ((2+3)*4)", code)
	}
}

// Implicit struct → union wrap on x86-64. Third-backend cross-
// check for the wrap-and-re-check pass. See arm64 + wasm
// counterparts for the same source.
func TestX86_64UnionImplicitWrap(t *testing.T) {
	src := `struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }

type Expr = Add | Mul | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
    }
}

function mk_add(l: i32, r: i32): Expr {
    return Add { l: l, r: r };
}

function main(): i32 {
    var a: Expr = Add { l: 2, r: 3 };
    var sum: i32 = eval(Lit { v: 5 });
    a = Mul { l: 2, r: sum };
    var built: Expr = mk_add(1, 2);
    return eval(a) + eval(built) + sum;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 18 {
		t.Errorf("got %d, want 18", code)
	}
}

// `s.lines()` on x86-64 — verifies the prelude function emits
// identical results on the third backend, picking up the same
// per-byte index + slice paths as wasm / arm64.
func TestX86_64StringLines(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var lf: string[] = "a\nb\nc".lines();
    if (lf.len() != 3) { return 1; }
    if (lf[0] != "a") { return 2; }
    if (lf[1] != "b") { return 3; }
    if (lf[2] != "c") { return 4; }

    var crlf: string[] = "a\r\nb\r\nc".lines();
    if (crlf.len() != 3) { return 5; }
    if (crlf[0] != "a") { return 6; }
    if (crlf[2] != "c") { return 7; }

    var trail: string[] = "a\nb\n".lines();
    if (trail.len() != 2) { return 8; }

    if (("".lines()).len() != 0) { return 9; }

    var solo: string[] = "\n".lines();
    if (solo.len() != 1) { return 10; }
    if (solo[0] != "") { return 11; }

    return 0;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (string lines)", code)
	}
}
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
    return xs.len();
}`, 5},
		{`function sum(xs: i32[]): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) {
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
    return p.age + p.name.len();
}`, 31},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

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

// Cheap f64 math intrinsics — abs/sqrt/floor/ceil/trunc lower to
// SSE one-liners; round is round-half-away-from-zero (matching the
// interpreter's math.Round and arm64's frinta), implemented via the
// trunc + exact-frac sequence. Mirrors TestArm64NativeBackendRunsUnderQemu's
// f64 cases and adds the rounding edge cases x86's roundsd can't do
// directly (ties-away on negatives, and the x+0.5 representability
// trap the naive formula falls into).
func TestX86_64FloatIntrinsics(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"abs", "function main(): i32 { return __abs_f64(0.0 - 42.0) as i32; }", 42},
		{"sqrt", "function main(): i32 { return __sqrt_f64(1764.0) as i32; }", 42},
		{"floor", "function main(): i32 { return __floor_f64(42.9) as i32; }", 42},
		{"ceil", "function main(): i32 { return __ceil_f64(41.1) as i32; }", 42},
		{"trunc", "function main(): i32 { return __trunc_f64(42.9) as i32; }", 42},
		// round: ties away from zero.
		{"round_tie_up", "function main(): i32 { return __round_f64(41.5) as i32; }", 42},
		{"round_down", "function main(): i32 { return __round_f64(42.4) as i32; }", 42},
		{"round_25", "function main(): i32 { return __round_f64(2.5) as i32; }", 3},
		// Negative ties away: round(-2.5) == -3, +45 == 42.
		{"round_neg_tie", "function main(): i32 { return (__round_f64(0.0 - 2.5) as i32) + 45; }", 42},
		// The largest double below 0.5 must round to 0, not 1 — the
		// naive trunc(x+0.5) gets this wrong because x+0.5 rounds up
		// to 1.0. +42 == 42 proves we return 0.
		{"round_below_half", "function main(): i32 { return (__round_f64(0.49999999999999994) as i32) + 42; }", 42},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, code := compileAndRunX86_64(t, c.src)
			if code != c.want {
				t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
			}
		})
	}
}

// f64 transcendentals via the x87 FPU (sin/cos/exp/log/pow) — no
// libm. Tolerance comparisons, matching the self-hosted compiler's
// contract (these are approximations, not bit-exact with the
// interpreter's Go math, but well within a few ulp).
func TestX86_64Transcendentals(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sin_0", "function main(): i32 { return __sin_f64(0.0) as i32; }", 0},
		{"cos_0", "function main(): i32 { return __cos_f64(0.0) as i32; }", 1},
		{"sin_halfpi", "function main(): i32 { var r: f64 = __sin_f64(1.5707963267948966); if (r > 0.999 && r < 1.001) { return 7; } return 0; }", 7},
		{"cos_pi", "function main(): i32 { var r: f64 = __cos_f64(3.141592653589793); if (r > 0.0 - 1.001 && r < 0.0 - 0.999) { return 7; } return 0; }", 7},
		{"exp_0", "function main(): i32 { return __exp_f64(0.0) as i32; }", 1},
		{"exp_2", "function main(): i32 { return __exp_f64(2.0) as i32; }", 7},
		{"exp_e", "function main(): i32 { var r: f64 = __exp_f64(1.0); if (r > 2.71 && r < 2.72) { return 7; } return 0; }", 7},
		{"log_e", "function main(): i32 { var r: f64 = __log_f64(2.718281828459045); if (r > 0.999 && r < 1.001) { return 7; } return 0; }", 7},
		{"log_10", "function main(): i32 { return __log_f64(10.0) as i32; }", 2},
		{"exp_log_roundtrip", "function main(): i32 { var r: f64 = __log_f64(__exp_f64(3.0)); if (r > 2.999 && r < 3.001) { return 7; } return 0; }", 7},
		{"pow_int", "function main(): i32 { return __pow_f64(2.0, 5.0) as i32; }", 32},
		{"pow_3_2", "function main(): i32 { return __pow_f64(3.0, 2.0) as i32; }", 9},
		{"pow_sqrt", "function main(): i32 { var r: f64 = __pow_f64(2.0, 0.5); if (r > 1.41 && r < 1.42) { return 7; } return 0; }", 7},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, code := compileAndRunX86_64(t, c.src)
			if code != c.want {
				t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
			}
		})
	}
}

// End-to-end x86-64 HTTP handler. Same shape as
// `TestArm64HttpHandler` — compiles a tiny `handle` program
// (no manual main; the checker synthesises one calling
// `tcp_serve(__port_from_env("PORT", 8080), handle)`),
// spawns the resulting binary on a Go-picked free port,
// sends two requests on separate connections, asserts both
// bodies round-trip. The second request validates that the
// first request's allocations are reclaimed (by reference
// counting) inside `tcp_serve` — a leak there would either
// OOM or scramble state between requests; both pass cleanly.
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

	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("method=" + req.method + " path=" + req.path + " body-len=" + req.body_len().to_string());
}`

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// modload (not bare parser.Parse) so the handler's std/http +
	// std/tcp imports resolve under no-prelude.
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
	// Monomorphise generic functions before codegen — the production
	// driver (cmd/fern) always runs this, and x86_64.Emit documents that
	// it expects a checked + monomorphised program. This harness was
	// missing the pass (the arm64 sibling compileAndRunArm64 already runs
	// it). Feeding Emit an un-monomorphised program leaves generic
	// instantiations unspecialised; that latent gap only surfaced as a
	// wrong differential result once a heap-layout shift (the core/int
	// to_string rewrite) perturbed it into view. Mirrors compileAndRunArm64.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

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
// `now_unix_ms()` on the x86_64 backend lowers to a
// `clock_gettime(CLOCK_REALTIME, &ts)` syscall + a
// `tv_sec * 1000 + tv_nsec / 1_000_000` reduction. Asserts
// the returned ms value is in a plausible range — past a
// sentinel epoch (2023) and before the i64 limit. Closes
// docs/STDLIB-DESIGN-RESEARCH.md Rec §4 Phase 2.x on this
// backend.
func TestX86_64InstantNow(t *testing.T) {
	_, code := compileAndRunX86_64(t, `
import "std/time";
function main(): i32 {
    var ts: Instant = time.instant_now();
    if (ts.sec < (1700000000 as i64)) { return 1; }
    if (ts.sec > (253402300800 as i64)) { return 2; }
    return 0;
}`)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

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
// `string[]` materialised by `__fern_args()`, populated
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
    while (i < a.len()) {
        print(a[i]);
        i = i + 1;
    }
    return a.len();
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
	if err := constfold.Fold(prog, nil); err != nil {
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
    return s.len();
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

// random_i32() — single CSPRNG i32 via a 4-byte getrandom(2)
// read. Cross-backend companion to the interp / arm64 / wasm
// random_i32 paths (issue #2747). We can't assert a specific
// value, so the program folds many draws into a "saw a non-zero
// AND saw two differing draws" signal — exit 7 means the source
// is live and varying, exit 0/1 flags a stuck generator.
func TestX86_64RandomI32(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function main(): i32 {
    var a: i32 = random_i32();
    var b: i32 = random_i32();
    if (a == 0) { return 0; }
    if (a == b) { return 1; }
    return 7;
}`)
	if code != 7 {
		t.Errorf("random_i32: exit = %d, want 7 (0=stuck-zero, 1=non-varying)", code)
	}
}

// s.as_bytes() — non-copying (data, len) → slice<u8> view.
// Was `undefined label "__method_string_as_bytes"` on x86-64
// before #2747. Verifies the slice length matches the source
// string and that indexing the view reads back the original
// bytes. Covers both the SSO inline form ("ABC", ≤7 bytes) and
// the heap form ("ABCDEFGHIJ", >7 bytes).
func TestX86_64StringAsBytes(t *testing.T) {
	// Inline string: 'A'+'B'+'C' = 65+66+67 = 198.
	if _, code := compileAndRunX86_64(t, `function main(): i32 {
    var b = "ABC".as_bytes();
    return b.len() + (b[0] as i32) + (b[1] as i32) + (b[2] as i32);
}`); code != 201 {
		t.Errorf("inline as_bytes: exit = %d, want 201 (3 + 65+66+67)", code)
	}
	// Heap string: len 10 + last byte 'J' (74) = 84.
	if _, code := compileAndRunX86_64(t, `function main(): i32 {
    var b = "ABCDEFGHIJ".as_bytes();
    return b.len() + (b[9] as i32);
}`); code != 84 {
		t.Errorf("heap as_bytes: exit = %d, want 84 (10 + 'J')", code)
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
	if err := constfold.Fold(prog, nil); err != nil {
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
	runCase("", 0)        // EOF before any byte → None
	runCase("hello\n", 1) // Some(line)
}

// Bare `read_line()` builtin — the stdin-only path through
// __fern_read_line (distinct from stdin().read_line()'s
// receiver-aware __fern_reader_read_line). Same .bss buffer +
// byte loop + Some/None wrap.
func TestX86_64ReadLineBuiltin(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	src := `function main(): i32 {
    match (read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return -1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
	runCase("", 0)        // EOF before any byte → None
	runCase("hello\n", 1) // Some(line)
}

// Closure factory pattern: `var f = makeAdder(7); f(35)`. The
// IR's Defunctionalise pass rewrites `f(35)` into a direct call
// to the hoisted `add` with env_ptr pulled out of the closure
// pair at offset +ptrW (=8 on native; was hardcoded to 4 for
// wasm — see Defunctionalise's pairEnvOffset parameter). The
// pair allocation itself can't elide here because the slot's
// writer is OpCallDirect makeAdder, not a direct OpMakeClosure.
// `var f = nested_fn; f()` crashes with SIGSEGV if
// defunctionalize detects only the directly-preceded
// OpMakeClosure / OpCallDirect-returning-closure source. Going
// through an intermediate variable (or a chain of them) kept
// the OpCallIndirect path, which at the backend would `call r11`
// where r11 = closure pair pointer — jumping into pair data,
// not into the fn pointer that the pair contained. The fix
// makes Phase 1 a fixed-point loop that chases OpLoadLocal
// through known-monomorphic slots.
// Chained-alias no-capture closures must not heap-allocate.
// The elide-closure-pair pass rewrites OpMakeClosure → OpMakeEnv
// even when the closure value flows through an intermediate
// `var f = nested_fn` slot. Verify at runtime that the chained
// alias still returns the right value (the no-allocation
// property itself is covered by the elide-closure-pair IR tests).
func TestX86_64ClosureChainNoAlloc(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function main(): i32 {
    function answer(): i32 { return 7; }
    var f = answer;
    var x: i32 = f();
    return x;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (chained no-capture alias returns 7)", code)
	}
}

// Native `use`-callback ABI: function values flowing through a
// FUNCTION PARAMETER (rather than through a local slot the
// defunctionalise pass can analyse) used to segfault — the
// OpCallIndirect handler emitted `call r11` on a closure-pair
// pointer, jumping into pair data instead of dereferencing
// fn_ptr from [pair + 0]. This PR uniforms the function-value
// ABI: OpConstFunc emits a static .rodata closure-pair cell
// `{fn_ptr, 0}`, and OpCallIndirect derefs every callee through
// the same pair shape (loading env from [pair + 8] into the
// next-arg-register past the user args).
// Result[T, E] pair-form: `Result[i32, i32]`-returning functions
// whose body is only `Ok(EXPR)` / `Err(EXPR)` literals lower to
// the (tag, payload) pair-form ABI just like `Option[i32]`.
// match on the result skips the heap-box rebox AND the heap-
// load tag dispatch — zero alloc end-to-end.
func TestX86_64ResultPairForm(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Ok path", `function divide(a: i32, b: i32): Result[i32, i32] {
    if (b == 0) { return Err(1); }
    return Ok(a / b);
}
function main(): i32 {
    match (divide(10, 2)) {
        Ok(v)  => { return v; },
        Err(_) => { return 99; }
    }
}`, 5},
		{"Err path", `function divide(a: i32, b: i32): Result[i32, i32] {
    if (b == 0) { return Err(7); }
    return Ok(a / b);
}
function main(): i32 {
    match (divide(10, 0)) {
        Ok(_)  => { return 99; },
        Err(e) => { return e; }
    }
}`, 7},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

// Pointer-shaped payloads (string here) now go through
// pair-form on natives too — `OpMakeSomeI32 / OpMakeNoneI32`
// emit a 16-byte heap box with the 8-byte string pointer at
// offset 8 (matching `payloadLayout(Option[string])`'s native
// alignment), and `OpCallDirectPair` at the consumer side
// reads 8 bytes from `[box+8]`. The round-trip is observable
// via `len(payload)` after a match.
func TestX86_64PointerPayloadPairForm(t *testing.T) {
	src := `function pick(b: boolean): Option[string] {
    if (b) { return Some("hello world"); }
    return None;
}
function main(): i32 {
    match (pick(true)) {
        Some(s) => { return s.len(); },
        None    => { return -1; }
    }
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 11 {
		t.Errorf("exit = %d, want 11 (len(\"hello world\"))", code)
	}
}

func TestX86_64UseCallback(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"use with no captures", `function tryThing(cb: (i32) => i32): i32 {
    return cb(42);
}
function main(): i32 {
    use n <- tryThing();
    return n;
}`, 42},
		{"use with capture", `function each(items: i32[], cb: (i32) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    var n: i32 = 10;
    function addN(x: i32): i32 { return x + n; }
    return each([5], addN);
}`, 15},
		{"top-level fn passed as callback", `function step(x: i32): i32 { return x + 1; }
function tryThing(cb: (i32) => i32): i32 {
    return cb(42);
}
function main(): i32 {
    return tryThing(step);
}`, 43},
		{"generic callee with use inference", `function each[T](items: T[], cb: (T) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    var nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + 1;
}`, 11},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

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
    function inner(): i32 { return s.len(); }
    return inner();
}
function main(): i32 { return outer("hello"); }`
	if _, code := compileAndRunX86_64(t, src); code != 5 {
		t.Errorf("got %d, want 5 (len(\"hello\") via captured string)", code)
	}
}

// Mirror of TestArm64LambdaWithBodyLocals. Anonymous lambdas
// used to drop the body's Var declarations on the floor: the
// checker stored them against a throwaway synthetic FuncDecl
// pointer that closureconv never re-keyed onto the hoisted
// lambda, so lowerFunc would panic with "var X has no slot".
// String-typed body local exercises the pointer-width slot
// path under x86-64's two-word string ABI.
func TestX86_64LambdaWithBodyLocals(t *testing.T) {
	src := `function main(): i32 {
    var greet = "hi";
    var f = function (n: i32): i32 {
        var sq = n * n;
        var tag = greet + "!";
        print(tag);
        return sq;
    };
    return f(6);
}`
	stdout, code := compileAndRunX86_64(t, src)
	if code != 36 {
		t.Errorf("got exit %d, want 36", code)
	}
	if stdout != "hi!\n" {
		t.Errorf("got stdout %q, want %q", stdout, "hi!\n")
	}
}

// Mirror of TestArm64LambdaCallsMethodOnCapturedString. Method
// calls inside a lambda body used to fall off the treeshake
// liveness walk because `walkExpr` had no case for
// `*ast.Lambda`; the lambda body was invisible at shake time
// (closureconv hoists it later), so `__method_string_trim`
// got pruned and link died on the undefined reference.
func TestX86_64LambdaCallsMethodOnCapturedString(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "  hi  ";
    var f = function (): string { return s.trim().to_owned(); };
    var got = f();
    if (got == "hi") { return 0; }
    return 1;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (s.trim() inside lambda body)", code)
	}
}

// Mirror of TestArm64NestedLambdaUniqueNames. Two anonymous
// lambdas hoisted in the same converter session used to
// collide on `__closure_lambda_1` because freshName keyed off
// `len(c.hoisted)` (which doesn't grow when an existing key
// gets incremented).
func TestX86_64NestedLambdaUniqueNames(t *testing.T) {
	src := `function main(): i32 {
    var outer = function (): i32 {
        var inner = function (): i32 {
            var x = 21;
            return x * 2;
        };
        var y = inner();
        return y;
    };
    return outer();
}`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
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
    function inner(): i32 { return s.len() + n; }
    return inner();
}
function main(): i32 { return outer("hi", 40); }`
	// len("hi") + 40 = 42
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Mirror of TestWASMClosureCapturesTuple — `targetTupleType`
// now recognises `*ast.CaptureRef` so `t.0` / `t.1` inside a
// closure body resolves through the tuple-offset path instead
// of falling through to the struct path and erroring with
// `field access on unresolved struct ""` at IR-emit time.
func TestX86_64ClosureCapturesTuple(t *testing.T) {
	src := `function build(): () => i64 {
    var t: (i64, i64) = (1000000000000i64, 2000000000000i64);
    function read(): i64 { return t.0 + t.1; }
    return read;
}
function main(): i32 {
    var f = build();
    if (f() != 3000000000000i64) { return 1; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (closure captures (i64, i64))", code)
	}
}

// Mirror of TestWASMClosureFStringCapture — closureconv now
// recurses through FString.Desugared so captured-name idents
// inside `f"…{cap}…"` get rewritten to CaptureRef nodes.
func TestX86_64ClosureFStringCapture(t *testing.T) {
	src := `
import "std/string";
function makeNamer(name: string): () => string {
    function build(): string { return f"hello, {name}!"; }
    return build;
}
function main(): i32 {
    var f = makeNamer("world");
    if (f() != "hello, world!") { return 1; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got %d, want 0", code)
	}
}

// Mirror of TestWASMMutableCapturedVar — assignment to a
// captured outer-scope variable now stores into the env block.
func TestX86_64MutableCapturedVar(t *testing.T) {
	src := `function makeCounter(): () => i32 {
    var count: i32 = 0;
    function tick(): i32 {
        count = count + 1;
        return count;
    }
    return tick;
}
function main(): i32 {
    var c = makeCounter();
    var a: i32 = c();
    var b: i32 = c();
    var d: i32 = c();
    return a + b + d;
}`
	if _, code := compileAndRunX86_64(t, src); code != 6 {
		t.Errorf("got %d, want 6 (counter increments in env)", code)
	}
}

// Mirror of TestWASMClosureCallsCapturedFn — the IR's call()
// path now handles `*ast.CaptureRef` callees so calling a
// captured function value inside a nested closure works.
func TestX86_64ClosureCallsCapturedFn(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function makeApplier(f: (i32) => i32): (i32) => i32 {
    function apply(x: i32): i32 { return f(x) + 1; }
    return apply;
}
function main(): i32 {
    var a = makeAdder(10);
    var ap = makeApplier(a);
    return ap(5);
}`
	if _, code := compileAndRunX86_64(t, src); code != 16 {
		t.Errorf("got %d, want 16", code)
	}
}

// Mirror of TestWASMClosureRecursiveSelfCall — closureconv now
// rewrites a recursive self-reference inside the hoisted body
// from the original local name (`fact`) to the hoisted name
// (`__closure_fact_1`) and forwards `__env` through so the
// recursive callee gets the same captured-state block.
func TestX86_64ClosureRecursiveSelfCall(t *testing.T) {
	src := `function makeFact(): (i32) => i32 {
    function fact(n: i32): i32 {
        if (n <= 1) { return 1; }
        return n * fact(n - 1);
    }
    return fact;
}
function main(): i32 {
    var f = makeFact();
    return f(5);
}`
	if _, code := compileAndRunX86_64(t, src); code != 120 {
		t.Errorf("got %d, want 120 (5!)", code)
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
		{"basic_set_get", `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 100);
    m = m.insert(2, 200);
    return m.get_or(2, 0);
}`, 200},
		{"iter_after_delete", `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 10);
    m = m.insert("b", 20);
    m = m.insert("c", 30);
    m = m.without("b").0;
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

func TestX86_64Defer(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"defer fires after return value computed", `function inner(): i32 {
    var x: i32 = 1;
    defer x = 99;
    x = 2;
    return x;
}
function main(): i32 { return inner(); }`, 2},
		{"multiple defers run LIFO", `function check(c: Cell[i32]): i32 {
    c.set(1);
    defer c.set(10);
    defer c.set(20);
    return c.get();
}
function main(): i32 {
    var c: Cell[i32] = cell_new(0);
    check(c);
    return c.get();
}`, 10},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestX86_64FStringInterpolation(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"interpolated i32", `
import "std/i32";
function main(): i32 {
    var n: i32 = 42;
    var s: string = f"n is {n}";
    return s.len();
}`, 7},
		{"interpolated string", `
import "std/i32";
function main(): i32 {
    var who: string = "world";
    var s: string = f"hello, {who}!";
    return s.len();
}`, 13},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestX86_64Generic(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"generic identity", `function id[T](x: T): T { return x; }
function main(): i32 { return id(42); }`, 42},
		{"generic with two type params", `function pick[A, B](a: A, b: B, take_first: boolean): A {
    return a;
}
function main(): i32 { return pick(7, "hi", true); }`, 7},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestX86_64Tuple(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"tuple destructure", `function pair(): (i32, i32) { return (10, 20); }
function main(): i32 {
    let (a, b) = pair();
    return a + b;
}`, 30},
		{"heterogeneous tuple element access", `function main(): i32 {
    var t: (i32, string, i32) = (1, "two", 3);
    return t.0 + t.2;
}`, 4},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestX86_64ForEach(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sum array", `function main(): i32 {
    var sum: i32 = 0;
    for n in [1, 2, 3, 4, 5] { sum = sum + n; }
    return sum;
}`, 15},
		{"break exits the loop", `function main(): i32 {
    var found: i32 = -1;
    for n in [10, 20, 30, 40] {
        if (n == 30) { found = n; break; }
    }
    return found;
}`, 30},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestX86_64IfLet(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Some matches", `function main(): i32 {
    var x: Option[i32] = Some(42);
    if let Some(v) = x { return v; }
    return 99;
}`, 42},
		{"None falls through", `function main(): i32 {
    var x: Option[i32] = None;
    if let Some(v) = x { return v; }
    return 99;
}`, 99},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

// `usize` is the target-aware native-pointer-width unsigned
// integer: 4 bytes on wasm32, 8 bytes on natives. Foundational
// for the arm64-darwin truncation fix tracked in
// BACKEND-PARITY.md — pointer-holding prelude locals need a
// type whose width follows the target. The cast machinery
// accepts usize as a source / dest in the data-pointer hop
// pattern (T[] / [T] / string / struct ↔ usize), and
// settled-literal lowering picks OpConstI64 on natives so
// values exceeding 32 bits round-trip without truncating.
// `usize + i32` arithmetic auto-widens the i32 side to usize.
// This is the unblock for prelude pointer-arithmetic migration
// (e.g. `entriesBase + idx * entryStride` where idx + stride
// are i32) without sprinkling explicit `as usize` everywhere.
// Pins the pattern across both natives.
func TestX86_64UsizeAutowiden(t *testing.T) {
	src := `function offset_compute(base: usize, idx: i32, stride: i32): usize {
    return base + idx * stride;
}
function main(): i32 {
    // 0x100000000 — exceeds 32 bits so a wrong truncation would
    // lose the high bit and the low 32 bits would be (base_lo +
    // 4*8) = (0 + 32). With usize-arithmetic the result is
    // 0x100000020 and the low 32 bits are 32 — matches.
    var heap_ptr: usize = 4294967296 as usize;
    var elem: usize = offset_compute(heap_ptr, 4, 8);
    return (elem as i32);
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 32 {
		t.Errorf("got %d, want 32 (low 32 bits of 0x100000020)", code)
	}
}

// TestX86_64UsizeDivRem — usize is 64-bit on x86-64, so `usize / usize`
// and `usize % usize` must use the 64-bit idiv/div register form. A
// 32-bit form truncates the dividend to its low 32 bits and produces a
// wrong quotient/remainder for values exceeding 2^32. Regression for the
// B1 finding in docs/ADVERSARIAL-REVIEW-2026-06.md. arm64 + wasm32 were
// already correct (wasm32's usize is genuinely 32-bit); x86-64 was the
// lone broken backend.
func TestX86_64UsizeDivRem(t *testing.T) {
	// 5_000_000_000 = 0x1_2A05_F200. Low 32 bits = 705032704.
	//   64-bit: 5000000000 / 3 = 1666666666, 5000000000 % 3 = 2.
	//   32-bit (buggy): 705032704 / 3 = 235010901, % 3 = 1.
	src := `function main(): i32 {
    var x: usize = 5000000000 as usize;
    var q: usize = x / 3;
    var r: usize = x % 3;
    if ((q as i32) != 1666666666) { return 1; }
    if ((r as i32) != 2) { return 2; }
    return 7;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 7 {
		t.Errorf("got %d, want 7 (usize div/rem truncated to 32 bits?)", code)
	}
}

// TestX86_64FloatToUsize — `f64 as usize` must produce a 64-bit
// truncation on natives (usize is pointer-width = 8 bytes). The shared
// IR cast lowering must not clamp usize's width to 32: a 32-bit
// float-trunc loses the high bits for values exceeding 2^32.
// Regression for B2 in docs/ADVERSARIAL-REVIEW-2026-06.md (shared IR, so
// arm64 has the matching test).
func TestX86_64FloatToUsize(t *testing.T) {
	src := `function main(): i32 {
    var f: f64 = 5000000000.0;
    var u: usize = f as usize;
    if (u == 5000000000 as usize) { return 7; }
    return 1;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 7 {
		t.Errorf("got %d, want 7 (f64->usize truncated to 32 bits?)", code)
	}
}

// Wide-scalar `Map[K, V]` works on natives even though the
// prelude's `__map_*_impl` functions declare K + V as `i32`:
// the native operand stack uses 8-byte slots so i64 / f64
// values flow through the i32-typed prelude params without
// truncation, and `__store_ptr` / `__load_ptr` (now usize-
// typed since PR #315) push 8 bytes through.
//
// Wasm32 needs proper per-instantiation dispatch — its typed
// stack rejects the i32 / i64 mismatch at component-validation
// time. Tracked in BACKEND-PARITY.md as a wasm-only limitation.
func TestX86_64WideScalarMap(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Map[i64, i32]", `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 100);
    return m.get_or(1i64, 0);
}`, 100},
		{"Map[i32, f64]", `
import "core/map";
function main(): i32 {
    var m: Map[i32, f64] = map_new(4);
    m = m.insert(1, 3.14);
    return m.get_or(1, 0.0) as i32;
}`, 3},
		{"Map[i64, string]", `
import "core/map";
function main(): i32 {
    var m: Map[i64, string] = map_new(4);
    m = m.insert(1i64, "hello");
    return (m.get_or(1i64, "")).len();
}`, 5},
		{"Map[string, i64]", `
import "core/map";
function main(): i32 {
    var m: Map[string, i64] = map_new(4);
    m = m.insert("hello", 42i64);
    return m.get_or("hello", 0i64) as i32;
}`, 42},
		{"Map[u64, i32]", `
import "core/map";
function main(): i32 {
    var m: Map[u64, i32] = map_new(4);
    m = m.insert(1u64, 100);
    return m.get_or(1u64, 0);
}`, 100},
		{"distinct high-bit i64 keys", `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(8);
    var k1: i64 = 0i64;
    var k2: i64 = 1i64 << 33i64;
    m = m.insert(k1, 1);
    m = m.insert(k2, 2);
    var v1: i32 = m.get_or(k1, 99);
    var v2: i32 = m.get_or(k2, 99);
    // Sum signals correct separation: v1=1, v2=2 → 3.
    // Coincident-collision would give v1=v2 → 2 or 4.
    return v1 + v2;
}`, 3},
		// m.keys() on Map[i64, _] needs to materialise an i64[]
		// snapshot, not the i32[] truncation the lang-level
		// `__map_keys_impl` would produce with its hard-coded
		// 4-byte destStride. The IR's emitWideMapKeys walks the
		// entries and memcpy's the raw 8-byte K slot into the
		// result. Without it, every key gets its upper 32 bits
		// dropped — distinct high-bit keys collide into the same
		// snapshot value.
		{"keys() preserves 8-byte values", `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 10);
    m = m.insert(1000000000000i64, 20);
    var keys: i64[] = m.keys();
    if (keys.len() != 2) { return 1; }
    if (keys[0] != 1i64 && keys[0] != 1000000000000i64) { return 2; }
    if (keys[1] != 1i64 && keys[1] != 1000000000000i64) { return 3; }
    if (keys[0] == keys[1]) { return 4; }
    return 0;
}`, 0},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestX86_64Usize(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"basic usize round-trip", `function main(): i32 {
    var x: usize = 42;
    return x as i32;
}`, 42},
		{"usize arithmetic", `function main(): i32 {
    var a: usize = 10;
    var b: usize = 32;
    return (a + b) as i32;
}`, 42},
		{"usize as fn param + return", `function dbl(x: usize): usize { return x + x; }
function main(): i32 {
    var n: usize = 21;
    return dbl(n) as i32;
}`, 42},
		{"large value survives on native (> 32 bits)", `function main(): i32 {
    var big: usize = 4294967301 as usize;
    var rt: i64 = big as i64;
    if ((rt >> 32) > 0i64) { return 42; }
    return 1;
}`, 42},
		{"string ptr round-trip through usize", `function main(): i32 {
    var s: string = "hello, " + "world";
    var ptr: usize = s as usize;
    var s2: string = ptr as string;
    return s2.len();
}`, 12},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
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
	if err := constfold.Fold(prog, nil); err != nil {
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
        Ok(s) => { return s.len(); },
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
                NotFound(p) => { return p.len(); },
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
        Err(_) => { return 1; },
        Ok(_) => { return 0; }
    }
    return 0 - 1;
}`
	_, code, dir := compileX86_64InDir(t, src, nil)
	if code != 0 {
		t.Errorf("write_file exit = %d, want 0 (Ok path)", code)
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
        Err(_) => { return 1; },
        Ok(_) => {}
    }
    match (read_file("rt.txt")) {
        Ok(s) => { return s.len(); },
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

// Slice construction + indexing on natives. `__slice_make` has to be
// emitted there and not only on wasm, or any program containing
// `a[lo:hi]` fails to link with "undefined reference to
// __slice_make". The natives' inline `__slice_idx_N` helper must also
// dereference the header's data_ptr field before indexing, not compute
// `header_ptr + i*N`. The runtime
// helper and fixes the inline so all strides (u8 / i32 /
// i64-shape) work for both reads and writes.
func TestX86_64SliceMake(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i32 slice read", `function main(): i32 {
    var arr: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = arr[1:4];
    return s[1];
}`, 30},
		{"u8 slice read", `function main(): i32 {
    var arr: u8[] = [10, 20, 30, 40, 50];
    var s: [u8] = arr[1:4];
    return s[1] as i32;
}`, 30},
		{"i64 slice read", `function main(): i32 {
    var arr: i64[] = [(1i64 << 40), (1i64 << 41), (1i64 << 42)];
    var s: [i64] = arr[1:3];
    return (s[0] >> 41) as i32;
}`, 1},
		{"len(slice)", `function main(): i32 {
    var arr: i32[] = [1, 2, 3, 4, 5];
    var s: [i32] = arr[1:4];
    return s.len();
}`, 3},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
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

func TestX86_64FloatBitCast(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"round-trip 1.0", `function main(): i32 {
    var x: f32 = 1.0;
    var b: i32 = f32_bits(x);
    var y: f32 = f32_from_bits(b);
    if (y == x) { return 0; }
    return 1;
}`, 0},
		{"round-trip 3.14", `function main(): i32 {
    var x: f32 = 3.14;
    var b: i32 = f32_bits(x);
    var y: f32 = f32_from_bits(b);
    if (y == x) { return 0; }
    return 1;
}`, 0},
		{"1.0 bits = 0x3F800000", `function main(): i32 {
    if (f32_bits(1.0) == 1065353216) { return 0; }
    return 1;
}`, 0},
		{"sign-bit preserved through round-trip", `function main(): i32 {
    var neg: f32 = 0.0 - 1.0;
    var b: i32 = f32_bits(neg);
    var back: f32 = f32_from_bits(b);
    if (back == neg) { return 0; }
    return 1;
}`, 0},
		// Regression pin for the OpFNeg fix. The old shape
		// emitted `0.0 - x`, which folded `-0.0` to `+0.0`
		// per IEEE-754. The sign-bit-XOR shape used here
		// preserves negative zero, so `f32_bits(-0.0)` is
		// the expected 0x80000000.
		{"-0.0 bits = sign bit", `function main(): i32 {
    var bits_u: u32 = f32_bits(-0.0) as u32;
    if (bits_u == 2147483648 as u32) { return 0; }
    return 1;
}`, 0},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

func TestX86_64ReaderWriter(t *testing.T) {
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
			stdout, code, _ := compileX86_64InDir(t, c.src, nil)
			if code != c.wantExit {
				t.Errorf("exit = %d, want %d (stdout = %q)", code, c.wantExit, stdout)
			}
			if !strings.Contains(stdout, c.wantStdout) {
				t.Errorf("stdout = %q, want to contain %q", stdout, c.wantStdout)
			}
		})
	}
}

// Wasm-shaped feature parity for the native x86-64 backend.
// Each case asserts the program returns 0 — same source as
// TestArm64FeatureParity so the backends stay observably
// equivalent on language-feature behaviour.
func TestX86_64FeatureParity(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"defer_basic", `
import "core/map";
function inner(trace: Map[string, i32]): i32 {
    trace = trace.insert("body-start", 1);
    defer trace.insert("first-defer", 10);
    defer trace.insert("second-defer", 20);
    trace = trace.insert("body-end", 2);
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
		{"fstring_interp", `
import "std/i32";
function main(): i32 {
    var x: i32 = 42;
    var s: string = f"x is {x}";
    if (s.len() == 7) { return 0; }
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
			if _, code := compileAndRunX86_64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
	}
}

// x86-64 counterpart to TestArm64NoPreludeStdlibImports — proves
// the no-prelude path through the prelude-to-modules stack works
// on the SysV ABI backend too. See the arm64 sibling for the
// rationale and per-case explanations.
func TestX86_64NoPreludeStdlibImports(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"i32_string_cycle", `
import "std/i32";
function main(): i32 {
    var s: string = (42).to_string_padded(6);
    if (s == "000042") { return 0; }
    return 1;
}`},
		{"array_method_chain", `
import "std/array";
function main(): i32 {
    var xs: i32[] = [0 - 3, 4, 0 - 1];
    var ys = xs.abs_each();
    if (ys[0] + ys[1] + ys[2] == 8) { return 0; }
    return 1;
}`},
		{"qualified_int_call", `
import "core/int";
function main(): i32 {
    var s: string = int.int_to_string_radix(255, 16);
    if (s == "ff") { return 0; }
    return 1;
}`},
		{"mixed_stdlib", `
import "std/i32";
import "std/string";
import "std/array";
function main(): i32 {
    var s: string = (0 - 42).to_string();
    if (s != "-42") { return 1; }
    var strs: string[] = ["b", "a", "c"];
    var joined: string = strs.join(",");
    if (joined != "b,a,c") { return 2; }
    return 0;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
	}
}

// Refcount builtins (`__rc_get` / `__rc_inc` / `__rc_dec`)
// exposed for Phase-1 testing. Validates Phase 1a (rc=1 on
// `__alloc_u8`) and Phase 1b (inc / dec are sentinel-aware and
// don't corrupt the rc word). The program returns 0 iff the
// observed rc progression is exactly 1 → 2 → 1.
func TestX86_64RcBuiltins(t *testing.T) {
	src := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(10);
    var r1: i32 = __rc_get(arr);
    __rc_inc(arr);
    var r2: i32 = __rc_get(arr);
    __rc_dec(arr);
    var r3: i32 = __rc_get(arr);
    return r1 + r2 + r3 - 4;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (rc progression off)", code)
	}
}

// Phase 1d transfer inc, refined by #4402 opt 1 (dead-alias dup/drop
// cancellation): a pure borrowed-view alias — never reassigned, never
// returned, never moved — elides its inc AND its exit-sweep dec as a
// net-zero pair, so the rc stays 1. An alias that is still referenced
// under the return keeps the ordinary transfer inc (rc 2).
func TestX86_64RcAliasInc(t *testing.T) {
	dead := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var alias: u8[] = arr;
    return __rc_get(arr) - 1;
}`
	if _, code := compileAndRunX86_64(t, dead); code != 0 {
		t.Errorf("dead alias: got exit %d, want 0 (borrowed view elides the inc — rc stays 1)", code)
	}
	live := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var alias: u8[] = arr;
    return __rc_get(arr) - 2 + alias.len() - 8;
}`
	if _, code := compileAndRunX86_64(t, live); code != 0 {
		t.Errorf("returned alias: got exit %d, want 0 (transfer inc kept — rc 2)", code)
	}
}

// Phase 1d-ii (+ Phase 1d-viii): FieldAccess + Index alias
// reads inc the rc; with Phase 1d-viii, the struct- / array-
// lit constructor also inc's the captured array. A LIVE alias
// ends at rc=3 — alloc (1) + lit store (1) + alias read (1);
// a DEAD one cancels the read's inc against its own exit dec
// and ends at rc=2. Both are pinned, because dropping either
// side of that pair alone is an over-release.
func TestX86_64RcAliasIncFieldAndIndex(t *testing.T) {
	for _, c := range []struct {
		name string
		// want names the rc the case's arithmetic subtracts, so a failure
		// says which expectation moved.
		want string
		src  string
	}{
		{"field_access", "rc=3 (alloc + struct-lit store + live alias read)", `
struct Holder { items: u8[] }
function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var h: Holder = Holder { items: inner };
    var alias: u8[] = h.items;
    // Precise drops (RC-Perceus) release the now-dead struct h AND the
    // now-dead alias at their last use; reference both in the return so they
    // stay live through the check — this measures the fully-aliased rc
    // (inner + h.items + alias). Both .len()-8 terms are 0, so the result is
    // unchanged.
    return __rc_get(inner) - 3 + h.items.len() - 8 + alias.len() - 8;
}`},
		{"index_load", "rc=3 (alloc + array-lit store + live alias read)", `
function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var matrix: u8[][] = [inner];
    var alias: u8[] = matrix[0];
    // Precise drops (RC-Perceus) release the now-dead matrix AND the now-dead
    // alias at their last use; reference both in the return so they stay live
    // through the check — this measures the fully-aliased rc (inner + the
    // array element + alias), which is what the test asserts. An alias that is
    // NOT read again cancels its inc against its own exit dec instead, which
    // is a different rc and the case below. matrix.len()-1 and alias.len()-8
    // are both 0, so the result is unchanged.
    return __rc_get(inner) - 3 + matrix.len() - 1 + alias.len() - 8;
}`},
		{"index_load_dead_alias", "rc=2 (alloc + array-lit store; the dead alias inc/dec cancel)", `
function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var matrix: u8[][] = [inner];
    var alias: u8[] = matrix[0];
    // The alias is never read again, so it is a pure borrowed view: the
    // transfer inc cancels against its own exit dec as a net-zero pair — the
    // #4402 opt-1 shape RcAliasInc's dead case pins for a plain ident, which
    // an element read reaches too now that it is not permanently borrow-
    // tainted (#6567). rc is therefore 2 (alloc + the array-literal element
    // store), and the underflow counter must still be 0: eliding ONE side of
    // that pair is an over-release, not an optimisation.
    return __rc_get(inner) - 2 + matrix.len() - 1 + __rc_underflow_count();
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0 — expected %s", code, c.want)
			}
		})
	}
}

// Phase 1d-iii: `y = x;` reassignment bumps the rc on x.
func TestX86_64RcAliasIncReassign(t *testing.T) {
	src := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var other: u8[] = __alloc_u8(8);
    other = arr;
    return __rc_get(arr) - 2;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (assign-alias should bump rc to 2)", code)
	}
}

// Phase 2d-borrow: passing an array to a function is a borrow —
// no caller-side inc, no callee-side exit dec. The rc is
// untouched across the call and stays at 1.
func TestX86_64RcAliasIncCallArg(t *testing.T) {
	src := `
function f(arr: u8[]): i32 { return 0; }
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var _: i32 = f(arr);
    return __rc_get(arr) - 1;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (borrowed arg: rc stays 1)", code)
	}
}

// Phase 3: the rc-underflow detector now runs on the native
// backends too — __fern_rc_dec bumps a BSS counter
// (__fern_rc_underflow) on any over-release, read back by
// __fern_rc_underflow_count(). This pins both the detector
// mechanism (clean→0, deliberate double-dec→1) AND that the
// IR-side drift fixes hold on x86_64: idiomatic `m = m.insert(...)`
// self-assignment reports 0 over-releases, same as wasm.
func TestX86_64RcUnderflowDetector(t *testing.T) {
	selfAssign := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("a", 9);
    return __rc_underflow_count() * 100
         + (m.len() - 2)
         + (m.get_or("a", 0) - 9);
}`
	if _, code := compileAndRunX86_64(t, selfAssign); code != 0 {
		t.Errorf("got %d, want 0 (x86 map self-assign: 0 underflow, correct contents)", code)
	}

	overRelease := `function main(): i32 {
    var a: u8[] = __alloc_u8(8);
    __rc_dec(a);   // 1 -> 0
    __rc_dec(a);   // 0 -> -1 (over-release, counted)
    return __rc_underflow_count();
}`
	if _, code := compileAndRunX86_64(t, overRelease); code != 1 {
		t.Errorf("got %d, want 1 (x86 double-dec over-release count)", code)
	}
}

// Phase 3 step 3: drop handlers, native parity. An array of
// pointer-shaped rc-tracked elements routes its scope-exit dec
// through __fern_drop_arr_ptr, which on the last reference dec's
// each element — balancing the per-element inc the IR emits at
// array-literal construction (Phase 1d-viii). Mirrors the wasm
// TestWASMRcDropArrayElements. The native helper additionally
// carries the low-address guard __fern_rc_dec has, so an
// array-typed slot that actually holds a non-pointer (an enum
// tag, stack garbage from a never-taken branch) is passed through
// instead of faulting.
func TestX86_64RcDropArrayElements(t *testing.T) {
	// Proof the drop FIRES: `consume` nests `inner` into a local
	// array and drops it on exit; inner's rc must return to its
	// pre-call value (1), not stay at the constructed 2.
	fires := `function consume(inner: u8[]): i32 {
    var outer: u8[][] = [inner];
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1);
}`
	if _, code := compileAndRunX86_64(t, fires); code != 0 {
		t.Errorf("got %d, want 0 (drop must dec the nested element back to rc 1)", code)
	}

	// And no over-release: nesting fresh + aliased elements and
	// dropping the outer array reports 0 underflows.
	noUnder := `function build(): i32 {
    var inner: i32[] = [1, 2, 3];
    var a: i32[][] = [inner];
    var b: i32[][] = [[4, 5], [6]];
    return a[0][1] + b[1][0];
}
function main(): i32 {
    return (build() - 8) + __rc_underflow_count();
}`
	if _, code := compileAndRunX86_64(t, noUnder); code != 0 {
		t.Errorf("got %d, want 0 (nested-array drop: correct values, 0 underflow)", code)
	}
}

// Phase 3 step 3: struct drop handlers. A user struct with
// pointer-shaped rc-tracked fields drops those fields on its last
// reference (gated by __fern_rc_is_unique) before dec'ing the box,
// balancing the per-field inc from Phase 1e-struct-ii.
func TestX86_64RcDropStructFields(t *testing.T) {
	// Drop FIRES: a struct holding an aliased array drops the field
	// on exit, so the array's rc returns to its pre-construction 1.
	fires := `struct Holder { items: u8[] }
function consume(inner: u8[]): i32 {
    var h: Holder = Holder { items: inner };
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1) + __rc_underflow_count();
}`
	if _, code := compileAndRunX86_64(t, fires); code != 0 {
		t.Errorf("got %d, want 0 (struct field drop must dec the array back to rc 1)", code)
	}

	// Aliased struct must NOT drop fields twice: `var h2 = h1` bumps
	// the struct rc, so only the last holder recurses into fields.
	aliased := `struct Holder { items: i32[] }
function main(): i32 {
    var inner: i32[] = [1, 2, 3];
    var h1: Holder = Holder { items: inner };
    var h2: Holder = h1;
    return h2.items[2] + __rc_underflow_count() - 3;
}`
	if _, code := compileAndRunX86_64(t, aliased); code != 0 {
		t.Errorf("got %d, want 0 (aliased struct: no double field-drop, 0 underflow)", code)
	}

	// Nested array field + a nested-struct field: the array field
	// recurses one level; the struct field is flat-dec'd. Neither
	// over-releases.
	nested := `struct Grid { rows: i32[][] }
struct Inner { v: i32[] }
struct Outer { inner: Inner }
function build(): i32 {
    var a: i32[] = [1, 2, 3];
    var g: Grid = Grid { rows: [a] };
    var arr: i32[] = [7, 8];
    var o: Outer = Outer { inner: Inner { v: arr } };
    return g.rows[0][1] + o.inner.v[0] + __rc_underflow_count();
}
function main(): i32 {
    return build() - 9;
}`
	if _, code := compileAndRunX86_64(t, nested); code != 0 {
		t.Errorf("got %d, want 0 (nested struct/array fields: correct values, 0 underflow)", code)
	}
}

// Phase 3 step 3: enum drop handlers (uniform case). A heap-boxed
// enum whose payload-carrying variants share an identical
// droppable-payload signature drops those payloads on its last
// reference (gated by __fern_rc_is_unique) before dec'ing the box.
// This covers unions (`type U = W | V2`), where every variant
// carries a single struct pointer at the same payload offset.
// Non-uniform / generic enums fall through to the plain box dec.
func TestX86_64RcDropEnumPayload(t *testing.T) {
	// Fresh struct boxed into a union, dropped at scope exit — the
	// box transfer-owns the payload, so the drop balances (0
	// over-releases) and the value round-trips.
	fresh := `struct W { a: i32[] }
struct V2 { b: i32 }
type U = W | V2;
function mk(): U { return W { a: [1, 2, 3] }; }
function build(): i32 {
    var u: U = mk();
    match (u) { W(w) => { return w.a[1] + __rc_underflow_count(); }, V2(x) => { return x.b; } }
    return 0 - 1;
}
function main(): i32 { return build() - 2; }`
	if _, code := compileAndRunX86_64(t, fresh); code != 0 {
		t.Errorf("got %d, want 0 (union payload drop: value 2, 0 underflow)", code)
	}

	// Aliased struct widened into a union: widening inc's the
	// payload, so dropping the union must not over-release the
	// still-live struct.
	aliased := `struct W { a: i32 }
struct V2 { b: i32 }
type U = W | V2;
function main(): i32 {
    var w: W = W { a: 7 };
    var u: U = w;
    return w.a + __rc_underflow_count() - 7;
}`
	if _, code := compileAndRunX86_64(t, aliased); code != 0 {
		t.Errorf("got %d, want 0 (aliased struct widened to union: 0 underflow)", code)
	}

	// Non-uniform enum (pointer payload in one variant, scalar in
	// another) falls through to the plain box dec — payload leaks,
	// but no over-release.
	nonUniform := `enum E { Arr(i32[]), Num(i32) }
function main(): i32 {
    var e: E = Arr([1, 2, 3]);
    var f: E = Num(9);
    match (e) {
        Arr(a) => { return a.len() + __rc_underflow_count() - 3; },
        Num(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`
	if _, code := compileAndRunX86_64(t, nonUniform); code != 0 {
		t.Errorf("got %d, want 0 (non-uniform enum: correct value, 0 underflow)", code)
	}
}

// Phase 1d-vi: dec on overwrite. See TestArm64RcDecOnOverwrite
// for the trace.
func TestX86_64RcDecOnOverwrite(t *testing.T) {
	src := `
function main(): i32 {
    var arr1: u8[] = __alloc_u8(8);
    var arr2: u8[] = __alloc_u8(8);
    var arr3: u8[] = __alloc_u8(8);
    arr1 = arr2;
    arr1 = arr3;
    return __rc_get(arr2) + __rc_get(arr3) - 3;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (arr2 rc=1, arr3 rc=2, sum=3)", code)
	}
}

// Phase 2: arr.push mutates in place when rc==1 and cap > len.
// First push of [10, 20] copies (cap=2=len, no spare); the
// copy bumps cap to max(2*newLen, 4) = 6, so the second push
// hits the fast path and returns the same pointer.
func TestX86_64ArrayPushInPlaceFastPath(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20];
    xs = xs.append(30);
    var addr_before: usize = xs as usize;
    xs = xs.append(40);
    var addr_after: usize = xs as usize;
    if (addr_before != addr_after) { return 1; }
    if (xs.len() != 4) { return 2; }
    if (xs[3] != 40) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (in-place fast path should reuse buffer)", code)
	}
}

// Phase 2: aliased rc>1 forces copy semantics even with spare
// cap — otherwise the other holder's view of the array would
// silently extend.
func TestX86_64ArrayPushAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20];
    xs = xs.append(30);
    var ys = xs;
    ys = ys.append(40);
    if (xs.len() != 3) { return 1; }
    if (xs[0] != 10) { return 2; }
    if (ys.len() != 4) { return 3; }
    if (ys[3] != 40) { return 4; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased push must copy)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetInPlaceFastPath. Phase 2b
// arr[i]=v lowers to __fern_arr_cow_inplace; rc==1 returns
// arr unchanged.
func TestX86_64ArrayIndexSetInPlaceFastPath(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var addr_before: usize = xs as usize;
    xs = xs.with(1, 999);
    var addr_after: usize = xs as usize;
    if (addr_before != addr_after) { return 1; }
    if (xs[1] != 999) { return 2; }
    if (xs[0] != 10) { return 3; }
    if (xs[2] != 30) { return 4; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (arr[i]=v in-place when rc==1)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetAliasedCopies.
func TestX86_64ArrayIndexSetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (xs[1] != 20) { return 2; }
    if (xs[2] != 30) { return 3; }
    if (ys[0] != 999) { return 4; }
    if (ys[1] != 20) { return 5; }
    if (ys[2] != 30) { return 6; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr[i]=v must copy)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetU8Stride. The int_to_string
// scratch[i] = digit pattern hit the wasm raw-_start path's
// __fern_rc_dec low-address guard before the helper internalised
// rc bookkeeping; covers the u8 stride here on x86-64 for
// regression parity.
func TestX86_64ArrayIndexSetU8Stride(t *testing.T) {
	src := `function main(): i32 {
    var buf: u8[] = __alloc_u8(4);
    buf = buf.with(0, 65 as u8);
    buf = buf.with(1, 66 as u8);
    buf = buf.with(2, 67 as u8);
    buf = buf.with(3, 68 as u8);
    return (buf[0] as i32) + (buf[1] as i32) + (buf[2] as i32) + (buf[3] as i32) - 266;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (u8 arr[i]=v preserves all writes)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetStructField.
func TestX86_64ArrayIndexSetStructField(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    var s: State = State{items: [10, 20, 30]};
    s = State { ...s, items: s.items.with(1, 999) };
    if (s.items[0] != 10) { return 1; }
    if (s.items[1] != 999) { return 2; }
    if (s.items[2] != 30) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct.field[i]=v in-place when rc==1)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetStructFieldAliasedCopies.
func TestX86_64ArrayIndexSetStructFieldAliasedCopies(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var s: State = State{items: arr};
    s = State { ...s, items: s.items.with(1, 999) };
    if (arr[0] != 10) { return 1; }
    if (arr[1] != 20) { return 2; }
    if (arr[2] != 30) { return 3; }
    if (s.items[0] != 10) { return 4; }
    if (s.items[1] != 999) { return 5; }
    if (s.items[2] != 30) { return 6; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased struct.field[i]=v must copy)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetNestedStructField.
func TestX86_64ArrayIndexSetNestedStructField(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var o: Outer = Outer{inner: Inner{items: [10, 20, 30]}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (o.inner.items[0] != 10) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    if (o.inner.items[2] != 30) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (a.b.field[i]=v in-place when rc==1)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetNestedStructFieldAliasedCopies.
func TestX86_64ArrayIndexSetNestedStructFieldAliasedCopies(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var o: Outer = Outer{inner: Inner{items: arr}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (arr[1] != 20) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased a.b.field[i]=v must copy)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetMat.
func TestX86_64ArrayIndexSetMat(t *testing.T) {
	src := `function main(): i32 {
    var mat: i32[][] = [[1, 2, 3], [4, 5, 6]];
    mat = mat.with(0, mat[0].with(1, 999));
    if (mat[0][0] != 1) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    if (mat[0][2] != 3) { return 3; }
    if (mat[1][0] != 4) { return 4; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v in-place when inner rc==1)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetMatInnerAliasedCopies.
func TestX86_64ArrayIndexSetMatInnerAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var mat: i32[][] = [[1, 2], [3, 4]];
    var inner = mat[0];
    mat = mat.with(0, mat[0].with(1, 999));
    if (inner[1] != 2) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v with aliased inner must copy)", code)
	}
}

// Mirror of TestArm64MapSetReturnsMap.
func TestX86_64MapSetReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    if (m.get_or("a", 0) != 1) { return 1; }
    if (m.get_or("b", 0) != 2) { return 2; }
    if (m.get_or("c", 0) != 3) { return 3; }
    if (m.len() != 3) { return 4; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.set returns Map)", code)
	}
}

// Mirror of TestArm64ArrayIndexSetObjMatInnerAliasedCopies.
func TestX86_64ArrayIndexSetObjMatInnerAliasedCopies(t *testing.T) {
	src := `struct State { mat: i32[][] }
function main(): i32 {
    var inner: i32[] = [1, 2, 3];
    var s: State = State{mat: [inner, [4, 5, 6]]};
    s = State { ...s, mat: s.mat.with(0, s.mat[0].with(1, 999)) };
    if (inner[1] != 2) { return 1; }
    if (s.mat[0][1] != 999) { return 2; }
    if (s.mat[0][0] != 1) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (obj.mat[i][j]=v with shared inner must copy)", code)
	}
}

// Mirror of TestArm64ArraySetSelfAssign.
func TestX86_64ArraySetSelfAssign(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    xs = xs.with(1, 999);
    if (xs[0] != 10) { return 1; }
    if (xs[1] != 999) { return 2; }
    if (xs[2] != 30) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (xs = xs.with(1, 999))", code)
	}
}

// Mirror of TestArm64ArraySetAliasedCopies.
func TestX86_64ArraySetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr.with must copy)", code)
	}
}

// Mirror of TestArm64MapDeleteReturnsMapBool.
func TestX86_64MapDeleteReturnsMapBool(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    if (!m.without("b").1) { return 1; }
    if (m.without("z").1)  { return 2; }
    var (m2, ok) = m.without("a");
    if (!ok) { return 3; }
    if (m2.has("a")) { return 4; }
    if (!m2.has("c")) { return 5; }
    if (m2.len() != 1) { return 6; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.delete returns (Map, bool))", code)
	}
}

// Mirror of TestArm64MapClearReturnsMap.
func TestX86_64MapClearReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("x", 10);
    m = m.insert("y", 20);
    if (m.len() != 2) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.has("x")) { return 3; }
    m = m.insert("z", 99);
    if (m.len() != 1) { return 4; }
    if (m.get_or("z", 0) != 99) { return 5; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.clear returns Map)", code)
	}
}

// Phase 2d: Map.set copy-on-write. A local alias of a map
// (var m2 = m1) bumps the handle rc to 2, so m2.insert(...) must
// COPY rather than mutate the shared buffer — m1 stays intact.
// Mirrors TestX86_64ArraySetAliasedCopies. The seed entry is a
// statement-form set (no reassign) so m1's rc stays 1 before the
// alias; under the borrowed-parameter model that set is in-place.
func TestX86_64MapSetAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);                 // in-place (rc==1)
    var m2 = m1;                    // alias → rc=2
    m2 = m2.insert("a", 999);          // rc>1 → copy; m1 unchanged
    if (m1.get_or("a", 0) != 1)   { return 1; }
    if (m2.get_or("a", 0) != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.set must copy)", code)
	}
}

// Phase 2d: Map.delete / Map.clear copy-on-write. An aliased map
// (var m2 = m1) has rc=2, so delete/clear copy and leave the
// source alias intact. The cow is threaded at the IR wrapper so
// the (bool/void)-returning impls hand the new handle back.
func TestX86_64MapDeleteClearAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    m1 = m1.insert("b", 2);
    var m2 = m1;                       // alias → rc=2
    var (m3, ok) = m2.without("a");     // rc>1 → copy; m1/m2 intact
    if (!ok)            { return 1; }
    if (m1.len() != 2)  { return 2; }  // original keeps "a"
    if (!m1.has("a"))   { return 3; }
    if (m3.len() != 1)  { return 4; }  // copy dropped "a"
    if (m3.has("a"))    { return 5; }
    var m4 = m1;                       // alias → rc=2
    m4 = m4.cleared();                   // rc>1 → copy; m1 intact
    if (m1.len() != 2)  { return 6; }
    if (m4.len() != 0)  { return 7; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.delete/clear must copy)", code)
	}
}

// Mirror of TestArm64TupleStructElem.
func TestX86_64TupleStructElem(t *testing.T) {
	src := `struct Inner { x: i32, y: i32 }
function main(): i32 {
    var t: (i32, Inner) = (1, Inner { x: 2, y: 3 });
    if (t.0 != 1) { return 1; }
    var inner: Inner = t.1;
    if (inner.x != 2) { return 2; }
    if (inner.y != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct in tuple slot)", code)
	}
}

// Mirror of TestArm64TupleArrayElem.
func TestX86_64TupleArrayElem(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, i32[]) = (1, [10, 20, 30]);
    if (t.0 != 1) { return 1; }
    var arr: i32[] = t.1;
    if (arr.len() != 3) { return 2; }
    if (arr[0] != 10) { return 3; }
    if (arr[2] != 30) { return 4; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (array in tuple slot)", code)
	}
}

// Mirror of TestArm64TupleNestedTuple.
func TestX86_64TupleNestedTuple(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 3));
    var (a, b) = t;
    if (a != 1) { return 1; }
    var (c, d) = b;
    if (c != 2) { return 2; }
    if (d != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (nested tuple destructure)", code)
	}
}

// Mirror of TestArm64LexerChainedTupleNumericAccess.
func TestX86_64LexerChainedTupleNumericAccess(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 3));
    if (t.0 != 1) { return 1; }
    if (t.1.0 != 2) { return 2; }
    if (t.1.1 != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (chained t.1.0 numeric access)", code)
	}
}

// Mirror of TestArm64EmptyMapDestinationInference.
func TestX86_64EmptyMapDestinationInference(t *testing.T) {
	src := `
import "core/map";
function take(m: Map[string, i32]): i32 { return m.len(); }
function mkEmpty(): Map[i32, string] { return Map {}; }
function main(): i32 {
    var a: Map[string, i32] = Map {};
    if (a.len() != 0) { return 1; }
    a = a.insert("k", 42);
    if (a.get_or("k", 0) != 42) { return 2; }
    var b: Map[i32, string] = Map {};
    if (b.len() != 0) { return 3; }
    b = b.insert(7, "hello");
    if (!b.has(7)) { return 4; }
    if (take(Map {}) != 0) { return 5; }
    var r = mkEmpty();
    if (r.len() != 0) { return 6; }
    var d: Map[i32, i32] = Map {};
    if (d.len() != 0) { return 7; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (empty Map literal inherits K/V from destination)", code)
	}
}

// Mirror of TestArm64EnumVariantInTuple.
func TestX86_64EnumVariantInTuple(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function main(): i32 {
    var t: (i32, Color) = (1, Green);
    if (t.0 != 1) { return 1; }
    match (t.1) {
        Red => { return 2; },
        Green => { },
        Blue => { return 3; }
    }
    var u: (i32, Option[i32]) = (5, Some(42));
    if (u.0 != 5) { return 4; }
    match (u.1) {
        Some(v) => { if (v != 42) { return 5; } },
        None => { return 6; }
    }
    var w: (Color, i32) = (Blue, 99);
    if (w.1 != 99) { return 7; }
    match (w.0) {
        Blue => { return 0; },
        _ => { return 8; }
    }
    return 9;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (enum variant in tuple slot)", code)
	}
}

// Mirror of TestArm64MapPointerShapedValues.
func TestX86_64MapPointerShapedValues(t *testing.T) {
	src := `
import "core/map";
struct P { x: i32, y: i32 }
function main(): i32 {
    var mt: Map[string, (i32, i32)] = Map {};
    mt = mt.insert("a", (3, 4));
    match (mt.get("a")) {
        Some(p) => { if (p.0 + p.1 != 7) { return 1; } },
        None => { return 2; }
    }
    var ms: Map[string, P] = Map {};
    ms = ms.insert("a", P { x: 3, y: 4 });
    match (ms.get("a")) {
        Some(s) => { if (s.x + s.y != 7) { return 3; } },
        None => { return 4; }
    }
    var ma: Map[i32, i32[]] = Map {};
    ma = ma.insert(1, [10, 20, 30]);
    match (ma.get(1)) {
        Some(arr) => { if (arr[0] + arr[2] != 40) { return 5; } },
        None => { return 6; }
    }
    var mi: Map[string, i32] = Map {};
    mi = mi.insert("a", 42);
    match (mi.get("a")) {
        Some(v) => { if (v != 42) { return 7; } },
        None => { return 8; }
    }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map pointer-shaped values)", code)
	}
}

// Mirror of TestArm64StructTupleFieldAccess.
func TestX86_64StructTupleFieldAccess(t *testing.T) {
	src := `struct Rec { pos: (i32, i32), name: string }
struct Nested { t: (i32, (i32, i32)) }
function main(): i32 {
    var r: Rec = Rec { pos: (3, 4), name: "p" };
    if (r.pos.0 != 3) { return 1; }
    if (r.pos.1 != 4) { return 2; }
    var n: Nested = Nested { t: (1, (2, 3)) };
    if (n.t.0 != 1) { return 3; }
    if (n.t.1.0 != 2) { return 4; }
    if (n.t.1.1 != 3) { return 5; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct tuple-field chained access)", code)
	}
}

// Mirror of TestArm64UnsignedComparison (regression guard — x86 was
// already correct, but pin it so a future refactor can't regress).
func TestX86_64UnsignedComparison(t *testing.T) {
	src := `function main(): i32 {
    var big: u32 = 4294967295u32;
    if (!(big > 0u32)) { return 1; }
    if (!(big > 1000000u32)) { return 2; }
    if (big < 5u32) { return 3; }
    if (!(big >= 4294967295u32)) { return 4; }
    if (big <= 100u32) { return 5; }
    var b64: u64 = 18446744073709551615u64;
    if (!(b64 > 9u64)) { return 6; }
    if (b64 < 9u64) { return 7; }
    var u: u8 = 200u8;
    if (!(u > 100u8)) { return 8; }
    var i: u32 = 4294967293u32;
    var c: i32 = 0;
    while (i > 4294967290u32) { c = c + 1; i = i - 1u32; }
    if (c != 3) { return 9; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (unsigned comparison condition codes)", code)
	}
}

// Mirror of TestArm64UnaryMinusWideTypes.
func TestX86_64UnaryMinusWideTypes(t *testing.T) {
	src := `function main(): i32 {
    var a: i64 = -5i64;
    if (a != 0i64 - 5i64) { return 1; }
    var b: f64 = -5.0;
    if (!(b < 0.0)) { return 2; }
    var c: f64 = -b;
    if (c != 5.0) { return 3; }
    var f: f32 = -2.5f32;
    if (!(f < 0.0f32)) { return 4; }
    var z: f64 = -0.0;
    if (f64_bits(z) == 0i64) { return 5; }
    var g: i64 = 10i64 + -3i64;
    if (g != 7i64) { return 6; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (unary minus on wide / non-i32 types)", code)
	}
}

// Mirror of TestArm64ScientificNotation.
func TestX86_64ScientificNotation(t *testing.T) {
	src := `function main(): i32 {
    var a: f64 = 1e3;
    if (a != 1000.0) { return 1; }
    var b: f64 = 1.5e3;
    if (b != 1500.0) { return 2; }
    var c: f64 = 1500.0e-3;
    if (c != 1.5) { return 3; }
    var d: f64 = 1.5e+3;
    if (d != 1500.0) { return 4; }
    var e: f64 = 2.5E2;
    if (e != 250.0) { return 5; }
    var f: f32 = 1.5e2f32;
    if (f != 150.0f32) { return 6; }
    var big: f64 = 1.8e19;
    if (!(big > 1.7e19)) { return 7; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (scientific-notation float literals)", code)
	}
}

// Mirror of TestArm64SubI32ArithmeticWraps.
func TestX86_64SubI32ArithmeticWraps(t *testing.T) {
	src := `struct S { v: u8 }
function main(): i32 {
    var a: u8 = 255u8;
    a = a + 1u8;
    if ((a as i32) != 0) { return 1; }
    var b: u8 = 0u8;
    b = b - 1u8;
    if ((b as i32) != 255) { return 2; }
    var c: u8 = 16u8;
    c = c * 16u8;
    if ((c as i32) != 0) { return 3; }
    var s: S = S { v: 200u8 };
    var h: u8 = s.v + 100u8;
    if ((h as i32) != 44) { return 4; }
    var k: u8 = 100u8;
    k = k + 50u8;
    if ((k as i32) != 150) { return 5; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (sub-i32 arithmetic wraps to width)", code)
	}
}
