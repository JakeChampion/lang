package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostSemanticProduction(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, prog := range semProductionPrograms {
		t.Run(prog.name, func(t *testing.T) {
			compiler := fernBin
			if prog.runtimeFloor {
				compiler = buildRuntimeCLI(t).bin
			}
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(prog.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					if prog.nativeOnly && target == "wasm32-wasi" {
						t.Skip("the program reaches builtins the wasi profile does not grant")
					}
					want := prog.want
					if target == "wasm32-wasi" && prog.wasm != "" {
						want = prog.wasm
					}
					got, report, leak := semCompileRun(t, gcc, runner, compiler, stdlibRoot, src, target, prog.stdin)
					if got != want {
						t.Fatalf("answered %q, want %q\nreport: %s", got, want, report)
					}
					for _, line := range prog.reports {
						if target != "wasm32-wasi" && !strings.Contains(report, line) {
							t.Fatalf("FERN_SEM_IR did not report %q:\n%s", line, report)
						}
					}
					semLeaks(t, target, leak, prog.leaks)
					if n := semProducedCount(t, report); n < prog.atLeast {
						t.Fatalf("produced %d declarations, want at least %d:\n%s", n, prog.atLeast, report)
					}
				})
			}
		})
	}
}

func semLeaks(t *testing.T, target string, leak, want int) {
	t.Helper()
	if target == "x86-64-sanitize" && leak != want {
		t.Fatalf("the produced bodies left %d bytes held at exit, want %d", leak, want)
	}
}

func semProducedCount(t *testing.T, report string) int {
	t.Helper()
	const marker = "module: produced "
	at := strings.Index(report, marker)
	if at < 0 {
		t.Fatalf("no production tally in the report:\n%s", report)
	}
	rest := report[at+len(marker):]
	n, err := strconv.Atoi(rest[:strings.Index(rest, " ")])
	if err != nil {
		t.Fatalf("unreadable tally %q: %v", rest, err)
	}
	return n
}

func semCompileRun(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, src, target string, stdin string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "prog")
	sanitize := target == "x86-64-sanitize"
	if sanitize {
		target = "x86-64-linux"
	}
	args := []string{"-target", target, "-o", out, src, stdlibRoot}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", "-o", out, src, stdlibRoot}
	}
	cmd := runX86_64Bin(runner, fernBin, args...)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR_REPORT=1")
	if sanitize {
		cmd.Env = append(cmd.Env, "FERN_SANITIZE=1")
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile (%s): %v\n%s", target, err, stderr.String())
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatal(err)
		}
		run = runX86_64Bin(runner, out)
	case "arm64-linux":
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatal(err)
		}
		_, qemu := arm64Tooling(t)
		run = runArm64Bin(qemu, out)
	default:
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		run = exec.Command("wasmtime", "run", out)
	}
	var runErr strings.Builder
	run.Stderr = &runErr
	run.Stdin = strings.NewReader(stdin)
	stdout, _ := run.Output()
	exit := strconv.Itoa(run.ProcessState.ExitCode())
	if target == "wasm32-wasi" && strings.Contains(runErr.String(), "exit with invalid exit status") {
		exit = "out-of-range"
	}
	return exit + "|" + string(stdout), stderr.String(), sanitizerLeak(t, runErr.String())
}

func sanitizerLeak(t *testing.T, out string) int {
	t.Helper()
	const marker = "fern-sanitizer: leak "
	at := strings.Index(out, marker)
	if at < 0 {
		return 0
	}
	rest := out[at+len(marker):]
	n, err := strconv.Atoi(rest[:strings.Index(rest, " ")])
	if err != nil {
		t.Fatalf("unreadable sanitizer leak line %q: %v", rest, err)
	}
	return n
}

var semProductionPrograms = []struct {
	name         string
	atLeast      int
	reports      []string
	want         string
	wasm         string
	leaks        int
	stdin        string
	nativeOnly   bool
	runtimeFloor bool
	src          string
}{
	{name: "string-append-grows-in-place", atLeast: 51, want: "0|700000 573879 605 307 264369 400 401 114 604 8192 692785 501 301 q\n", src: `
import "std/i32";

struct Box { s: string }

function grown(n: i32, piece: string): string {
    let s: string = "";
    for i in 0..n { s = s + piece; }
    return s;
}

function suffixed(a: string): string {
    a = a + "!";
    return a;
}

function sum(s: string): i32 {
    let h: i32 = 0;
    for i in 0..s.len() { h = (h * 31 + (s[i] as i32)) % 1000003; }
    return h;
}

function main(): i32 {
    let big: string = grown(70000, "abcdefghij");
    let s: string = "start";
    let kept: string = "";
    for i in 0..300 {
        s = s + "xy";
        if (i == 150) { kept = s; }
    }
    let base: string = grown(400, "q");
    let other: string = suffixed(base);
    let acc: string = grown(300, "mn");
    let vlen: i32 = 0;
    match (acc[0:4]) {
        Some(w) => {
            acc = acc + "tail";
            vlen = w.len() + (w[3] as i32);
        },
        None => {}
    }
    let d: string = "ab";
    for i in 0..12 { d = d + d; }
    let b = Box { s: "f" };
    for i in 0..500 { b = Box { s: b.s + "g" }; }
    let xs: string[] = ["p", "q"];
    for i in 0..300 { xs = xs.with(0, xs[0] + "z"); }
    print(f"{big.len()} {sum(big)} {s.len()} {kept.len()} {sum(kept)} {base.len()} {other.len()} {vlen} {acc.len()} {d.len()} {sum(d)} {b.s.len()} {xs[0].len()} {xs[1]}");
    return 0;
}
`},
	{name: "a-map-with-a-wide-column-routes", atLeast: 175, want: "0|a 299 300 true 192629283898350 2093 7 -2 false true 7 -9 46938\nf 20 395 19.5 1.5 21.5 2 3.75 9\nu 40 0 14388460377493430 s7\nb 256 33410 3\nc 10 9 true 245\n", src: `
import "core/map";
import "std/i32";
import "std/i64";
import "std/u64";
import "std/f64";

struct P { x: i32 }

function f64s(xs: f64[]): f64 {
  let t: f64 = 0.0;
  for x in xs { t = t + x; }
  return t;
}

function main(): i32 {
  let a: Map[i64, i64] = Map {};
  let i: i32 = 0;
  while (i < 300) { a = a.insert((i as i64) * 4294967311, (i as i64) * 3); i = i + 1; }
  while (i < 600) { a = a.insert(((i - 300) as i64) * 4294967311, 7); i = i + 1; }
  let kept = a;
  let gone: boolean = a.contains(4294967311 * 5);
  let a2 = a.remove(4294967311 * 5);
  a = a2;
  let ks: i64 = 0;
  for k in kept.keys() { ks = ks + k; }
  let vs: i64 = 0;
  for v in a.values() { vs = vs + v; }
  let hit: i64 = 0;
  match (a.get(4294967311 * 9)) { Some(v) => { hit = v; }, None => { hit = 0 - 1; } }
  let miss: i64 = 0;
  match (a.get(12345)) { Some(v) => { miss = v; }, None => { miss = 0 - 2; } }
  let it: i64 = 0;
  for (k, v) in a { it = it + k / 4294967311 + v; }
  print(f"a {a.len()} {kept.len()} {gone} {ks} {vs} {hit} {miss} {a.contains(4294967311 * 5)} {kept.contains(4294967311 * 5)} {a.get_or(4294967311 * 8, 0 - 9)} {a.get_or(1, 0 - 9)} {it}");

  let f: Map[i32, f64] = Map {};
  i = 0;
  while (i < 50) { f = f.insert(i % 20, (i as f64) * 0.5); i = i + 1; }
  let g: Map[string, f32] = Map {};
  g = g.insert("x", 1.25);
  g = g.insert("y", 2.5);
  g = g.insert("x", 3.75);
  let fo: f64 = 0.0;
  match (f.get(3)) { Some(v) => { fo = v; }, None => {} }
  let gx: f32 = g.get_or("x", 0.0);
  let gz: f32 = g.get_or("z", 9.0);
  print(f"f {f.len()} {f64s(f.values())} {f.get_or(19, 0.0)} {f.get_or(99, 1.5)} {fo} {g.len()} {gx} {gz}");

  let u: Map[u64, string] = Map {};
  i = 0;
  while (i < 40) { u = u.insert((i as u64) * 18446744073709551 as u64, f"s{i}"); i = i + 1; }
  let u2 = u.cleared();
  let uk: u64 = 0;
  for k in u.keys() { uk = uk + k / (1000 as u64); }
  let u7: string = u.get_or(18446744073709551 as u64 * 7, "none");
  print(f"u {u.len()} {u2.len()} {uk} {u7}");

  let b: Map[u8, u8] = Map {};
  i = 0;
  while (i < 300) { b = b.insert((i % 256) as u8, (i % 7) as u8); i = i + 1; }
  let bs: i32 = 0;
  for k in b.keys() { bs = bs + (k as i32); }
  for v in b.values() { bs = bs + (v as i32); }
  print(f"b {b.len()} {bs} {b.get_or(255 as u8, 0 as u8)}");

  let c: Map[i64, P] = Map {};
  i = 0;
  while (i < 30) { c = c.insert((i % 10) as i64 - 5, P { x: i }); i = i + 1; }
  let c2 = c;
  let cg: boolean = c2.contains(0 - 5);
  let c3 = c2.remove(0 - 5);
  let px: i32 = 0;
  for v in c.values() { px = px + v.x; }
  print(f"c {c.len()} {c3.len()} {cg} {px}");
  return 0;
}
`},
	{name: "element-read-outlives-the-array-write", atLeast: 2, want: "6||a|g|bb|ee|ccc|dddd|ffffff|\n", src: semHeldElementSource},
	{name: "field-read-outlives-the-record-move", atLeast: 2, want: "0|200|a|900|\n", src: semHeldFieldSource},
	{name: "async-function", atLeast: 4, want: "16|4 squares ending 9\n\n", src: `
import "std/i32";
async function compute(): i32 { return 7; }
pub async function squares(n: i32): i32[] {
    let out: i32[] = [];
    for i in 0..n { out = out.append(i * i); }
    return out;
}
async function label(xs: i32[]): string { return f"{xs.len()} squares ending {xs[xs.len() - 1]}"; }
function main(): i32 {
    let xs: i32[] = squares(4);
    print(label(xs) + "\n");
    return compute() + xs[3];
}
`},
	{name: "source-lambda-returning-callable", atLeast: 3, want: "42|", src: `
function main(): i32 {
    let mk: () => ((i32) => i32) = ((): ((i32) => i32) => { let g: (i32) => i32 = ((y: i32) => y); return g; });
    let f: (i32) => i32 = mk();
    return f(42);
}
`},
	{name: "source-lambda-returning-callable-bare", atLeast: 3, want: "42|", src: `
function main(): i32 {
    let mk: () => ((i32) => i32) = ((): (i32) => i32 => { let g: (i32) => i32 = ((y: i32) => y); return g; });
    let f: (i32) => i32 = mk();
    return f(42);
}
`},
	{name: "generic-struct-inside-a-callable-spelling", atLeast: 6, want: "11|", src: `
struct Slot[T] { v: T }

function slot_of(n: i32): Slot[i32] { return Slot[i32] { v: n }; }

function take(p: ((i32) => Slot[i32], i32)): i32 {
    let f: (i32) => Slot[i32] = p.0;
    let s: Slot[i32] = f(p.1);
    return s.v;
}

function unwrap(q: ((Slot[i32]) => i32, Slot[i32])): i32 {
    let g: (Slot[i32]) => i32 = q.0;
    return g(q.1);
}

function main(): i32 {
    let p: ((i32) => Slot[i32], i32) = (((b: i32): Slot[i32] => slot_of(b)), 4);
    let q: ((Slot[i32]) => i32, Slot[i32]) = (((s: Slot[i32]): i32 => s.v), Slot[i32] { v: 7 });
    return take(p) + unwrap(q);
}
`},
	{name: "a-lambda-declaring-a-callable-result-over-a-generic-struct", atLeast: 5, want: "15|", src: `
struct Slot[T] { v: T }

function slot_of(n: i32): Slot[i32] {
    return Slot[i32] { v: n };
}

function outer(): (i32) => (i32) => Slot[i32] {
    return (a: i32): (i32) => Slot[i32] => ((b: i32): Slot[i32] => slot_of(a + b));
}

function main(): i32 {
    let f: (i32) => (i32) => Slot[i32] = outer();
    let g: (i32) => Slot[i32] = f(10);
    let s: Slot[i32] = g(5);
    return s.v;
}
`},
	{name: "if-expr-boolean-branches", atLeast: 1, want: "1|", src: `
function main(): i32 {
    let v: boolean = if (true) { false } else { true };
    let w: boolean = if (v) { 1 < 2 } else { 2 < 1 };
    return if (v) { 0 } else { if (w) { 42 } else { 1 } };
}
`},
	{name: "if-expr-lambda-arms-annotated", atLeast: 4, want: "42|", src: `
function main(): i32 {
    let f: (i32) => i32 = if (true) { ((x: i32): i32 => x) } else { ((y: i32): i32 => y + 1) };
    return f(42);
}
`},
	{name: "match-expr-lambda-arms-annotated", atLeast: 4, want: "42|", src: `
enum Pick { A, B }
function main(): i32 {
    let p: Pick = Pick.A;
    let f: (i32) => i32 = match (p) { A => ((x: i32): i32 => x), B => ((y: i32): i32 => y + 1) };
    return f(42);
}
`},
	{name: "if-expr-lambda-arms-unannotated", atLeast: 4, want: "42|", src: `
function main(): i32 {
    let f: (i32) => i32 = if (true) { ((x: i32) => x) } else { ((y: i32) => y + 1) };
    return f(42);
}
`},
	{name: "if-expr-function-name-arms", atLeast: 6, want: "41|", src: `
function inc(x: i32): i32 { return x + 1; }
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { let c: boolean = true; let f: (i32) => i32 = (if (c) { inc } else { dbl }); return f(40); }
`},
	{name: "if-expr-function-name-arms-nested", atLeast: 9, want: "80|", src: `
function inc(x: i32): i32 { return x + 1; }
function dbl(x: i32): i32 { return x * 2; }
function neg(x: i32): i32 { return 0 - x; }
function main(): i32 {
    let a: i32 = 2;
    let f: (i32) => i32 = (if (a == 1) { inc } else { (if (a == 2) { dbl } else { neg }) });
    return f(40);
}
`},
	{name: "if-expr-function-name-arms-assigned-and-passed", atLeast: 8, want: "41|", src: `
function inc(x: i32): i32 { return x + 1; }
function dbl(x: i32): i32 { return x * 2; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    let c: boolean = false;
    let f: (i32) => i32 = inc;
    f = (if (c) { inc } else { dbl });
    return f(20) + apply((if (c) { dbl } else { (x: i32): i32 => x - 1 }), 2);
}
`},
	{name: "immediately-called-capturing-lambda", atLeast: 4, want: "13|", src: `
function g(n: i32): i32 { return n * 2; }
function main(): i32 {
    let n: i32 = 5;
    let t: i32 = ((): i32 => { return n + 2; })();
    let i: i32 = 0;
    while (i < 3) { t = t + ((): i32 => { return g(i); })(); i = i + 1; }
    return t;
}
`},
	{name: "immediately-called-capturing-lambda-returned", atLeast: 2, want: "3|", src: `
function main(): i32 { let k: i32 = 3; return ((): i32 => { return k; })(); }
`},
	{name: "immediately-called-capturing-lambda-in-a-loop", atLeast: 2, want: "18|", src: `
function main(): i32 {
    let base: string = "ab";
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        total = total + ((): i32 => {
            let s: string = base + "c";
            if (i > 1) { return s.len() * 2; }
            return s.len();
        })();
        i = i + 1;
    }
    return total;
}
`},
	{name: "immediately-called-capturing-lambda-with-parameters", atLeast: 2, want: "64|", src: `
function main(): i32 {
    let k: i32 = 4;
    let m: i32 = 7;
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        t = t + ((x: i32, y: i32): i32 => { if (x > k) { return x - y + m; } return x * k + y; })(i, 3);
        i = i + 1;
    }
    return t;
}
`},
	{name: "immediately-called-lambda-with-wide-captures", atLeast: 3, want: "15|", src: `
function main(): i32 {
    let big: i64 = 5000000000i64;
    let ratio: f64 = 1.5;
    let hits: i32 = 0;
    let acc: i64 = 0i64;
    let i: i32 = 0;
    while (i < 3) {
        acc = acc + ((): i64 => { return big + (i as i64); })();
        let scaled: f64 = ((d: f64): f64 => { defer { hits = hits + 1; } return ratio * d + (big as f64) * 0.0; })(2.0);
        acc = acc + (scaled as i64);
        i = i + 1;
    }
    return ((acc - 15000000000i64) as i32) + hits;
}
`},
	{name: "immediately-called-lambda-in-an-if-expr-arm", atLeast: 2, want: "5|", src: `
function main(): i32 { let c: boolean = true; let x: i32 = (if (c) { ((): i32 => { return 5; })() } else { 2 }); return x; }
`},
	{name: "scalar-calls", atLeast: 3, want: "15|", src: `
function add(a: i32, b: i32): i32 { return a + b; }
function total(xs: i32[]): i32 {
    let t: i32 = 0;
    for x in xs { t = add(t, x); }
    return t;
}
function main(): i32 { return total([1, 2, 3, 4, 5]); }
`},
	{name: "a-wildcard-only-match-on-a-scalar", atLeast: 3, want: "63|", src: `
function label(n: i32): string {
    if (n > 3) { return "big" + "-past-the-sso-inline-threshold"; }
    return "small" + "-past-the-sso-inline-threshold";
}
function pick(n: i32): i32 {
    let t: i32 = 0;
    match (n > 3) { _ => { t = n; } }
    match (label(n)) { _ => { t = t + 1; } }
    return t;
}
function main(): i32 { return pick(5) * 10 + pick(2); }
`},
	{name: "an-option-array-literal-settles-from-any-element", atLeast: 2, want: "104|", src: `
function total(c: i32): i32 {
    let a = [Some(1), Some(2), None];
    let n: i32 = 0;
    for o in a { match (o) { Some(x) => { n = n + x; }, None => {} } }
    let b = if (c > 3) { [Some(7), None] } else { [Some(1)] };
    return match (b[0]) { Some(v) => n + v, None => n };
}
function main(): i32 { return total(5) * 10 + total(1); }
`},
	{name: "a-method-on-a-concrete-map-receiver", atLeast: 3, want: "210|", wasm: "out-of-range|", src: `
import "core/map";
function (m: Map[string, i32]) goi(k: string, fallback: i32): i32 {
    if (m.contains(k)) { return m.get_or(k, 0); }
    return fallback;
}
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("a", 10);
    return m.goi("a", 1) + m.goi("b", 2) * 100;
}
`},
	{name: "a-map-column-of-closures", atLeast: 2, want: "2|", src: `
import "core/map";
function run(k: i32): i32 {
    let tag: string = "t" + "-past-the-sso-inline-threshold";
    let m: Map[i32, () => i32] = map_new(4);
    m = m.insert(1, (): i32 => { return tag.len() + k; });
    m = m.insert(2, (): i32 => { return 7; });
    m = m.insert(1, (): i32 => { return tag.len() * 2 + k; });
    let got: i32 = 0;
    match (m.get(1)) { Some(f) => { got = f(); }, None => { got = 0 - 1; } }
    let had: boolean = m.contains(2);
    let rest = m.remove(2);
    if (!had || rest.len() != 1) { return 0 - 2; }
    return got;
}
function main(): i32 { return run(3) - run(1); }
`},
	{name: "a-match-arm-rebuilds-its-variant-in-place", atLeast: 3, want: "60|", src: `
enum E { V(i32, string), W(i32, string) }
function flip(own x: E): E {
    return match (x) { V(a, s) => W(a + 1, s + "-w"), W(a, s) => V(a * 2, s) };
}
function weight(e: E): i32 {
    return match (e) { V(a, s) => a + s.len(), W(a, s) => a * 100 + s.len() };
}
function main(): i32 {
    let y = flip(flip(V(3, "abc")));
    let z = W(5, "k" + "-past-the-sso-inline-threshold");
    let keep = z;
    z = flip(z);
    return weight(y) + weight(z) + weight(keep) % 7;
}
`},
	{name: "owned-array-handback", atLeast: 3, want: "4|", src: `
function grown(own xs: i32[]): i32[] { return xs.append(9); }
function span(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    let ys: i32[] = grown([1, 2, 3]);
    return span(ys);
}
`},
	{name: "strings-and-records", atLeast: 3, want: "7|hi row\n\n", src: `
struct Row { name: string, n: i32 }
function greet(r: Row): string { return "hi " + r.name; }
function mk(n: i32): Row { return Row { name: "row", n: n }; }
function main(): i32 {
    let r: Row = mk(7);
    print(greet(r) + "\n");
    return r.n;
}
`},
	{name: "enum-match-in-a-loop", atLeast: 3, want: "23|", src: `
enum Shape { Dot, Line(i32), Box(i32, i32) }
function area(s: Shape): i32 {
    match (s) { Dot => { return 0; }, Line(n) => { return n; }, Box(w, h) => { return w * h; } }
}
function total(ss: Shape[]): i32 {
    let t: i32 = 0;
    for s in ss { t = t + area(s); }
    return t;
}
function main(): i32 { return total([Dot, Line(3), Box(4, 5)]); }
`},
	{name: "map-and-string-keys", atLeast: 3, want: "1|", src: `
import "core/map";
function tally(words: string[]): Map[string, i32] {
    let m: Map[string, i32] = map_new(8);
    for w in words { m = m.insert(w, 1); }
    return m;
}
function seen(words: string[], k: string): i32 {
    let m: Map[string, i32] = tally(words);
    if (m.contains(k)) { return 1; }
    return 0;
}
function main(): i32 { return seen(["a", "b"], "b") + seen(["a", "b"], "z"); }
`},
	{name: "map-column-snapshot", atLeast: 4, want: "30|", src: `
import "core/map";
function str_keys(n: i32): i32 {
    let ks: string[] = [];
    {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("alpha", 1);
        m = m.insert("beta", 2);
        ks = m.keys();
    }
    let t: i32 = 0;
    for k in ks { t = t + k.len(); }
    return t;
}
function str_values(n: i32): i32 {
    let vs: string[] = [];
    {
        let m: Map[i32, string] = map_new(4);
        m = m.insert(1, "one");
        vs = m.values();
    }
    let t: i32 = 0;
    for v in vs { t = t + v.len(); }
    return t;
}
function i32_keys(n: i32): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(7, 1);
    m = m.insert(9, 2);
    let t: i32 = 0;
    for k in m.keys() { t = t + k; }
    return t + m.len();
}
function main(): i32 { return str_keys(0) + str_values(0) + i32_keys(0); }
`},
	{name: "closure-field-built-and-dropped-apart", atLeast: 4, want: "4|", src: `
struct Holder { f: (i32) => i32 }

function make(n: i32): Holder {
    let xs: i32[] = [n, n + 1, n + 2];
    return Holder { f: (x: i32): i32 => { return x + xs[0] + xs[2]; } };
}

function apply(h: Holder): i32 {
    return h.f(1);
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let h: Holder = make(i);
        t = t + apply(h) % 3;
        i = i + 1;
    }
    return t % 7;
}`},
	{name: "value-if-arm-is-a-call", atLeast: 3, want: "3|", src: `
struct Xyz { n: i32, valid: boolean }
function gen(): boolean { return true; }
function mk(n: i32): Xyz { return Xyz { n: n, valid: true }; }
function main(): i32 {
    let v: boolean = (if (true) { gen() } else { false });
    let x: Xyz = (if (v) { mk(3) } else { Xyz { n: 4, valid: false } });
    return x.n;
}`},
	{name: "generic-enum-methods-clone-per-instantiation", atLeast: 6, want: "62|", src: `
import "core/cmp";

@derive(cmp.Eq)
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) or_else(d: T): T { match (o) { Sm(x) => { return x; }, Nn => { return d; } } }
function (o: Opt[T]) swap[U](other: Opt[U]): Opt[U] { match (o) { Sm(x) => { return other; }, Nn => { return Nn; } } }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let s: Opt[string] = Sm("round " + i.to_string());
        let n: Opt[i32] = s.swap(Sm(i));
        let e: Opt[string] = Nn;
        if (s == e) { t = t + 100; }
        if (s == s.swap(Sm("round " + i.to_string()))) { t = t + 1; }
        t = t + n.or_else(1) + s.or_else("").len();
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "builtin-union-payload-settles-the-literal", atLeast: 1, want: "4|", src: `
import "std/result";

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let r: Result[i32, string] = Ok(i);
        let s: Result[string, string] = r.and(Ok("vw"));
        let e: Result[i32, string] = Err("no");
        let f: Result[string, string] = e.and(Ok("xyz"));
        t = t + s.unwrap_or("").len() + f.unwrap_or("q").len() + r.unwrap_or(0);
        i = i + 1;
    }
    return t % 7;
}`},
	{name: "generic-enum-method-at-a-composite-key", atLeast: 2, want: "9|", src: `
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) get_or(d: T): T {
    let t: Opt[T] = o;
    match (t) { Sm(x) => { return x; }, Nn => { return d; } }
}

function main(): i32 {
    let o: Opt[(i32, i32)] = Sm((3, 4));
    let n: Opt[(i32, i32)] = Nn;
    let p: (i32, i32) = o.get_or((0, 0));
    let q: (i32, i32) = n.get_or((1, 1));
    return p.0 + p.1 + q.0 + q.1;
}`},
	{name: "generic-enum-at-a-simple-and-a-composite-key", atLeast: 3, want: "37|", src: `
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) get_or(d: T): T {
    match (o) { Sm(x) => { return x; }, Nn => { return d; } }
}

function main(): i32 {
    let a: Opt[i32] = Sm(3);
    let b: Opt[(i32, i32)] = Sm((1, 2));
    let n: Opt[i32] = Nn;
    let p: (i32, i32) = b.get_or((5, 5));
    return a.get_or(9) * 10 + n.get_or(4) + p.0 + p.1;
}`},
	{name: "generic-enum-at-an-inferred-tuple-key", atLeast: 2, want: "0|", src: `
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) get_or(d: T): T {
    match (o) { Sm(x) => { return x; }, Nn => { return d; } }
}

function main(): i32 {
    let o = Sm((3, 4));
    let p: (i32, i32) = o.get_or((0, 0));
    let q: i32 = 0;
    match (Sm((5, 6))) { Sm(t) => { q = t.0 + t.1; }, Nn => { q = 0; } }
    return p.0 + p.1 + q - 18;
}
`},
	{name: "generic-enum-tuple-key-matched-inline-only", atLeast: 1, want: "0|", src: `
enum Opt[T] { Sm(T), Nn }
function main(): i32 { let q: i32 = 0; match (Sm((5, 6))) { Sm(t) => { q = t.0 + t.1; }, Nn => { q = 0; } } return q - 11; }
`},
	{name: "generic-enum-bound-from-a-later-field", atLeast: 1, want: "0|", src: `
enum Opt[T] { Sm((i32, i32), T), Nn }
function main(): i32 { let o = Sm((1, 2), 3); let q: i32 = 0; match (o) { Sm(_, t) => { q = t; }, Nn => { q = 0; } } return q - 3; }
`},
	{name: "generic-enum-bound-from-a-later-field-matched-inline", atLeast: 1, want: "0|", src: `
enum Opt[T] { Sm((i32, i32), T), Nn }
function main(): i32 { let q: i32 = 0; match (Sm((1, 2), 3)) { Sm(p, t) => { q = t + p.0; }, Nn => { q = 0; } } return q - 4; }
`},
	{name: "generic-enum-bound-from-a-later-field-after-a-function", atLeast: 3, want: "0|", src: `
enum Opt[T] { Sm((i32) => i32, T), Nn }
function inc(x: i32): i32 { return x + 1; }
function main(): i32 { let o = Sm(inc, 3); let q: i32 = 0; match (o) { Sm(f, t) => { q = f(t); }, Nn => { q = 0; } } return q - 4; }
`},
	{name: "generic-enum-at-a-nested-tuple-key", atLeast: 2, want: "182|", wasm: "out-of-range|", src: `
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) get_or(d: T): T {
    match (o) { Sm(x) => { return x; }, Nn => { return d; } }
}

function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let o: Opt[(i32[], (i32, string))] = Sm(([i, 2], (3, "four")));
        let n: Opt[(i32[], (i32, string))] = Nn;
        let p: (i32[], (i32, string)) = o.get_or(([0], (0, "")));
        let q: (i32[], (i32, string)) = n.get_or(([7], (1, "x")));
        total = total + p.0.len() + p.0[1] + p.1.0 + p.1.1.len() + q.0[0] + q.1.1.len();
        i = i + 1;
    }
    return total % 256;
}`},
	{name: "generic-enum-at-a-simple-and-a-composite-key-without-methods", atLeast: 1, want: "6|", src: `
enum Opt[T] { Sm(T), Nn }

function main(): i32 {
    let a: Opt[i32] = Sm(3);
    let b: Opt[(i32, i32)] = Sm((1, 2));
    let x: i32 = 0;
    match (a) { Sm(n) => { x = n; }, Nn => { x = 0; } }
    match (b) { Sm(p) => { x = x + p.0 + p.1; }, Nn => { } }
    return x;
}`},
	{name: "use-binding-takes-the-callee-parameter-type", atLeast: 5, want: "13|", src: `
import "core/cmp";

function with_name(n: i32, k: (string) => i32): i32 { return k("name-" + n.to_string()); }

function plain(i: i32): i32 {
    use s <- with_name(i);
    return s.len() * 2;
}

function capturing(i: i32): i32 {
    use s <- with_name(i);
    return s.len() + i % 3;
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        t = t + plain(i) + capturing(i);
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "lambda-returns-a-lambda-from-a-lambda", atLeast: 5, want: "63|", src: `
function main(): i32 {
    let mk = (): ((i32) => i32) => { return (x: i32): i32 => x * 2; };
    let mk2 = (): (i32) => i32 => { return (x: i32): i32 => x + 3; };
    let f = mk();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        t = t + f(i) + mk2()(i);
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "capturing-lambda-returned-from-a-branch-of-a-lambda", atLeast: 4, want: "33|", src: `
function main(): i32 {
    let pick = (flag: boolean, n: i32): (i32) => i32 => {
        if (flag) { return (x: i32): i32 => x + n; }
        return (x: i32): i32 => x * 2;
    };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        t = t + pick(i % 2 == 0, i)(3);
        i = i + 1;
    }
    return t % 97;
}
`},
	{name: "block-bodied-lambda-with-a-use-binding", atLeast: 4, want: "50|", src: `
function give(x: i32, cb: (i32) => i32): i32 { return cb(x); }

function main(): i32 {
    let bound = (): i32 => {
        use n <- give(41);
        return n + 1;
    };
    let tail = (x: i32) => { let y: i32 = x + 1; y * 2 };
    return bound() + tail(3);
}`},
	{name: "value-match-first-arm-is-a-match-of-lambdas", atLeast: 8, want: "42|", src: `
enum Status { Active, Inactive, Pending }
function main(): i32 {
    let v0: Status = Pending;
    let f: (i32) => i32 = (match (v0) {
        Active => (match (v0) { Active => ((d: i32) => d), Inactive => ((e: i32) => e - 1), Pending => ((g: i32) => g + 1) }),
        Inactive => ((c: i32) => c * 2),
        Pending => ((h: i32) => h + 40)
    });
    return f(2);
}`},
	{name: "view-loop-rebinds-a-live-view", atLeast: 1, want: "14|", src: `
function main(): i32 {
    let t: string = "abcde" + "fghij";
    let s: str = slice_unchecked(t, 0, 5);
    let v: str = s;
    let i: i32 = 0;
    while (i < 3) { v = slice_unchecked(t, 5, 10); i = i + 1; }
    let u: string = "xyz" + "w";
    return v.len() + s.len() + u.len();
}`},
	{name: "view-loop-rebinds-a-view-parameter", atLeast: 2, want: "202|", wasm: "out-of-range|", src: `
function walk(p: str, n: i32): i32 {
    let v: str = p;
    let i: i32 = 0;
    while (i < n) { v = slice_unchecked(p, 1, p.len()); i = i + 1; }
    return v.len() * 10 + p.len();
}

function main(): i32 {
    let t: string = "abcdef" + "g";
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 20) { total = total + walk(slice_unchecked(t, 0, 5), k % 3); k = k + 1; }
    return total % 256;
}`},
	{name: "a-view-alias-outlives-its-rebound-source", atLeast: 2, want: "21|", src: `
function pick(t: string, k: i32): i32 {
    let a: str = slice_unchecked(t, 0, 3);
    let b: str = a;
    a = slice_unchecked(t, 3, 6);
    let n: i32 = 0;
    while (n < k) { a = slice_unchecked(t, n, n + 1); n = n + 1; }
    return b.len() * 10 + a.len() + (b[0] as i32) - 97;
}

function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 25) {
        let t: string = "abcdefg" + "hij";
        total = total + pick(t, i % 4);
        i = i + 1;
    }
    return total % 256;
}`},
	{name: "a-view-yielded-by-an-if-arm-outlives-its-rebound-source", atLeast: 2, want: "17|", src: `
function pick(t: string, k: i32): i32 {
    let a: str = slice_unchecked(t, 0, 3);
    let n: i32 = 0;
    let b: str = if (k > 1) { a } else { slice_unchecked(t, 1, 2) };
    while (n < k) { a = slice_unchecked(t, n, n + 1); n = n + 1; }
    return b.len() * 10 + a.len();
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 25) { let t: string = "abcdefg" + "hij"; total = total + pick(t, i % 4); i = i + 1; }
    return total % 256;
}`},
	{name: "a-view-yielded-by-a-match-arm-outlives-its-rebound-source", atLeast: 2, want: "157|", wasm: "out-of-range|", src: `
function pick(t: string, k: i32): i32 {
    let a: str = slice_unchecked(t, 0, 3);
    let n: i32 = 0;
    let b: str = match (k) { 0 => a, 1 => slice_unchecked(t, 1, 2), _ => a };
    while (n < k) { a = slice_unchecked(t, n, n + 1); n = n + 1; }
    return b.len() * 10 + a.len();
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 25) { let t: string = "abcdefg" + "hij"; total = total + pick(t, i % 4); i = i + 1; }
    return total % 256;
}`},
	{name: "a-for-over-views-rebinds-an-outer-view", atLeast: 2, want: "125|", src: `
function longest(ws: str[]): i32 {
    let best: str = "";
    for w in ws { if (w.len() > best.len()) { best = w; } }
    return best.len();
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 25) {
        let t: string = "abcdefg" + "hij";
        let ws: str[] = [slice_unchecked(t, 0, 2), slice_unchecked(t, 2, 7), slice_unchecked(t, 1, 4)];
        total = total + longest(ws);
        i = i + 1;
    }
    return total % 256;
}`},
	{name: "a-chained-append-on-an-array-field", atLeast: 1, want: "0|", src: `
struct TA { w: i32, s: string }
struct Blk { id: i32, xs: TA[] }
function main(): i32 {
    let blocks: Blk[] = [Blk { id: 1, xs: [] }, Blk { id: 2, xs: [TA { w: 1, s: "a" }] }];
    let out: Blk[] = [];
    for b in blocks {
        if (b.id == 2) {
            let xs: TA[] = b.xs.append(TA { w: 2, s: "b" + "c" }).append(TA { w: 3, s: "d" });
            b = Blk { ...b, xs: xs };
        }
        out = out.append(b);
    }
    return out[1].xs.len() + out[1].xs[1].s.len() + out.len() - 7;
}
`},
	{name: "a-string-literal-is-already-a-view", atLeast: 4, want: "68|", src: `
function span(fmt: string, take: boolean): i32 {
    let spec: str = "";
    if (take) { spec = slice_unchecked(fmt, 1, 4); }
    let sum: i32 = spec.len();
    let k: i32 = 0;
    while (k < spec.len()) { sum = sum + (spec[k] as i32); k = k + 1; }
    return sum;
}

function rounds(fmt: string, n: i32): i32 {
    let out: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let spec: str = "";
        if (i % 3 != 0) { spec = slice_unchecked(fmt, 0, i % 5); }
        out = out + spec.len();
        i = i + 1;
    }
    return out;
}

function width(s: str): i32 { return s.len(); }

function main(): i32 {
    let fmt: string = "abcdefgh" + "ijkl";
    let many: str[] = ["", "ab", "cde"];
    let total: i32 = span(fmt, true) + span(fmt, false) + rounds(fmt, 200);
    total = total + width("wxyz");
    for m in many { total = total + m.len(); }
    let last: str = "";
    if (total > 0) { last = slice_unchecked(fmt, 2, 6); }
    return total + last.len();
}`},
	{name: "a-trait-method-implemented-for-str", atLeast: 4, want: "3|bc! bc!bc! yz! a!bc!\n\n", src: `
import "std/i32";
trait Shout { function shout(self: Self): string; }
impl Shout for str { function shout(self: Self): string { return self + "!"; } }
function loud[T: Shout](x: T): string { return x.shout() + x.shout(); }
function all(xs: str[]): string {
    let out: string = "";
    for x in xs { out = out + x.shout(); }
    return out;
}
function main(): i32 {
    let owned: string = "ab" + "cd";
    let s: str = slice_unchecked(owned, 1, 3);
    let xs: str[] = [slice_unchecked(owned, 0, 1), s];
    print(s.shout() + " " + loud(s) + " " + "yz".shout() + " " + all(xs) + "\n");
    return s.shout().len();
}`},
	{name: "an-inherent-method-on-str", atLeast: 2, want: "4|bcbc xyxy\n\n", src: `
function (self: str) twice(): string { return self + self; }
function main(): i32 {
    let owned: string = "ab" + "cd";
    let s: str = slice_unchecked(owned, 1, 3);
    print(s.twice() + " " + "xy".twice() + "\n");
    return s.twice().len();
}`},
	{name: "a-map-iterator-reads-its-snapshot", atLeast: 6, want: "181|", wasm: "181|", src: `
import "core/map"; import "core/iter";

function sum_values(m: Map[string, i32]): i32 {
    let total: i32 = 0;
    let it: iter.ArrayIter[(string, i32)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        total = total + __mapvalue_it + __mapkey_it.len();

    }
    return total;
}

function longest(m: Map[string, string]): i32 {
    let best: i32 = 0;
    let it: iter.ArrayIter[(string, string)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        let v: string = __mapvalue_it;
        if (v.len() > best) { best = v.len(); }

    }
    return best;
}

function rounds(m: Map[string, i32], n: i32): i32 {
    let out: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let it: iter.ArrayIter[(string, i32)] = m.iter();
        for (__mapkey_it, __mapvalue_it) in it { out = out + __mapvalue_it;  }
        i = i + 1;
    }
    return out;
}

function narrow_keys(m: Map[i32, i32]): i32 {
    let total: i32 = 0;
    let it: iter.ArrayIter[(i32, i32)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it { total = total + __mapkey_it * __mapvalue_it;  }
    return total;
}

function narrow_key_str_value(m: Map[i32, string]): i32 {
    let total: i32 = 0;
    let it: iter.ArrayIter[(i32, string)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it { total = total + __mapkey_it + __mapvalue_it.len();  }
    return total;
}

function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("aa", 1);
    m = m.insert("bbb", 2);
    m = m.insert("cccc", 4);
    let t: Map[string, string] = map_new(4);
    t = t.insert("k", "vvvvv");
    t = t.insert("kk", "vv");
    let empty: Map[string, i32] = map_new(4);
    let a: Map[i32, i32] = map_new(4);
    a = a.insert(2, 5);
    a = a.insert(3, 7);
    let b: Map[i32, string] = map_new(4);
    b = b.insert(10, "xyz");
    b = b.insert(20, "pq");
    return sum_values(m) + longest(t) + rounds(m, 50) + sum_values(empty)
        + narrow_keys(a) + narrow_key_str_value(b);
}`},
	{name: "a-type-variable-only-a-constraint-mentions", atLeast: 13, want: "50|", src: `
import "core/iter" as iter;

function tail(xs: i32[]): i32 {
    match (iter.last(iter.of(xs))) {
        Some(v) => { return v; },
        None => { return 0 - 1; },
    }
}

function pick(n: i32): i32 {
    match (iter.nth(iter.range(0, 20), n)) {
        Some(v) => { return v; },
        None => { return 0 - 1; },
    }
}

function rest[T, I: iter.Iterator[T]](it: I): T[] {
    return it.collect();
}

function words(ws: string[]): i32 {
    let out: string[] = rest(iter.of(ws));
    let sum: i32 = 0;
    for w in out { sum = sum + w.len(); }
    return sum;
}

function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        total = total + pick(i % 20);
        total = total + tail([i, i + 1, i + 2]);
        total = total + words(["ab", "cde", "f"]);
        i = i + 1;
    }
    return total % 1000;
}`},
	{name: "a-chained-receiver-keeps-its-instantiation", atLeast: 20, want: "8|", src: `
import "std/ndarray" as ndarray;

function main(): i32 {
    let a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4], [2, 2]);
    let m: ndarray.NdArray[i32] = a.transpose().map((x: i32): i32 => x * 2);
    return m.get_unchecked([1, 1]);
}`},
	{name: "a-callbacks-return-pins-the-methods-own-variable", atLeast: 27, want: "15|", src: `
import "std/ndarray" as ndarray;

function sum_cell(c: ndarray.NdArray[i32]): ndarray.NdArray[i32] {
    return ndarray.from_flat([c.fold_all(0, (acc: i32, x: i32): i32 => acc + x)], []);
}

function main(): i32 {
    let a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
    let sums: ndarray.NdArray[i32] = a.map_rank(1, sum_cell);
    return sums.get_unchecked([1]);
}`},
	{name: "a-callback-slot-keeps-its-instantiation", atLeast: 5, want: "3|big/lam\n", src: `
struct Slot[T] { v: T }

function (s: Slot[T]) through[T, U](f: (Slot[T]) => Slot[U]): Slot[U] {
    return f(s);
}

function label(s: Slot[i32]): Slot[string] {
    if (s.v > 5) { return Slot[string] { v: "big" }; }
    return Slot[string] { v: "small" };
}

function main(): i32 {
    let a: Slot[i32] = Slot[i32] { v: 7 };
    let b: Slot[string] = a.through(label);
    let c: Slot[string] = a.through((s: Slot[i32]): Slot[string] => Slot[string] { v: "lam" });
    print(b.v + "/" + c.v);
    return b.v.len();
}`},
	{name: "an-ascription-names-a-destination", atLeast: 3, want: "27|", src: `
function total(xs: i32[]): i32 {
    let s: i32 = 0;
    for x in xs { s = s + x; }
    return s;
}

function widen(n: i32): i32 {
    return (n as u8) as i32;
}

function main(): i32 {
    let empty: i32[] = [] as i32[];
    let held: i32[] = [4, 5] as i32[];
    let opt: Option[i32] = None as Option[i32];
    let seen: i32 = 0;
    match (opt) { Some(v) => { seen = v; }, None => { seen = 1; } }
    return total(empty) + total(held) + total([6, 7] as i32[]) + widen(260) + seen;
}`},
	{name: "pinned-array-receivers", atLeast: 45, want: "24|a, b and c\n", src: `
import "std/array" as array;

function main(): i32 {
    let words: string[] = ["a", "b", "c"];
    let joined: string = words.join_with_last(", ", " and ");
    let ns: i32[] = [3, 1, 4, 1, 5];
    let sums: i32[] = ns.cumsum();
    let pos: boolean = ns.every_positive();
    print(joined);
    let last: i32 = sums[sums.len() - 1];
    if (!pos) { return 0; }
    return joined.len() + last;
}`},
	{name: "a-generic-enum-keeps-its-arguments", atLeast: 3, want: "103|", src: `
enum Box[T] {
    Now(T),
    Later(i32, (i32) => Box[T])
}

function hold[T](tok: i32, next: Box[T]): Box[T] {
    return Later(tok, (w: i32) => next);
}

function main(): i32 {
    let b: Box[i32] = hold(3, Now(7));
    match (b) {
        Now(v) => { return v; },
        Later(t, k) => { return t + 100; }
    }
    return 0;
}`},
	{name: "a-slice-retains-the-elements-it-copied", atLeast: 3, want: "19|beta/gamma\n", src: `
function total(ws: [string]) : i32 {
    let n: i32 = 0;
    for w in ws { n = n + w.len(); }
    return n;
}

function widths(rs: [i32[]]): i32 {
    let n: i32 = 0;
    for r in rs { n = n + r.len(); }
    return n;
}

function main(): i32 {
    let all: string[] = ["alpha", "beta", "gamma", "delta"];
    let mid: [string] = all[1:3];
    print(mid[0] + "/" + mid[1]);
    let grid: i32[][] = [[1], [2, 2], [3, 3, 3]];
    let tail: [i32[]] = grid[1:3];
    return total(mid) + widths(tail) + all[3].len();
}`},
	{name: "value-if-arm-is-a-value-if-of-a-call", atLeast: 3, want: "13|", src: `
function gen(): boolean { return true; }
function pick(n: i32): boolean { return n > 2; }
function main(): i32 {
    let v: boolean = (if (pick(3)) { (if (pick(1)) { gen() } else { pick(5) }) } else { pick(0) });
    let w: boolean = (if (pick(3)) { (if (pick(4)) { gen() } else { false }) } else { false });
    return (if (v) { 3 } else { 4 }) + (if (w) { 10 } else { 20 });
}`},
	{name: "annotated-lambda-literal-body", atLeast: 3, want: "7|", src: `
function apply(f: (i32) => u8, n: i32): u8 { return f(n); }
function main(): i32 {
    let r: u8 = apply(((x: i32): u8 => 5), 1);
    return (r as i32) + 2;
}`},
	{name: "closure-in-a-tuple", atLeast: 2, want: "6|", src: `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let xs: i32[] = [i, i + 1, i + 2];
        let p: ((i32) => i32, i32) = (((x: i32) => x + xs[0] + xs[2]), i);
        t = t + (p.0)(1) % 3 + p.1 % 2;
        i = i + 1;
    }
    return t % 7;
}`},
	{name: "map-iteration-both-columns", atLeast: 3, want: "65|", src: `
import "core/map";
import "std/string";
function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    m = m.insert(4, 40);
    let had: boolean = m.contains(2);
    let m2 = m.remove(2);
    let t: i32 = 0;
    for (k, v) in m2 { t = t + k * v; }
    let names: Map[string, i32] = Map { "a": 1, "bb": 2 };
    let n: i32 = 0;
    for (k2, v2) in names { n = n + k2.len() * v2; }
    return (t % 100) + n;
}`},
	{name: "value-if-arm-is-a-unary", atLeast: 1, want: "1|", src: `
function main(): i32 {
    let big: i64 = 5000000000;
    let flags: boolean[] = (if (big > 0) { [true, (if (big > 1) { (!false) } else { true })] } else { [false] });
    let wide: u64[] = (if (flags[1]) { [(if (flags[0]) { (big as u64) } else { (7 as u64) })] } else { [1] });
    let negs: i64[] = (if (flags[0]) { [(if (flags[1]) { -big } else { -(big + 1) })] } else { [2] });
    return ((wide[0] % 1000) as i32) + ((negs[0] % 7) as i32) + (if (flags[1]) { 3 } else { 0 });
}`},
	{name: "value-block-takes-its-bindings-type", atLeast: 1, want: "15|", src: `
struct Pt { x: i32, y: i32 }
function main(): i32 {
    let a: Option[i32] = (if (true) { None } else { None });
    let b: Option[i32] = (if (false) { None } else { Some(5) });
    let p: Pt = (if (true) { Pt { x: 1, y: 2 } } else { Pt { x: 3, y: 4 } });
    let n: i32 = 0;
    match (a) { Some(v) => { n = n + v; }, None => { n = n + 7; } }
    match (b) { Some(v) => { n = n + v; }, None => { n = n + 70; } }
    return n + p.x + p.y;
}`},
	{name: "option-results-of-lambdas", atLeast: 3, want: "29|", src: `
function id[T](x: T): T { return x; }
function main(): i32 {
    function some_of(k: i32): Option[i32] { return id(Some(k)); }
    let none_of: (i32) => Option[i32] = ((x: i32) => None);
    let n: i32 = 0;
    match (some_of(9)) { Some(v) => { n = n + v; }, None => { n = n + 1; } }
    match (none_of(9)) { Some(v) => { n = n + v; }, None => { n = n + 20; } }
    return n;
}`},
	{name: "local-function-returns-a-capturing-lambda", atLeast: 3, want: "5|", src: `
function main(): i32 {
    let acc: i32 = 3;
    function mk(k: i32): (i32) => i32 { return ((x: i32) => acc + x + k); }
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let f: (i32) => i32 = mk(i);
        t = t + f(2) % 7;
        i = i + 1;
    }
    return t % 100;
}`},
	{name: "literal-argument-typed-from-the-destination", atLeast: 2, want: "78|", src: `
function id[T](x: T): T { return x; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let xs: i32[] = id([]);
        let ys: i32[] = id([i, i + 1]);
        let fs: ((i32) => i32)[] = id([((x: i32) => x + i)]);
        t = t + xs.len() + ys[1] + (fs[0])(2);
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "value-block-of-capturing-lambdas", atLeast: 3, want: "54|", src: `
function main(): i32 {
    let base: i32 = 5;
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 60) {
        let k: i32 = i;
        let f: (i32) => i32 = (if (i % 2 == 0) { ((x: i32) => x + base) } else { ((x: i32) => x * k) });
        let g: (i32) => i32 = (match (i % 3) { 0 => ((x: i32) => x - base), 1 => ((x: i32) => k), _ => ((x: i32) => x + 1) });
        t = t + f(2) % 11 + g(3) % 7;
        i = i + 1;
    }
    return t % 101;
}`},
	{name: "tuple-and-struct-match-of-capturing-lambdas", atLeast: 9, want: "51|", src: `
struct P { a: i32, b: i32 }
function pick(p: P, base: i32): (i32) => i32 {
    return match (p) { P { a: 0, b } => ((x: i32) => x - base + b), P { a: 1, b } => ((x: i32) => b), _ => ((x: i32) => x + 1) };
}
function main(): i32 {
    let base: i32 = 5;
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 60) {
        let k: i32 = i;
        let g: (i32) => i32 = (match ((i % 3, 0)) { (0, _) => ((x: i32) => x - base), (1, _) => ((x: i32) => k), _ => ((x: i32) => x + 1) });
        let h: (i32) => i32 = pick(P { a: i % 3, b: i }, base);
        t = t + g(3) % 7 + h(3) % 5;
        i = i + 1;
    }
    return t % 101;
}`},
	{name: "value-block-tail-local-struct-array", atLeast: 1, want: "56|", src: `
struct P { x: i32, y: i32 }
function main(): i32 {
    let ps = { let j = 1; let q = match (j) { 1 => [P{x:5,y:6}], _ => [P{x:0,y:0}] }; q };
    return ps[0].x * 10 + ps[0].y;
}`},
	{name: "value-block-tail-local-struct", atLeast: 1, want: "29|", src: `
struct R { name: string, n: i32 }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 20) {
        let r = { let q = if (i % 2 == 0) { R{name: "a" + "b", n: i} } else { R{name: "c", n: 1} }; q };
        t = t + r.n + r.name.len();
        i = i + 1;
    }
    return t % 101;
}`},
	{name: "value-block-tail-local-owning-struct-array-in-loop", atLeast: 1, want: "39|", src: `
struct R { name: string, n: i32 }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 20) {
        let rs = { let q = if (i % 2 == 0) { [R{name: "a" + "b", n: i}] } else { [R{name: "c", n: 1}, R{name: "d", n: 2}] }; q };
        t = t + rs[rs.len() - 1].n + rs[0].name.len();
        i = i + 1;
    }
    return t % 101;
}`},
	{name: "record-literal-is-the-struct-it-names", atLeast: 2, want: "6|", src: `
struct Xyz { n: i32, valid: boolean, tag: string }
function pick[T](c: boolean, a: T, b: T): T { return if (c) { a } else { b }; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 40) {
        let v: Xyz = pick(i % 2 == 0, (Xyz { n: i, valid: (if (i % 3 == 0) { pick(true, false, true) } else { true }), tag: "a" + "b" }), (Xyz { n: 1, valid: false, tag: "c" }));
        let w: Xyz = Xyz { ...v, n: (if (v.valid) { pick(false, 1, 2) } else { 3 }) };
        t = t + w.n + (if (w.valid) { 10 } else { 0 }) + w.tag.len();
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "keyed-map-probes", atLeast: 3, want: "96|", src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B, C(string) }
function names(): i32 {
    let m: Map[Name, i32] = map_new(8);
    m = m.insert(Name { first: "ada", rank: 1 }, 10);
    m = m.insert(Name { first: "bob", rank: 2 }, 20);
    if (m.get_or(Name { first: "a" + "da", rank: 1 }, 0 - 1) != 10) { return 1; }
    if (!m.contains(Name { first: "ada", rank: 1 })) { return 2; }
    m = m.insert(Name { first: "ada", rank: 1 }, 99);
    if (m.len() != 2) { return 3; }
    return m.get_or(Name { first: "ada", rank: 1 }, 0 - 1) - 90;
}
function tags(): i32 {
    let em: Map[Tag, i32] = map_new(8);
    em = em.insert(A(1), 1);
    em = em.insert(B, 2);
    em = em.insert(C("x" + "y"), 3);
    if (em.get_or(C("xy"), 0) != 3) { return 1; }
    if (em.get_or(A(2), 0 - 1) != 0 - 1) { return 2; }
    let total: i32 = 0;
    for (k, v) in em { total = total + v; }
    match (em.get(A(1))) { Some(v) => { if (v != 1) { return 3; } }, None => { return 4; } }
    return total;
}
function main(): i32 { return names() * 10 + tags(); }
`},
	{name: "keyed-map-keys-outlive-the-map", atLeast: 2, want: "27|", src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
function ranks(): i32 {
    let ks: Coord[] = [];
    {
        let m: Map[Coord, i32] = map_new(2);
        let i: i32 = 0;
        while (i < 6) { m = m.insert(Coord { a: i, b: i * 2 }, i); i = i + 1; }
        ks = m.keys();
    }
    let t: i32 = 0;
    for k in ks { t = t + k.b; }
    return t;
}
function main(): i32 { return ranks() - 3; }
`},
	{name: "keyed-map-of-boxes-shared", atLeast: 2, want: "28|", src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
struct Box { n: i32, tag: string }
function boxes(): i32 {
    let m: Map[Coord, Box] = map_new(4);
    let i: i32 = 0;
    while (i < 20) { m = m.insert(Coord { a: i, b: i * 2 }, Box { n: i * 10, tag: "v" }); i = i + 1; }
    m = m.insert(Coord { a: 7, b: 14 }, Box { n: 777, tag: "w" });
    if (m.len() != 20) { return 1; }
    let shared: Map[Coord, Box] = m;
    m = m.insert(Coord { a: 99, b: 0 }, Box { n: 1, tag: "z" });
    if (shared.len() != 20 || m.len() != 21) { return 2; }
    let t: i32 = 0;
    match (m.get(Coord { a: 7, b: 14 })) { Some(c) => { t = c.n + c.tag.len(); }, None => { return 3; } }
    for k in shared.keys() { t = t + k.b % 3; }
    return t + shared.get_or(Coord { a: 3, b: 6 }, Box { n: 0, tag: "" }).n;
}
function main(): i32 { return boxes() % 100; }
`},
	{name: "a-map-of-i64-values-takes-the-typed-path", atLeast: 2, want: "0|", src: `
import "core/map";
function total(m: Map[i32, i64]): i64 {
    let t: i64 = 0;
    for (k, v) in m { t = t + v + (k as i64); }
    return t;
}
function main(): i32 {
    let m: Map[i32, i64] = map_new(2);
    let i: i32 = 0;
    while (i < 12) { m = m.insert(i, (i as i64) * 1000000000); i = i + 1; }
    m = m.insert(3, 7000000000);
    let shared: Map[i32, i64] = m;
    m = m.insert(20, 5);
    let vs: i64[] = m.values();
    let s: i64 = 0;
    for v in vs { s = s + v; }
    let g: i64 = m.get_or(3, 0);
    let miss: i64 = m.get_or(99, 42);
    let hit: i64 = 0;
    match (m.get(11)) { Some(v) => { hit = v; }, None => { hit = 0 - 1; } }
    let wo: Map[i32, i64] = m.remove(5);
    m = wo;
    let r: i64 = s / 1000000000 + g / 1000000000 + miss + hit / 1000000000 + total(m) / 1000000000 + (shared.len() as i64) + (m.len() as i64);
    return (r as i32) - 219;
}
`},
	{name: "an-i64-map-pairs-each-key-with-its-value", atLeast: 1, want: "0|", src: `
import "core/map";
function main(): i32 {
    let m: Map[i32, i64] = map_new(2);
    let i: i32 = 0;
    while (i < 40) { m = m.insert((i * 7919) % 101, (((i * 7919) % 101) as i64) * 3); i = i + 1; }
    let wo: Map[i32, i64] = m.remove(14);
    m = wo;
    let bad: i32 = 0;
    for (k, v) in m { if (v != (k as i64) * 3) { bad = bad + 1; } }
    let ks: i32[] = m.keys();
    let vs: i64[] = m.values();
    let j: i32 = 0;
    while (j < ks.len()) { if (vs[j] != (ks[j] as i64) * 3) { bad = bad + 100; } j = j + 1; }
    return bad;
}
`},
	{name: "the-unit-value-in-every-position", atLeast: 5, want: "0|", src: `
function sink(u: void): i32 { return 7; }
function fallible(): Result[void, i32] { return Ok(()); }
function main(): i32 {
    let u: void = ();
    let v = ();
    let t: (void, i32) = ((), 5);
    let w: (i32, void) = (4, u);
    let n: i32 = 0;
    match (fallible()) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    let (a, b) = t;
    let (_, c) = w;
    let f = sink;
    let g = (k: i32): i32 => sink(u) + k;
    return n + sink(u) + sink(v) + sink(t.0) + sink(w.1) + t.1 + w.0 + sink(a) + b + sink(c) + f(()) + g(1) - 72;
}
`},
	{name: "map-delete-releases-the-entry", atLeast: 3, want: "35|", src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
struct Box { n: i32, tag: string }
function by_string(): i32 {
    let m: Map[string, i32] = map_new(2);
    m = m.insert("a" + "x", 1);
    m = m.insert("b" + "y", 2);
    let gone: boolean = m.contains("ax");
    let m2 = m.remove("ax");
    if (!gone) { return 0 - 1; }
    m2 = m2.insert("a" + "x", 3);
    return m2.len() * 10 + m2.get_or("ax", 0);
}
function by_value(): i32 {
    let m: Map[i32, string] = map_new(2);
    m = m.insert(1, "one" + "!");
    m = m.insert(2, "two" + "!");
    let gone: boolean = m.contains(1);
    let m2 = m.remove(1);
    if (!gone) { return 0 - 1; }
    return m2.get_or(2, "").len();
}
function by_key(): i32 {
    let m: Map[Coord, Box] = map_new(2);
    let i: i32 = 0;
    while (i < 5) { m = m.insert(Coord { a: i, b: i }, Box { n: i, tag: "t" + "!" }); i = i + 1; }
    let gone: boolean = m.contains(Coord { a: 2, b: 2 });
    let m2 = m.remove(Coord { a: 2, b: 2 });
    if (!gone) { return 0 - 1; }
    if (m2.contains(Coord { a: 2, b: 2 })) { return 0 - 2; }
    return m2.len() + m2.get_or(Coord { a: 4, b: 4 }, Box { n: 0, tag: "" }).n;
}
function main(): i32 { return by_string() + by_value() + by_key(); }
`},
	{name: "map-delete-and-clear", atLeast: 4, want: "131|", wasm: "out-of-range|", src: `
import "core/map";
function survivors(n: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    let i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 10); i = i + 1; }
    let gone: boolean = m.contains(1);
    let m1 = m.remove(1);
    if (!gone) { return 0 - 1; }
    return m1.len() * 10 + m1.get_or(4, 0);
}
function emptied(n: i32): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 1);
    m = m.cleared();
    m = m.insert(7, 3);
    return m.len();
}
function absent(n: i32): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(5, 50);
    let missing: boolean = m.contains(99);
    let m1 = m.remove(99);
    if (missing) { return 0 - 1; }
    return m1.get_or(5, 0);
}
function main(): i32 { return survivors(5) + emptied(0) + absent(0); }
`},
	{name: "tuple-element-take", atLeast: 3, want: "219|", wasm: "out-of-range|", src: `
import "core/map";
function chained(n: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    let i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 10); i = i + 1; }
    m = m.remove(1);
    m = m.remove(2);
    return m.len() * 100 + m.get_or(4, 0);
}
function relayed(n: i32): i32 {
    let t: (i32[], string) = ([n, n + 1], "ab");
    let b: i32[] = t.0;
    let s: string = t.1;
    b = b.append(n + 2);
    return b.len() * 10 + s.len() + b[2];
}
function main(): i32 { return (chained(6) + relayed(1)) & 255; }
`},
	{name: "inclusive-range", atLeast: 3, want: "48|", src: `
function closed(n: i32): i32 {
    let s: i32 = 0;
    for i in 0..=n { s = s + i; }
    return s;
}
function single(n: i32): i32 {
    let c: i32 = 0;
    for i in n..=n { c = c + 1; }
    for j in (n + 4)..=n { c = c + 100; }
    return c;
}
function skipping(n: i32): i32 {
    let s: i32 = 0;
    for i in 0..=n {
        if (i == 4) { continue; }
        if (i == 9) { break; }
        s = s + i;
    }
    return s;
}
function main(): i32 { return (closed(5) + single(3) + skipping(12)) & 255; }
`},
	{name: "container-read-is-not-a-take", atLeast: 3, want: "71|", src: `
import "core/map";
function seeded(n: i32): Map[i32, (i32[], i32)] {
    let m: Map[i32, (i32[], i32)] = map_new(4);
    return m.insert(1, ([n, n + 1], n));
}
function shared(n: i32): i32 {
    let m: Map[i32, (i32[], i32)] = seeded(n);
    let fallback: (i32[], i32) = ([], 0);
    let first: i32[] = m.get_or(1, fallback).0;
    first = first.append(99);
    let again: (i32[], i32) = m.get_or(1, fallback);
    return first.len() * 100 + again.0.len() * 10 + again.1;
}
function main(): i32 { return shared(7) & 255; }
`},
	{name: "map-clear-call-receiver", atLeast: 2, want: "1|", src: `
import "core/map";
function built(n: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    let i: i32 = 0;
    while (i < n) { m = m.insert(i, i); i = i + 1; }
    return m;
}
function main(): i32 { return built(3).cleared().insert(7, 3).len(); }
`},
	{name: "lent-view-handback", atLeast: 4, want: "12|", src: `
@noinline function handed(text: string): string { return text; }
function laundered(src: string): string {
    let v: str = slice_unchecked(src, 0, 3);
    return handed(v);
}
function fresh_of(text: string): string { return text + "!"; }
function main(): i32 {
    let s: string = "12345 abc";
    let v: str = slice_unchecked(s, 0, 4);
    return handed(v).len() + fresh_of(v).len() + laundered(s).len();
}
`},
	{name: "a-function-value-hands-back-an-owned-array", atLeast: 3, want: "30|", src: `
struct Id { name: string }
function ident_of(e: Id, own acc: string[]): string[] { return acc.append(e.name); }
function each(es: Id[], rounds: i32, reads: (Id, own string[]) => string[]): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < rounds) {
        for e in es { for n in reads(e, []) { t = t + n.len(); } }
        r = r + 1;
    }
    return t;
}
function main(): i32 {
    let es: Id[] = [Id { name: "ab" + "" }, Id { name: "cde" + "" }];
    let t: i32 = each(es, 5, ident_of);
    for e in es { t = t + e.name.len(); }
    return t;
}
`},
	{name: "lent-view-stored", atLeast: 5, want: "18|", src: `
struct Tok { text: string, line: i32 }
function tok_of(text: string, line: i32): Tok { return Tok { text: text, line: line }; }
function add_word(acc: string[], w: string): string[] { return acc.append(w); }
function words_of(src: string): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i + 2 <= src.len()) {
        let v: str = slice_unchecked(src, i, i + 2);
        out = add_word(out, v);
        i = i + 2;
    }
    return out;
}
function toks_of(src: string): Tok[] {
    let out: Tok[] = [];
    let i: i32 = 0;
    while (i + 2 <= src.len()) {
        let v: str = slice_unchecked(src, i, i + 2);
        out = out.append(tok_of(v, i));
        i = i + 2;
    }
    return out;
}
function main(): i32 {
    let n: i32 = 0;
    for w in words_of("abcdef") { n = n + w.len(); }
    for t in toks_of("abcdef") { n = n + t.text.len() + t.line; }
    return n;
}
`},
	{name: "lent-view-kept-through-a-local", atLeast: 6, want: "0|", src: `
struct Item { name: string }
struct Acc { items: Item[] }
function mk(name: string): Item { return Item { name: name }; }
function (a: Acc) with_item(it: Item): Acc { return Acc { items: a.items.append(it) }; }
function add(acc: Acc, name: string): Acc {
    let st: Acc = acc;
    st = st.with_item(mk(name));
    return st;
}
function build(): Acc {
    let src: string = "hello:" + "world";
    let acc: Acc = Acc { items: [] };
    acc = add(acc, slice_unchecked(src, 6, 11));
    return acc;
}
function churn(n: i32): i32 {
    let a: i32[] = [n, n + 1, n + 2];
    let s: string = "abcdefghijklmnop" + "qrstuvwxyz";
    let t: string = s + s;
    return a.len() + t.len();
}
function main(): i32 {
    let acc: Acc = build();
    let z: i32 = churn(3) + churn(4) + churn(5);
    let s: string = acc.items[0].name;
    if (s == "world" && z > 0) { return 0; }
    return 1;
}
`},
	{name: "borrowed-param-append", atLeast: 4, want: "199|", wasm: "out-of-range|", src: `
function push(buf: i32[], v: i32): i32[] { return buf.append(v); }
function build(n: i32): i32[] {
    let out: i32[] = [];
    let i: i32 = 0;
    while (i < n) { out = push(out, i); i = i + 1; }
    return out;
}
function set0(buf: i32[], v: i32): i32[] { return buf.with(0, v); }
function main(): i32 {
    let a: i32[] = build(64);
    let b: i32[] = set0(a, 99);
    if (a[0] != 0) { return 1; }
    if (b[0] != 99) { return 2; }
    if (a.len() != 64 || b.len() != 64) { return 3; }
    return 199;
}
`},
	{name: "borrowed-param-append-counted-elems", atLeast: 4, want: "4|", src: `
struct Box { name: string }
function add(acc: Box[], n: string): Box[] { return acc.append(Box { name: n }); }
function tag(i: i32): string {
    let tbl: string[] = ["aa", "bb", "cc", "dd", "ee", "ff", "gg", "hh", "ii", "jj"];
    return "payload-" + tbl[i % 10] + "-longer-tail";
}
function build(n: i32): Box[] {
    let out: Box[] = [];
    let i: i32 = 0;
    while (i < n) { out = add(out, tag(i)); i = i + 1; }
    return out;
}
function main(): i32 {
    let all: Box[] = build(9);
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < all.len()) {
        if (all[i].name != tag(i)) { return 1 + i; }
        n = n + all[i].name.len();
        i = i + 1;
    }
    return n % 97;
}
`},
	{name: "borrowed-param-append-nested", atLeast: 4, want: "199|", wasm: "out-of-range|", src: `
struct D { name: string, fields: string[] }
function declared(ds: D[], name: string): boolean {
    let i: i32 = 0;
    while (i < ds.len()) {
        if (ds[i].name == name) { return true; }
        i = i + 1;
    }
    return false;
}
function add_one(acc: D[], name: string): D[] {
    if (declared(acc, name)) { return acc; }
    return acc.append(D { name: name, fields: [] });
}
function add_two(acc: D[], a: string, b: string): D[] { return add_one(add_one(acc, a), b); }
function main(): i32 {
    let all: D[] = [];
    all = add_one(all, "seed-value-one");
    all = add_two(all, "seed-value-two", "seed-value-three");
    all = add_two(all, "seed-value-four", "seed-value-five");
    all = add_one(all, "seed-value-one");
    let want: string[] = ["seed-value-one", "seed-value-two", "seed-value-three", "seed-value-four", "seed-value-five"];
    if (all.len() != want.len()) { return 1; }
    let i: i32 = 0;
    while (i < all.len()) {
        if (all[i].name != want[i]) { return 2 + i; }
        i = i + 1;
    }
    return 199;
}
`},
	{name: "call-result-link", atLeast: 15, want: "82|", src: `
function f(b: i32[], v: i32): i32[] { return b.append(v); }
function h(b: i32[], v: i32): i32[] {
    if (v % 2 == 0) { return b; }
    return b.append(v);
}
function two(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }
function cond(b: i32[], v: i32): i32[] { b = h(b, v); b = h(b, v + 1); return h(b, v + 3); }
function looped(b: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) { b = h(b, i); i = i + 1; }
    return b;
}
function read_back(b: i32[], v: i32): i32 {
    let t: i32[] = h(b, v);
    let u: i32[] = f(t, 7);
    return t.len() * 100 + u.len();
}
function branch(b: i32[], v: i32): i32[] {
    let t: i32[] = h(b, v);
    if (v > 3) { return t; }
    return [9, 9];
}
function then_append(b: i32[], v: i32): i32[] {
    b = h(b, v);
    b = b.append(100);
    return f(b, 101);
}
function early(b: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) {
        b = f(b, i);
        if (i == 2) { return b; }
        i = i + 1;
    }
    return b;
}
function sum(a: i32[]): i32 {
    let s: i32 = 0;
    for x in a { s = s + x; }
    return s;
}
function word(b: string[], v: i32): string[] {
    let tbl: string[] = ["aa", "bbb", "c"];
    if (v % 3 == 0) { return b; }
    return b.append("w-" + tbl[v % 3]);
}
function words(b: string[], v: i32): string[] { b = word(b, v); return word(b, v + 1); }
function pair(a: string[], b: string[], v: i32): string[] {
    a = word(a, v);
    return word(a, b.len());
}
function total(a: string[]): i32 {
    let s: i32 = 0;
    for x in a { s = s + x.len(); }
    return s;
}
function ints(): i32 {
    let acc: i32[] = [1, 2, 3];
    let r: i32[] = two(acc, 5);
    if (acc.len() != 3 || r.len() != 5 || sum(acc) != 6 || sum(r) != 17) { return 1; }
    acc = r;
    let k: i32 = 0;
    while (k < 20) {
        let before: i32 = acc.len();
        let r2: i32[] = cond(acc, k);
        if (acc.len() != before) { return 2; }
        acc = r2;
        k = k + 1;
    }
    let keep: i32[] = acc;
    let s1: i32 = sum(acc);
    acc = looped(acc, 7);
    if (sum(keep) != s1) { return 3; }
    k = 0;
    while (k < 6) {
        let want: i32 = acc.len() * 100 + acc.len() + 1;
        if (k % 2 == 1) { want = (acc.len() + 1) * 100 + acc.len() + 2; }
        if (read_back(acc, k) != want) { return 4; }
        acc = branch(acc, k);
        acc = then_append(acc, k);
        k = k + 1;
    }
    let keep2: i32[] = acc;
    let l2: i32 = keep2.len();
    acc = early(acc, 5);
    if (keep2.len() != l2 || acc.len() != l2 + 3) { return 5; }
    return 100 + sum(acc) % 50;
}
function strs(): i32 {
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < 30) {
        let old: string[] = acc;
        let n: i32 = old.len();
        acc = words(acc, i);
        if (old.len() != n) { return 6; }
        acc = pair(acc, acc, i);
        i = i + 1;
    }
    let shared: string[] = acc;
    let t: i32 = total(shared);
    let r: string[] = pair(acc, acc, 1);
    if (total(shared) != t || total(acc) != t || r.len() < acc.len()) { return 7; }
    return 100 + total(r) % 50;
}
function main(): i32 {
    let a: i32 = ints();
    let b: i32 = strs();
    if (a < 100) { return a; }
    if (b < 100) { return b; }
    return (a - 100) + (b - 100);
}
`},
	{name: "array-lent-twice", atLeast: 3, want: "32|", src: `
function push(b: i32[], v: i32): i32[] { return b.append(v); }
function pair(a: i32[], b: i32[]): i32 {
    let t: i32[] = push(a, 7);
    return t.len() * 10 + b.len();
}
function main(): i32 {
    let acc: i32[] = [];
    acc = acc.append(1);
    acc = acc.append(2);
    return pair(acc, acc);
}
`},
	{name: "borrowed-param-obligations", atLeast: 5, want: "211|", wasm: "out-of-range|", src: `
struct B { n: string }
function tagx(i: i32): string { let t: string[] = ["aa", "bb", "cc", "dd"]; return "long-payload-" + t[i % 4]; }
function maybe(xs: B[], flag: boolean): B[] {
    if (flag) { return xs.append(B { n: tagx(1) }); }
    return [];
}
function ignore(xs: B[]): i32 { let ys: B[] = xs.append(B { n: tagx(2) }); return ys.len(); }
function twice(xs: B[]): B[] { xs = xs.append(B { n: tagx(3) }); xs = xs.append(B { n: tagx(0) }); return xs; }
function seed(n: i32): B[] { let o: B[] = []; let i: i32 = 0; while (i < n) { o = o.append(B { n: tagx(i) }); i = i + 1; } return o; }
function main(): i32 {
    let a: B[] = maybe(seed(3), true);
    let b: B[] = maybe(seed(3), false);
    let c: i32 = ignore(seed(3));
    let d: B[] = twice(seed(3));
    let live: B[] = seed(2);
    let e: B[] = twice(live);
    return a.len() * 1000 + b.len() * 100 + c * 10 + d.len() + e.len() + live.len();
}
`},
	{name: "field-append", atLeast: 9, want: "199|", wasm: "out-of-range|", src: `
struct R { ops: i32[], n: i32 }
struct Box { name: string }
struct Q { items: Box[], tag: string }
function emitop(r: R, op: i32): R { return R { ...r, ops: r.ops.append(op) }; }
function addbox(q: Q, n: string): Q { return Q { ...q, items: q.items.append(Box { name: n }) }; }
function emit2(r: R, a: i32, b: i32): R { return emitop(emitop(r, a), b); }
function osz(code: i32[], size: i32): i32[] {
    if (size == 16) { return code.append(102); }
    return code;
}
function via(r: R, size: i32): R { return R { ...r, ops: osz(r.ops, size) }; }
function keeps(): i32 {
    let r: R = R { ops: [1, 2, 3], n: 7 };
    let s: R = emitop(r, 4);
    let u: R = emit2(r, 5, 6);
    let v: R = via(r, 16);
    return r.ops.len() * 1000 + s.ops.len() * 100 + u.ops.len() * 10 + v.ops.len();
}
function dying(n: i32): i32 {
    let r: R = R { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < n) { r = emitop(r, i); r = via(r, 16); i = i + 1; }
    return r.ops.len();
}
function boxes(n: i32): i32 {
    let q: Q = Q { items: [], tag: "t" };
    let i: i32 = 0;
    while (i < n) { q = addbox(q, "name-" + q.tag); i = i + 1; }
    let k: Q = addbox(q, "extra-longer-name-payload");
    let total: i32 = 0;
    for b in q.items { total = total + b.name.len(); }
    return total * 1000 + k.items.len() * 10 + q.items.len();
}
function main(): i32 {
    if (keeps() != 3454) { return 1; }
    if (dying(1000) != 2000) { return 2; }
    if (boxes(5) != 30065) { return 3; }
    return 199;
}
`},
	{name: "own-forwarded-through-a-caller", atLeast: 3, want: "62|", src: `
function visit(x: string, own acc: string[]): string[] {
    if (x.len() > 2) { return acc.append(x); }
    return acc;
}
function fold[T](xs: string[], own acc: T, f: (string, own T) => T): T {
    for x in xs { acc = f(x, acc); }
    return acc;
}
function forward(xs: string[], own acc: string[]): string[] { return fold(xs, acc, visit); }
function census(rows: string[][], acc: string[]): string[] {
    let a: string[] = acc;
    for r in rows { a = forward(r, a); }
    return a;
}
function main(): i32 {
    let seed: string[] = ["seed-one", "seed-two"];
    let out: string[] = census([["alpha", "b", "gamma"], ["delta", "e"], ["epsilon"]], seed);
    return (out.len() * 10 + seed.len()) % 251;
}
`},
	{name: "capture-write", atLeast: 3, want: "7|", src: `
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    let total: i32 = 0;
    let add = (x: i32): i32 => { total = total + x; return total; };
    let a: i32 = apply(add, 3);
    let b: i32 = apply(add, 4);
    return total;
}
`},
	{name: "generic-clones", atLeast: 6, want: "50|", src: `
function first[T](xs: T[]): T { return xs[0]; }
function append_all[T](into: T[], more: T[]): T[] {
    let out: T[] = into;
    for m in more { out = out.append(m); }
    return out;
}
function fold[T](xs: i32[], own acc: T, visit: (i32, own T) => T): T {
    for x in xs { acc = visit(x, acc); }
    return acc;
}
function add(x: i32, own acc: i32): i32 { return acc + x; }
function total(xs: i32[]): i32 { return fold(xs, 0, add); }
function main(): i32 {
    let names: string[] = append_all(["ab" + ""], ["cde" + "", "f" + ""]);
    return first([40]) + total([1, 2, 3]) + names.len() + names[2].len();
}
`},
	{name: "record-handed-through-wrapper", atLeast: 4, want: "4|", src: `
struct R { ops: i32[], n: i32, name: string }
function emit(r: R, op: i32): R { return R { ...r, ops: r.ops.append(op) }; }
function helper(r: R, slot: i32): R {
    r = emit(r, slot);
    r = emit(r, slot + 1);
    return emit(r, slot + 2);
}
function build(n: i32): R {
    let r: R = R { ops: [], n: 0, name: "h" + "" };
    let i: i32 = 0;
    while (i < n) { r = helper(r, i); i = i + 1; }
    return r;
}
function main(): i32 {
    let r: R = build(501);
    if (__arr_push_shared_count() > 0) { return 200 + (__arr_push_shared_count() % 50); }
    return r.ops.len() % 100 + r.name.len();
}
`},
	{name: "beyond-ast", atLeast: 3, want: "23|", src: `
function picked(k: i32): i32 {
    let rows: i32[] = [k, k];
    let some: i32[] = (match (Some(rows)) { Some(r) => r, None => [] });
    return some.len();
}
function widen(k: i32): i32[] {
    let o: Option[i32] = Some(k);
    return (match (o) { Some(v) => [v, v, v], None => [] });
}
function main(): i32 { return picked(4) * 10 + widen(2).len(); }
`},
	{name: "value-blocks", atLeast: 3, want: "1|", src: `
enum Shade { Dark, Light }
struct Tagged { n: i32, tag: string }
function vb_words(k: i32): i32 {
    let o: Option[i32] = Some(k);
    let words: string[] = (match (o) { Some(v) => ["a" + "b", "c"], None => [] });
    let sh: Shade = Dark;
    let tag: string = (match (sh) { Dark => "d" + "k", Light => "l" });
    let ot: Option[string] = Some(tag);
    let cell: Tagged = (match (ot) { Some(t) => Tagged { n: k, tag: t }, None => Tagged { n: 0, tag: "" } });
    let pair: (i32, string) = (if (k > 1) { (k, cell.tag) } else { (0, "z") });
    return words.len() * 100 + tag.len() * 10 + cell.tag.len() + pair.1.len();
}
function vb_rows(k: i32): i32 {
    let rows: i32[] = (if (k > 0) { [k, k] } else { [k] });
    let orows: Option[i32[]] = Some(rows);
    let picked: i32[] = (match (orows) { Some(r) => r, None => [0 - 1] });
    return rows.len() * 10 + picked.len();
}
function main(): i32 { return (vb_words(3) + vb_rows(2) + vb_rows(0)) & 255; }
`},
	{name: "overflow-operators", atLeast: 7, want: "4|", src: `
function s32(a: i32, b: i32): i32 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function u32s(a: u32, b: u32): u32 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function s64(a: i64, b: i64): i64 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function u8s(a: u8, b: u8): u8 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function c32(a: i32, b: i32): i32 {
    let t: i32 = 0;
    match (a +? b) { Some(v) => { t = t + v; }, None => { t = t + 1; } }
    match (a -? b) { Some(v) => { t = t + v; }, None => { t = t + 2; } }
    match (a *? b) { Some(v) => { t = t + v; }, None => { t = t + 3; } }
    match (a /? b) { Some(v) => { t = t + v; }, None => { t = t + 4; } }
    match (a %? b) { Some(v) => { t = t + v; }, None => { t = t + 5; } }
    match (a <<? b) { Some(v) => { t = t + v; }, None => { t = t + 6; } }
    match (a >>? b) { Some(v) => { t = t + v; }, None => { t = t + 7; } }
    return t;
}
function c64(a: i64, b: i64): i64 {
    let t: i64 = 0;
    match (a +? b) { Some(v) => { t = t + v; }, None => { t = t + 1i64; } }
    match (a *? b) { Some(v) => { t = t + v; }, None => { t = t + 3i64; } }
    match (a /? b) { Some(v) => { t = t + v; }, None => { t = t + 4i64; } }
    match (a <<? b) { Some(v) => { t = t + v; }, None => { t = t + 6i64; } }
    return t;
}
function cu(a: u32, b: u32): u32 {
    let t: u32 = 0;
    match (a +? b) { Some(v) => { t = t + v; }, None => { t = t + 1u32; } }
    match (a -? b) { Some(v) => { t = t + v; }, None => { t = t + 2u32; } }
    match (a *? b) { Some(v) => { t = t + v; }, None => { t = t + 3u32; } }
    match (a %? b) { Some(v) => { t = t + v; }, None => { t = t + 5u32; } }
    match (a >>? b) { Some(v) => { t = t + v; }, None => { t = t + 7u32; } }
    return t;
}
function main(): i32 {
    let r: i32 = s32(2147483000, 1000) + s32(-5, 7) + (u32s(4000000000u32, 500000000u32) as i32) + ((s64(9223372036854775000i64, 1000i64) >> 40i64) as i32) + (u8s(200u8, 100u8) as i32);
    r = r + c32(2147483000, 1000) + c32(7, 0) + c32(-2147483648, -1) + ((c64(5i64, 3i64) & 1023i64) as i32) + (cu(4000000000u32, 500000000u32) as i32);
    return r & 255;
}
`},
	{name: "type-param-only-in-a-lambda-parameter", atLeast: 10, want: "52|", src: `
import "core/iter" as iter;

function filter[T, I: iter.Iterator[T]](it: I, keep: (T) => boolean): T[] {
    let out: T[] = [];
    let cur = it;
    let go = true;
    while (go) {
        match (cur.next()) {
            Some(t) => {
                if (keep(t.0)) { out = out.append(t.0); }
                cur = t.1;
            },
            None => { go = false; }
        }
    }
    return out;
}

function map[T, U, I: iter.Iterator[T]](it: I, f: (T) => U): U[] {
    let out: U[] = [];
    let cur = it;
    let go = true;
    while (go) {
        match (cur.next()) {
            Some(t) => {
                out = out.append(f(t.0));
                cur = t.1;
            },
            None => { go = false; }
        }
    }
    return out;
}

function kept(xs: i32[]): i32 {
    let big = filter(iter.of(xs), (x: i32): boolean => { return x > 3; });
    return big.len() * 10 + big[0] + big[1];
}

function doubled(xs: i32[]): i32 {
    let all = map(iter.of(xs), (x: i32): i32 => { return x * 2; });
    return all.len() + all[2];
}

function main(): i32 { return kept([5, 2, 8, 1, 4]) + doubled([1, 2, 3]); }
`},
	{name: "str-binding-beside-a-generic-struct", atLeast: 2, want: "32|", src: `
struct Box[T] { v: T }
function head(s: string): i32 {
    let v: str = slice_unchecked(s, 0, 3);
    let w: str = slice_unchecked(v, 1, 3);
    return v.len() * 10 + w.len();
}
function main(): i32 {
    let b: Box[i32] = Box { v: head("hello" + "") };
    return b.v;
}
`},
	{name: "defer-binding-out-of-its-block", atLeast: 4, want: "16|", src: `
function conditional(enabled: boolean): i32 {
    let seen: i32 = 1;
    loop {
        if (enabled) {
            let items: i32[] = [2];
            defer seen = items[0];
            items = [9];
        }
        break;
    }
    return seen;
}
function per_iteration(): i32 {
    let seen: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let k: i32[] = [i];
        defer seen = seen + k[0];
        i = i + 1;
    }
    return seen;
}
function from_value_block(): i32 {
    let seen: i32 = 0;
    loop {
        let yielded: i32 = { let items: i32[] = [2]; defer seen = items[0]; items = [4]; 1 };
        seen = seen + yielded;
        break;
    }
    return seen;
}
function main(): i32 {
    return conditional(true) + per_iteration() + from_value_block();
}
`},
	{name: "defer-binding-at-a-type-with-no-literal-zero", atLeast: 4, want: "21|", src: `
struct P { a: i32 }
function via_record(on: boolean): i32 {
    let seen: i32 = 0;
    loop {
        if (on) { let v: P = P { a: 3 }; defer seen = v.a; v = P { a: 7 }; }
        break;
    }
    return seen;
}
function via_tuple(on: boolean): i32 {
    let seen: i32 = 0;
    loop {
        if (on) { let v: (i32, i32) = (1, 2); defer seen = v.0; v = (5, 6); }
        break;
    }
    return seen;
}
function via_variant(on: boolean): i32 {
    let seen: i32 = 0;
    loop {
        if (on) {
            let v: Option[i32] = Some(3);
            defer { match (v) { Some(n) => { seen = n; }, None => { seen = 1; } } }
            v = Some(9);
        }
        break;
    }
    return seen;
}
function main(): i32 { return via_record(true) + via_tuple(true) + via_variant(true); }
`},
	{name: "defer-typed-return-temp", atLeast: 3, want: "72|", src: `
function snapshot(): i32[] {
    let items: i32[] = [7];
    defer items = [9];
    return items;
}
function labelled(): string {
    let s: string = "ok";
    defer s = "late" + "";
    return s;
}
function main(): i32 {
    return snapshot()[0] * 10 + labelled().len();
}
`},
	{name: "defer-return-temp-at-a-type-with-no-literal-zero", atLeast: 7, want: "8|", src: `
struct P { a: i32, s: string }
enum Shape { Dot, Box(i32) }
function record(n: i32): P {
    let p: P = P { a: n, s: "p" };
    defer p = P { a: 0, s: "" };
    return p;
}
function pair(n: i32): (i32, string) {
    let t: (i32, string) = (n, "one");
    defer t = (0, "");
    return t;
}
function variant(n: i32): Shape {
    let s: Shape = Shape.Dot;
    defer s = Shape.Box(n);
    if (n % 2 == 0) { return Shape.Box(n); }
    return s;
}
function maybe(n: i32): Option[P] {
    let log: i32 = 0;
    defer log = log + 1;
    if (n < 0) { return None; }
    return Some(P { a: n, s: "m" });
}
function risky(fail: boolean): Result[i32, i32] {
    let code: i32 = 0;
    errdefer { code = code + 7; }
    if (fail) { return Err(code + 1); }
    return Ok(50);
}
function two_defers(n: i32): P {
    let p: P = P { a: n, s: "x" };
    defer { p = P { a: 0, s: "" }; }
    let q: P = p;
    defer q = P { a: 5, s: "q" };
    return P { a: p.a + q.a, s: p.s + q.s };
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let pr: (i32, string) = pair(i);
        t = t + record(i).a + pr.0 + pr.1.len();
        match (variant(i)) { Shape.Dot => { t = t + 1; }, Shape.Box(v) => { t = t + v; } }
        match (maybe(i - 100)) { Some(p) => { t = t + p.a + p.s.len(); }, None => { t = t + 2; } }
        match (risky(i % 3 == 0)) { Ok(v) => { t = t + v; }, Err(e) => { t = t + e; } }
        t = t + two_defers(i).s.len();
        i = i + 1;
    }
    return t % 97;
}
`},
	{name: "defer-in-a-closure-factory", atLeast: 7, want: "90|", src: `
function capturing(k: i32): (i32) => i32 {
    let seen: i32 = 0;
    defer seen = seen + 1;
    return (x: i32): i32 => x + k;
}
function through_local(k: i32): (i32) => i32 {
    let f: (i32) => i32 = (x: i32): i32 => x * k;
    let seen: i32 = 0;
    defer seen = seen + 1;
    return f;
}
function plain(): (i32) => i32 {
    let n: i32 = 0;
    defer n = 1;
    return (x: i32): i32 => x + 4;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        t = t + capturing(i)(1) + plain()(i) + through_local(i)(2);
        i = i + 1;
    }
    return t % 97;
}
`},
	{name: "a-function-value-returned-from-a-declaration", atLeast: 9, want: "8|", src: `
function scalar_capture(n: i32): (i32) => i32 {
    return (x: i32): i32 => { return x + n; };
}
function array_capture(n: i32): (i32) => i32 {
    let xs: i32[] = [n, n + 1, n + 2];
    return (x: i32): i32 => { return x + xs[0] + xs[2]; };
}
function two_captures(n: i32): (i32) => i32 {
    let xs: i32[] = [n, n];
    let ys: i32[] = [n, n];
    return (x: i32): i32 => { return x + xs[0] + ys[1]; };
}
function churn_one(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let f: (i32) => i32 = array_capture(i);
        t = t + f(0) % 3;
        i = i + 1;
    }
    return t;
}
function churn_two(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let g: (i32) => i32 = two_captures(i);
        t = t + g(0) % 3;
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    let s: (i32) => i32 = scalar_capture(2);
    return (churn_one() + churn_two()) % 7 + s(3);
}
`},
	{name: "a-function-value-in-a-record-field", atLeast: 5, want: "4|", src: `
struct Holder { f: (i32) => i32 }
function plain(x: i32): i32 { return x + 1; }
function make(n: i32): Holder {
    let xs: i32[] = [n, n + 1, n + 2];
    return Holder { f: (x: i32): i32 => { return x + xs[0] + xs[2]; } };
}
function main(): i32 {
    let a: Holder = Holder { f: plain };
    let t: i32 = a.f(1);
    let i: i32 = 0;
    while (i < 50) {
        let h: Holder = make(i);
        t = t + h.f(0) % 3;
        i = i + 1;
    }
    return t % 7;
}
`},
	{name: "the-bytes-of-a-string", atLeast: 3, want: "6|", src: `
function total(s: string): i32 {
    let bs = s.as_bytes();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < bs.len()) { t = t + (bs[i] as i32); i = i + 1; }
    return t;
}
function first(s: string): i32 {
    let bs = s.as_bytes();
    if (bs.len() == 0) { return 0; }
    return bs[0] as i32;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { t = t + total("abc") % 5 + first("z") % 3; i = i + 1; }
    return t % 7;
}
`},

	{name: "the-at-binder-in-a-match-arm", atLeast: 4, want: "3|", src: `
enum Shape { Circle(string), Square(string) }

function label(s: Shape): string {
    match (s) {
        Circle(c) => { return c; },
        Square(q) => { return q; }
    }
}

function whole_after_payload(s: Shape): i32 {
    match (s) {
        w @ Circle(c) => { return c.len() + label(w).len(); },
        _ => { return 0; }
    }
}

function whole_as_value(s: Shape): Shape {
    return match (s) {
        w @ Circle(v) => w,
        w2 @ Square(v2) => w2
    };
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        t = t + whole_after_payload(Shape.Circle("abc"));
        t = t + label(whole_as_value(Shape.Square("wxyz"))).len();
        i = i + 1;
    }
    return t % 7;
}
`},

	{name: "a-reference-cast-to-an-address", runtimeFloor: true, atLeast: 2, want: "0|", src: `
import "std/string";
@noinline function keep_slot(p: usize): usize { return p; }
function check_slot(s: string): i32 {
    let n: i32 = s.len();
    let address: usize = s.bytes() as usize;
    if (__raw_load_ptr(address, 0) != n as usize) { return 1; }
    let view: [u8] = s.as_bytes();
    let slot: usize = keep_slot(view as usize);
    if (slot != view as usize) { return 2; }
    return 0;
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { t = t + check_slot("abc"); i = i + 1; }
    return t;
}
`},

	{name: "the-array-reduce-and-join-builtins", atLeast: 70, want: "1|", src: `
import "std/array";

function total(xs: i32[]): i32 { return xs.sum() + xs.product(); }

function names(sep: string): i32 {
    let parts: string[] = ["ab", "cde", "f"];
    return parts.join(sep).len();
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { t = t + total([2, 3, 4]) + names("--"); i = i + 1; }
    return t % 7;
}
`},

	{name: "stdin-and-the-stream-handles", atLeast: 44, want: "11|alpha\nbeta\n", stdin: "alpha\nbeta\n", src: `
import "std/io";

function main(): i32 {
    let text: string = "";
    match (io.read_all_stdin()) { Ok(input) => { text = input; }, Err(_) => { return 253; } }
    let w: Writer = stdout();
    w.write(text);
    let e: Writer = stderr();
    e.write("");
    return text.len();
}
`},

	{name: "a-closure-that-captures-a-closure", atLeast: 5, want: "1|", src: `
function make(n: i32): (i32) => i32 {
    let xs: i32[] = [n, n + 1, n + 2];
    return (x: i32): i32 => { return x + xs[0] + xs[2]; };
}

function wrap(n: i32): (i32) => i32 {
    let inner: (i32) => i32 = make(n);
    return (x: i32): i32 => { return inner(x) + 1; };
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let f: (i32) => i32 = wrap(i);
        t = t + f(0) % 3;
        i = i + 1;
    }
    return t % 7;
}
`},
	{name: "self-tail-recursion", atLeast: 2, want: "1|", src: `
function count(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return count(n - 1, acc + 1);
}

function main(): i32 { return count(1000000, 0) % 7; }
`},
	{name: "an-array-of-capturing-closures", atLeast: 4, want: "2|", src: `
function build(n: i32): ((i32) => i32)[] {
    let caps: i32[] = [n, n + 1, n + 2];
    let other: i32[] = [n * 2, n * 3];
    return [((x: i32) => { return x + caps[0] + caps[2]; }),
            ((y: i32) => { return y + other[0] + other[1]; })];
}

function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let fs: ((i32) => i32)[] = build(i);
        total = total + fs[0](1) % 3 + fs[1](1) % 5;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 90; }
    return total % 7;
}
`},
	{name: "sibling-function-with-a-wide-signature", atLeast: 3, want: "28|", src: `
function main(): i32 {
    function scale(k: i64): f64 { return (k as f64) / 4.0; }
    function twice(n: i32): i32 { return (scale((n as i64) * 6i64) as i32) + (scale(9i64) as i32); }
    let half: (i64) => i64 = (x: i64) => x / 2i64;
    let via: (i32) => i32 = (n: i32) => (half((n as i64) * 5i64) as i32);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 30) {
        t = t + twice(i) + via(i);
        i = i + 1;
    }
    return t % 251;
}`},
	{name: "nested-function-returning-an-array-of-functions", atLeast: 3, want: "65|", src: `
function main(): i32 {
    function make(k: i32): ((i32) => i32)[] { return [((a: i32) => a + k), ((b: i32) => b * k)]; }
    function pick(n: i32): ((i32) => i32)[] { return make(n + 1); }
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 40) {
        let fs: ((i32) => i32)[] = pick(i);
        t = t + fs[0](i) % 13 + fs[1](2) % 7;
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "value-block-arm-is-a-template-call-over-a-capturing-lambda", atLeast: 3, want: "74|", src: `
function id[T](x: T): T { return x; }
function main(): i32 {
    let k: i32 = 3;
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 40) {
        let c: boolean = i % 3 == 0;
        let a: ((i32) => i32)[] = [(if (c) { id(((x: i32) => x + k)) } else { ((y: i32) => y - k) })];
        t = t + a[0](i) % 17;
        i = i + 1;
    }
    return t % 211;
}`},
	{name: "passthrough-forwards-a-nested-value-block-of-lambda-arrays", atLeast: 3, want: "148|", wasm: "out-of-range|", src: `
enum Color { Red, Green, Blue }
function id[T](x: T): T { return x; }
function choose(p: Color, k: i32): i32 {
    let v1: ((i32) => i32)[] = (match (p) {
        Red => [((a: i32) => a + k)],
        Green => [id(((b: i32) => b * k))],
        Blue => id((match (p) { Red => [((x: i32) => x)], Green => [((y: i32) => y - k)], Blue => [((z: i32) => 9 + k), ((w: i32) => w)] }))
    });
    return v1[0](4) + v1.len();
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 30) {
        let p: Color = if (i % 3 == 0) { Red } else if (i % 3 == 1) { Green } else { Blue };
        t = t + choose(p, i);
        i = i + 1;
    }
    return t % 223;
}`},
	{name: "a-map-unit-is-shared", atLeast: 4, want: "111|", src: `
import "core/map";
function id[T](x: T): T { return x; }
function same(m: Map[i32, i32]): Map[i32, i32] { return m; }
function total(m: Map[i32, i32], n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + m.get_or(i, 0); i = i + 1; }
    return t;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 40) {
        let m: Map[i32, i32] = map_new(8);
        let i: i32 = 0;
        while (i < 6) { m = m.insert(i, i * r); i = i + 1; }
        let a: Map[i32, i32] = id(m);
        let b: Map[i32, i32] = same(a);
        acc = acc + total(a, 6) % 17 + total(b, 6) % 13 + total(m, 6) % 7;
        r = r + 1;
    }
    return acc % 113;
}`},
	{name: "a-shared-string-map-releases-its-columns-once", atLeast: 2, want: "49|", src: `
import "core/map";
function pick(c: boolean, a: Map[string, string], b: Map[string, string]): Map[string, string] { return if (c) { a } else { b }; }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 30) {
        let m: Map[string, string] = map_new(4);
        let key: string = if (r % 2 == 0) { "even" } else { "odd" };
        m = m.insert(key + "k", key + "v");
        m = m.insert("x", "y");
        let n: Map[string, string] = map_new(4);
        n = n.insert("z", "w");
        let p: Map[string, string] = pick(r % 2 == 0, m, n);
        acc = acc + p.len() + m.len() + n.len() + p.get_or("x", "").len();
        r = r + 1;
    }
    return acc % 101;
}`},
	{name: "a-shared-map-is-copied-before-it-is-written", atLeast: 1, want: "72|", src: `
import "core/map";
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 30) {
        let m: Map[i32, i32] = map_new(8);
        m = m.insert(1, 10);
        let snapshot: Map[i32, i32] = m;
        m = m.insert(1, 99);
        m = m.insert(2, 20);
        acc = acc + snapshot.len() * 100 + snapshot.get_or(1, -1) + m.get_or(1, -1) + m.get_or(2, -1) + m.len();
        r = r + 1;
    }
    return acc % 127;
}`},
	{name: "a-map-the-frame-still-reads-is-not-written", atLeast: 2, want: "85|", src: `
import "core/map";
function grown(m: Map[i32, i32], k: i32): Map[i32, i32] { return m.insert(k, k * 3); }
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    let n: Map[i32, i32] = m.insert(2, 20);
    let g: Map[i32, i32] = grown(m, 7);
    let had: boolean = n.contains(2);
    let rest = n.remove(2);
    let snapshot: Map[i32, i32] = n;
    let had2: boolean = n.contains(1);
    let rest2 = n.remove(1);
    if (g.get_or(7, -1) != 21 || !had || !had2) { return 99; }
    return m.len() + n.len() * 2 + g.len() * 4 + rest.len() * 8 + snapshot.len() * 16 + rest2.len() * 32;
}`},
	{name: "a-shared-string-map-is-copied-before-it-is-written", atLeast: 1, want: "31|", src: `
import "core/map";
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 30) {
        let word: string = if (r % 2 == 0) { "even" } else { "odd" };
        let m: Map[string, string] = map_new(4);
        m = m.insert(word + "k", word + "v");
        let snapshot: Map[string, string] = m;
        m = m.insert(word + "k", "changed");
        m = m.insert("x", word);
        acc = acc + snapshot.len() + snapshot.get_or(word + "k", "").len() + m.get_or(word + "k", "").len() + m.len();
        r = r + 1;
    }
    return acc % 101;
}`},
	{name: "os-floor-queries", atLeast: 2, want: "255|", nativeOnly: true, src: `
function queries(): i32 {
    let n: i32 = 0;
    match (getcwd()) { Ok(wd) => { if (wd.len() > 0) { n = n + 1; } }, Err(e) => {} }
    if (hostname().len() > 0) { n = n + 2; }
    if (cpu_count() > 0) { n = n + 4; }
    if ((geteuid() as i64) >= 0) { n = n + 8; }
    if (uname_field(0).len() > 0) { n = n + 16; }
    if (isatty(1) || !isatty(1)) { n = n + 32; }
    let old: i32 = umask(18);
    if (umask(old) == 18) { n = n + 64; }
    if (environ().len() > 0) { n = n + 128; }
    return n;
}
function main(): i32 { return queries(); }
`},
	{name: "os-floor-handles", atLeast: 2, want: "15|", wasm: "out-of-range|", src: `
function handles(path: string): i32 {
    let n: i32 = 0;
    match (open_reader(path)) {
        Ok(r) => {
            match (r.stat()) { Ok(st) => { n = n + 1; }, Err(e) => { n = n + 1000; } }
            match (r.flags()) { Ok(f) => { n = n + 2; }, Err(e) => { n = n + 2000; } }
            match (r.seek(0, 0)) { Ok(off) => { n = n + 4; }, Err(e) => { n = n + 4000; } }
            match (r.close()) { Err(e) => { n = n + 8000; }, Ok(_) => { n = n + 8; } }
        },
        Err(e) => { n = n + 16000; },
    }
    return n;
}
function main(): i32 { return handles("/dev/null") % 256; }
`},
	{name: "os-floor-files", atLeast: 2, want: "255|", nativeOnly: true, src: `
function files(dir: string): i32 {
    let n: i32 = 0;
    let sub: string = dir + "/d";
    match (create_dir(sub, 448)) { Ok(u) => { n = n + 1; }, Err(e) => { n = n + 1000; } }
    match (access(sub, 0)) { Ok(u) => { n = n + 2; }, Err(e) => { n = n + 2000; } }
    match (rename(sub, dir + "/e")) { Ok(u) => { n = n + 4; }, Err(e) => { n = n + 4000; } }
    match (create_symlink(dir + "/e", dir + "/l")) { Ok(u) => { n = n + 8; }, Err(e) => { n = n + 8000; } }
    match (read_link(dir + "/l")) { Ok(t) => { if (t.len() > 0) { n = n + 16; } }, Err(e) => { n = n + 16000; } }
    match (chmod(dir + "/e", 493)) { Ok(u) => { n = n + 32; }, Err(e) => { n = n + 32000; } }
    match (statfs(dir)) { Ok(fs) => { n = n + 64; }, Err(e) => { n = n + 64000; } }
    match (remove_file(dir + "/l")) { Ok(u) => { n = n + 128; }, Err(e) => { n = n + 128000; } }
    match (remove_dir(dir + "/e")) { Ok(u) => { n = n + 256; }, Err(e) => { n = n + 256000; } }
    return n;
}
function main(): i32 {
    let n: i32 = 0;
    match (temp_dir("fernsem")) {
        Ok(d) => { n = files(d); match (remove_dir(d)) { Ok(u) => { }, Err(e) => { n = n + 7; } } },
        Err(e) => { n = 5; },
    }
    return n % 256;
}
`},
	{name: "os-floor-subprocess", atLeast: 1, want: "3|hi\n\n", nativeOnly: true, src: `
function main(): i32 {
    let p: ProcessResult = subprocess("/bin/echo", ["hi"], "");
    print(p.stdout);
    return p.exit_code + p.stdout.len();
}
`},
	{name: "generic-struct-method-redeclares-receiver-vars", atLeast: 2, want: "27|", src: `
struct Pair[T] { a: T, b: T }
function (p: Pair[T]) put[T](v: T): Pair[T] { return Pair { a: p.b, b: v }; }
function main(): i32 {
    let p: Pair[i32] = Pair { a: 1, b: 2 };
    p = p.put(7);
    return p.a * 10 + p.b;
}
`},
	{name: "ordmap-bounded-method-clones", atLeast: 61, want: "52|", src: `
import "std/ordmap";
function main(): i32 {
    let m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
    m = m.insert(3, 4);
    m = m.insert(3, 5);
    m = m.insert(9, 1);
    return m.get_or(3, 0) * 10 + m.len();
}
`},
	{name: "void-callback", atLeast: 5, want: "1|a\nb\nc!\nd\ne\nf\n", src: `
function each(xs: string[], f: (string) => void): void {
    for x in xs { f(x); }
}
function show(x: string): void { print(x); }
struct Visitor { hit: (string) => void }
function main(): i32 {
    each(["a", "b"], show);
    each(["c"], (x: string): void => { print(x + "!"); });
    let v: Visitor = Visitor { hit: show };
    v.hit("d");
    let g: (string) => void = show;
    g("e");
    let pair: ((string) => void, i32) = (show, 1);
    pair.0("f");
    return pair.1;
}
`},
	{name: "closure-in-a-variant-field", atLeast: 4, want: "33|", src: `
enum Step {
    Done(i32),
    Next(i32, (i32) => Step),
}
function make(n: i32, k: i32): Step {
    if (n <= 0) { return Done(k); }
    return Next(n, (x: i32): Step => make(n - 1, k + x));
}
function run(s: Step): i32 {
    let cur: Step = s;
    let guard: i32 = 0;
    while (guard < 100) {
        match (cur) {
            Done(v) => { return v; },
            Next(w, f) => { cur = f(w); }
        }
        guard = guard + 1;
    }
    return -1;
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        total = total + run(make(4, i));
        i = i + 1;
    }
    let dropped: Step = make(2, 7);
    return total;
}
`},
	{name: "os-floor-signals-and-process", atLeast: 2, want: "9|", nativeOnly: true, src: `
function signals(): i32 {
    let n: i32 = 0;
    if (signal_ignore(2) >= 0) { n = n + 1; }
    if (signal_disposition(2) >= 0) { n = n + 1; }
    if (signal_default(2) >= 0) { n = n + 1; }
    let was: i64 = signal_mask(0, 0i64);
    if (was >= 0i64) { n = n + 1; }
    if (priority() > -21) { n = n + 1; }
    let gs: i64[] = getgroups();
    if (gs.len() >= 0) { n = n + 1; }
    match (setgroups(gs)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (set_process_group(0, 0)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (chroot("/")) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    let fds: i32[] = [timer_fd(1)];
    if (poll(fds, 2000) > 0) { n = n + 1; }
    if (poll([], 0) == 0) { n = n + 1; }
    return n;
}
function main(): i32 { return signals(); }
`},
	{name: "os-floor-sockets-and-ids", atLeast: 2, want: "9|", nativeOnly: true, src: `
function sockets(): i32 {
    let n: i32 = 0;
    let listener: i32 = tcp_listen(0);
    if (listener >= 0) {
        let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], tcp_local_port(listener), false);
        if (c >= 0) {
            let a: i32 = tcp_accept(listener);
            if (a >= 0) {
                if (tcp_send_bytes(c, [112u8, 105u8, 110u8, 103u8]) == 4) { n = n + 1; }
                if (tcp_pollable(c) == c) { n = n + 1; }
                let got: u8[] = __alloc_u8(16);
                if (tcp_recv_into(a, got) == 4 && got[0] as i32 == 112) { n = n + 1; }
                if (tcp_close(a) >= 0) { n = n + 1; }
            }
            if (tcp_close(c) >= 0) { n = n + 1; }
        }
        if (tcp_close(listener) >= 0) { n = n + 1; }
    }
    sync();
    match (setuid(0i64)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (setgid(0i64)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (set_priority(priority())) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    return n;
}
function main(): i32 { return sockets(); }
`},
	{name: "os-floor-times-nodes-terminal", atLeast: 3, want: "12|", nativeOnly: true, src: `
function files(dir: string): i32 {
    let n: i32 = 0;
    let f: string = dir + "/f";
    match (write_file(f, "x")) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (chmod_at(f, 420, true)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (set_file_times(f, 1000000i64, 0i64, 2000000i64, 0i64, 0)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (stat(f)) {
        Ok(st) => { if (st.mtime == 2000000i64) { n = n + 1; } },
        Err(_) => {}
    }
    match (mknod(dir + "/fifo", 4096 + 420, 0, 0)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (chdir(dir)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (remove_file("fifo")) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (remove_file("f")) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    return n;
}
function terminal(): i32 {
    let n: i32 = 0;
    match (window_size(1)) {
        Ok(w) => { if (w.rows >= 0i64) { n = n + 1; } },
        Err(_) => { n = n + 1; }
    }
    let r: Reader = stdin();
    if (!r.isatty()) { n = n + 1; }
    match (r.window_size()) {
        Ok(w) => { if (w.cols >= 0i64) { n = n + 1; } },
        Err(_) => { n = n + 1; }
    }
    match (open_reader("/dev/null")) {
        Ok(h) => {
            match (h.dup_onto(19)) {
                Err(_) => {},
                Ok(_) => { n = n + 1; }
            }
        },
        Err(_) => {}
    }
    return n;
}
function main(): i32 {
    let n: i32 = 0;
    match (temp_dir("fernsem")) {
        Ok(d) => {
            n = n + files(d);
            match (remove_dir(d)) {
                Ok(_) => {},
                Err(_) => {}
            }
        },
        Err(_) => {}
    }
    return n + terminal();
}
`},
	{name: "array-slice-of-scalars", atLeast: 2, want: "125|", src: `
function sum(xs: [u8]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { t = t + (xs[i] as i32); i = i + 1; }
    return t;
}
function main(): i32 {
    let bytes: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
    let total: i32 = 0;
    let i: i32 = 0;
    while (i + 3 <= 10) { total = total + sum(bytes[i:i + 3]); i = i + 3; }
    total = total + sum(bytes[i:]);
    let words: i32[] = [10, 20, 30, 40];
    let mid: [i32] = words[1:3];
    let wide: i64[] = [100i64, 200i64, 300i64];
    let tail: [i64] = wide[1:];
    let fl: f64[] = [1.5, 2.5, 3.5];
    let head: [f64] = fl[0:2];
    return total + mid[0] + mid.len() + (tail[1] as i32) + tail.len() + (head[1] as i32);
}
`},
	{name: "a-capture-the-closure-writes-is-a-cell", atLeast: 7, want: "42|", src: `
function tally(): i32 {
    let n: i32 = 0;
    let wide: i64 = 100i64;
    let ratio: f64 = 1.5;
    let flag: boolean = false;
    let byte: u8 = 250u8;
    let bump = (k: i32): i32 => {
        n = n + k;
        wide = wide + (k as i64);
        ratio = ratio * 2.0;
        flag = !flag;
        byte = byte + 3u8;
        return n;
    };
    let a: i32 = bump(1);
    let b: i32 = bump(2);
    let t: i32 = 0;
    if (flag) { t = 1000; }
    return a + b + n + (wide as i32) + (ratio as i32) + t + (byte as i32);
}
function counter(c: Cell[i64]): i32 {
    c.set(c.get() + 1i64);
    return c.get() as i32;
}
function apply(f: () => i32): i32 { return f(); }
function rebound(): i32 {
    let s: string = "a";
    let read = (): i32 => { return s.len(); };
    let i: i32 = 0;
    let n: i32 = 0;
    while (i < 3) { s = s + "bb"; n = n + apply(read); i = i + 1; }
    return n;
}
function main(): i32 {
    let c: Cell[i64] = cell_new(4i64);
    let k: i32 = counter(c) + counter(c);
    return (tally() + k + rebound()) % 100;
}
`},
	{name: "os-floor-fresh-results-are-freed", atLeast: 3, want: "57|", nativeOnly: true, src: `
function leaves(dir: string, p: string): i32 {
    let n: i32 = 0;
    match (create_dir(p + ".d", 493)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (remove_dir(p + ".d")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (create_link(p, p + ".h")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (rename(p + ".h", p + ".r")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (remove_file(p + ".r")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chmod(p, 420)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chmod_at(p, 420, true)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chown_at(p, 0 - 1, 0 - 1, true)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (set_file_times(p, 1000i64, 0i64, 1000i64, 0i64, 0)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chdir(dir)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    return n;
}
function each(dir: string): i32 {
    let n: i32 = 0;
    match (getcwd()) { Ok(cwd) => { if (cwd.len() > 0) { n = n + 1; } }, Err(_) => {} }
    let host: string = hostname();
    if (host.len() > 0) { n = n + 1; }
    let sys: string = uname_field(0);
    n = n + sys.len() % 7;
    let env: string[] = environ();
    if (env.len() > 0) { n = n + 1; }
    match (create_symlink("target", dir + "/lnk")) {
        Ok(_) => {
            match (read_link(dir + "/lnk")) {
                Ok(t) => { n = n + t.len(); },
                Err(_) => {}
            }
        },
        Err(_) => { n = n + 1; }
    }
    match (create_symlink("target", dir + "/missing/lnk")) {
        Ok(_) => {},
        Err(_) => { n = n + 2; }
    }
    match (write_file(dir + "/f", "hello")) {
        Ok(_) => {
            match (truncate(dir + "/f", 3i64)) {
                Ok(_) => { n = n + 3; },
                Err(_) => {}
            }
        },
        Err(_) => {}
    }
    match (termios_get(0)) {
        Ok(words) => {
            match (termios_set(0, 0, words)) {
                Ok(_) => { n = n + 4; },
                Err(_) => { n = n + 5; }
            }
        },
        Err(_) => { n = n + 6; }
    }
    n = n + leaves(dir, dir + "/f") + leaves(dir, dir + "/missing/f");
    let rb: u8[] = random_bytes(24);
    n = n + rb.len();
    if (cpu_count() >= 0) { n = n + 1; }
    let gs: i64[] = getgroups();
    if (gs.len() >= 0) { n = n + 1; }
    let r: ProcessResult = subprocess("echo", ["hi", "there"], "");
    n = n + r.exit_code + r.stdout.len() + r.stderr.len();
    return n;
}
function main(): i32 {
    let n: i32 = 0;
    match (temp_dir("fernfresh")) {
        Ok(d) => {
            let i: i32 = 0;
            while (i < 3) { n = n + each(d); i = i + 1; }
            match (remove_dir_all(d)) {
                Ok(_) => {},
                Err(_) => {}
            }
        },
        Err(_) => {}
    }
    return n % 100;
}
`},
	{name: "a-view-is-a-map-read-key", atLeast: 46, want: "47|", src: `
import "core/map";
import "std/string";
function tally(text: string, m: Map[string, i32]): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i + 2 <= text.len()) {
        let w: str = slice_unchecked(text, i, i + 2);
        n = n + m.get_or(w, 0);
        i = i + 2;
    }
    return n;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 20) {
        let m: Map[string, i32] = map_new(8);
        m = m.insert("ab", 3);
        m = m.insert("cd", 5);
        let text: string = "ab" + "cd" + "ef";
        acc = acc + tally(text, m);
        r = r + 1;
    }
    return acc % 113;
}`},
	{name: "a-builtin-string-result-is-never-its-argument", atLeast: 60, want: "29|over-releases 0\n", src: `
import "std/string";
import "std/io";
function sq(s: string): string { return s.replace("Q", "Z"); }
function blank(s: string): string { return s.replace("", "Z"); }
function words(n: i32): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < n) { out = out + " x"; i = i + 1; }
    return out;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 20) {
        acc = acc + sq(words(r % 4)).len() + sq(words(r % 3) + "Q").len() + blank(words(r % 2)).len();
        r = r + 1;
    }
    print("over-releases " + __rc_underflow_count().to_string());
    return acc % 109;
}`},
	{name: "an-unannotated-binding-takes-its-call-s-type", atLeast: 52, want: "21|", src: `
import "std/option";
import "std/result";
function mapped(): i32 {
    let s: Option[i32] = Some(5);
    let m = s.map((x: i32): i32 => { return x * 2; });
    return m.unwrap_or(0);
}
function chained(): i32 {
    let r: Result[i32, string] = Ok(7);
    let d = r.map((x: i32): i32 => { return x + 1; });
    let e = d.map_err((m: string): string => { return m + "!"; });
    return e.unwrap_or(0);
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 20) { acc = acc + mapped() + chained(); i = i + 1; }
    return acc % 113;
}`},
	{name: "a-variant-literal-types-itself-where-the-parameter-cannot", atLeast: 44, want: "39|", src: `
import "std/option";
function both(): i32 {
    let s: Option[i32] = Some(5);
    let other: Option[i32] = Some(9);
    return s.and(Some(9)).unwrap_or(0) + s.and(other).unwrap_or(0);
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 20) { acc = acc + both(); i = i + 1; }
    return acc % 107;
}`},
	{name: "a-variant-is-a-value-not-a-type-head", atLeast: 60, want: "77|", src: `
import "core/cmp";
@derive(cmp.Eq, cmp.Display, cmp.Ord)
enum Shape { Circle(i32), Square(i32), Empty }
function direct(): string { return Empty.to_string(); }
function qualified(): string { return Shape.Circle(1).to_string(); }
function bound(): string { let e: Shape = Empty; return e.to_string(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 20) { acc = acc + direct().len() + qualified().len() + bound().len(); i = i + 1; }
    return acc % 101;
}`},
	{name: "a-method-reads-its-receiver-by-name", atLeast: 97, want: "49|", src: `
import "std/json";
@derive(json.Json)
struct Bag { items: i32[], names: string[] }
struct Held { xs: f64[] }
function (self: Held) render(): string { return self.xs.to_json(); }
function loose(b: Bag): string { return b.items.to_json(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 20) {
        let bag: Bag = Bag { items: [1, 2, 3], names: ["a", "b"] };
        let h: Held = Held { xs: [1.5, 2.5] };
        let nums: i32[] = [10, 20];
        acc = acc + bag.to_json().len() + h.render().len() + loose(bag).len() + nums.to_json().len();
        i = i + 1;
    }
    if (Bag { items: [1, 2, 3], names: ["a", "b"] }.to_json() != "{\"items\":[1,2,3],\"names\":[\"a\",\"b\"]}") { return 1; }
    if (Held { xs: [1.5, 2.5] }.render() != "[1.5,2.5]") { return 2; }
    return acc % 101;
}`},
	{name: "a-composite-compares-through-its-own-method", atLeast: 51, want: "42|", src: `
import "core/cmp";
@derive(cmp.Eq, cmp.Ord)
struct Point { x: i32, y: string }
@derive(cmp.Eq, cmp.Ord)
enum Shape { Dot, Line(i32), Box(i32, string) }
@derive(cmp.Eq, cmp.Ord)
struct Holder[T] { v: T }
function same[T](a: Holder[T], b: Holder[T]): boolean { return a == b; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 20) {
        let a: Point = Point { x: 2, y: "hi" };
        let b: Point = Point { x: 2, y: "hi" };
        let c: Point = Point { x: 2, y: "hj" };
        if (!(a == b)) { return 1; }
        if (a == c) { return 2; }
        if (!(a != c)) { return 3; }
        if (!(a < c)) { return 4; }
        if (a < b) { return 5; }
        if (!(a <= b)) { return 6; }
        if (!(c > a)) { return 7; }
        if (!(a >= b)) { return 8; }
        if (!(Box(2, "a") == Box(2, "a"))) { return 9; }
        if (Box(2, "a") == Box(2, "b")) { return 10; }
        if (!(Box(2, "a") < Box(2, "b"))) { return 11; }
        if (!(Line(1) < Box(0, ""))) { return 12; }
        if (!(Dot == Dot)) { return 13; }
        if (Dot == Line(0)) { return 14; }
        let p: Holder[i32] = Holder { v: 1 };
        let q: Holder[i32] = Holder { v: 2 };
        let r: Holder[string] = Holder { v: "a" };
        if (!same(p, Holder { v: 1 })) { return 15; }
        if (same(p, q)) { return 16; }
        if (!(p < q)) { return 17; }
        if (!same(r, Holder { v: "a" })) { return 18; }
        acc = acc + 1;
        i = i + 1;
    }
    return acc + 22;
}`},
	{name: "a-u8-converts-to-and-from-a-float", atLeast: 1, want: "128|", wasm: "out-of-range|", src: `
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 256) {
        let b: u8 = i as u8;
        let f: f64 = b as f64;
        if ((f as u8) != b) { return 1; }
        if ((f as i32) != i) { return 2; }
        acc = acc + ((f as i32) & 1);
        i = i + 1;
    }
    if ((300.7 as u8) != 44u8) { return 3; }
    if ((255.9 as u8) != 255u8) { return 4; }
    if ((0.5 as u8) != 0u8) { return 5; }
    let w: u8 = 200u8;
    if ((w as f64) * 2.0 != 400.0) { return 6; }
    if ((w as f32) != 200.0) { return 7; }
    return acc;
}`},
	{name: "use-binding-typed-from-its-callee", atLeast: 3, want: "42|", src: `
function with_doubled(n: i32, k: (i32) => i32): i32 { return k(n * 2); }

function main(): i32 {
    use v <- with_doubled(21);
    return v;
}
`},
	{name: "use-binding-is-a-string", atLeast: 3, want: "25|", src: `
function taker(f: (string) => i32): i32 { return f("hi"); }

function main(): i32 {
    use x <- taker();
    return x.len() + 23;
}
`},
	{name: "use-binding-inside-a-lambda", atLeast: 4, want: "42|", src: `
function give(x: i32, cb: (i32) => i32): i32 { return cb(x); }

function main(): i32 {
    let bound = (): i32 => {
        use n <- give(41);
        return n + 1;
    };
    return bound();
}
`},
	{name: "narrow-scalar-columns-snapshot", atLeast: 1, want: "38|", src: `
import "core/map";

function main(): i32 {
    let m: Map[u8, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 12) { m = m.insert(i as u8, i * 2); i = i + 1; }
    let ks: u8[] = m.keys();
    let s: i32 = 0;
    for k in ks { s = s + (k as i32); }
    let w: Map[i32, u8] = map_new(2);
    i = 0;
    while (i < 12) { w = w.insert(i, (i * 2) as u8); i = i + 1; }
    for v in w.values() { s = s + (v as i32); }
    let b: Map[i32, boolean] = map_new(2);
    i = 0;
    while (i < 12) { b = b.insert(i, i % 3 == 0); i = i + 1; }
    for f in b.values() { if (f) { s = s + 10; } }
    return s - 200;
}
`},
	{name: "explicit-type-arguments-at-a-call", atLeast: 2, want: "42|", src: `
function pick[T](xs: T[], i: i32): T { return xs[i]; }

function main(): i32 {
    let xs: i64[] = [1, 2, 3];
    let ys: i32[] = [4, 5, 6];
    return (pick[i64](xs, 1) as i32) + pick[i32](ys, 2,) + 34;
}
`},
	{name: "labelled-range-loop", atLeast: 1, want: "13|", src: `
function main(): i32 {
    let sum: i32 = 0;
    outer: for i in 0..4 {
        for j in 0..4 {
            if (j == 2) { continue outer; }
            sum = sum + 1;
        }
        sum = sum + 100;
    }
    let k: i32 = 0;
    scan: for a in 0..10 {
        while (true) {
            k = k + 1;
            if (k == 5) { break scan; }
        }
    }
    return sum + k;
}
`},
	{name: "char-literals", atLeast: 3, want: "42|", src: `
const NEWLINE: char = '\n';

function upper_ascii(c: char): char {
    let n: i32 = c as i32;
    if (n >= 97 && n <= 122) { return (n - 32) as char; }
    return c;
}

function main(): i32 {
    let c: char = 'x';
    if (upper_ascii(c) != 'X') { return 1; }
    if (upper_ascii('Q') != 'Q') { return 2; }
    if ((NEWLINE as i32) != 10) { return 3; }
    if (('\u{1F600}' as i32) != 128512) { return 4; }
    let back: char = 65 as char;
    if (back != 'A') { return 5; }
    return (c as i32) - 78;
}
`},
	{name: "arithmetic-operators-on-a-struct", atLeast: 5, want: "42|", src: `
struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
function (a: V) sub(b: V): V { return V { x: a.x - b.x }; }
function (a: V) mul(b: V): V { return V { x: a.x * b.x }; }
function (a: V) neg(): V { return V { x: 0 - a.x }; }

function main(): i32 {
    let a: V = V { x: 5 };
    let b: V = V { x: 3 };
    let d: V = (a + b) * (a - b);
    let e: V = -(a - b);
    return d.x + e.x + 28;
}
`},
	{name: "a-lambda-returning-a-lambda-is-called-through-its-result", atLeast: 3, want: "42|", src: `
function main(): i32 {
    let mk = () => { return (b: i32): i32 => b * 2; };
    let f = mk();
    let add = (n: i32) => { return (a: i32, b: i32): i32 => a + b; };
    return f(4) + mk()(3) + add(0)(20, 8);
}
`},
	{name: "a-view-result-is-anchored-to-its-argument", atLeast: 3, want: "0|abcdefghijklmnopqrstuvwxyz01234567890\n23456789012345678901234567890ab0\nbcdefghijklmnopqrstuvwxyz01234567891\n23456789012345678901234567890ab1\ncdefghijklmnopqrstuvwxyz01234567892\n23456789012345678901234567890ab2\n", src: `
import "std/i32";
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function pick(a: string, n: i32): str { return a.tail(n); }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let r: str = ("abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string()).tail(i);
        let junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZYYYY" + i.to_string();
        let local: string = "0123456789012345678901234567890ab" + i.to_string();
        let q: str = pick(local, 2);
        let junk2: string = "WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWVVVV" + i.to_string();
        print(r);
        print(q);
        t = t + r.len() + q.len() + junk.len() + junk2.len();
        i = i + 1;
    }
    return t - 194;
}
`},
	{name: "a-slice-view-keeps-its-local-alive", atLeast: 1, want: "0|abcdefghijklmnopqrstuvwxyz0123\nabcdefghijklmnopqrstuvwxyz0123\nabcdefghijklmnopqrstuvwxyz0123\n", src: `
import "std/i32";
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string();
        match (s[0:30]) {
            Some(v) => {
                let junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ" + i.to_string();
                print(v);
                t = t + v.len() + junk.len();
            },
            None => { t = t + 1; }
        }
        i = i + 1;
    }
    return t - 204;
}
`},
	{name: "an-option-view-result-takes-its-payload", atLeast: 2, want: "0|abc\nabc\nabc\n", src: `
import "std/i32";
function first_three(s: string): Option[str] {
    let v: str = s[0:3]?;
    return Some(v);
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        match (first_three("abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string())) {
            Some(v) => {
                let junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ" + i.to_string();
                print(v);
                t = t + v.len() + junk.len();
            },
            None => { t = t + 100; }
        }
        match (first_three("ab")) {
            Some(v) => { t = t + 100; },
            None => { t = t + 1; }
        }
        i = i + 1;
    }
    return t - 126;
}
`},
	{name: "a-view-of-counted-strings-carries-its-own-bytes", atLeast: 2, want: "0|the quick \nbrown fox \njumps over\n the lazy \ndog\n", src: `
import "std/string";
function fold_line(line: string, width: i32): i32 {
    let n: i32 = 0;
    let rest: str = line;
    while (rest.len() > width) {
        print(rest.take(width));
        rest = rest.drop(width);
        n = n + 1;
    }
    print(rest);
    return n;
}
function main(): i32 {
    return fold_line("the quick brown fox jumps over the lazy dog", 10) - 4;
}
`},
	{name: "a-view-result-of-either-parameter-is-anchored-to-both", atLeast: 2, want: "0|", src: `
import "std/i32";
function either(a: string, b: string, first: boolean): str {
    if (first) { return a; }
    return b;
}
function main(): i32 {
    let v: str = either(7.to_string(), 42.to_string(), false);
    return v.len() - 2;
}
`},
	{name: "a-view-result-of-two-parameters-passes-through-a-caller", atLeast: 3, want: "232|", wasm: "out-of-range|", src: `
import "std/i32";
function either(a: str, b: str, first: boolean): str {
    if (first) { return slice_unchecked(a, 1, a.len()); }
    return b;
}
function through(a: string, b: string, k: i32): str { return either(a, b, k % 2 == 0); }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 40) {
        let x: string = "ab" + i.to_string();
        let v: str = through(x, "q" + i.to_string(), i);
        let w: str = either(v, x, i % 3 == 0);
        t = t + v.len() + w.len();
        i = i + 1;
    }
    return t % 256;
}
`},
	{name: "a-value-holding-views-of-two-sources-is-anchored-to-both", atLeast: 4, want: "119|bcdefghijklmnopqrstuvwxyz01234\nDEFGHIJKLMNOPQRSTUVWXYZ9876543\nabcdefghijklmnopqrstuvwxyz0123\n23456789abcdefghijklmnopqrstuv\nklmnopqrstuvwxyz0123456789abcd\nRSTUVWXYZ0123456789ABCDEFGHIJK\nbcdefghijklmnopqrstuvwxyz01234\nDEFGHIJKLMNOPQRSTUVWXYZ9876543\nabcdefghijklmnopqrstuvwxyz0123\n23456789abcdefghijklmnopqrstuv\nklmnopqrstuvwxyz0123456789abcd\nqrstuvwxyz0123456789abcdefghij\nbcdefghijklmnopqrstuvwxyz01234\nDEFGHIJKLMNOPQRSTUVWXYZ9876543\nabcdefghijklmnopqrstuvwxyz0123\n23456789abcdefghijklmnopqrstuv\nklmnopqrstuvwxyz0123456789abcd\nRSTUVWXYZ0123456789ABCDEFGHIJK\n", src: `
import "std/i32";
function keep(v: str, xs: str[]): str[] { return xs.append(v); }
function pair(a: string, b: string): str[] {
    let o: str[] = [];
    o = o.append(slice_unchecked(a, 0, 30));
    o = o.append(slice_unchecked(b, 2, 32));
    return o;
}
function pick(a: string, b: string, first: boolean): str {
    let v: str = slice_unchecked(b, 1, 31);
    if (first) { v = slice_unchecked(a, 0, 30); }
    return v;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let s1: string = "abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string();
        let s2: string = "ABCDEFGHIJKLMNOPQRSTUVWXYZ9876543210" + i.to_string();
        let local: str[] = [slice_unchecked(s1, 1, 31), slice_unchecked(s2, 3, 33)];
        let xs: str[] = pair("abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string(), "0123456789abcdefghijklmnopqrstuvwxyz" + i.to_string());
        let ys: str[] = [];
        ys = keep(slice_unchecked("klmnopqrstuvwxyz0123456789abcdefghij" + i.to_string(), 0, 30), ys);
        let p: str = pick("qrstuvwxyz0123456789abcdefghijklmnop" + i.to_string(), "QRSTUVWXYZ0123456789ABCDEFGHIJKLMNOP" + i.to_string(), i == 1);
        let junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZYYYY" + i.to_string();
        let junk2: string = "WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWVVVV" + i.to_string();
        print(local[0]);
        print(local[1]);
        print(xs[0]);
        print(xs[1]);
        print(ys[0]);
        print(p);
        t = t + local[0].len() + xs[1].len() + ys.len() + p.len() + junk.len() + junk2.len();
        i = i + 1;
    }
    return t - 400;
}
`},
	{name: "a-kept-view-parameter-is-a-fresh-view", atLeast: 5, want: "18|bcd\nde\nefgh7\n", src: `
import "std/i32";
struct P { a: str, n: i32 }
function rec(v: str): P { return P { a: v, n: 1 }; }
function opt(v: str): Option[str] { return Some(v); }
function arr(v: str): str[] { return [v, v]; }
@noinline function same(v: str): str { return v; }
function main(): i32 {
    let s: string = "abcdefgh" + 7.to_string();
    let p: P = rec(slice_unchecked(s, 1, 4));
    let o: Option[str] = opt(slice_unchecked(s, 0, 5));
    let xs: str[] = arr(slice_unchecked(s, 3, 5));
    let w: str = same(slice_unchecked(s, 4, 9));
    let n: i32 = 0;
    match (o) { Some(v) => { n = v.len(); }, None => { n = 100; } }
    print(p.a);
    print(xs[1]);
    print(w);
    return p.a.len() + n + xs[0].len() + xs.len() + w.len() + p.n;
}
`},
	{name: "a-copied-array-of-views-owns-its-boxes", atLeast: 4, want: "3|ab\ncd\n", src: `
function mk(s: string): str[] {
    let xs: str[] = [];
    xs = xs.append(slice_unchecked(s, 0, 2));
    return xs;
}
function grow(xs: str[], s: string): str[] { return xs.append(slice_unchecked(s, 2, 4)); }
function step(s: string): str[] { let xs: str[] = mk(s); return grow(xs, s); }
function main(): i32 {
    let s: string = "abcdefgh";
    let ys: str[] = step(s);
    let junk: str[] = mk("zzzzzzzz");
    print(ys[0]);
    print(ys[1]);
    return ys.len() + junk.len();
}
`},
	{name: "every-copy-of-a-shared-array-of-views-owns-its-boxes", atLeast: 2, want: "13|abcd\nabcdef\nabgh\nab\nababbc\n", src: `
struct H { names: str[], n: i32 }
function mk(s: string): str[] {
    let xs: str[] = [];
    xs = xs.append(slice_unchecked(s, 0, 2));
    xs = xs.append(slice_unchecked(s, 2, 4));
    return xs;
}
function main(): i32 {
    let s: string = "abcdefgh";
    let xs: str[] = mk(s);
    let ys: str[] = xs.append(slice_unchecked(s, 4, 6));
    let ws: str[] = xs.with(1, slice_unchecked(s, 6, 8));
    let zs: [str] = xs[0:1];
    let h: H = H { names: xs, n: 1 };
    let g: H = h;
    g = H { ...g, names: g.names.append(slice_unchecked(s, 1, 3)) };
    let junk: str[] = mk("zzzzzzzz");
    print(xs[0] + xs[1]);
    print(ys[0] + ys[1] + ys[2]);
    print(ws[0] + ws[1]);
    print(zs[0]);
    print(h.names[0] + g.names[0] + g.names[2]);
    return xs.len() + ys.len() + ws.len() + zs.len() + g.names.len() + junk.len();
}
`},
	{name: "every-read-of-a-view-map-value-owns-a-unit", atLeast: 2, want: "13|ab\ncde\nh\nab\ncde\nab\ncde\ncde\nab\n", src: `
import "core/map";
function fill(s: string): Map[i32, str] {
    let m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(s, 0, 2));
    m = m.insert(2, slice_unchecked(s, 2, 5));
    return m;
}
function main(): i32 {
    let s: string = "abcdefgh";
    let m: Map[i32, str] = fill(s);
    let n: i32 = 0;
    match (m.get(1)) { Some(v) => { print(v); n = n + v.len(); }, None => { n = 100; } }
    let d: str = m.get_or(2, slice_unchecked(s, 7, 8));
    let e: str = m.get_or(9, slice_unchecked(s, 7, 8));
    let junk: str[] = [slice_unchecked("zzzzzzzz", 0, 3), slice_unchecked("yyyyyyyy", 0, 3)];
    print(d);
    print(e);
    let vs: str[] = m.values();
    for v in vs { print(v); }
    for (k, v) in m { print(v); n = n + k; }
    match (m.get(2)) { Some(v) => { print(v); }, None => { n = 100; } }
    print(m.get_or(1, "q"));
    return n + d.len() + e.len() + vs.len() + junk.len();
}
`},
	{name: "a-copied-view-map-owns-its-boxes", atLeast: 3, want: "28|gh\nabcdefgh!\nlit\nq\ngone\nbc\nabcdefgh!\nbc\nbc\nd\n", src: `
import "core/map";
function fill(s: string): Map[string, str] {
    let m: Map[string, str] = map_new(4);
    m = m.insert("a", slice_unchecked(s, 0, 2));
    m = m.insert("b", slice_unchecked(s, 2, 5));
    m = m.insert("d", "lit");
    return m;
}
function grow(m: Map[string, str], s: string): Map[string, str] {
    return m.insert("e", slice_unchecked(s, 3, 4));
}
function main(): i32 {
    let s: string = "abcdefgh";
    let m: Map[string, str] = fill(s);
    let w: Map[i64, str] = map_new(2);
    w = w.insert(5 as i64, slice_unchecked(s, 1, 3));
    let t: string = s + "!";
    let c: str = t;
    m = m.insert("c", c);
    let held: Map[string, str] = m;
    let had: boolean = held.contains("b");
    let m2 = held.remove("b");
    m = m.insert("a", slice_unchecked(s, 6, 8));
    let m3: Map[string, str] = grow(m, s);
    let junk: str[] = [slice_unchecked("zzzzzzzz", 0, 3), slice_unchecked("yyyyyyyy", 0, 3)];
    print(m.get_or("a", "q"));
    print(m.get_or("c", "q"));
    print(m.get_or("d", "q"));
    print(m.get_or("x", "q"));
    print(m2.get_or("b", "gone"));
    print(w.get_or(5 as i64, "q"));
    let n: i32 = 0;
    for (k, v) in m2 { n = n + v.len(); }
    match (m.get("c")) { Some(v) => { print(v); }, None => { n = 100; } }
    match (w.get(5 as i64)) { Some(v) => { print(v); }, None => { n = 100; } }
    for v in w.values() { print(v); }
    print(m3.get_or("e", "q"));
    return n + m.len() + m2.len() + m3.len() + junk.len();
}
`},
	{name: "a-loop-phi-over-an-anchored-call-result-is-produced", atLeast: 3, want: "12|abc\n", src: `
import "std/i32";
@noinline function id(xs: str[]): str[] { return xs; }
function build(s: string, k: i32): str[] {
    let xs: str[] = [slice_unchecked(s, 0, 3)];
    let i: i32 = 0;
    while (i < k) { xs = id(xs); i = i + 1; }
    return xs;
}
function main(): i32 {
    let s: string = "abcdefgh" + 7.to_string();
    let r: str[] = build(s, 5);
    let junk: string = "zzzzzzzzzz" + 8.to_string();
    print(r[0]);
    return r.len() + junk.len();
}
`},
	{name: "a-loop-keeping-views-of-two-sources-through-a-call-is-produced", atLeast: 3, want: "21|ab,A,bc,B,cd,C,de,D,ef,E,\n", src: `
import "std/i32";
function keep(v: str, xs: str[]): str[] { return xs.append(v); }
function build(a: string, b: string, k: i32): str[] {
    let xs: str[] = [];
    let i: i32 = 0;
    while (i < k) {
        let t: str = slice_unchecked(a, i, i + 2);
        xs = keep(t, xs);
        xs = keep(slice_unchecked(b, i, i + 1), xs);
        i = i + 1;
    }
    return xs;
}
function main(): i32 {
    let a: string = "abcdefgh" + 1.to_string();
    let b: string = "ABCDEFGH" + 2.to_string();
    let xs: str[] = build(a, b, 5);
    let junk: string = "zzzzzzzzzz" + 3.to_string();
    let line: string = "";
    for x in xs { line = line + x + ","; }
    print(line);
    return xs.len() + junk.len();
}
`},
	{name: "a-loop-keeping-an-anonymous-slice-through-a-call-is-produced", atLeast: 3, want: "5|", src: `
function keep(v: str, xs: str[]): str[] { return xs.append(v); }
function build(s: string, n: i32): str[] {
    let xs: str[] = [];
    let i: i32 = 0;
    while (i < n) { xs = keep(slice_unchecked(s, i, i + 2), xs); i = i + 1; }
    return xs;
}
function main(): i32 { return build("abcdefgh", 5).len(); }
`},
	{name: "a-str-local-assigned-a-view-of-a-branch-local-is-produced", atLeast: 4, want: "12|<p:xx><>amp:x!\n", src: `
function mk(n: i32): string {
    let s: string = "stamp:";
    let i: i32 = 0;
    while (i < n) { s = s + "x"; i = i + 1; }
    return s;
}
function branch(n: i32): string {
    let tail: str = "";
    if (n != 0) {
        let s: string = mk(n);
        tail = slice_unchecked(s, 4, 8);
    }
    return "<" + tail + ">";
}
function block(n: i32): string {
    let tail: str = "none";
    {
        let s: string = mk(n);
        tail = slice_unchecked(s, 2, 7);
    }
    return tail + "!";
}
function main(): i32 {
    print(branch(3) + branch(0) + block(4));
    return branch(5).len() + block(1).len();
}
`},
	{name: "a-str-local-assigned-a-view-of-a-loop-body-local-is-produced", atLeast: 3, want: "8|[bccc][-]\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function last(k: i32): string {
    let t: str = "-";
    let i: i32 = 0;
    while (i < k) {
        let s: string = mk(i);
        t = slice_unchecked(s, 1, s.len());
        i = i + 1;
    }
    return "[" + t + "]";
}
function main(): i32 {
    print(last(4) + last(0));
    return last(6).len();
}
`},
	{name: "a-str-array-built-from-a-view-of-a-branch-local-is-produced", atLeast: 3, want: "3|ab|\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function f(n: i32): string {
    let a: str[] = [];
    if (n != 0) {
        let s: string = mk(n);
        a = a.append(slice_unchecked(s, 0, 2));
    }
    let out: string = "";
    for v in a { out = out + v + "|"; }
    return out;
}
function main(): i32 { print(f(3) + f(0)); return f(2).len(); }
`},
	{name: "a-str-array-built-from-a-view-of-a-dominating-local-is-produced", atLeast: 3, want: "3|ab|\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function f(n: i32): string {
    let a: str[] = [];
    let s: string = mk(n);
    if (n != 0) {
        a = a.append(slice_unchecked(s, 0, 2));
    }
    let out: string = "";
    for v in a { out = out + v + "|"; }
    return out;
}
function main(): i32 { print(f(3) + f(0)); return f(2).len(); }
`},
	{name: "a-tuple-option-and-array-of-views-of-a-loop-body-local-is-produced", atLeast: 3, want: "13|abc|a,lit,c,c,c x|-,lit\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function f(n: i32): string {
    let t: (str, i32) = ("x", 0);
    let o: Option[str] = None;
    let a: str[] = ["lit"];
    let i: i32 = 0;
    while (i < n) {
        let s: string = mk(i + 1);
        t = (slice_unchecked(s, 0, 3), i);
        o = Some(slice_unchecked(s, 0, 1));
        a = a.append(slice_unchecked(s, 2, 3));
        i = i + 1;
    }
    let out: string = t.0 + "|";
    match (o) { Some(v) => { out = out + v; }, None => { out = out + "-"; } }
    for v in a { out = out + "," + v; }
    return out;
}
function main(): i32 { print(f(3) + " " + f(0)); return f(2).len(); }
`},
	{name: "a-void-function-with-a-defer-and-a-return-is-produced", atLeast: 2, want: "0|adbd", src: `
function f(n: i32): void {
    defer write("d");
    if (n > 0) { write("a"); return; }
    write("b");
    return;
}
function main(): i32 { f(1); f(0); return 0; }
`},
	{name: "an-array-with-a-view-of-a-branch-local-is-produced", atLeast: 3, want: "3|ab| zz|\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function f(n: i32): string {
    let a: str[] = ["zz"];
    if (n != 0) {
        let s: string = mk(n);
        a = a.with(0, slice_unchecked(s, 0, 2));
    }
    let out: string = "";
    for v in a { out = out + v + "|"; }
    return out;
}
function main(): i32 { print(f(3) + " " + f(0)); return f(2).len(); }
`},
	{name: "a-map-inserted-a-view-of-a-branch-local-is-produced", atLeast: 3, want: "3|k|1 0\n", src: `
import "core/map";
import "std/i32";
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function f(n: i32): string {
    let m: Map[string, str] = Map {};
    if (n != 0) {
        let s: string = mk(n);
        m = m.insert("k", slice_unchecked(s, 0, 2));
    }
    let out: string = "";
    for k in m.keys() { out = out + k + "|"; }
    return out + m.len().to_string();
}
function main(): i32 { print(f(3) + " " + f(0)); return f(2).len(); }
`},
	{name: "a-str-array-a-call-returns-from-a-branch-local-is-produced", atLeast: 4, want: "4|a|b|\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function heads(s: string): str[] { return [slice_unchecked(s, 0, 1), slice_unchecked(s, 1, 2)]; }
function f(n: i32): string {
    let a: str[] = [];
    if (n != 0) {
        let s: string = mk(n);
        a = heads(s);
    }
    let out: string = "";
    for v in a { out = out + v + "|"; }
    return out;
}
function main(): i32 { print(f(3) + f(0)); return f(2).len(); }
`},
	{name: "a-tuple-a-call-returns-from-a-branch-local-is-produced", atLeast: 4, want: "4|a/bc -/-\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function split2(s: string): (str, str) { return (slice_unchecked(s, 0, 1), slice_unchecked(s, 1, 3)); }
function f(n: i32): string {
    let p: (str, str) = ("-", "-");
    if (n != 0) {
        let s: string = mk(n);
        p = split2(s);
    }
    return p.0 + "/" + p.1;
}
function main(): i32 { print(f(3) + " " + f(0)); return f(2).len(); }
`},
	{name: "an-option-a-call-returns-from-a-loop-body-local-is-produced", atLeast: 4, want: "7|some:ab none none\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function first(s: string, n: i32): Option[str] {
    if (n > 2) { return Some(slice_unchecked(s, 0, 2)); }
    return None;
}
function f(n: i32): string {
    let o: Option[str] = None;
    let i: i32 = 0;
    while (i < n) {
        let s: string = mk(i);
        o = first(s, i);
        i = i + 1;
    }
    match (o) { Some(v) => { return "some:" + v; }, None => { return "none"; } }
}
function main(): i32 { print(f(5) + " " + f(2) + " " + f(0)); return f(4).len(); }
`},
	{name: "a-three-variant-enum-a-call-returns-from-a-branch-local-is-produced", atLeast: 4, want: "5|ab/bc b:a c\n", src: `
enum Tri { A(str, str), B(str), C }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function pick(s: string, which: i32): Tri {
    if (which == 1) { return A(slice_unchecked(s, 0, 2), slice_unchecked(s, 1, 3)); }
    if (which == 2) { return B(slice_unchecked(s, 0, 1)); }
    return C;
}
function f(n: i32): string {
    let t: Tri = C;
    if (n != 0) {
        let s: string = mk(n + 2);
        t = pick(s, n);
    }
    let out: string = "";
    match (t) { A(a, b) => { out = a + "/" + b; }, B(x) => { out = "b:" + x; }, C => { out = "c"; } }
    return out;
}
function main(): i32 { print(f(1) + " " + f(2) + " " + f(3)); return f(1).len(); }
`},
	{name: "a-declared-enum-of-views-keeps-its-source-alive", atLeast: 4, want: "0|ab/bc b:a c\n", src: `
import "std/i32";
enum Tri { A(str, str), B(str), C }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function pick(s: string, which: i32): Tri {
    if (which == 1) { return A(slice_unchecked(s, 0, 2), slice_unchecked(s, 1, 3)); }
    if (which == 2) { return B(slice_unchecked(s, 0, 1)); }
    return C;
}
function f(n: i32): string {
    let t: Tri = pick(mk(n + 2), n);
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    let out: string = "";
    match (t) { A(a, b) => { out = a + "/" + b; }, B(x) => { out = "b:" + x; }, C => { out = "c"; } }
    return out;
}
function main(): i32 { print(f(1) + " " + f(2) + " " + f(3)); return 0; }
`},
	{name: "a-record-a-call-returns-from-a-branch-local-is-produced", atLeast: 4, want: "8|a/bc/2/3 -/-/1/0\n", src: `
import "std/i32";
struct Pair { a: str, b: str, n: i32 }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function halves(s: string, n: i32): Pair { return Pair { a: slice_unchecked(s, 0, 1), b: slice_unchecked(s, 1, 3), n: n }; }
function f(n: i32): string {
    let p: Pair = Pair { a: "-", b: "-", n: 0 };
    if (n != 0) {
        let s: string = mk(n);
        p = halves(s, n);
    }
    return p.a + "/" + p.b + "/" + p.b.len().to_string() + "/" + p.n.to_string();
}
function main(): i32 { print(f(3) + " " + f(0)); return f(2).len(); }
`},
	{name: "a-dyn-holding-a-view-keeps-its-source-alive", atLeast: 4, want: "0|31 31\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function wrap(s: string): dyn Size { let p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
function g(n: i32): i32 {
    let d: dyn Size = wrap(mk(n));
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return d.size();
}
function main(): i32 { print(g(3).to_string() + " " + g(2).to_string()); return 0; }
`},
	{name: "a-dyn-holding-a-view-merged-past-its-source-is-copied", atLeast: 4, want: "0|31 26\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function churn(): i32 {
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return junk.len();
}
function wrap(s: string): dyn Size { let p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
function g(n: i32): i32 {
    let d: dyn Size = P { a: "q" };
    if (n != 0) {
        let s: string = mk(n);
        d = wrap(s);
    }
    churn();
    return d.size();
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`},
	{name: "a-recursive-enum-merged-past-its-source-is-copied", atLeast: 4, want: "0|3 0\n", src: `
import "std/i32";
enum L { Cons(str, L), Nil }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function two(s: string): L { return Cons(slice_unchecked(s, 0, 1), Cons(slice_unchecked(s, 1, 3), Nil)); }
function count(l: L): i32 { match (l) { Cons(h, t) => { return h.len() + count(t); }, Nil => { return 0; } } return 0; }
function g(n: i32): i32 {
    let l: L = Nil;
    if (n != 0) {
        let s: string = mk(n);
        l = two(s);
    }
    return count(l);
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`},
	{name: "a-record-holding-an-array-of-itself-merged-is-copied", atLeast: 4, want: "0|52 35\n", src: `
import "std/i32";
struct Tree { name: str, kids: Tree[] }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function grow(s: string): Tree {
    let leaf: Tree = Tree { name: slice_unchecked(s, 1, 3), kids: [] };
    return Tree { name: slice_unchecked(s, 0, 1), kids: [leaf, leaf] };
}
function weight(t: Tree): i32 {
    let n: i32 = t.name.len() * 10 + (t.name[0] as i32) - 97;
    for k in t.kids { n = n + weight(k); }
    return n;
}
function g(n: i32): i32 {
    let t: Tree = Tree { name: "z", kids: [] };
    if (n != 0) {
        let s: string = mk(n);
        t = grow(s);
    }
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return weight(t);
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`},
	{name: "mutually-recursive-enums-merged-are-copied", atLeast: 4, want: "0|337 0\n", src: `
import "std/i32";
enum A { AV(str, B), AEnd }
enum B { BV(str, A), BEnd }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function build(s: string): A { return AV(slice_unchecked(s, 0, 2), BV(slice_unchecked(s, 2, 5), AV(slice_unchecked(s, 1, 2), BEnd))); }
function la(a: A): i32 { match (a) { AV(h, b) => { return h.len() * 100 + lb(b); }, AEnd => { return 0; } } return 0; }
function lb(b: B): i32 { match (b) { BV(h, a) => { return h.len() * 10 + la(a); }, BEnd => { return 7; } } return 0; }
function g(n: i32): i32 {
    let a: A = AEnd;
    if (n != 0) {
        let s: string = mk(n);
        a = build(s);
    }
    return la(a);
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`},
	{name: "a-dyn-over-several-boxes-merged-in-a-loop-is-copied", atLeast: 4, want: "0|31,103,213,7,1009,1009 26 31,103,213,213\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
struct P { a: str }
struct Q { n: i32 }
enum E { One(str), Two(str, str) }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
impl Size for Q { function size(self: Q): i32 { return self.n; } }
impl Size for E {
    function size(self: E): i32 {
        match (self) { One(a) => { return 100 + a.len(); }, Two(a, b) => { return 200 + a.len() * 10 + b.len(); } }
        return 0;
    }
}
impl Size for string { function size(self: string): i32 { return 1000 + self.len(); } }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function pick(s: string, k: i32): dyn Size {
    if (k == 0) { let p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
    if (k == 1) { let e: E = One(slice_unchecked(s, 0, 3)); return e; }
    if (k == 2) { let e2: E = Two(slice_unchecked(s, 0, 1), slice_unchecked(s, 1, 4)); return e2; }
    let q: Q = Q { n: s.len() };
    return q;
}
function g(n: i32): string {
    let d: dyn Size = P { a: "q" };
    let out: string = "";
    let k: i32 = 0;
    while (k < n) {
        let s: string = mk(k + 2);
        if (k == 4) {
            let t: string = s + "!";
            d = t;
        } else {
            d = pick(s, k);
        }
        let junk: string[] = [];
        let i: i32 = 0;
        while (i < 20) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
        out = out + d.size().to_string() + ",";
        k = k + 1;
    }
    let junk2: string[] = [];
    let j: i32 = 0;
    while (j < 50) { junk2 = junk2.append("yy" + j.to_string()); j = j + 1; }
    return out + d.size().to_string();
}
function main(): i32 { print(g(5) + " " + g(0) + " " + g(3)); return 0; }
`},
	{name: "a-dyn-widened-from-a-viewless-branch-local-is-produced", atLeast: 4, want: "0|21 26\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
struct P { a: str }
struct Q { n: i32 }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
impl Size for Q { function size(self: Q): i32 { return self.n; } }
function churn(): i32 {
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return junk.len();
}
function g(n: i32): i32 {
    let d: dyn Size = P { a: "q" };
    if (n != 0) {
        let q: Q = Q { n: n * 7 };
        d = q;
    }
    churn();
    return d.size();
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`},
	{name: "a-view-map-a-call-returns-from-a-branch-local-is-copied", atLeast: 4, want: "0|ab/bcc/2 -/-/0\n", src: `
import "std/i32";
import "core/map";
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function churn(): i32 {
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return junk.len();
}
function index(s: string): Map[i32, str] {
    let m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(s, 0, 2));
    return m.insert(2, slice_unchecked(s, 1, 4));
}
function g(n: i32): string {
    let m: Map[i32, str] = map_new(4);
    if (n != 0) {
        let s: string = mk(n);
        m = index(s);
    }
    churn();
    return m.get_or(1, "-") + "/" + m.get_or(2, "-") + "/" + m.len().to_string();
}
function main(): i32 { print(g(3) + " " + g(0)); return 0; }
`},
	{name: "a-map-of-view-records-a-call-returns-in-a-loop-is-copied", atLeast: 4, want: "0|2;x=ab/1;y=bcc/2 0\n", src: `
import "std/i32";
import "core/map";
struct P { a: str, n: i32 }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function index(s: string): Map[string, P] {
    let m: Map[string, P] = map_new(4);
    m = m.insert("x", P { a: slice_unchecked(s, 0, 2), n: 1 });
    m = m.insert("y", P { a: slice_unchecked(s, 1, 4), n: 2 });
    return m;
}
function g(n: i32): string {
    let m: Map[string, P] = map_new(4);
    let k: i32 = 0;
    while (k < n) {
        let s: string = mk(k + 2);
        m = index(s);
        k = k + 1;
    }
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    let out: string = m.len().to_string();
    for key in m.keys() {
        let p: P = m.get_or(key, P { a: "-", n: 0 });
        out = out + ";" + key + "=" + p.a + "/" + p.n.to_string();
    }
    return out;
}
function main(): i32 { print(g(3) + " " + g(0)); return 0; }
`},
	{name: "a-view-map-value-read-keeps-the-map-alive", atLeast: 4, want: "0|2;bcc\n", src: `
import "std/i32";
import "core/map";
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function index(s: string): Map[i32, str] {
    let m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(s, 0, 2));
    return m.insert(2, slice_unchecked(s, 1, 4));
}
function g(n: i32): string {
    let m: Map[i32, str] = index(mk(n));
    let v: str = m.get_or(2, "-");
    let out: string = "2" + ";";
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return out + v;
}
function main(): i32 { print(g(3)); return 0; }
`},
	{name: "a-nested-array-a-call-returns-from-a-branch-local-is-produced", atLeast: 4, want: "5|a;bc;|\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function grid(s: string): str[][] { return [[slice_unchecked(s, 0, 1)], [slice_unchecked(s, 1, 2), slice_unchecked(s, 2, 3)]]; }
function f(n: i32): string {
    let g: str[][] = [];
    if (n != 0) {
        let s: string = mk(n);
        g = grid(s);
    }
    let out: string = "";
    for row in g { for v in row { out = out + v; } out = out + ";"; }
    return out;
}
function main(): i32 { print(f(3) + "|" + f(0)); return f(2).len(); }
`},
	{name: "an-array-a-call-extends-with-a-loop-body-local-is-produced", atLeast: 4, want: "8|abc,abc,abc,|\n", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function both(a: str[], s: string): str[] { return a.append(slice_unchecked(s, 0, 3)); }
function f(n: i32): string {
    let a: str[] = [];
    let i: i32 = 0;
    while (i < n) {
        let s: string = mk(i + 1);
        a = both(a, s);
        i = i + 1;
    }
    let out: string = "";
    for v in a { out = out + v + ","; }
    return out;
}
function main(): i32 { print(f(3) + "|" + f(0)); return f(2).len(); }
`},
	{name: "a-str-array-a-call-returns-from-a-dominating-local-is-produced", atLeast: 4, want: "2|", src: `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function heads(s: string): str[] { return [slice_unchecked(s, 0, 1), slice_unchecked(s, 1, 2)]; }
function pick(n: i32): i32 {
    let s: string = mk(n);
    let a: str[] = [];
    let i: i32 = 0;
    while (i < n) {
        if (i % 2 == 0) { a = heads(s); }
        i = i + 1;
    }
    return a.len();
}
function main(): i32 { return pick(9); }
`},
	{name: "std-time-format-rfc3339-is-produced", atLeast: 65, want: "20|1970-01-02T01:01:01Z\n", src: `
import "std/time";
function main(): i32 {
    let s: string = time.instant_from_unix(90061 as i64).format_rfc3339();
    print(s);
    return s.len();
}
`},
	{name: "an-array-of-a-parameters-views-is-anchored-to-it", atLeast: 4, want: "0|", src: `
import "std/string";
import "std/i32";
function g(x: string): str[] { let o: str[] = []; o = o.append(slice_unchecked(x, 0, 2)); o = o.append(slice_unchecked(x, 2, 5)); return o; }
function pairs(x: string): str[] {
    let o: str[] = [];
    let i: i32 = 0;
    while (i + 2 <= x.len()) { o = o.append(slice_unchecked(x, i, i + 2)); i = i + 2; }
    return o;
}
function swapped(x: string): str[] {
    let o: str[] = [slice_unchecked(x, 0, 1), slice_unchecked(x, 1, 2)];
    return o.with(0, slice_unchecked(x, 2, 4));
}
function main(): i32 {
    let xs: str[] = g("ab" + 12345.to_string());
    let n: i32 = xs.len() + xs[1].len();
    let k: i32 = 0;
    while (k < 20) {
        let ps: str[] = pairs("cd" + (k * 1000).to_string());
        n = n + ps.len();
        if (ps[0].starts_with("cd")) { n = n + 1; }
        k = k + 1;
    }
    let ws: str[] = swapped("q" + 4567.to_string());
    if (ws[0].starts_with("56")) { n = n + 100; }
    return n - 183;
}
`},
	{name: "a-checked-slice-is-an-option", atLeast: 2, want: "0|bc\nab\n", src: `
function f(t: string): Option[str] { return t[0:2]; }
function main(): i32 {
    let s: string = "abcdef";
    let o: Option[str] = s[1:3];
    let n: i32 = 0;
    match (o) { Some(v) => { print(v); n = n + v.len(); }, None => { n = n + 10; } }
    match (f(s)) { Some(v) => { print(v); n = n + v.len(); }, None => { n = n + 10; } }
    match (s[5:9]) { Some(v) => { n = n + 10; }, None => { n = n + 1; } }
    return n - 5;
}
`},
	{name: "a-function-field-names-its-signature-records", atLeast: 3, want: "0|", src: `
struct Ctx { value: i32 }
struct Fault { why: string }
struct Stage { name: string, run: (Ctx) => Result[Ctx, Fault] }
function names(a: Stage[]): i32 { return a.len(); }
function main(): i32 {
    let xs: Stage[] = [Stage { name: "a", run: (c: Ctx): Result[Ctx, Fault] => Ok(c) }];
    return names(xs) - 1;
}
`},
	{name: "a-literal-takes-its-operands-width", atLeast: 2, want: "0|", src: `
function ms(recv_deadline_ms: i32): i32 {
    let read_start_ns: i64 = monotonic_ns();
    let deadline_ns: i64 = (recv_deadline_ms as i64) * 1000000;
    let remaining_ms: i32 = ((deadline_ns - (monotonic_ns() - read_start_ns)) / 1000000) as i32;
    let back: i64 = 7000000000 - (monotonic_ns() - read_start_ns);
    if (back < 6000000000) { return 0 - 1; }
    return remaining_ms;
}
function main(): i32 { if (ms(5000) > 4000) { return 0; } return 1; }
`},
	{name: "an-unannotated-map-literal-names-its-columns", atLeast: 1, want: "39|", src: `import "core/map";
function main(): i32 {
    let m = Map { true: 5, false: 9 };
    let n = Map { 1: 10, 2: 20 };
    let s: i32 = m.get_or(true, 0) + m.get_or(false, 0) + n.get_or(2, 0);
    if (m.contains(true)) { s = s + 1; }
    return s + m.len() + n.len();
}
`},
	{name: "a-match-value-local-starts-at-its-types-zero", atLeast: 1, want: "20|", src: `struct P { x: i32, name: string }
struct Q { n: i32 }
function main(): i32 {
    let t: (i32, i32) = (7, 2);
    let p: P = match (t) { (a, b) => P { x: a + b, name: "q" } };
    let r: Result[Q, string] = match (t) { (a, b) => { if (a > b) { Ok(Q { n: a }) } else { Err("no") } } };
    let xs: i32[] = match (t) { (a, b) => [a, b, 1] };
    let out: i32 = p.x + p.name.len() + xs.len();
    match (r) { Ok(q) => { return out + q.n; }, Err(_) => { return 51; } }
}
`},
	{name: "a-variant-literal-settles-its-float-payload", atLeast: 2, want: "0|", src: `
function process(): Option[f64] {
    let v: f64 = Some(3.14)?;
    return Some(v * 2.0);
}
function main(): i32 {
    match (process()) {
        Some(f) => { if (f > 6.0 && f < 7.0) { return 0; } return 1; },
        None => { return 2; },
    }
    return 99;
}
`},
	{name: "raw-floor-intrinsics", runtimeFloor: true, atLeast: 3, want: "3|Abc\n", nativeOnly: true, src: `
function chr_of(b: i32): string { let p: usize = __raw_alloc(1); __raw_store8(p, 0, b); return __raw_string(p, 1); }
function cat2(a: string, b: string): string {
    let la: i32 = a.len();
    let lb: i32 = b.len();
    let p: usize = __raw_alloc(la + lb);
    __memcpy(p, __raw_data(a), la);
    __memcpy(p + la, __raw_data(b), lb);
    return __raw_string(p, la + lb);
}
function main(): i32 {
    let t: string = cat2(chr_of(65), "bc");
    let sc: usize = __raw_scratch(16);
    __raw_store_ptr(sc, 0, __raw_data(t));
    if (__raw_load8(__raw_load_ptr(sc, 0), 1) != 98) { return 90; }
    if (__syscall3(4000, 0, 0, 0) != -38) { return 91; }
    if (__syscall4(4000, 1, 2, 3, 4) != -38) { return 95; }
    if (__syscall5(4000, 1, 2, 3, 4, 5) != -38) { return 96; }
    if (__syscall6(4000, 1, 2, 3, 4, 5, 6) != -38) { return 92; }
    if (__raw_load_ptr(__raw_environ(), 0) == (0 as usize)) { return 93; }
    let kept: u8[] = __rc_inc(__alloc_u8(8));
    let none: i32[] = __raw_array(__raw_arr_box(0));
    if (__rc_underflow_count() != 0) { return 94; }
    print(t);
    return t.len() + kept.len() - 8 + none.len();
}
`},
	{name: "runtime-helpers-take-the-typed-path", atLeast: 1, want: "5|01234!\n", reports: []string{"runtime __fern_arr_slice_u8: produced", "runtime __fern_str_concat: produced"}, src: `
import "std/i32";

function main(): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < 5) { s = s + i.to_string(); i = i + 1; }
    print(s + "!");
    return s.len();
}
`},
	{name: "string-helpers-take-the-typed-path", atLeast: 1, want: "221|HELLO, WORLD|hello, world|ababab|Hell0, W0rld\na-b--c\nAZ\n", wasm: "out-of-range|HELLO, WORLD|hello, world|ababab|Hell0, W0rld\na-b--c\nAZ\n", reports: []string{
		"runtime __fern_str_cmp: produced", "runtime __fern_str_to_upper: produced",
		"runtime __fern_str_to_lower: produced", "runtime __fern_str_repeat: produced",
		"runtime __fern_str_trim: produced", "runtime __fern_str_replace: produced",
		"runtime __fern_string_from_bytes_u8: produced", "runtime __fern_str_split: produced",
		"runtime __fern_str_bytes_u8: produced", "runtime __fern_arr_str_join: produced",
		"runtime __fern_str_lines: produced",
	}, src: `
import "std/string";

function main(): i32 {
    let t: string = "  Hello, World  ".trim();
    print(t.to_ascii_upper() + "|" + t.to_ascii_lower() + "|" + "ab".repeat(3) + "|" + t.replace("o", "0"));
    let parts: string[] = "a,b,,c".split(",");
    print(parts.join("-"));
    let cs: string[] = "héllo".split("");
    let ls: string[] = "x\r\ny\n".lines();
    let bs: u8[] = "AZ".bytes();
    print(string_from_bytes_unchecked(bs));
    let lt: i32 = 0;
    if ("abc" < "abd") { lt = 1; }
    return parts.len() * 10 + cs.len() + ls.len() * 100 + lt * 1000;
}
`},
	{name: "stdio-helpers-take-the-typed-path", atLeast: 1, want: "0|got A\n", reports: []string{
		"runtime __fern_print_str: produced", "runtime __fern_putchar: produced",
		"runtime __fern_eprint_str: produced",
	}, src: `
function main(): i32 {
    write("got ");
    putchar(65);
    putchar(10);
    eprint("to stderr");
    return 0;
}
`},
	{name: "process-helpers-take-the-typed-path", atLeast: 1, want: "142|", nativeOnly: true, reports: []string{
		"runtime __fern_random_bytes_u8: produced", "runtime __fern_random_i32: produced",
		"runtime __fern_isatty: produced", "runtime __fern_process_alive: produced",
		"runtime __fern_cpu_count: produced", "runtime __fern_sleep_ms: produced",
		"runtime __fern_sleep_ns: produced",
	}, src: `
function main(): i32 {
    sleep_ms(1 as i64);
    sleep_ns(1000 as i64);
    let r: i32 = random_i32();
    let bs: u8[] = random_bytes(8);
    let n: i32 = cpu_count();
    let tty: boolean = isatty(99);
    let alive: boolean = process_alive(1);
    let pr: i32 = priority();
    let old: i32 = umask(18);
    sync();
    let score: i32 = bs.len();
    if (n > 0) { score = score + 10; }
    if (!tty) { score = score + 100; }
    if (alive) { score = score + 1000; }
    if (pr > (0 - 21) && pr < 20) { score = score + 10000; }
    if (umask(old) == 18) { score = score + 20000; }
    return score % 256;
}
`},
	{name: "fs-bundle-takes-the-typed-path", atLeast: 2, want: "5|", nativeOnly: true, reports: []string{
		"runtime __fern_path_copy: produced", "runtime __fern_io_error: produced",
		"runtime __fern_sync: produced", "runtime __fern_umask: produced",
		"runtime __fern_priority: produced",
	}, src: `
function mk(p: string): IoError { return NotFound(p + "!"); }
function main(): i32 {
    let old: i32 = umask(18);
    sync();
    let pr: i32 = priority();
    let back: i32 = umask(old);
    let n: i32 = 0;
    match (mk("path")) {
        NotFound(p) => { n = p.len(); },
        _ => { n = 99; }
    }
    if (pr > (0 - 21) && pr < 20 && back == 18) { return n; }
    return 1;
}
`},
	{name: "unit-result-literal", atLeast: 2, want: "9|", src: `
function check(x: i32): Result[void, IoError] {
    if (x < 0) { return Err(NotFound("negative")); }
    return Ok(());
}
function main(): i32 {
    let n: i32 = 0;
    match (check(3)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    match (check(0 - 3)) { Ok(_) => { n = n + 100; }, Err(NotFound(p)) => { n = n + p.len(); }, Err(_) => { n = n + 1000; } }
    return n;
}
`},
	{name: "fs-leaves-take-the-typed-path", atLeast: 3, want: "63|", nativeOnly: true, reports: []string{
		"runtime __fern_create_dir: produced", "runtime __fern_chmod: produced",
		"runtime __fern_create_symlink: produced", "runtime __fern_rename: produced",
		"runtime __fern_remove_file: produced", "runtime __fern_remove_dir: produced",
		"runtime __fern_set_priority: produced", "runtime __fern_truncate: produced",
	}, src: `
function ok(r: Result[void, IoError]): i32 {
    match (r) { Ok(_) => { return 1; }, Err(_) => { return 0; } }
}
function missing(r: Result[void, IoError]): i32 {
    match (r) { Err(NotFound(p)) => { return p.len(); }, _ => { return 100; } }
}
function main(): i32 {
    let d: string = "/tmp/fern_sem_fs_leaves";
    remove_file(d + "/t");
    remove_dir(d);
    let n: i32 = 0;
    n = n + ok(create_dir(d, 493));
    n = n + ok(chmod(d, 448));
    n = n + ok(create_symlink("nowhere", d + "/s"));
    n = n + ok(rename(d + "/s", d + "/t"));
    n = n + ok(remove_file(d + "/t"));
    n = n + ok(remove_dir(d));
    n = n + ok(set_priority(priority()));
    n = n + missing(truncate(d + "/gone", 2 as i64));
    n = n + missing(remove_file(d + "/gone"));
    return n;
}
`},
	{name: "fs-bundle-reads-and-writes", atLeast: 1, want: "13|", nativeOnly: true, reports: []string{
		"runtime __fern_create_dir_all: produced", "runtime __fern_write_file: produced",
		"runtime __fern_read_file: produced", "runtime __fern_utf8_valid: produced",
		"runtime __fern_read_file_bytes_u8: produced", "runtime __fern_stat: produced",
		"runtime __fern_read_dir: produced", "runtime __fern_remove_dir_all: produced",
	}, src: `
function main(): i32 {
    let d: string = "/tmp/fern_sem_fs_bundle";
    remove_dir_all(d);
    let n: i32 = 0;
    match (create_dir_all(d + "/x/y")) { Ok(_) => { n = n + 1; }, Err(_) => {} }
    match (write_file(d + "/x/f.txt", "hello world")) { Ok(_) => { n = n + 1; }, Err(_) => {} }
    match (read_file(d + "/x/f.txt")) { Ok(s) => { n = n + s.len(); }, Err(_) => {} }
    match (read_file_bytes(d + "/x/f.txt")) { Ok(b) => { n = n + b.len(); }, Err(_) => {} }
    match (stat(d + "/x/f.txt")) { Ok(st) => { if (st.is_file) { n = n + 100; } }, Err(_) => {} }
    match (read_dir(d + "/x")) { Ok(es) => { n = n + es.len() * 1000; }, Err(_) => {} }
    match (read_file(d + "/nope")) { Ok(_) => {}, Err(NotFound(p)) => { n = n + p.len() * 10000; }, Err(_) => {} }
    match (remove_dir_all(d)) { Ok(_) => { n = n + 1; }, Err(_) => {} }
    return n % 256;
}
`},
	{name: "sys-helpers-take-the-typed-path", atLeast: 1, want: "105|", nativeOnly: true,
		reports: []string{
			"runtime __fern_env: produced", "runtime __fern_environ: produced",
			"runtime __fern_getcwd: produced", "runtime __fern_hostname: produced",
			"runtime __fern_uname_field: produced", "runtime __fern_rlimit_nofile: produced",
		}, src: `
function main(): i32 {
    let n: i32 = 0;
    match (env("FERN_SEM_PROBE")) { Some(v) => { n = n + v.len(); }, None => { n = n + 100; } }
    if (environ().len() > 0) { n = n + 1; }
    match (getcwd()) { Ok(wd) => { if (wd.len() > 0) { n = n + 1; } }, Err(e) => {} }
    if (hostname().len() > 0) { n = n + 1; }
    if (uname_field(0).len() > 0) { n = n + 1; }
    if (rlimit_nofile() > 0) { n = n + 1; }
    return n % 256;
}
`},
	{name: "proc-leaves-take-the-typed-path", atLeast: 1, want: "99|", nativeOnly: true, reports: []string{
		"runtime __fern_subprocess: produced", "runtime __fern_sp_drain: produced",
		"runtime __fern_sp_exec_at: produced", "runtime __fern_proc_fork: produced",
		"runtime __fern_proc_waitpid: produced", "runtime __fern_signal_mask: produced",
		"runtime __fern_timer_fd: produced",
	}, src: `
function main(): i32 {
    let n: i32 = 0;
    let r: ProcessResult = subprocess("/bin/echo", ["hi", "there"], "");
    n = n + r.stdout.len() + r.exit_code * 100;
    let pid: i32 = proc_fork();
    if (pid == 0) { exit(7); }
    let st: i32 = proc_waitpid(pid);
    n = n + st * 1000;
    let old: i64 = signal_mask(0, 0 as i64);
    if (old >= (0 as i64)) { n = n + 1; }
    let tfd: i32 = timer_fd(5);
    if (tfd >= 0) { n = n + 1; }
    return n % 256;
}
`},
	{name: "arr-slice-takes-the-typed-path", atLeast: 1, want: "20|", nativeOnly: true,
		reports: []string{"runtime __fern_arr_slice: produced"}, src: `
struct P { x: i32, name: string }
function main(): i32 {
    let a: i32[] = [1, 2, 3, 4, 5];
    let b: [i32] = a[1:4];
    let s: string[] = ["aa", "bbb", "c", "dddd"];
    let t: [string] = s[2:4];
    let ps: P[] = [P { x: 1, name: "one" }, P { x: 2, name: "two" }, P { x: 3, name: "three" }];
    let qs: [P] = ps[0:2];
    let n: i32 = b.len() * 100 + b[0] * 10 + b[2];
    n = n + t[1].len() * 1000 + qs[1].name.len() * 10000;
    return n % 256;
}
`},
	{name: "usize-widens-to-i64-whole", runtimeFloor: true, atLeast: 1, want: "0|", nativeOnly: true, src: `
function main(): i32 {
    let p: usize = __raw_scratch(8);
    let w: i64 = p as i64;
    __store_i64(p, w);
    if (__raw_load_ptr(p, 0) != p) { return 1; }
    if (__load_i64(p) != w) { return 2; }
    return 0;
}
`},
	{name: "usize-unsigned-operators", atLeast: 1, want: "63|", src: semUsizeOperatorsSource},
	{name: "usize-operators-at-register-width", atLeast: 3, want: "255|", nativeOnly: true, src: semUsizeWideOperatorsSource},
	{name: "usize-wide-literal", atLeast: 1, want: "70|", wasm: "1|", src: semUsizeWideLiteralSource},
	{name: "usize-wide-product", atLeast: 2, want: "156|", wasm: "112|", src: semUsizeWideProductSource},
	{name: "c-call-trampolines", atLeast: 7, want: "0|", nativeOnly: true, src: semCCallSource},
	{name: "raw-floor-symbols-link-without-a-helper", runtimeFloor: true, atLeast: 1, want: "7|", nativeOnly: true, src: `
function main(): i32 {
    let sc: usize = __raw_scratch(8);
    __raw_store8(sc, 0, 7);
    let sp: usize = __raw_splice_pipe();
    __raw_store_ptr(sp, 0, sc);
    if (__raw_load_ptr(__raw_environ(), 0) == (0 as usize)) { return 1; }
    return __raw_load8(__raw_load_ptr(sp, 0), 0);
}
`},
	{name: "dyn-dispatch", atLeast: 6, want: "42|", src: `
trait Shape {
    function area(self: Self): i32;
    function sides(self: Self): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function sides(self: Self): i32 { return 4; }
}
struct Triangle { base: i32, height: i32 }
impl Shape for Triangle {
    function area(self: Self): i32 { return (self.base * self.height) / 2; }
    function sides(self: Self): i32 { return 3; }
}
function describe(s: dyn Shape): i32 {
    return s.area() * 10 + s.sides();
}

function main(): i32 {
    let sq: dyn Shape = Square { side: 3 };
    let tr: dyn Shape = Triangle { base: 4, height: 5 };
    return describe(sq) + describe(tr) - 155;
}
`},
	{name: "dyn-over-a-counted-record", atLeast: 3, want: "29|", src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct Named { label: string, side: i32 }
impl Shape for Named {
    function area(self: Self): i32 { return self.side * self.side + self.label.len(); }
}
function describe(s: dyn Shape): i32 { return s.area(); }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let n: dyn Shape = Named { label: "sq" + i.to_string(), side: 3 };
        t = t + describe(n);
        i = i + 1;
    }
    return t % 97;
}
`},
	{name: "dyn-method-operands-and-results", atLeast: 6, want: "35|635\n", src: `
import "std/i32";

trait Label {
    function tag(self: Self, prefix: string, n: i32): string;
    function weight(self: Self, k: f64): f64;
}
struct Box { name: string, size: i32 }
impl Label for Box {
    function tag(self: Self, prefix: string, n: i32): string { return prefix + self.name + n.to_string(); }
    function weight(self: Self, k: f64): f64 { return k * 2.0; }
}
enum Shape { Dot, Line(i32) }
impl Label for Shape {
    function tag(self: Self, prefix: string, n: i32): string {
        match (self) {
            Dot => { return prefix + "dot"; },
            Line(len) => { return prefix + "line" + (len + n).to_string(); }
        }
    }
    function weight(self: Self, k: f64): f64 { return k + 1.0; }
}
function show(l: dyn Label, i: i32): i32 {
    let s: string = l.tag("<", i);
    let w: f64 = l.weight(1.5);
    return s.len() + (w * 2.0) as i32;
}

function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 20) {
        let b: dyn Label = Box { name: "b" + i.to_string(), size: i };
        total = total + show(b, i);
        total = total + show(Shape.Line(i), i);
        total = total + show(Shape.Dot, i);
        i = i + 1;
    }
    print(total.to_string());
    return total % 200;
}
`},
	{name: "dyn-dispatch-into-two-implementations", atLeast: 4, want: "42|", src: `
trait Shape {
    function area(self: Self): i32;
    function sides(self: Self): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function sides(self: Self): i32 { return 4; }
}
struct Triangle { base: i32, height: i32 }
impl Shape for Triangle {
    function area(self: Self): i32 { return (self.base * self.height) / 2; }
    function sides(self: Self): i32 { return 3; }
}
function describe(s: dyn Shape): i32 { return s.area() * 10 + s.sides(); }

function main(): i32 {
    let sq: dyn Shape = Square { side: 3 };
    let tr: dyn Shape = Triangle { base: 4, height: 5 };
    return describe(sq) + describe(tr) - 155;
}
`},
	{name: "a-rebound-dyn-value-is-owned", atLeast: 3, want: "0|", src: semDynShapes + `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let s: dyn Shape = Square { side: 1, tag: "a" + i.to_string() };
        if (i % 2 == 1) { s = Tri.Right(i, "b" + i.to_string()); }
        t = t + s.area();
        i = i + 1;
    }
    return t - 24;
}
`},
	{name: "a-returned-dyn-value-is-owned", atLeast: 4, want: "0|", src: semDynShapes + `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let s: dyn Shape = make(i);
        t = t + s.area();
        i = i + 1;
    }
    return t - 50;
}
`},
	{name: "a-dyn-field-is-owned", atLeast: 4, want: "0|", src: semDynShapes + `
struct Holder { s: dyn Shape }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let h: Holder = Holder { s: make(i) };
        t = t + h.s.area();
        i = i + 1;
    }
    return t - 50;
}
`},
	{name: "a-dyn-array-owns-its-elements", atLeast: 4, want: "0|", src: semDynShapes + `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let xs: dyn Shape[] = [make(i), make(i + 1)];
        t = t + xs[0].area() + xs[1].area();
        i = i + 1;
    }
    return t - 136;
}
`},
	{name: "a-dyn-option-payload-is-owned", atLeast: 4, want: "0|", src: semDynShapes + `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let o: Option[dyn Shape] = Some(make(i));
        if let Some(s) = o { t = t + s.area(); }
        i = i + 1;
    }
    return t - 50;
}
`},
	{name: "a-dyn-map-value-is-owned", atLeast: 4, want: "0|", src: `import "core/map";` + semDynShapes + `
function main(): i32 {
    let t: i32 = 0;
    let m: Map[i32, dyn Shape] = map_new(8);
    let i: i32 = 0;
    while (i < 6) { m = m.insert(i % 3, make(i)); i = i + 1; }
    i = 0;
    while (i < 3) { if let Some(s) = m.get(i) { t = t + s.area(); } i = i + 1; }
    return t - 35;
}
`},
	{name: "a-captured-dyn-value-is-owned", atLeast: 5, want: "0|", src: semDynShapes + `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let s: dyn Shape = make(i);
        let f = (): i32 => { return s.area(); };
        t = t + f() + s.area();
        i = i + 1;
    }
    return t - 100;
}
`},
	{name: "a-dyn-parameter-handed-back-is-retained", atLeast: 4, want: "0|", src: semDynShapes + `
function pass(s: dyn Shape): dyn Shape { return s; }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let q: Square = Square { side: 2, tag: "k" + i.to_string() };
        let s: dyn Shape = pass(q);
        t = t + s.area() + q.tag.len();
        i = i + 1;
    }
    return t - 48;
}
`},
	{name: "a-dyn-array-lent-to-a-produced-callee", atLeast: 4, want: "0|", src: `
trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self * 2; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function total(xs: dyn Show[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show(); } return t; }

function main(): i32 {
    let xs: dyn Show[] = [1, 2];
    return total(xs) - 6;
}
`},
	{name: "a-primitive-dyn-value-is-boxed", atLeast: 5, want: "0|", src: semPrimitiveShows + `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let d: dyn Show = make(i);
        let e: dyn Show = i + 1;
        t = t + d.show() + e.show();
        i = i + 1;
    }
    return t - 63;
}
`},
	{name: "a-boxed-string-dyn-held-in-an-array", atLeast: 5, want: "0|", src: semPrimitiveShows + `
struct Holder { d: dyn Show }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let xs: dyn Show[] = [make(i), make(i + 1)];
        let h: Holder = Holder { d: "h" + i.to_string() };
        t = t + xs[0].show() + xs[1].show() + h.d.show();
        i = i + 1;
    }
    return t - 66;
}
`},
	{name: "a-boxed-string-returned-as-a-view-holding-dyn-is-produced", atLeast: 6, want: "0|1005 31\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
impl Size for string { function size(self: string): i32 { return 1000 + self.len(); } }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function pick(s: string): dyn Size { let t: string = s + "!"; return t; }
function wrap(s: string): dyn Size { let p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
function main(): i32 {
    let d: dyn Size = pick(mk(2));
    let e: dyn Size = wrap(mk(3));
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    print(d.size().to_string() + " " + e.size().to_string());
    return 0;
}
`},
	{name: "a-str-only-dyn-implementation-is-produced", atLeast: 2, want: "0|", src: `
trait Size { function size(self: Self): i32; }
impl Size for str { function size(self: str): i32 { return self.len(); } }
function main(): i32 { let d: dyn Size = "abc"; return d.size() - 3; }
`},
	{name: "a-string-boxed-through-an-impl-for-str-is-produced", atLeast: 6, want: "0|1050 1043 1050 26\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
impl Size for str { function size(self: str): i32 { return 1000 + self.len() * 10 + (self[0] as i32) - 97; } }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function churn(): i32 {
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return junk.len();
}
function pick(s: string): dyn Size { let t: string = s + "!"; return t; }
function g(n: i32): i32 {
    let d: dyn Size = P { a: "q" };
    if (n != 0) { let s: string = mk(n); d = s; }
    churn();
    return d.size();
}
function main(): i32 {
    let d: dyn Size = pick(mk(2));
    let lit: dyn Size = "xy";
    churn();
    print(d.size().to_string() + " " + lit.size().to_string() + " " + g(3).to_string() + " " + g(0).to_string());
    return 0;
}
`},
	{name: "a-dyn-whose-only-implementer-is-str-is-produced", atLeast: 2, want: "0|1073 1043\n", src: `
import "std/i32";
trait Size { function size(self: Self): i32; }
impl Size for str { function size(self: str): i32 { return 1000 + self.len() * 10 + (self[0] as i32) - 97; } }
function pick(n: i32): dyn Size { if (n > 0) { return "x" + "yz"; } return "ab"; }
function main(): i32 {
    let d: dyn Size = pick(1);
    let lit: dyn Size = "xy";
    print((d.size() + lit.size() - 1023).to_string() + " " + lit.size().to_string());
    return 0;
}
`},
	{name: "a-wide-scalar-dyn-value-is-boxed-at-its-width", atLeast: 5, want: "0|", src: `
trait Show { function show(self: Self): i32; }
impl Show for i64 { function show(self: Self): i32 { return (self / 1000000000) as i32; } }
impl Show for f64 { function show(self: Self): i32 { return (self * 4.0) as i32; } }
impl Show for boolean { function show(self: Self): i32 { if (self) { return 1; } return 0; } }
function pick(i: i32): dyn Show {
    if (i % 3 == 0) { return 5000000000 as i64; }
    if (i % 3 == 1) { return 2.5; }
    return i > 3;
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let d: dyn Show = pick(i);
        t = t + d.show();
        i = i + 1;
    }
    return t - 31;
}
`},
	{name: "a-mixed-dyn-array-literal-at-each-destination", atLeast: 5, want: "0|", src: `
trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self * 2; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
struct H { xs: dyn Show[] }
function total(xs: dyn Show[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show(); } return t; }
function mk(): dyn Show[] { return [3, "abc"]; }

function main(): i32 {
    let xs: dyn Show[] = [1, "ab"];
    let h: H = H { xs: [2, "x"] };
    return total(xs) + total(h.xs) + total(mk()) + total([5, "q"]) - 29;
}
`},
	{name: "an-owned-dyn-value-over-a-generic-implementation", atLeast: 3, want: "0|", src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct W[T] { v: T, tag: string }
impl[T] Shape for W[T] { function area(self: Self): i32 { return self.tag.len(); } }
function make(i: i32): dyn Shape { return W { v: "v" + i.to_string(), tag: "w" + i.to_string() }; }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let s: dyn Shape = make(i);
        t = t + s.area();
        i = i + 1;
    }
    return t - 12;
}
`},
	{name: "a-dyn-array-over-a-generic-implementations-instances", atLeast: 3, want: "44|", src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct W[T] { v: T, tag: string }
impl[T] Shape for W[T] { function area(self: Self): i32 { return self.tag.len(); } }
struct Sq { side: i32, name: string }
impl Shape for Sq { function area(self: Self): i32 { return self.side * self.side + self.name.len(); } }

function mk(i: i32): dyn Shape {
    if (i % 3 == 0) { return W { v: "s" + i.to_string(), tag: "ab" + i.to_string() }; }
    if (i % 3 == 1) { return W { v: i, tag: "c" + i.to_string() }; }
    return W { v: W { v: "in" + i.to_string(), tag: "x" }, tag: "yz" + i.to_string() };
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 30) {
        let xs: dyn Shape[] = [mk(i), Sq { side: 2, name: "q" + i.to_string() }];
        for x in xs { t = t + x.area(); }
        i = i + 1;
    }
    return t % 256;
}
`},
	{name: "an-owned-dyn-value-over-a-generic-enum-implementation", atLeast: 3, want: "0|", src: `
import "std/i32";
trait Shape { function area(self: Self): i32; }
enum Opt[T] { Sm(T, string), Nn }
impl[T] Shape for Opt[T] { function area(self: Self): i32 { match (self) { Sm(_, s) => { return s.len(); }, Nn => { return 0; } } } }

function mk(i: i32): dyn Shape {
    let o: Opt[i32] = Sm(i, "abc" + i.to_string());
    return o;
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) { let s: dyn Shape = mk(i); t = t + s.area(); i = i + 1; }
    return t - 40;
}
`},
	{name: "a-borrowed-dyn-value-over-a-generic-implementation", atLeast: 2, want: "0|", src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct W[T] { v: T, tag: string }
impl[T] Shape for W[T] { function area(self: Self): i32 { return self.tag.len(); } }

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let s: dyn Shape = W { v: "v" + i.to_string(), tag: "w" + i.to_string() };
        t = t + s.area();
        i = i + 1;
    }
    return t - 12;
}
`},
	{name: "a-function-value-whose-type-nests-a-function-type", atLeast: 9, want: "41|", src: `
function with_name(n: i32, k: (string) => i32): i32 { return k("x") + n; }
function through_local(i: i32): i32 {
    let f: (i32, (string) => i32) => i32 = with_name;
    return f(i, (s: string): i32 => s.len());
}
function taker(f: (string) => i32): i32 { return f("hi"); }
function use_through_local(): i32 {
    let t = taker;
    use x <- t();
    if (x == "hi") { return 5; }
    return 0;
}
function runner(k: (i32) => i32, v: i32): i32 { return k(v) + 1; }
function apply2(h: ((i32) => i32, i32) => i32, v: i32): i32 { return h((z: i32): i32 => z * 3, v); }
function adder(n: i32): (i32) => i32 { return (b: i32): i32 => b + n; }
function through_result(): i32 {
    let mk: (i32) => ((i32) => i32) = adder;
    let add3 = mk(3);
    return add3(4) + mk(10)(0);
}
function main(): i32 {
    return through_local(3) + use_through_local() + apply2(runner, 5) + through_result() - 1;
}
`},
	{name: "a-call-of-a-call-through-a-function-value", atLeast: 9, want: "58|", src: `
struct M { k: i32 }
function (m: M) make(): (i32) => i32 { let k = m.k; return (b: i32): i32 => b + k; }
function adder(n: i32): (i32) => i32 { return (b: i32): i32 => b + n; }
function doubler(n: i32): (i32) => i32 { return (b: i32): i32 => b * 2; }
function id[T](x: T): T { return x; }
function inc(b: i32): i32 { return b + 1; }
function c3(a: i32): (i32) => ((i32) => i32) { return (b: i32): (i32) => i32 => (c: i32): i32 => a * 100 + b * 10 + c; }
function call_through(mk: (i32) => ((i32) => i32)): i32 { return mk(1)(2); }
struct F { k: f64 }
function (f: F) scale(): (f64) => f64 { let k = f.k; return (x: f64): f64 => x * k; }
function mkf(k: f64): (f64) => f64 { return (x: f64): f64 => x * k; }
function mkw(k: i64): (i64) => i64 { return (x: i64): i64 => x * k; }
function wide(mk: (i64) => ((i64) => i64)): i32 { if (mk(3i64)(5000000000i64) == 15000000000i64) { return 1; } return 0; }
function floats(): i32 {
    let f = F { k: 2.0 };
    let m: (f64) => ((f64) => f64) = mkf;
    let r = 0;
    if (f.scale()(1.5) == 3.0) { r = r + 1; }
    if (m(2.0)(1.5) == 3.0) { r = r + 1; }
    return r + wide(mkw);
}
function main(): i32 {
    let add: (i32) => ((i32) => i32) = adder;
    let dbl = doubler;
    let three = c3;
    let k = 3;
    let m = M { k: 4 };
    return add(10)(20) + dbl(0)(5) + call_through(adder) + id((b: i32): i32 => b + k)(4)
        + id(inc)(5) + three(1)(2)(3) - 100 + m.make()(5) - 33 + floats();
}
`},
	{name: "a-function-array-holds-boxes", atLeast: 13, want: "64|", src: `
struct M { k: i32 }
enum E { Wrap(() => i32), No }
function a(b: i32): i32 { return b + 1; }
function bb(b: i32): i32 { return b + 2; }
function z(): i32 { return 7; }
function y(): i32 { return 8; }
function pickf(i: i32): (i32) => i32 { let fs = [a, bb]; return fs[i]; }
function pickz(i: i32): () => i32 { let fs = [z, y]; return fs[i]; }
function refill(xs: ((i32) => i32)[]): i32 { let fs: ((i32) => i32)[] = []; fs = xs; return fs[0](1) + fs[1](1); }
function (m: M) run(fs: ((i32) => i32)[]): i32 { return fs[0](m.k); }
function main(): i32 {
    let f = pickf(0);
    let t: ((() => i32), i32) = (z, 1);
    let o: Option[() => i32] = Some(y);
    let w = Wrap(z);
    let r = f(10) + pickf(1)(10) + pickz(1)() + refill([a, bb]) + M { k: 4 }.run([a]) + t.0() + t.1;
    match (o) { Some(g) => { r = r + g(); }, None => {} }
    match (w) { Wrap(h) => { r = r + h(); }, No => {} }
    return r;
}
`},
	{name: "a-lambda-returning-a-capturing-lambda", atLeast: 5, want: "42|", src: `
function main(): i32 {
    let curry = (a: i32) => { return (b: i32): i32 => a + b; };
    let add5 = curry(5);
    let c3 = (a: i32) => { return (b: i32) => { return (c: i32): i32 => a + b + c; }; };
    let add = (x: i32) => (y: i32) => x + y;
    return add5(4) + curry(1)(2) + c3(1)(2)(3) + add(3)(4) + 17;
}
`},
	{name: "a-stored-field-read-is-retained", atLeast: 11, want: "24|", src: `
struct In { x: i32 }
enum Tag { A(i32), B }
struct E { env: i32[], live: boolean }
struct I { ins: In[], live: boolean }
struct N { inner: In, live: boolean }
struct T { tag: Tag, live: boolean }
function mke(k: i32): E { return E { env: [k, k + 1], live: true }; }
function mki(k: i32): I { return I { ins: [In { x: k }], live: true }; }
function mkn(k: i32): N { return N { inner: In { x: k }, live: true }; }
function mkt(k: i32): T { return T { tag: A(k), live: true }; }
function put_env(xs: i32[][], a: E): i32[][] { return xs.append(a.env); }
function put_ins(xs: In[][], a: I): In[][] { return xs.append(a.ins); }
function put_inner(xs: In[], a: N): In[] { return xs.append(a.inner); }
function put_tag(xs: Tag[], a: T): Tag[] { return xs.append(a.tag); }
function lit_env(a: E): i32[][] { return [a.env]; }
function tag_of(t: Tag): i32 { match (t) { A(n) => { return n; }, B => { return 0; } } }
function main(): i32 {
    let es: i32[][] = [];
    let k: i32 = 1;
    while (k < 5) { es = put_env(es, mke(k)); k = k + 1; }
    let is: In[][] = [];
    k = 1;
    while (k < 5) { is = put_ins(is, mki(k)); k = k + 1; }
    let ns: In[] = [];
    k = 1;
    while (k < 5) { ns = put_inner(ns, mkn(k)); k = k + 1; }
    let ts: Tag[] = [];
    k = 1;
    while (k < 5) { ts = put_tag(ts, mkt(k)); k = k + 1; }
    let ls: i32[][] = [];
    k = 1;
    while (k < 5) { ls = ls.append(lit_env(mke(k))[0]); k = k + 1; }
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        t = t * 3 + es[i][0] * 1000 + is[i][0].x * 100 + ns[i].x * 10 + tag_of(ts[i]) + ls[i][1];
        i = i + 1;
    }
    return t % 256;
}
`},
	{name: "an-array-literal-settles-to-its-concrete-sibling", atLeast: 4, want: "59|", src: `
function half(x: f32): f32 { return x / 2.0; }
function big(): i64 { return 5000000000; }
function id[T](x: T): T { return x; }
function main(): i32 {
    let xs = id([4, half(1.0), 0.5]);
    let ys = [0.25, half(0.5), 3];
    let zs = [1, big(), 2];
    let ws = [1.5, 2.25];
    let tu = (1.5, 2);
    let s: f32 = 0.0;
    for x in xs { s = s + x; }
    for y in ys { s = s + y; }
    let t: i64 = 0;
    for z in zs { t = t + z; }
    let w: f64 = 0.0;
    for v in ws { w = w + v; }
    return (s * 4.0) as i32 + (t / 1000000000) as i32 + (w * 4.0) as i32 + (tu.0 * 2.0) as i32 + tu.1;
}
`},
	{name: "a-map-reached-only-through-an-enum-payload-is-produced", atLeast: 81, want: "4|", src: `
import "core/map";
enum J { N, S(string), O(Map[string, J]) }
function width(v: J): i32 {
    match (v) {
        O(m) => {
            let n: i32 = 0;
            for (k, x) in m { n = n + k.len() + width(x); }
            return n;
        },
        S(s) => { return s.len(); },
        _ => { return 1; }
    }
}
function main(): i32 { return width(S("abc")) + width(N); }
`},
	{name: "std-json-encode-is-produced", atLeast: 52, want: "2|42\n", src: `
import "std/json";
function main(): i32 {
    let s: string = json.json_encode(JNumber("42"));
    print(s);
    return s.len();
}
`},
	{name: "an-imported-module-does-not-see-the-programs-enums", atLeast: 89, want: "28|", src: `
import "core/map";
import "core/cmp";
import "std/json";

@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B }

enum Reply { Ok, Busy }

function code(r: Reply): i32 {
    match (r) {
        Reply.Ok => { return 1; },
        Reply.Busy => { return 2; },
    }
}

function main(): i32 {
    let em: Map[Tag, i32] = map_new(8);
    em = em.insert(A(3), 30);
    em = em.insert(B, 4);
    let n: i32 = em.entries().len();
    let parsed: i32 = 0;
    match (json.json_parse("12")) {
        Some(_) => { parsed = 1; },
        None => { parsed = 0; },
    }
    return n * 10 + parsed * 5 + code(Reply.Ok) + code(Reply.Busy);
}
`},
	{name: "a-function-returning-a-function-returns-a-box", atLeast: 14, want: "53|", src: `
enum Box { W((i32) => i32), No }
function id[T](x: T): T { return x; }
function inc(x: i32): i32 { return x + 1; }
function through(a: i32): (i32) => i32 { return id(((x: i32) => x + a)); }
function bound(a: i32): (i32) => i32 { let g: (i32) => i32 = id(((x: i32) => x * a)); return g; }
function either(k: i32): (i32) => i32 { if (k > 0) { return inc; } return (x: i32) => x + k; }
function unbox(b: Box): (i32) => i32 { match (b) { W(f) => { return f; }, No => { return inc; } } }
function main(): i32 {
    function mk(a: i32): (i32) => i32 { return id(((x: i32) => x)); }
    let f: (i32) => i32 = mk(1);
    let g = through(2);
    let h = bound(3);
    let e = either(0);
    let u = unbox(W((x: i32) => x - 1));
    return f(4) + g(5) + h(6) + e(7) + u(8) + either(1)(9);
}
`},
	{name: "a-str-result-returns-an-owned-call-result", atLeast: 47, want: "65|", src: `
import "std/string";
function mk(p: string): string { return p + "xy"; }
function owned(p: string): str { return mk(p); }
function view(pre: string): str { let base: string = pre + "  xy  "; return slice_unchecked(base, 0, 6).trim(); }
function main(): i32 { let v: str = view("ab"); let w: str = owned("abc"); return v.len() * 10 + w.len(); }
`},
	{name: "a-generic-variant-carries-a-function-of-its-parameter", atLeast: 4, want: "84|", src: `
enum Box[T] { Fn(T, (T) => T), Two((T, T) => T), Empty }
function inc(x: i32): i32 { return x + 1; }
function add(a: i32, b: i32): i32 { return a + b; }
function run(b: Box[i32]): i32 { match (b) { Fn(n, f) => { return f(n); }, Two(g) => { return g(40, 2); }, Empty => { return 0; } } }
function main(): i32 { return run(Fn(41, inc)) + run(Two(add)) + run(Empty); }
`},
	{name: "a-view-stored-in-a-container-is-a-fresh-view", atLeast: 3, want: "99|", src: `
import "std/i32";
struct P { a: i32, s: str }
function mk(i: i32): string { return "abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string(); }
function round(i: i32): i32 {
    let b: string = mk(i);
    let u: str = slice_unchecked(b, 2, 30);
    let t: (i32, str) = (1, u);
    let xs: str[] = [u];
    xs = xs.append(u);
    xs = xs.with(0, u);
    let p: P = P { a: 2, s: u };
    let o: Option[str] = Some(u);
    let k: i32 = 0;
    match (o) { Some(w) => { k = w.len(); }, None => { k = 0; } }
    let junk: string = mk(i + 1);
    return t.0 + t.1.len() + xs.len() + xs[1].len() + p.s.len() + k + u.len() + junk.len() - 150;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 20) { t = t + round(i); i = i + 1; } return t % 256; }
`},
	{name: "a-view-stored-in-a-map-is-a-fresh-view", atLeast: 3, want: "99|", src: `
import "core/map";
import "std/i32";
function mk(i: i32): string { return "abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string(); }
function round(i: i32): i32 {
    let b: string = mk(i);
    let u: str = slice_unchecked(b, 2, 30);
    let w: str = slice_unchecked(b, 0, 5);
    let m: Map[i32, str] = map_new(4);
    m = m.insert(1, u);
    m = m.insert(2, w);
    m = m.insert(1, w);
    let junk: string = mk(i + 1);
    let k: i32 = 0;
    return m.len() * 10 + k + u.len() + w.len() + junk.len() - 60;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 20) { t = t + round(i); i = i + 1; } return t % 256; }
`},
	{name: "an-unannotated-none-payload-settles-at-void", atLeast: 2, want: "199|", wasm: "out-of-range|", src: `
function round(i: i32): i32 {
    let o = Some(None);
    let k: i32 = 0;
    match (o) { Some(inner) => { match (inner) { Some(_) => { k = 1; }, None => { k = 9; } } }, None => { k = 2; } }
    return k + i % 3;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 20) { t = t + round(i); i = i + 1; } return t; }
`},
	{name: "a-try-exit-replays-a-deferred-assignment-on-its-edge-alone", atLeast: 3, want: "70|", src: `
import "std/i32";
function get(k: i32): Option[string] { if (k % 3 == 0) { return None; } return Some("v" + k.to_string()); }
function step(k: i32): Option[i32] {
    let acc: i32 = 0;
    let names: string[] = [];
    defer acc = acc + 1;
    names = names.append("a" + k.to_string());
    let x: string = get(k)?;
    names = names.append(x);
    acc = acc + x.len() + names.len();
    return Some(acc);
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 30) { match (step(i)) { Some(v) => { t = t + v; }, None => { t = t + 100; } } i = i + 1; }
    return t % 256;
}
`},
	{name: "a-try-exit-runs-the-deferred-cleanup-on-the-failure-edge", atLeast: 3, want: "81|", src: `
function fails(k: i32): Option[i32] { if (k % 2 == 0) { return None; } return Some(k); }
function bump(c: Cell[i32]): void { c.set(c.get() + 1); }
function step(k: i32, out: Cell[i32]): Option[i32] {
    let n: i32 = 0;
    defer bump(out);
    let x: i32 = fails(k)?;
    return Some(n + x);
}
function main(): i32 {
    let out: Cell[i32] = cell_new(0);
    let t: i32 = 0; let k: i32 = 1;
    while (k <= 6) { match (step(k, out)) { Some(v) => { t = t + v; }, None => { t = t + 10; } } k = k + 1; }
    return t + out.get() * 7;
}
`},
	{name: "an-arm-array-beside-a-parameter-or-call-result", atLeast: 9, want: "44|", src: `
function pick(ys: ((i32) => i32)[], c: boolean): i32 {
    let v1: i32 = 3;
    let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => x + v1)] } else { ys });
    return xs[0](1);
}
function mk(k: i32): ((i32) => i32)[] { return [((z: i32) => z + k)]; }
function keep(ys: ((i32) => i32)[]): ((i32) => i32)[] { return ys; }
function main(): i32 {
    let v2: i32 = 7;
    let zs: ((i32) => i32)[] = [((z: i32) => z + 10)];
    let ws: ((i32) => i32)[] = (if (v2 > 100) { [((x: i32) => x + v2)] } else { mk(20) });
    let w: i32 = 5;
    let qs: ((i32) => i32)[] = (match (w) { 5 => keep([((y: i32) => y + v2)]), _ => zs });
    return pick(zs, true) + pick(zs, false) + ws[0](1) + qs[0](1);
}
`},
	{name: "std-i32-methods-on-a-scalar-receiver", atLeast: 2, want: "3|mixed -42 9000000000\n", src: `
import "std/i32";
import "std/i64";
function fold(s: string): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < s.len()) {
        let b: u8 = s[i];
        out = out + b.to_ascii_lower().to_ascii_string();
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let n: i32 = 0 - 42;
    let w: i64 = 9000000000i64;
    let c: u8 = 90;
    print(fold("MiXeD") + " " + n.to_string() + " " + w.to_string());
    if (c.to_ascii_lower().is_ascii_lower()) { return 3; }
    return 1;
}
`},
	{name: "a-lambda-from-a-nested-function-in-a-generic", atLeast: 2, want: "7|", src: `
pub function make[T](seed: T): T {
    function idmaker(base: T): (T) => T {
        return (x: T) => x;
    }
    let f: (T) => T = idmaker(seed);
    return f(seed);
}

function main(): i32 {
    return make(7);
}
`},
	{name: "a-generic-struct-inside-a-generic-function", atLeast: 3, want: "10|", src: `
struct Slot[T] { v: T }

pub function make[T](seed: T): T {
    let c: Slot[T] = Slot[T] { v: seed };
    return c.v;
}

function main(): i32 {
    let s: string = make("ab" + "c");
    let w: i64 = make(5000000000 as i64);
    if (w != 5000000000 as i64) { return 1; }
    return make(7) + s.len();
}
`},
	{name: "a-generic-struct-with-a-function-field-inside-a-generic-function", atLeast: 7, want: "15|", src: `
struct Box[T] { f: (T) => T }

pub function apply[T](seed: T): T {
    let b: Box[T] = Box[T] { f: (x: T): T => x };
    return b.f(seed);
}

pub function nested[T](seed: T): T {
    let b: Box[T] = Box[T] { f: (x: T): T => {
        let c: Box[T] = Box[T] { f: (y: T): T => y };
        return c.f(x);
    } };
    return b.f(seed);
}

function main(): i32 {
    let s: string = apply("ab" + "c");
    let t: string = nested("de" + "f");
    return apply(7) + nested(2) + s.len() + t.len();
}
`},
	{name: "a-generic-struct-over-a-function-parameters-result", atLeast: 7, want: "7|", src: `
struct Slot[T] { v: T }

pub function hold[T](f: () => T): i32 {
    let c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

struct Maker { f: () => string }

struct Box[U] { f: () => U }

function seven(): i32 { return 7; }
function pick(): () => string { return (): string => "de" + "f"; }

function main(): i32 {
    let m: Maker = Maker { f: (): string => "gh" + "i" };
    let b: Box[i32] = Box[i32] { f: (): i32 => 3 };
    let w: Box[string] = Box[string] { f: (): string => "j" + "k" };
    return hold((): i32 => 7) + hold(() => "ab" + "c") + hold(seven) + hold(pick()) + hold(m.f) + hold(b.f) + hold(w.f);
}
`},
	{name: "a-return-bound-constructor-called-from-generic-methods", atLeast: 11, want: "8|", src: `
enum Node[T] { Leaf(T[]), Branch(Node[T][]) }

struct Stack[T] { root: Node[T], n: i32 }

pub function stack_new[T](): Stack[T] {
    return Stack[T] { root: Leaf([]), n: 0 };
}

function size_of[T](node: Node[T]): i32 {
    match (node) {
        Leaf(xs) => { return xs.len(); },
        Branch(kids) => { return kids.len(); }
    }
}

pub function (s: Stack[T]) pushed(x: T): Stack[T] {
    match (s.root) {
        Leaf(xs) => { return Stack[T] { root: Leaf(xs.append(x)), n: s.n + 1 }; },
        Branch(kids) => { return s; }
    }
}

pub function (s: Stack[T]) cleared(): Stack[T] {
    return stack_new();
}

pub function (s: Stack[T]) size(): i32 {
    return size_of(s.root);
}

function main(): i32 {
    let s: Stack[string] = stack_new();
    s = s.pushed("a" + "b").pushed("c");
    let n: Stack[i32] = stack_new();
    n = n.pushed(4);
    return s.size() + n.size() + n.cleared().size() + s.cleared().size() + 5;
}
`},
	{name: "a-fn-param-bound-generic-called-from-a-generic-method", atLeast: 10, want: "5|", src: `
struct Slot[T] { v: T }

pub function hold[T](f: () => T): i32 {
    let c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

struct Box[U] { u: U }

pub function (b: Box[U]) held(): i32 {
    return hold((): i32 => 7) + hold((): string => "ab" + "c");
}

function main(): i32 {
    let b: Box[i32] = Box[i32] { u: 1 };
    let w: Box[string] = Box[string] { u: "x" };
    return b.held() + w.held() + hold((): i32 => 2);
}
`},
	{name: "a-generic-struct-over-a-function-parameters-parameter", atLeast: 5, want: "3|", src: `
struct Slot[T] { v: T[] }

pub function count[T](f: (T) => i32): i32 {
    let c: Slot[T] = Slot[T] { v: [] };
    return c.v.len() + 1;
}

function strlen(s: string): i32 { return s.len(); }

function main(): i32 {
    return count((x: i32): i32 => x) + count((s: string): i32 => s.len()) + count(strlen);
}
`},
	{name: "a-generic-lambda-capturing-a-type-variable", atLeast: 25, want: "63|", src: `
struct Box[T] { f: (i32) => T }

pub function boxed[T](seed: T): Box[T] {
    return Box { f: (k: i32): T => seed };
}

function boxed_len(b: Box[string]): i32 {
    return b.f(1).len();
}

pub function direct[T](seed: T): T {
    let g: (i32) => T = (k: i32): T => seed;
    return g(1);
}

pub function returned[T](seed: T): (i32) => T {
    return (k: i32): T => seed;
}

pub function stored[T](seed: T): T {
    let fs: ((i32) => T)[] = [(k: i32): T => seed];
    return fs[0](1);
}

pub function made[T](seed: T): ((i32) => T)[] {
    let fs: ((i32) => T)[] = [(k: i32): T => seed];
    return fs;
}

pub function held[T](seed: T): T {
    let f: (i32) => T = (k: i32): T => seed;
    let fs: ((i32) => T)[] = [f];
    return fs[0](1);
}

function bit(ok: boolean, b: i32): i32 {
    if (ok) {
        return b;
    }
    return 0;
}

function main(): i32 {
    let big: i64 = 5000000000;
    let r = bit(direct(7) == 7, 1) + bit(direct(big) == big, 2) + bit(direct("ab" + "c").len() == 3, 4);
    r = r + bit(returned(big)(1) == big, 8) + bit(returned("d" + "ef")(2).len() == 3, 16);
    let kept: boolean = stored(7) == 7 && stored(big) == big && stored("g" + "h").len() == 2;
    let away: boolean = made(7)[0](1) == 7 && made(big)[0](1) == big && made("l" + "m")[0](1).len() == 2;
    let field: boolean = boxed(7).f(1) == 7 && boxed(big).f(1) == big && boxed_len(boxed("n" + "op")) == 3;
    return r + bit(kept && away && field && held(7) == 7 && held(big) == big && held("i" + "jk").len() == 3, 32);
}
`},
	{name: "a-lambda-inside-a-lifted-generic-lambda", atLeast: 9, want: "14|", src: `
pub function make[T](seed: T): T {
    function outer(base: T): (T) => T {
        return (x: T): T => ((y: T): T => y)(x);
    }
    let f: (T) => T = outer(seed);
    return f(seed);
}

pub function keep[T](seed: T): T {
    function outer(base: T): (T) => T {
        return (x: T): T => {
            let inner: (T) => T = (y: T): T => y;
            return inner(x);
        };
    }
    let f: (T) => T = outer(seed);
    return f(seed);
}

function main(): i32 {
    let s: string = make("ab" + "c");
    let t: string = keep("de" + "f");
    return make(7) + keep(1) + s.len() + t.len();
}
`},
	{name: "a-nested-generic-function-returning-a-curried-function", atLeast: 5, want: "10|", src: `
pub function make[T](seed: T): T {
    function outer(base: T): (T) => (T) => T {
        return (x: T): (T) => T => (y: T): T => y;
    }
    let f: (T) => (T) => T = outer(seed);
    let g: (T) => T = f(seed);
    return g(seed);
}

function main(): i32 {
    let s: string = make("ab" + "c");
    return make(7) + s.len();
}
`},
	{name: "a-nested-generic-function-returning-a-lambda-that-names-no-type-variable", atLeast: 7, want: "14|", src: `
pub function make[T](seed: T): T {
    function outer(base: T): (string) => i32 {
        return (s: string): i32 => s.len() + 1;
    }
    let f: (string) => i32 = outer(seed);
    let n: i32 = f("ab" + "c");
    return seed;
}

pub function keep[T](seed: T): T {
    function outer(base: T): (string) => i32 {
        let k: i32 = 2;
        return (s: string): i32 => s.len() + k;
    }
    let f: (string) => i32 = outer(seed);
    let n: i32 = f("de" + "f");
    return seed;
}

function main(): i32 {
    let s: string = make("ab" + "c");
    let t: string = keep("gh" + "i");
    return make(7) + keep(1) + s.len() + t.len();
}
`},
	{name: "the-indirect-cone-runs-through-an-erased-generic-body", atLeast: 6, want: "12|", src: `
function helper(own xs: string[]): i32 {
    return xs.len();
}

pub function pick[T](seed: T): T {
    let n: i32 = helper(["a" + "b"]);
    return seed;
}

pub function make[T](seed: T): T {
    function outer(base: T): (string) => i32 {
        return (s: string): i32 => pick(s).len() + 1;
    }
    let f: (string) => i32 = outer(seed);
    let n: i32 = f("ab" + "c");
    return seed;
}

function main(): i32 {
    let s: string = make("ab" + "c");
    return make(7) + s.len() + pick(2);
}
`},
	{name: "a-lambda-lifted-out-of-a-lifted-lambda", atLeast: 7, want: "23|", src: `
struct Box { f: (i32) => i32 }

function adder(n: i32): (i32) => (i32) => i32 {
    return (x: i32) => (y: i32) => x + y + n;
}

function boxed(n: i32): i32 {
    let b: Box = Box { f: (x: i32): i32 => {
        let c: Box = Box { f: (y: i32): i32 => y + x + n };
        return c.f(x);
    } };
    return b.f(n);
}

function listed(seed: i32, n: i32): i32 {
    let fs: ((i32) => i32)[] = [((k: i32): i32 => {
        let gs: ((i32) => i32)[] = [((j: i32): i32 => seed + j)];
        return gs[0](k + n);
    })];
    return fs[0](0);
}

function main(): i32 {
    let f: (i32) => (i32) => i32 = adder(1);
    let g: (i32) => i32 = f(2);
    return g(3) + boxed(3) + listed(7, 1);
}
`},
	{name: "a-lambda-binding-fewer-variables-than-its-creator", atLeast: 2, want: "9|", src: `
pub function make[A, B](a: A, b: B): A {
    function idmaker(base: A, other: B): (A) => A {
        return (x: A) => x;
    }
    let f: (A) => A = idmaker(a, b);
    return f(a);
}

function main(): i32 {
    let s: string = "x";
    return make(7, s) + make(2, true);
}
`},
	{name: "a-value-block-that-leaves-only-by-return-or-break", atLeast: 6, want: "134|ab\nabbb!\nzeromany\n", wasm: "out-of-range|ab\nabbb!\nzeromany\n", src: `
function f(c: boolean): i32 { let x: i32 = { if (c) { return 1; } return 2; }; return x + 100; }
function g(n: i32): string {
    let s: string = "a";
    let i: i32 = 0;
    while (i < 5) {
        if (i == n) { let t: string = { if (n > 2) { return s + "!"; } break; }; s = s + t; }
        s = s + "b";
        i = i + 1;
    }
    return s;
}
function h(c: boolean): i32 { let x: i32 = if (c) { return 1; } else { return 2; }; return x + 100; }
function k(n: i32): string { let x: string = match (n) { 0 => { return "zero"; }, _ => { return "many"; } }; return x + "?"; }
function j(c: boolean, stop: boolean): i32 {
    let i: i32 = 0;
    while (i < 4) {
        i = i + 1;
        if (c) { let t: i32 = { if (stop) { return 0; } continue; }; i = i + t; }
    }
    return i;
}
function main(): i32 {
    print(g(1));
    print(g(3));
    print(k(0) + k(4));
    return f(true) * 10 + f(false) + h(true) * 30 + h(false) * 40 + j(true, false) + j(false, false) * 2 + j(true, true);
}
`},
	{name: "a-generic-enum-from-a-function-value-or-a-tuple-element", atLeast: 5, want: "59|", src: `
enum Opt[T] { Non, Has(T) }
function run(f: (i32) => Opt[string], k: i32): i32 { match (f(k)) { Has(s) => { return s.len(); }, Non => { return 0; } } }
function pick(t: (Opt[string], i32)): i32 { let e: Opt[string] = t.0; match (e) { Has(s) => { return s.len() + t.1; }, Non => { return t.1; } } }
function main(): i32 {
    let g: (i32) => Opt[i32] = (k: i32): Opt[i32] => { if (k > 0) { return Has(k * 6); } return Non; };
    let a: i32 = 0;
    match (g(7)) { Has(v) => { a = v; }, Non => { a = 1; } }
    let t: (Opt[string], i32) = (Has("abc" + "de"), 2);
    let u: (i32, Opt[i32]) = (3, Has(4));
    let b: i32 = 0;
    match (u.1) { Has(v) => { b = v + u.0; }, Non => { b = 0; } }
    return a + run((k: i32): Opt[string] => { if (k > 1) { return Has("x" + "yz"); } return Non; }, 2) + pick(t) + b;
}
`},
	{name: "a-generic-enum-with-a-generic-struct-payload", atLeast: 8, want: "40|xyz\n", src: `
struct Box[T] { v: T, n: i32 }
function (self: Box[T]) size(): i32 { return self.n * 2; }
enum E[U] { A(Box[U]), B(i32) }
function (self: E[U]) boxed(d: Box[U]): Box[U] { match (self) { A(b) => { return b; }, B(k) => { return d; } } }
function (self: E[U]) weight(): i32 { match (self) { A(b) => { return b.size(); }, B(k) => { return k; } } }
enum Holder[T] { Has(T), Empty }
function first(h: Holder[string[]]): string { match (h) { Has(xs) => { return xs[0]; }, Empty => { return ""; } } }
function main(): i32 {
  let e: E[string] = A(Box { v: "abc" + "d", n: 3 });
  let f: E[i32] = A(Box { v: 7, n: 5 });
  let g: E[i32] = B(9);
  let names: string[] = ["x" + "yz", "w"];
  let h = Has(names);
  print(first(h));
  return e.boxed(Box { v: "", n: 0 }).v.len() + g.boxed(Box { v: 11, n: 0 }).v + e.weight() + f.weight() + g.weight();
}
`},
	{name: "a-generic-enum-at-an-array-type-argument", atLeast: 7, want: "86|xyz-\n", src: `
enum Fut[T] { Ready(T), Pending(i32, (i32) => Fut[T]) }
enum Holder[T] { Has(T), Empty }
function step(fd: i32): Fut[u8[]] { let b: u8[] = [1 as u8, 2 as u8, fd as u8]; return Ready(b); }
function run(f: Fut[u8[]]): i32 {
    match (f) { Ready(b) => { return b.len() + (b[2] as i32); }, Pending(k, g) => { return run(g(k)); } }
}
function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i * 3); i = i + 1; }
    return a;
}
function probe(h: Holder[i32[]]): i32 { match (h) { Has(xs) => { return xs[1]; }, Empty => { return 0; } } }
function first(h: Holder[string[]]): string { match (h) { Has(xs) => { return xs[0]; }, Empty => { return "-"; } } }
function main(): i32 {
    let names: string[] = ["x" + "yz", "w"];
    let h = Has(names);
    print(first(h) + first(Empty));
    let bound: Holder[i32[]] = Has(mk(4));
    return run(Pending(5, step)) * 10 + probe(Has(mk(8))) + probe(bound) + probe(Empty);
}
`},
	{name: "a-generic-record-over-an-unspelled-function-value", atLeast: 52, want: "0|7 abc arm\n", src: `
import "std/i32";
struct Slot[T] { v: T }
pub function keep[T](f: () => T): T {
    let c: Slot[T] = Slot[T] { v: f() };
    return c.v;
}
function pick(o: Option[() => string]): string {
    match (o) { Some(f) => { return keep(f); }, None => { return "none"; } }
}
function main(): i32 {
    let fs: (() => i32)[] = [(): i32 => 7];
    let gs: (() => string)[] = [(): string => "ab" + "c"];
    print(keep(fs[0]).to_string() + " " + keep(gs[0]) + " " + pick(Some((): string => "arm")));
    return 0;
}
`},
	{name: "a-value-if-widens-a-literal-arm", atLeast: 5, want: "0|1 -1 4 9 1\n", src: `
import "std/i64";
function pick(k: i32): i64 {
    let p = match (k) { 0 => { (1, 4) }, 1 => { (2, -4611686018427387905) }, _ => { (3, 5) } };
    return p.1 + 4611686018427387904;
}
function main(): i32 {
    let r: i64 = 5;
    let a = if (r > 0) { (2, 4611686018427387905) } else { (3, 4) };
    let b = if (r < 0) { (3, 4) } else { (2, 4611686018427387905) };
    let q = if (r > 0) { 4 } else { 4611686018427387905 };
    print((a.1 - 4611686018427387904).to_string() + " " + pick(1).to_string() + " " + q.to_string()
        + " " + (pick(2) - 4611686018427387900).to_string() + " " + (b.1 - 4611686018427387904).to_string());
    return 0;
}
`},
	{name: "a-literal-built-local-moves-at-its-last-use", atLeast: 57, want: "0|21\n", src: `
import "std/i32";
struct Box { xs: i64[], tag: string }
@noinline function take(own xs: i64[]): i32 { return xs.len() as i32; }
@noinline function takeb(own b: Box): i32 { return b.xs.len() as i32 + b.tag.len() as i32; }
@noinline function takes(own s: string): i32 { return s.len() as i32; }
@noinline function mk(): string { return "ab" + "cd"; }
function one(): i32 {
    let xs: i64[] = [];
    xs = xs.append(1);
    return take(xs) + 0;
}
function two(): i32 {
    let xs: i64[] = [5, 6];
    xs = xs.append(7);
    let r: i32 = take(xs);
    return r;
}
function three(): i32 {
    let b: Box = Box { xs: [1, 2], tag: "t" };
    b = Box { ...b, tag: b.tag + "u" };
    let r: i32 = takeb(b);
    return r;
}
function four(): i32 {
    let s: string = "ab";
    s = s + "cd";
    let r: i32 = takes(s);
    return r;
}
function five(): i32 {
    let s: string = mk();
    return takes(s);
}
function main(): i32 {
    function nested(): i32 {
        let b: Box = Box { xs: [], tag: "nest" };
        let r: i32 = takeb(b);
        return r + 1;
    }
    print((one() + two() + three() + four() + five() + nested()).to_string());
    return 0;
}
`},
	{name: "a-local-stored-after-a-gap-moves-at-the-call", atLeast: 52, want: "0|15\n", src: `
import "std/i32";
struct Pair { a: i64[], b: i64[] }
@noinline function step(own xs: i64[], own ys: i64[], k: i64): Pair {
    return Pair { a: xs.append(k), b: ys.append(k * 2) };
}
@noinline function take(own xs: i64[]): i32 { return xs.len() as i32; }
function threaded(n: i32): i32 {
    let xs: i64[] = [];
    let ys: i64[] = [1];
    let i: i32 = 0;
    while (i < n) {
        let p: Pair = step(xs, ys, i as i64);
        xs = p.a;
        ys = p.b;
        i = i + 1;
    }
    return xs.len() as i32 + ys.len() as i32;
}
function gap(): i32 {
    let xs: i64[] = [1, 2];
    let r: i32 = take(xs);
    let m: i32 = r + 1;
    xs = [3];
    return m + xs.len() as i32;
}
function main(): i32 {
    print((threaded(5) + gap()).to_string());
    return 0;
}
`},
	{name: "an-erased-generic-forwarding-its-function-parameter", atLeast: 51, want: "0|5 wx\n", src: `
import "std/i32";
struct Slot[T] { v: T }
pub function keep[T](f: () => T): T {
    let c: Slot[T] = Slot[T] { v: f() };
    return c.v;
}
pub function wrap[U](g: () => U): U { return keep(g); }
function main(): i32 {
    let n: i32 = 4;
    print(wrap((): i32 => n + 1).to_string() + " " + wrap((): string => "w" + "x"));
    return 0;
}
`},
}

const semDynShapes = `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct Square { side: i32, tag: string }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side + self.tag.len(); }
}
enum Tri { Right(i32, string), Flat }
impl Shape for Tri {
    function area(self: Self): i32 {
        match (self) { Right(b, t) => { return b + t.len(); }, Flat => { return 0; } }
    }
}
function make(n: i32): dyn Shape {
    if (n % 2 == 0) { return Square { side: n, tag: "sq" + n.to_string() }; }
    return Tri.Right(n, "tri" + n.to_string());
}
`

const semPrimitiveShows = `
import "std/i32";

trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self * 2; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
struct Sq { side: i32, tag: string }
impl Show for Sq { function show(self: Self): i32 { return self.side + self.tag.len(); } }
function make(i: i32): dyn Show {
    if (i % 3 == 0) { return i; }
    if (i % 3 == 1) { return "s" + i.to_string(); }
    return Sq { side: i, tag: "t" + i.to_string() };
}
`

const semUsizeOperatorsSource = `
struct Region { base: usize }
function main(): i32 {
    let n: i32 = args().len() + 69;
    let one: usize = (n - 69) as usize;
    let top: usize = one << (n - 39);
    let a: usize = top + (n as usize);
    let b: usize = (n - 59) as usize;
    let got: i32 = 0;
    if ((a / b) as i64 == 195225792i64) { got = got + 1; }
    if ((a % b) as i32 == 6) { got = got + 2; }
    if ((top >> (n - 40)) as i32 == 2 && (a >> 28) as i32 == 8) { got = got + 4; }
    if (top > b && a >= top && b < a && !(a <= b)) { got = got + 8; }
    if (a / (0 as usize) == (0 as usize) && a % (0 as usize) == a) { got = got + 16; }
    let words: usize[] = [top, a];
    let s: Region = Region { base: top };
    let t: Region = Region { base: b };
    if ((words[0] >> (n - 40)) as i32 == 2 && (words[1] / b) as i64 == 195225792i64 && s.base > t.base) { got = got + 32; }
    return got;
}
`

const semUsizeWideOperatorsSource = `
struct Region { base: usize }
function scaled(n: i32): usize { return ((n as i64) * 1000000000i64) as usize; }
function addresses(n: i32): i32 {
    let a: usize = scaled(n);
    let b: usize = scaled(1);
    let one: usize = (n - 69) as usize;
    let top: usize = one << (n - 7);
    let got: i32 = 0;
    if ((a / b) as i32 == 70) { got = got + 1; }
    if ((a % (b + (7 as usize))) as i64 == 999999517i64) { got = got + 2; }
    if (((one << (n - 30)) >> (n - 32)) as i32 == 4) { got = got + 4; }
    if ((top >> 62) as i32 == 2) { got = got + 8; }
    if (top > a && a >= b && b < top && !(a <= b)) { got = got + 16; }
    let words: usize[] = [top, a];
    let s: Region = Region { base: a };
    let t: Region = Region { base: b };
    if ((words[0] >> 62) as i32 == 2 && (s.base / t.base) as i32 == 70 && words[0] > s.base) { got = got + 32; }
    return got;
}
function wide(n: i32): i32 {
    let one: u64 = (n - 69) as u64;
    let top: u64 = one << ((n - 7) as u64);
    let a: u64 = top + (70 as u64);
    let b: u64 = (n - 60) as u64;
    let got: i32 = 0;
    if (((a / b) >> 59) as i32 == 1 && (a % b) as i32 == 8) { got = got + 1; }
    if ((top >> 62) as i32 == 2 && a > b && !(top < b)) { got = got + 2; }
    return got;
}
function main(): i32 {
    let n: i32 = args().len() + 69;
    return addresses(n) + wide(n) * 64;
}
`

const semUsizeWideLiteralSource = `
function main(): i32 {
    let a: usize = 70000000000 as usize;
    let b: usize = 1000000000 as usize;
    return (a / b) as i32;
}
`

const semUsizeWideProductSource = `
function addr(x: i64): usize { return x as usize; }
function main(): i32 {
    let n: i64 = (args().len() as i64) * 70000000000i64;
    let a: usize = n as usize;
    let b: usize = addr(n * 2i64);
    let unit: usize = 100000000 as usize;
    return ((a / unit) + (b / unit) * (4 as usize)) as i32;
}
`

const semCCallSource = `
function run0(cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(cb: usize, a: usize): i32 { return __c_call1(cb, a) as i32; }
function run2(cb: usize, a: usize, b: usize): i32 { return __c_call2(cb, a, b) as i32; }
function run3(cb: usize, a: usize, b: usize, c: usize): i32 { return __c_call3(cb, a, b, c) as i32; }
function run4(cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function runf(cb: usize, a: usize): f64 { return __c_call1_f64(cb, a) + (__c_call2_f32(cb, a, a) as f64); }
function main(): i32 {
    let n: i32 = args().len();
    if (n > 100) {
        let cb: usize = n as usize;
        return run0(cb) + run1(cb, cb) + run2(cb, cb, cb) + run3(cb, cb, cb, cb) + run4(cb, cb, cb, cb, cb) + (runf(cb, cb) as i32);
    }
    return 0;
}
`

const semDominatingViewSource = `
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function heads(s: string): str[] { return [slice_unchecked(s, 0, 1), slice_unchecked(s, 1, 2)]; }
function pick(n: i32): i32 {
    let s: string = mk(n);
    let tail: str = "";
    let a: str[] = [];
    let b: str[] = [];
    let i: i32 = 0;
    while (i < n) {
        if (i % 2 == 0) {
            tail = slice_unchecked(s, 1, 3);
            a = a.append(slice_unchecked(s, 0, 2));
            b = heads(s);
        }
        i = i + 1;
    }
    return tail.len() + a.len() + b.len();
}
function main(): i32 { return pick(9); }
`

const semTupleElementTakeSource = `
struct T { a: i64[], b: i64[] }

// A literal of constants is a static box, which no update can reuse; one
// element from a call keeps the record's arrays on the heap.
@noinline function zero(): i64 { return 0 as i64; }

function touch(t: T, at: i32): T {
  return T { ...t, a: t.a.with(at, 1 as i64) };
}

function pair(t: T, at: i32): (T, boolean) {
  return (touch(t, at), true);
}

function main(): i32 {
  let t: T = T { a: [zero(), 0 as i64, 0 as i64, 0 as i64], b: [zero()] };
  let k: i32 = 0;
  while (k < 10) {
    let p: (T, boolean) = pair(t, k % 4);
    t = p.0;
    t = touch(t, (k + 1) % 4);
    if (!p.1) {
      return 1;
    }
    k = k + 1;
  }
  return 0;
}
`

const semTupleDestructureTakeSource = `
struct T { a: i64[], b: i64[] }

// A literal of constants is a static box, which no update can reuse; one
// element from a call keeps the record's arrays on the heap.
@noinline function zero(): i64 { return 0 as i64; }

function touch(t: T, at: i32): T {
  return T { ...t, a: t.a.with(at, 1 as i64) };
}

function pair(t: T, at: i32): (T, boolean) {
  return (touch(t, at), true);
}

function main(): i32 {
  let t: T = T { a: [zero(), 0 as i64, 0 as i64, 0 as i64], b: [zero()] };
  let k: i32 = 0;
  while (k < 10) {
    let (u, ok) = pair(t, k % 4);
    t = u;
    t = touch(t, (k + 1) % 4);
    if (!ok) {
      return 1;
    }
    k = k + 1;
  }
  return 0;
}
`

const semSpreadTwoWithsSource = `
struct C { fds: i64[], t: i64[], k: boolean[] }

// A literal of constants is a static box, which no update can reuse; one
// element from a call keeps the record's arrays on the heap.
@noinline function zero(): i64 { return 0 as i64; }

@noinline function respond(c: C, at: i32, v: i64, keep: boolean): C {
  return C { ...c, t: c.t.with(at, v), k: c.k.with(at, c.k[at] && keep) };
}

function main(): i32 {
  let c: C = C { fds: [zero() + 1 as i64, 2 as i64, 3 as i64, 4 as i64], t: [zero(), 0 as i64, 0 as i64, 0 as i64], k: [true, true, true, true] };
  let r: i32 = 0;
  while (r < 10) {
    c = respond(c, r % 4, r as i64, true);
    r = r + 1;
  }
  if (!c.k[0]) {
    return 1;
  }
  return 0;
}
`

const semHeldElementSource = `
import "std/string";

function ins(own a: string[], lo: i32, hi: i32): string[] {
    let i: i32 = lo + 1;
    while (i < hi) {
        let v: string = a[i];
        let j: i32 = i - 1;
        let moving: boolean = j >= lo;
        while (moving) {
            if (a[j].len() > v.len()) {
                a = a.with(j + 1, a[j]);
                j = j - 1;
                moving = j >= lo;
            } else {
                moving = false;
            }
        }
        a = a.with(j + 1, v);
        i = i + 1;
    }
    return a;
}

function main(): i32 {
    let xs: string[] = ["ccc", "a", "bb", "dddd", "", "ee", "ffffff", "g"];
    let n: i32 = xs.len();
    xs = ins(xs, 0, n);
    let out: string = "";
    for x in xs { out = out + x + "|"; }
    print(out);
    return xs[7].len();
}
`

const semHeldFieldSource = `
import "std/i32";

struct Scope { names: string[] }
struct St { values: string[], scope: Scope, n: i32 }

@noinline
function step(s: St, k: i32): St {
    return St { ...s, values: s.values.append("v"), n: s.n + k };
}

@noinline
function block(s: St, k: i32): St {
    let scope: Scope = s.scope;
    let i: i32 = 0;
    while (i < k) {
        s = step(s, i);
        i = i + 1;
    }
    return St { ...s, scope: scope };
}

function main(): i32 {
    let s: St = St { values: [], scope: Scope { names: ["a"] }, n: 0 };
    let j: i32 = 0;
    while (j < 20) {
        s = block(s, 10);
        j = j + 1;
    }
    print(s.values.len().to_string() + "|" + s.scope.names[0] + "|" + s.n.to_string() + "|");
    return 0;
}
`

const semElementBesideArraySource = `
import "std/i32";
import "std/string";

struct Table { names: string[], values: string[] }
struct Added { t: Table, id: i32 }

@noinline
function add(t: Table, name: string, value: string): Added {
    let id: i32 = t.values.len();
    return Added { t: Table { ...t, names: t.names.append(name), values: t.values.append(value) }, id: id };
}

@noinline
function copy_entry(t: Table, at: i32): Added {
    return add(t, t.names[at], t.values[at]);
}

function main(): i32 {
    let t: Table = Table { names: [], values: [] };
    let i: i32 = 0;
    while (i < 8) {
        let a: Added = add(t, "key" + "s", "value" + "s");
        t = a.t;
        i = i + 1;
    }
    let total: i32 = 0;
    i = 0;
    while (i < 24) {
        let a: Added = copy_entry(t, i % 8);
        t = a.t;
        total = total + a.id;
        i = i + 1;
    }
    print(f"{t.names.len()}|{t.values[31]}|{total}\n");
    return t.values.len() - 32;
}
`

func TestSelfHostSemanticAllocationCounts(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, prog := range []struct {
		name   string
		allocs int64
		src    string
	}{
		{"element-read-outlives-the-array-write", 3, semHeldElementSource},
		{"field-read-outlives-the-record-move", 13, semHeldFieldSource},
		{"an-element-handed-beside-its-dying-array", 44, semElementBesideArraySource},
		{"a-view-of-a-dominating-source-is-not-copied", 28, semDominatingViewSource},
		{"a-tuple-element-is-taken-past-a-later-sibling-read", 2, semTupleElementTakeSource},
		{"a-destructured-element-is-taken-past-a-later-sibling-read", 2, semTupleDestructureTakeSource},
		{"a-field-is-taken-past-a-sibling-read-in-a-later-block", 4, semSpreadTwoWithsSource},
	} {
		t.Run(prog.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(prog.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := semAllocations(t, fernBin, stdlibRoot, src); got != prog.allocs {
				t.Fatalf("the produced bodies allocated %d times, want %d", got, prog.allocs)
			}
		})
	}
}

func semAllocations(t *testing.T, fernBin, stdlibRoot, src string) int64 {
	t.Helper()
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, src, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, output)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	output, _ := exec.Command(out).CombinedOutput()
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(string(output)), &allocs, &frees, &live); err != nil {
		t.Fatalf("no leakcheck summary: %v\n%s", err, output)
	}
	return allocs
}
