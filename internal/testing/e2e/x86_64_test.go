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
)

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

func TestX86_64Arithmetic(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { return 2 + 3 * 4; }`, 14},
		{`function main(): i32 { return 100 - 7 * 8; }`, 44},
		{`function main(): i32 { return 100 / 7; }`, 14},
		{`function main(): i32 { return 100 % 7; }`, 2},
		{`function main(): i32 { let x: i32 = 5; let y: i32 = 7; return x * y; }`, 35},
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

func TestX86_64ControlFlow(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    let sum: i32 = 0;
    let i: i32 = 1;
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
    let a: i32 = classify(0 - 5);
    let b: i32 = classify(0);
    let c: i32 = classify(7);
    return a * 100 + b * 10 + c;
}`, 123},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

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

func TestX86_64ImportAlias(t *testing.T) {
	src := `
import "std/string" as s;
import "std/i32" as nums;
function main(): i32 {
    if (s.repeat_char(120 as char, 3) != "xxx") { return 1; }
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
    if (string.repeat_char(120 as char, 4) != "xxxx") { return 1; }
    if (string.repeat_char(45 as char, 3) != "---") { return 2; }
    if (string.repeat_char(120 as char, 0) != "") { return 3; }
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
    let a: string = "foo";
    let b: string = "barbaz";
    return (a + b).len();
}`, 9},
		{`function main(): i32 { return ("aa" + "bb" + "cc").len(); }`, 6},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

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
    let lhs: Expr = Add(Add { l: 2, r: 3 });
    let rhs: Expr = Lit(Lit { v: 4 });
    let prod: Expr = Mul(Mul { l: eval(lhs), r: eval(rhs) });
    return eval(prod);
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 20 {
		t.Errorf("got %d, want 20 ((2+3)*4)", code)
	}
}

func TestX86_64GenericUnions(t *testing.T) {
	src := `struct Leaf[T] { v: T }
struct Pair[T] { a: T, b: T }
struct Lit { v: i32 }

type Tree[T] = Leaf[T] | Pair[T] | Lit;

function leafOf(t: Tree[i32]): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Pair(p) => { return p.a + p.b; },
        Lit(x) => { return x.v; },
    }
}

function main(): i32 {
    let a: Tree[i32] = Tree.Leaf(Leaf[i32] { v: 4 });
    let b: Tree[i32] = Pair[i32] { a: 5, b: 6 };
    let c: Tree[i32] = Lit { v: 7 };
    return leafOf(a) + leafOf(b) + leafOf(c);
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 22 {
		t.Errorf("got %d, want 22 (4 + 11 + 7)", code)
	}
}

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
    let a: Expr = Add { l: 2, r: 3 };
    let sum: i32 = eval(Lit { v: 5 });
    a = Mul { l: 2, r: sum };
    let built: Expr = mk_add(1, 2);
    return eval(a) + eval(built) + sum;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 18 {
		t.Errorf("got %d, want 18", code)
	}
}

func TestX86_64StringLines(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let lf: string[] = "a\nb\nc".lines();
    if (lf.len() != 3) { return 1; }
    if (lf[0] != "a") { return 2; }
    if (lf[1] != "b") { return 3; }
    if (lf[2] != "c") { return 4; }

    let crlf: string[] = "a\r\nb\r\nc".lines();
    if (crlf.len() != 3) { return 5; }
    if (crlf[0] != "a") { return 6; }
    if (crlf[2] != "c") { return 7; }

    let trail: string[] = "a\nb\n".lines();
    if (trail.len() != 2) { return 8; }

    if (("".lines()).len() != 0) { return 9; }

    let solo: string[] = "\n".lines();
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
    let xs: i32[] = [10, 20, 30];
    return xs[1];
}`, 20},
		{`function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4, 5];
    return xs.len();
}`, 5},
		{`function sum(xs: i32[]): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
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

func TestX86_64Struct(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`struct Point { x: i32, y: i32 }
function main(): i32 {
    let p: Point = Point { x: 10, y: 32 };
    return p.x + p.y;
}`, 42},
		{`struct Person { age: i32, name: string }
function main(): i32 {
    let p: Person = Person { age: 25, name: "Claude" };
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
    let a: f32 = 3.5;
    let b: f32 = 1.5;
    return (a + b) as i32;
}`, 5},
		{`function main(): i32 {
    let a: f32 = 10.0;
    let b: f32 = 3.0;
    return (a / b) as i32;
}`, 3},
		{`function main(): i32 {
    let pi: f64 = 3.14f64;
    let two: f64 = 2.0f64;
    if (pi * two > 6.0f64) { return 42; }
    return 0;
}`, 42},
		{`function main(): i32 {
    let n: i32 = 7;
    let f: f64 = (n as f64) * 1.5f64;
    return f as i32;
}`, 10},
		{`function main(): i32 {
    let x: f32 = 5.5;
    let y: f32 = 0.0 - x;
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
		{"round_tie_up", "function main(): i32 { return __round_f64(41.5) as i32; }", 42},
		{"round_down", "function main(): i32 { return __round_f64(42.4) as i32; }", 42},
		{"round_25", "function main(): i32 { return __round_f64(2.5) as i32; }", 3},
		{"round_neg_tie", "function main(): i32 { return (__round_f64(0.0 - 2.5) as i32) + 45; }", 42},
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

func TestX86_64Transcendentals(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sin_0", "import \"std/f64\"; function main(): i32 { return (0.0).sin() as i32; }", 0},
		{"cos_0", "import \"std/f64\"; function main(): i32 { return (0.0).cos() as i32; }", 1},
		{"sin_halfpi", "import \"std/f64\"; function main(): i32 { let r: f64 = (1.5707963267948966).sin(); if (r > 0.999 && r < 1.001) { return 7; } return 0; }", 7},
		{"cos_pi", "import \"std/f64\"; function main(): i32 { let r: f64 = (3.141592653589793).cos(); if (r > 0.0 - 1.001 && r < 0.0 - 0.999) { return 7; } return 0; }", 7},
		{"exp_0", "import \"std/f64\"; function main(): i32 { return (0.0).exp() as i32; }", 1},
		{"exp_2", "import \"std/f64\"; function main(): i32 { return (2.0).exp() as i32; }", 7},
		{"exp_e", "import \"std/f64\"; function main(): i32 { let r: f64 = (1.0).exp(); if (r > 2.71 && r < 2.72) { return 7; } return 0; }", 7},
		{"log_e", "import \"std/f64\"; function main(): i32 { let r: f64 = (2.718281828459045).log(); if (r > 0.999 && r < 1.001) { return 7; } return 0; }", 7},
		{"log_10", "import \"std/f64\"; function main(): i32 { return (10.0).log() as i32; }", 2},
		{"exp_log_roundtrip", "import \"std/f64\"; function main(): i32 { let r: f64 = ((3.0).exp()).log(); if (r > 2.999 && r < 3.001) { return 7; } return 0; }", 7},
		{"pow_int", "import \"std/f64\"; function main(): i32 { return (2.0).pow(5.0) as i32; }", 32},
		{"pow_3_2", "import \"std/f64\"; function main(): i32 { return (3.0).pow(2.0) as i32; }", 9},
		{"pow_sqrt", "import \"std/f64\"; function main(): i32 { let r: f64 = (2.0).pow(0.5); if (r > 1.41 && r < 1.42) { return 7; } return 0; }", 7},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, code := compileAndRunX86_64(t, c.src)
			if code != c.want {
				t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
			}
		})
	}
}

func TestX86_64HttpHandler(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	src := `
import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("method=" + req.method + " path=" + req.path + " body-len=" + req.body_len().to_string());
}`

	binPath, runner := compileX86_64Bin(t, src)

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
		{"GET /first HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", "method=GET path=/first body-len=0"},
		{"POST /second HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello", "method=POST path=/second body-len=5"},
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

func TestX86_64InstantNow(t *testing.T) {
	_, code := compileAndRunX86_64(t, `
import "std/time";
function main(): i32 {
    let ts: Instant = time.instant_now();
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

func TestX86_64Args(t *testing.T) {
	src := `function main(): i32 {
    let a: string[] = args();
    let i: i32 = 0;
    while (i < a.len()) {
        print(a[i]);
        i = i + 1;
    }
    return a.len();
}`
	binPath, runner := compileX86_64Bin(t, src)
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

func TestX86_64TailCall(t *testing.T) {
	src := `function sum_to(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return sum_to(n - 1, acc + n);
}
function main(): i32 {
    return sum_to(100000, 0);
}`
	binPath, runner := compileX86_64Bin(t, src)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != 80 {
		t.Errorf("sum_to(100000, 0) → exit = %d, want 80", got)
	}
}

func TestX86_64RandomBytes(t *testing.T) {
	out, code := compileAndRunX86_64(t, `function main(): i32 {
    let s: u8[] = random_bytes(16);
    write(string_from_bytes_unchecked(s));
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

func TestX86_64RandomI32(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function main(): i32 {
    let a: i32 = random_i32();
    let b: i32 = random_i32();
    if (a == 0) { return 0; }
    if (a == b) { return 1; }
    return 7;
}`)
	if code != 7 {
		t.Errorf("random_i32: exit = %d, want 7 (0=stuck-zero, 1=non-varying)", code)
	}
}

func TestX86_64StringAsBytes(t *testing.T) {
	if _, code := compileAndRunX86_64(t, `function main(): i32 {
    let b = "ABC".as_bytes();
    return b.len() + (b[0] as i32) + (b[1] as i32) + (b[2] as i32);
}`); code != 201 {
		t.Errorf("inline as_bytes: exit = %d, want 201 (3 + 65+66+67)", code)
	}
	if _, code := compileAndRunX86_64(t, `function main(): i32 {
    let b = "ABCDEFGHIJ".as_bytes();
    return b.len() + (b[9] as i32);
}`); code != 84 {
		t.Errorf("heap as_bytes: exit = %d, want 84 (10 + 'J')", code)
	}
}

func TestX86_64ReadLine(t *testing.T) {
	src := `import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(_)) => { return 1; },
        Ok(None()) => { return 0; },
        Err(_) => { return 2; }
    }
    return -1;
}`
	binPath, runner := compileX86_64Bin(t, src)

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
	runCase("", 0)
	runCase("hello\n", 1)
}

func TestX86_64ClosureChainNoAlloc(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function main(): i32 {
    function answer(): i32 { return 7; }
    let f = answer;
    let x: i32 = f();
    return x;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (chained no-capture alias returns 7)", code)
	}
}

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
    let n: i32 = 10;
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
    let nums: i32[] = [10, 20, 30];
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
    let f = makeAdder(7);
    return f(35);
}`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

func TestX86_64ClosureMultipleInstances(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    let add5 = makeAdder(5);
    let add10 = makeAdder(10);
    return add5(1) + add10(1);
}`
	if _, code := compileAndRunX86_64(t, src); code != 17 {
		t.Errorf("got %d, want 17", code)
	}
}

func TestX86_64ClosureCapturesParamAndVar(t *testing.T) {
	src := `function outer(seed: i32): i32 {
    let bonus: i32 = 100;
    function inner(x: i32): i32 { return x + seed + bonus; }
    return inner(2);
}
function main(): i32 { return outer(40); }`
	if _, code := compileAndRunX86_64(t, src); code != 142 {
		t.Errorf("got %d, want 142", code)
	}
}

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

func TestX86_64LambdaWithBodyLocals(t *testing.T) {
	src := `function main(): i32 {
    let greet = "hi";
    let f = (n: i32): i32 => {
        let sq = n * n;
        let tag = greet + "!";
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

func TestX86_64LambdaCallsMethodOnCapturedString(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    let s: string = "  hi  ";
    let f = (): string => { return s.trim().to_owned(); };
    let got = f();
    if (got == "hi") { return 0; }
    return 1;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (s.trim() inside lambda body)", code)
	}
}

func TestX86_64NestedLambdaUniqueNames(t *testing.T) {
	src := `function main(): i32 {
    let outer = (): i32 => {
        let inner = (): i32 => {
            let x = 21;
            return x * 2;
        };
        let y = inner();
        return y;
    };
    return outer();
}`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

func TestX86_64ClosureMultiCapture(t *testing.T) {
	src := `function make2(a: i32, b: i32): (i32) => i32 {
    function f(x: i32): i32 { return a + b + x; }
    return f;
}
function main(): i32 {
    let h = make2(10, 20);
    return h(12);
}`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

func TestX86_64ClosureCapturesMixedPointers(t *testing.T) {
	src := `function outer(s: string, n: i32): i32 {
    function inner(): i32 { return s.len() + n; }
    return inner();
}
function main(): i32 { return outer("hi", 40); }`
	if _, code := compileAndRunX86_64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

func TestX86_64ClosureCapturesTuple(t *testing.T) {
	src := `function build(): () => i64 {
    let t: (i64, i64) = (1000000000000i64, 2000000000000i64);
    function read(): i64 { return t.0 + t.1; }
    return read;
}
function main(): i32 {
    let f = build();
    if (f() != 3000000000000i64) { return 1; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (closure captures (i64, i64))", code)
	}
}

func TestX86_64ClosureFStringCapture(t *testing.T) {
	src := `
import "std/string";
function makeNamer(name: string): () => string {
    function build(): string { return f"hello, {name}!"; }
    return build;
}
function main(): i32 {
    let f = makeNamer("world");
    if (f() != "hello, world!") { return 1; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got %d, want 0", code)
	}
}

func TestX86_64MutableCapturedVar(t *testing.T) {
	src := `function makeCounter(): () => i32 {
    let count: i32 = 0;
    function tick(): i32 {
        count = count + 1;
        return count;
    }
    return tick;
}
function main(): i32 {
    let c = makeCounter();
    let a: i32 = c();
    let b: i32 = c();
    let d: i32 = c();
    return a + b + d;
}`
	if _, code := compileAndRunX86_64(t, src); code != 6 {
		t.Errorf("got %d, want 6 (counter increments in env)", code)
	}
}

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
    let a = makeAdder(10);
    let ap = makeApplier(a);
    return ap(5);
}`
	if _, code := compileAndRunX86_64(t, src); code != 16 {
		t.Errorf("got %d, want 16", code)
	}
}

func TestX86_64ClosureRecursiveSelfCall(t *testing.T) {
	src := `function makeFact(): (i32) => i32 {
    function fact(n: i32): i32 {
        if (n <= 1) { return 1; }
        return n * fact(n - 1);
    }
    return fact;
}
function main(): i32 {
    let f = makeFact();
    return f(5);
}`
	if _, code := compileAndRunX86_64(t, src); code != 120 {
		t.Errorf("got %d, want 120 (5!)", code)
	}
}

func TestX86_64Map(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"basic_set_get", `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 100);
    m = m.insert(2, 200);
    return m.get_or(2, 0);
}`, 200},
		{"iter_after_delete", `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 10);
    m = m.insert("b", 20);
    m = m.insert("c", 30);
    m = m.remove("b");
    if (m.contains("b")) { return 1; }
    let total: i32 = 0;
    let it = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        total = total + __mapvalue_it;

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
    let x: i32 = 1;
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
    let c: Cell[i32] = cell_new(0);
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
    let n: i32 = 42;
    let s: string = f"n is {n}";
    return s.len();
}`, 7},
		{"interpolated string", `
import "std/i32";
function main(): i32 {
    let who: string = "world";
    let s: string = f"hello, {who}!";
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
    let t: (i32, string, i32) = (1, "two", 3);
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
    let sum: i32 = 0;
    for n in [1, 2, 3, 4, 5] { sum = sum + n; }
    return sum;
}`, 15},
		{"break exits the loop", `function main(): i32 {
    let found: i32 = -1;
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
    let x: Option[i32] = Some(42);
    if let Some(v) = x { return v; }
    return 99;
}`, 42},
		{"None falls through", `function main(): i32 {
    let x: Option[i32] = None;
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

func TestX86_64UsizeAutowiden(t *testing.T) {
	src := `function offset_compute(base: usize, idx: i32, stride: i32): usize {
    return base + idx * stride;
}
function main(): i32 {
    // 0x100000000 — exceeds 32 bits so a wrong truncation would
    // lose the high bit and the low 32 bits would be (base_lo +
    // 4*8) = (0 + 32). With usize-arithmetic the result is
    // 0x100000020 and the low 32 bits are 32 — matches.
    let heap_ptr: usize = 4294967296 as usize;
    let elem: usize = offset_compute(heap_ptr, 4, 8);
    return (elem as i32);
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 32 {
		t.Errorf("got %d, want 32 (low 32 bits of 0x100000020)", code)
	}
}

func TestX86_64UsizeDivRem(t *testing.T) {
	src := `function main(): i32 {
    let x: usize = 5000000000 as usize;
    let q: usize = x / 3;
    let r: usize = x % 3;
    if ((q as i32) != 1666666666) { return 1; }
    if ((r as i32) != 2) { return 2; }
    return 7;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 7 {
		t.Errorf("got %d, want 7 (usize div/rem truncated to 32 bits?)", code)
	}
}

func TestX86_64FloatToUsize(t *testing.T) {
	src := `function main(): i32 {
    let f: f64 = 5000000000.0;
    let u: usize = f as usize;
    if (u == 5000000000 as usize) { return 7; }
    return 1;
}`
	_, code := compileAndRunX86_64(t, src)
	if code != 7 {
		t.Errorf("got %d, want 7 (f64->usize truncated to 32 bits?)", code)
	}
}

func TestX86_64WideScalarMap(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Map[i64, i32]", `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 100);
    return m.get_or(1i64, 0);
}`, 100},
		{"Map[i32, f64]", `
import "core/map";
function main(): i32 {
    let m: Map[i32, f64] = map_new(4);
    m = m.insert(1, 3.14);
    return m.get_or(1, 0.0) as i32;
}`, 3},
		{"Map[i64, string]", `
import "core/map";
function main(): i32 {
    let m: Map[i64, string] = map_new(4);
    m = m.insert(1i64, "hello");
    return (m.get_or(1i64, "")).len();
}`, 5},
		{"Map[string, i64]", `
import "core/map";
function main(): i32 {
    let m: Map[string, i64] = map_new(4);
    m = m.insert("hello", 42i64);
    return m.get_or("hello", 0i64) as i32;
}`, 42},
		{"Map[u64, i32]", `
import "core/map";
function main(): i32 {
    let m: Map[u64, i32] = map_new(4);
    m = m.insert(1u64, 100);
    return m.get_or(1u64, 0);
}`, 100},
		{"distinct high-bit i64 keys", `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(8);
    let k1: i64 = 0i64;
    let k2: i64 = 1i64 << 33i64;
    m = m.insert(k1, 1);
    m = m.insert(k2, 2);
    let v1: i32 = m.get_or(k1, 99);
    let v2: i32 = m.get_or(k2, 99);
    // Sum signals correct separation: v1=1, v2=2 → 3.
    // Coincident-collision would give v1=v2 → 2 or 4.
    return v1 + v2;
}`, 3},
		{"keys() preserves 8-byte values", `
import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 10);
    m = m.insert(1000000000000i64, 20);
    let keys: i64[] = m.keys();
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
    let x: usize = 42;
    return x as i32;
}`, 42},
		{"usize arithmetic", `function main(): i32 {
    let a: usize = 10;
    let b: usize = 32;
    return (a + b) as i32;
}`, 42},
		{"usize as fn param + return", `function dbl(x: usize): usize { return x + x; }
function main(): i32 {
    let n: usize = 21;
    return dbl(n) as i32;
}`, 42},
		{"large value survives on native (> 32 bits)", `function main(): i32 {
    let big: usize = 4294967301 as usize;
    let rt: i64 = big as i64;
    if ((rt >> 32) > 0i64) { return 42; }
    return 1;
}`, 42},
		{"string ptr round-trip through usize", `function main(): i32 {
    let s: string = "hello, " + "world";
    let ptr: usize = s as usize;
    let s2: string = ptr as string;
    return s2.len();
}`, 12},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func compileX86_64InDir(t *testing.T, src string, seed map[string]string) (stdout string, exitCode int, dir string) {
	t.Helper()
	binPath, runner := compileX86_64Bin(t, src)
	dir = t.TempDir()
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	cmd := runX86_64Bin(runner, binPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode(), dir
}

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
	if code != 18 {
		t.Errorf("got %d, want 18 (len of missing-file path via NotFound payload)", code)
	}
}

func TestX86_64ReadFileBytesOk(t *testing.T) {
	src := `function main(): i32 {
    match (read_file_bytes("blob.bin")) {
        Ok(b) => {
            if (b.len() != 4) { return 1; }
            if ((b[0] as i32) != 0) { return 2; }
            if ((b[1] as i32) != 255) { return 3; }
            if ((b[2] as i32) != 128) { return 4; }
            if ((b[3] as i32) != 10) { return 5; }
            return 0;
        },
        Err(_) => { return 6; }
    }
    return 7;
}`
	_, code, _ := compileX86_64InDir(t, src, map[string]string{
		"blob.bin": "\x00\xff\x80\n",
	})
	if code != 0 {
		t.Errorf("got %d, want 0 (1-5=byte mismatch, 6=err path)", code)
	}
}

func TestX86_64ReadFileBytesNotFound(t *testing.T) {
	src := `function main(): i32 {
    match (read_file_bytes("does_not_exist.bin")) {
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
	if code != 18 {
		t.Errorf("got %d, want 18 (len of missing-file path via NotFound payload)", code)
	}
}

func TestX86_64ReadFileInvalidUtf8(t *testing.T) {
	src := `function bad(path: string): i32 {
    match (read_file(path)) {
        Ok(_) => { return 1; },
        Err(e) => {
            match (e) {
                InvalidUtf8(p) => { if (p != path) { return 2; } return 0; },
                _ => { return 3; }
            }
        }
    }
    return 4;
}
function main(): i32 {
    match (read_file("valid.txt")) {
        Ok(s) => { if (s.len() != 15) { return 10; } },
        Err(_) => { return 11; }
    }
    if (bad("bad.bin") != 0) { return 20 + bad("bad.bin"); }
    if (bad("overlong.bin") != 0) { return 30 + bad("overlong.bin"); }
    if (bad("surrogate.bin") != 0) { return 40 + bad("surrogate.bin"); }
    if (bad("toobig.bin") != 0) { return 50 + bad("toobig.bin"); }
    if (bad("truncated.bin") != 0) { return 60 + bad("truncated.bin"); }
    if (bad("stray.bin") != 0) { return 70 + bad("stray.bin"); }
    match (read_file_bytes("bad.bin")) {
        Ok(b) => { if (b.len() != 2) { return 80; } },
        Err(_) => { return 81; }
    }
    return 0;
}`
	_, code, _ := compileX86_64InDir(t, src, map[string]string{
		"valid.txt":     "h\xc3\xa9llo \xe2\x82\xac \xf0\x9f\x99\x82",
		"bad.bin":       "\xff\xfe",
		"overlong.bin":  "\xc0\x80",
		"surrogate.bin": "\xed\xa0\x80",
		"toobig.bin":    "\xf4\x90\x80\x80",
		"truncated.bin": "\xe2\x82",
		"stray.bin":     "a\x80b",
	})
	if code != 0 {
		t.Errorf("got %d, want 0 (1x=valid-file, 2x-7x=vector accepted or misclassified, 8x=bytes sibling)", code)
	}
}

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

func TestX86_64IndirectCall(t *testing.T) {
	_, code := compileAndRunX86_64(t, `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
    let f: (i32, i32) => i32 = add;
    return f(20, 22);
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (indirect call through function value)", code)
	}
}

func TestX86_64SliceMake(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i32 slice read", `function main(): i32 {
    let arr: i32[] = [10, 20, 30, 40, 50];
    let s: [i32] = arr[1:4];
    return s[1];
}`, 30},
		{"u8 slice read", `function main(): i32 {
    let arr: u8[] = [10, 20, 30, 40, 50];
    let s: [u8] = arr[1:4];
    return s[1] as i32;
}`, 30},
		{"i64 slice read", `function main(): i32 {
    let arr: i64[] = [(1i64 << 40), (1i64 << 41), (1i64 << 42)];
    let s: [i64] = arr[1:3];
    return (s[0] >> 41) as i32;
}`, 1},
		{"len(slice)", `function main(): i32 {
    let arr: i32[] = [1, 2, 3, 4, 5];
    let s: [i32] = arr[1:4];
    return s.len();
}`, 3},
	} {
		_, code := compileAndRunX86_64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

func TestX86_64EprintExit(t *testing.T) {
	out, code := compileAndRunX86_64(t, `function main(): i32 {
    eprint("oops");
    exit(7);
    return 99;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (exit(7) should not fall through to return 99)", code)
	}
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
    let x: f32 = 1.0;
    let b: i32 = f32_bits(x);
    let y: f32 = f32_from_bits(b);
    if (y == x) { return 0; }
    return 1;
}`, 0},
		{"round-trip 3.14", `function main(): i32 {
    let x: f32 = 3.14;
    let b: i32 = f32_bits(x);
    let y: f32 = f32_from_bits(b);
    if (y == x) { return 0; }
    return 1;
}`, 0},
		{"1.0 bits = 0x3F800000", `function main(): i32 {
    if (f32_bits(1.0) == 1065353216) { return 0; }
    return 1;
}`, 0},
		{"sign-bit preserved through round-trip", `function main(): i32 {
    let neg: f32 = 0.0 - 1.0;
    let b: i32 = f32_bits(neg);
    let back: f32 = f32_from_bits(b);
    if (back == neg) { return 0; }
    return 1;
}`, 0},
		{"-0.0 bits = sign bit", `function main(): i32 {
    let bits_u: u32 = f32_bits(-0.0) as u32;
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
            match (w.write("first")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_appender("ap.txt")) {
        Ok(w) => {
            match (w.write("-second")) { Err(_) => { return 4; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 5; }, Ok(_) => {} }
        },
        Err(_) => { return 6; }
    }
    match (read_file("ap.txt")) {
        Ok(s) => { write(s); return 0; },
        Err(_) => { return 7; }
    }
    return 0 - 1;
}`, "first-second", 0},
		{"open_reader_sso_path", `import "std/string";
function main(): i32 {
    match (open_writer("sso.txt")) {
        Ok(w) => {
            match (w.write("hi")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
        },
        Err(_) => { return 3; }
    }
    let p: string = slice_unchecked("x=sso.txt", 2, 9) + "";
    match (open_reader(p)) {
        Ok(r) => {
            match (r.read_chunk(8)) {
                Ok(s) => { write(s); r.close(); return 0; },
                Err(e) => { return 4; }
            }
        },
        Err(e) => { return 5; }
    }
    return 0 - 1;
}`, "hi", 0},
		{"reader_read_chunk", `function main(): i32 {
    match (open_writer("rc.txt")) {
        Ok(w) => {
            match (w.write("hello world")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_reader("rc.txt")) {
        Ok(r) => {
            match (r.read_chunk(5)) { Ok(s) => { write(s); write(":"); }, Err(_) => { return 4; } }
            match (r.read_chunk(20)) { Ok(s) => { write(s); }, Err(_) => { return 5; } }
            match (r.read_chunk(20)) { Ok(s) => { if (s.len() > 0) { return 6; } return 0; }, Err(_) => { return 8; } }
        },
        Err(_) => { return 7; }
    }
    return 0 - 1;
}`, "hello: world", 0},
		{"streaming_roundtrip_lines", `import "std/io";

function main(): i32 {
    match (open_writer("rt.txt")) {
        Ok(w) => {
            match (w.write("line 1\n")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.write("line 2\n")) { Err(_) => { return 2; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 3; }, Ok(_) => {} }
        },
        Err(_) => { return 4; }
    }
    match (open_reader("rt.txt")) {
        Ok(r) => {
            match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 5; } }
            match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 6; } }
            match (io.read_line(r)) { Ok(None()) => {}, _ => { return 7; } }
            match (r.close()) { Err(_) => { return 8; }, Ok(_) => {} }
            return 0;
        },
        Err(_) => { return 9; }
    }
    return 0 - 1;
}`, "line 1\nline 2\n", 0},
		{"open_exclusive_refuses_existing", `function main(): i32 {
    match (open_exclusive("ex.txt")) {
        Ok(w) => {
            match (w.write("new")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_exclusive("ex.txt")) {
        Ok(_) => { return 4; },
        Err(e) => {
            match (e) {
                AlreadyExists(p) => { write("exists:" + p); },
                _ => { return 5; }
            }
        }
    }
    match (read_file("ex.txt")) {
        Ok(s) => { write(":" + s); return 0; },
        Err(_) => { return 6; }
    }
    return 0 - 1;
}`, "exists:ex.txt:new", 0},
		{"open_exclusive_is_not_world_readable", `function main(): i32 {
    // The whole point of the primitive is a file only its creator holds
    // (#8776), and split -n writes the user's piped input into one. The
    // assertion is that nobody else can read it, NOT that the mode is
    // exactly 0600: a umask can only clear bits, so no-group-no-other
    // holds for a 0600 request under every umask while a 0644 one fails
    // it under all of them. Exact equality would pin the runner's umask.
    // A non-zero exit IS the leaked permission bits: 36 is the group- and
    // other-read of a 0644 create.
    match (open_exclusive("mode.txt")) {
        Ok(w) => { match (w.close()) { Err(_) => { return 1; }, Ok(_) => {} } },
        Err(_) => { return 2; }
    }
    match (stat("mode.txt")) {
        Ok(st) => {
            let others: i32 = (st.mode & (63 as u32)) as i32;
            if (others != 0) { return others; }
            write("private");
            return 0;
        },
        Err(_) => { return 3; }
    }
    return 0 - 1;
}`, "private", 0},
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

func TestX86_64FeatureParity(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"defer_basic", `
function inner(trace: Cell[i32]): i32 {
    trace.set(trace.get() * 10 + 1);
    defer trace.set(trace.get() * 10 + 3);
    defer trace.set(trace.get() * 10 + 4);
    trace.set(trace.get() * 10 + 2);
    return 42;
}
function main(): i32 {
    let trace: Cell[i32] = cell_new(0);
    let r: i32 = inner(trace);
    if (r != 42) { return 1; }
    // Body steps 1 and 2, then the defers in LIFO order: 4, then 3.
    if (trace.get() != 1243) { return 2; }
    return 0;
}`},
		{"fstring_interp", `
import "std/i32";
function main(): i32 {
    let x: i32 = 42;
    let s: string = f"x is {x}";
    if (s.len() == 7) { return 0; }
    return 1;
}`},
		{"for_each_array", `function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4, 5];
    let sum: i32 = 0;
    for x in xs { sum = sum + x; }
    if (sum == 15) { return 0; }
    return 1;
}`},
		{"if_let_match", `function main(): i32 {
    let o: Option[i32] = Some(42);
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
    let p = divmod(17, 5);
    if (p.0 == 3 && p.1 == 2) { return 0; }
    return 1;
}`},
		{"generic_infer_from_arg", `function id[T](x: T): T { return x; }
function main(): i32 {
    let a = id(42);
    let b = id(7);
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

func TestX86_64NoPreludeStdlibImports(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"i32_string_cycle", `
import "std/i32";
function main(): i32 {
    let s: string = (42).to_string_padded(6);
    if (s == "000042") { return 0; }
    return 1;
}`},
		{"array_method_chain", `
import "std/array";
function main(): i32 {
    let xs: i32[] = [0 - 3, 4, 0 - 1];
    let ys = xs.abs_each();
    if (ys[0] + ys[1] + ys[2] == 8) { return 0; }
    return 1;
}`},
		{"qualified_int_call", `
import "core/int";
function main(): i32 {
    let s: string = int.int_to_string_radix(255, 16);
    if (s == "ff") { return 0; }
    return 1;
}`},
		{"mixed_stdlib", `
import "std/i32";
import "std/string";
import "std/array";
function main(): i32 {
    let s: string = (0 - 42).to_string();
    if (s != "-42") { return 1; }
    let strs: string[] = ["b", "a", "c"];
    let joined: string = strs.join(",");
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

func TestX86_64RcDropEnumPayload(t *testing.T) {
	fresh := `struct W { a: i32[] }
struct V2 { b: i32 }
type U = W | V2;
function mk(): U { return W { a: [1, 2, 3] }; }
function build(): i32 {
    let u: U = mk();
    match (u) { W(w) => { return w.a[1] + __rc_underflow_count(); }, V2(x) => { return x.b; } }
    return 0 - 1;
}
function main(): i32 { return build() - 2; }`
	if _, code := compileAndRunX86_64(t, fresh); code != 0 {
		t.Errorf("got %d, want 0 (union payload drop: value 2, 0 underflow)", code)
	}

	aliased := `struct W { a: i32 }
struct V2 { b: i32 }
type U = W | V2;
function main(): i32 {
    let w: W = W { a: 7 };
    let u: U = w;
    return w.a + __rc_underflow_count() - 7;
}`
	if _, code := compileAndRunX86_64(t, aliased); code != 0 {
		t.Errorf("got %d, want 0 (aliased struct widened to union: 0 underflow)", code)
	}

	nonUniform := `enum E { Arr(i32[]), Num(i32) }
function main(): i32 {
    let e: E = Arr([1, 2, 3]);
    let f: E = Num(9);
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

func TestX86_64ArrayPushAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20];
    xs = xs.append(30);
    let ys = xs;
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

func TestX86_64ArrayIndexSetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys = xs;
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

func TestX86_64ArrayIndexSetU8Stride(t *testing.T) {
	src := `function main(): i32 {
    let buf: u8[] = __alloc_u8(4);
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

func TestX86_64ArrayIndexSetStructField(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    let s: State = State{items: [10, 20, 30]};
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

func TestX86_64ArrayIndexSetStructFieldAliasedCopies(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    let arr: i32[] = [10, 20, 30];
    let s: State = State{items: arr};
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

func TestX86_64ArrayIndexSetNestedStructField(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    let o: Outer = Outer{inner: Inner{items: [10, 20, 30]}};
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

func TestX86_64ArrayIndexSetNestedStructFieldAliasedCopies(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    let arr: i32[] = [10, 20, 30];
    let o: Outer = Outer{inner: Inner{items: arr}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (arr[1] != 20) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased a.b.field[i]=v must copy)", code)
	}
}

func TestX86_64ArrayIndexSetMat(t *testing.T) {
	src := `function main(): i32 {
    let mat: i32[][] = [[1, 2, 3], [4, 5, 6]];
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

func TestX86_64ArrayIndexSetMatInnerAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let mat: i32[][] = [[1, 2], [3, 4]];
    let inner = mat[0];
    mat = mat.with(0, mat[0].with(1, 999));
    if (inner[1] != 2) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v with aliased inner must copy)", code)
	}
}

func TestX86_64MapSetReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
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

func TestX86_64ArrayIndexSetObjMatInnerAliasedCopies(t *testing.T) {
	src := `struct State { mat: i32[][] }
function main(): i32 {
    let inner: i32[] = [1, 2, 3];
    let s: State = State{mat: [inner, [4, 5, 6]]};
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

func TestX86_64ArraySetSelfAssign(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
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

func TestX86_64ArraySetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr.with must copy)", code)
	}
}

func TestX86_64MapDeleteReturnsMapBool(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    let hadb: boolean = m.contains("b");
    let mb = m.remove("b");
    if (!hadb) { return 1; }
    m = mb;
    if (m.remove("z").len() != m.len())  { return 2; }
    let ok: boolean = m.contains("a");
    let m2 = m.remove("a");
    if (!ok) { return 3; }
    if (m2.contains("a")) { return 4; }
    if (!m2.contains("c")) { return 5; }
    if (m2.len() != 1) { return 6; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.delete returns (Map, bool))", code)
	}
}

func TestX86_64MapClearReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("x", 10);
    m = m.insert("y", 20);
    if (m.len() != 2) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.contains("x")) { return 3; }
    m = m.insert("z", 99);
    if (m.len() != 1) { return 4; }
    if (m.get_or("z", 0) != 99) { return 5; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.clear returns Map)", code)
	}
}

func TestX86_64MapSetAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);                 // in-place (rc==1)
    let m2 = m1;                    // alias → rc=2
    m2 = m2.insert("a", 999);          // rc>1 → copy; m1 unchanged
    if (m1.get_or("a", 0) != 1)   { return 1; }
    if (m2.get_or("a", 0) != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.set must copy)", code)
	}
}

func TestX86_64MapDeleteClearAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    let m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    m1 = m1.insert("b", 2);
    let m2 = m1;                       // alias → rc=2
    let ok: boolean = m2.contains("a");
    let m3 = m2.remove("a");     // rc>1 → copy; m1/m2 intact
    if (!ok)            { return 1; }
    if (m1.len() != 2)  { return 2; }  // original keeps "a"
    if (!m1.contains("a"))   { return 3; }
    if (m3.len() != 1)  { return 4; }  // copy dropped "a"
    if (m3.contains("a"))    { return 5; }
    let m4 = m1;                       // alias → rc=2
    m4 = m4.cleared();                   // rc>1 → copy; m1 intact
    if (m1.len() != 2)  { return 6; }
    if (m4.len() != 0)  { return 7; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.delete/clear must copy)", code)
	}
}

func TestX86_64TupleStructElem(t *testing.T) {
	src := `struct Inner { x: i32, y: i32 }
function main(): i32 {
    let t: (i32, Inner) = (1, Inner { x: 2, y: 3 });
    if (t.0 != 1) { return 1; }
    let inner: Inner = t.1;
    if (inner.x != 2) { return 2; }
    if (inner.y != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct in tuple slot)", code)
	}
}

func TestX86_64TupleArrayElem(t *testing.T) {
	src := `function main(): i32 {
    let t: (i32, i32[]) = (1, [10, 20, 30]);
    if (t.0 != 1) { return 1; }
    let arr: i32[] = t.1;
    if (arr.len() != 3) { return 2; }
    if (arr[0] != 10) { return 3; }
    if (arr[2] != 30) { return 4; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (array in tuple slot)", code)
	}
}

func TestX86_64TupleNestedTuple(t *testing.T) {
	src := `function main(): i32 {
    let t: (i32, (i32, i32)) = (1, (2, 3));
    let (a, b) = t;
    if (a != 1) { return 1; }
    let (c, d) = b;
    if (c != 2) { return 2; }
    if (d != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (nested tuple destructure)", code)
	}
}

func TestX86_64LexerChainedTupleNumericAccess(t *testing.T) {
	src := `function main(): i32 {
    let t: (i32, (i32, i32)) = (1, (2, 3));
    if (t.0 != 1) { return 1; }
    if (t.1.0 != 2) { return 2; }
    if (t.1.1 != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (chained t.1.0 numeric access)", code)
	}
}

func TestX86_64EmptyMapDestinationInference(t *testing.T) {
	src := `
import "core/map";
function take(m: Map[string, i32]): i32 { return m.len(); }
function mkEmpty(): Map[i32, string] { return Map {}; }
function main(): i32 {
    let a: Map[string, i32] = Map {};
    if (a.len() != 0) { return 1; }
    a = a.insert("k", 42);
    if (a.get_or("k", 0) != 42) { return 2; }
    let b: Map[i32, string] = Map {};
    if (b.len() != 0) { return 3; }
    b = b.insert(7, "hello");
    if (!b.contains(7)) { return 4; }
    if (take(Map {}) != 0) { return 5; }
    let r = mkEmpty();
    if (r.len() != 0) { return 6; }
    let d: Map[i32, i32] = Map {};
    if (d.len() != 0) { return 7; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (empty Map literal inherits K/V from destination)", code)
	}
}

func TestX86_64EnumVariantInTuple(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function main(): i32 {
    let t: (i32, Color) = (1, Green);
    if (t.0 != 1) { return 1; }
    match (t.1) {
        Red => { return 2; },
        Green => { },
        Blue => { return 3; }
    }
    let u: (i32, Option[i32]) = (5, Some(42));
    if (u.0 != 5) { return 4; }
    match (u.1) {
        Some(v) => { if (v != 42) { return 5; } },
        None => { return 6; }
    }
    let w: (Color, i32) = (Blue, 99);
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

func TestX86_64MapPointerShapedValues(t *testing.T) {
	src := `
import "core/map";
struct P { x: i32, y: i32 }
function main(): i32 {
    let mt: Map[string, (i32, i32)] = Map {};
    mt = mt.insert("a", (3, 4));
    match (mt.get("a")) {
        Some(p) => { if (p.0 + p.1 != 7) { return 1; } },
        None => { return 2; }
    }
    let ms: Map[string, P] = Map {};
    ms = ms.insert("a", P { x: 3, y: 4 });
    match (ms.get("a")) {
        Some(s) => { if (s.x + s.y != 7) { return 3; } },
        None => { return 4; }
    }
    let ma: Map[i32, i32[]] = Map {};
    ma = ma.insert(1, [10, 20, 30]);
    match (ma.get(1)) {
        Some(arr) => { if (arr[0] + arr[2] != 40) { return 5; } },
        None => { return 6; }
    }
    let mi: Map[string, i32] = Map {};
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

func TestX86_64StructTupleFieldAccess(t *testing.T) {
	src := `struct Rec { pos: (i32, i32), name: string }
struct Nested { t: (i32, (i32, i32)) }
function main(): i32 {
    let r: Rec = Rec { pos: (3, 4), name: "p" };
    if (r.pos.0 != 3) { return 1; }
    if (r.pos.1 != 4) { return 2; }
    let n: Nested = Nested { t: (1, (2, 3)) };
    if (n.t.0 != 1) { return 3; }
    if (n.t.1.0 != 2) { return 4; }
    if (n.t.1.1 != 3) { return 5; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct tuple-field chained access)", code)
	}
}

func TestX86_64UnsignedComparison(t *testing.T) {
	src := `function main(): i32 {
    let big: u32 = 4294967295u32;
    if (!(big > 0u32)) { return 1; }
    if (!(big > 1000000u32)) { return 2; }
    if (big < 5u32) { return 3; }
    if (!(big >= 4294967295u32)) { return 4; }
    if (big <= 100u32) { return 5; }
    let b64: u64 = 18446744073709551615u64;
    if (!(b64 > 9u64)) { return 6; }
    if (b64 < 9u64) { return 7; }
    let u: u8 = 200u8;
    if (!(u > 100u8)) { return 8; }
    let i: u32 = 4294967293u32;
    let c: i32 = 0;
    while (i > 4294967290u32) { c = c + 1; i = i - 1u32; }
    if (c != 3) { return 9; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (unsigned comparison condition codes)", code)
	}
}

func TestX86_64UnaryMinusWideTypes(t *testing.T) {
	src := `function main(): i32 {
    let a: i64 = -5i64;
    if (a != 0i64 - 5i64) { return 1; }
    let b: f64 = -5.0;
    if (!(b < 0.0)) { return 2; }
    let c: f64 = -b;
    if (c != 5.0) { return 3; }
    let f: f32 = -2.5f32;
    if (!(f < 0.0f32)) { return 4; }
    let z: f64 = -0.0;
    if (f64_bits(z) == 0i64) { return 5; }
    let g: i64 = 10i64 + -3i64;
    if (g != 7i64) { return 6; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (unary minus on wide / non-i32 types)", code)
	}
}

func TestX86_64ScientificNotation(t *testing.T) {
	src := `function main(): i32 {
    let a: f64 = 1e3;
    if (a != 1000.0) { return 1; }
    let b: f64 = 1.5e3;
    if (b != 1500.0) { return 2; }
    let c: f64 = 1500.0e-3;
    if (c != 1.5) { return 3; }
    let d: f64 = 1.5e+3;
    if (d != 1500.0) { return 4; }
    let e: f64 = 2.5E2;
    if (e != 250.0) { return 5; }
    let f: f32 = 1.5e2f32;
    if (f != 150.0f32) { return 6; }
    let big: f64 = 1.8e19;
    if (!(big > 1.7e19)) { return 7; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (scientific-notation float literals)", code)
	}
}

func TestX86_64SubI32ArithmeticWraps(t *testing.T) {
	src := `struct S { v: u8 }
function main(): i32 {
    let a: u8 = 255u8;
    a = a + 1u8;
    if ((a as i32) != 0) { return 1; }
    let b: u8 = 0u8;
    b = b - 1u8;
    if ((b as i32) != 255) { return 2; }
    let c: u8 = 16u8;
    c = c * 16u8;
    if ((c as i32) != 0) { return 3; }
    let s: S = S { v: 200u8 };
    let h: u8 = s.v + 100u8;
    if ((h as i32) != 44) { return 4; }
    let k: u8 = 100u8;
    k = k + 50u8;
    if ((k as i32) != 150) { return 5; }
    return 0;
}`
	if _, code := compileAndRunX86_64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (sub-i32 arithmetic wraps to width)", code)
	}
}
