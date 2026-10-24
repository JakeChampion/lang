package e2ecompiler

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var strictIRCorpus = []struct {
	name string
	src  string
	want int
}{
	{"dyn-view-merged-past-its-source", `import "std/i32";
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
    let d: dyn Size = P { a: "q" };
    if (n != 0) {
        let s: string = mk(n);
        d = wrap(s);
    }
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return d.size();
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`, 0},
	{"views-of-two-sources", `function g(x: string, y: string): str[] { let o: str[] = []; o = o.append(slice_unchecked(x, 0, 1)); o = o.append(slice_unchecked(y, 0, 1)); return o; }
function main(): i32 { let xs: str[] = g("ab", "cd"); return xs.len(); }
`, 2},
	{"view-map-value-reads", `import "core/map";
function main(): i32 {
    let b: string = "abcdefgh";
    let m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(b, 2, 6));
    let n: i32 = m.get_or(1, "").len() + m.get_or(2, "x").len();
    match (m.get(1)) { Some(v) => { n = n + v.len(); }, None => { n = n + 100; } }
    for v in m.values() { n = n + v.len(); }
    for (k, v) in m { n = n + k + v.len(); }
    return n;
}
`, 18},
	{"local-view-held-twice", `function g(s: string): str[] { let w: str = slice_unchecked(s, 0, 2); let o: str[] = [w, w]; return o; }
function main(): i32 { return g("abcd").len(); }
`, 2},
	{"builtin-result-value-block-early-return", `function probe(p: string): i32 {
    let si: FileStat = match (stat(p)) { Ok(v) => v, Err(_) => { return 1; } };
    let held: FileStat = si;
    return 4;
}
function main(): i32 { return probe("/nonexistent-9326/a") + probe("/nonexistent-9326/b"); }
`, 2},
	{"wrapped-result-value-block-early-return", `function mine(p: string): Result[FileStat, string] {
    match (stat(p)) { Ok(v) => { return Ok(v); }, Err(_) => { return Err("no"); } }
}
function probe(p: string): i32 {
    let si: FileStat = match (mine(p)) { Ok(v) => v, Err(_) => { return 1; } };
    let held: FileStat = si;
    return 4;
}
function main(): i32 { return probe("/nonexistent-9326/a") + probe("/nonexistent-9326/b"); }
`, 2},
	{"destructured-array-closure", `function main(): i32 {
    let t: (i32[], i32) = ([3i32], 4i32);
    let (a, b) = t;
    let fs: ((i32) => i32)[] = [((x: i32) => (x + a[0i32]))];
    return (fs[0i32](1i32) + b) & 63i32;
}
`, 8},
	{"checked-operators", `
function f(a: u8, b: u8): i32 {
    match (a +? b) { Some(v) => { return v as i32; }, None => { return 99; } }
}
function g(a: i32, b: i32): i32 {
    match (a *? b) { Some(v) => { return v; }, None => { return 7; } }
}
function main(): i32 { return f(100, 3) + g(2, 3) - f(250, 10); }
`, 10},
	{"closures", `
function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    let n: i32 = 5;
    let add: (i32) => i32 = (x: i32): i32 => { return x + n; };
    let dbl: (i32) => i32 = (x: i32): i32 => { return x * 2; };
    let fs: ((i32) => i32)[] = [add, dbl];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < fs.len()) { t = t + apply(fs[i], 3); i = i + 1; }
    return t;
}
`, 14},
	{"enum-match-guard", `
enum Shape { Circle(i32), Rect(i32, i32), Empty }
function area(s: Shape): i32 {
    match (s) {
        Circle(r) when r > 10 => { return 999; },
        Circle(r) => { return 3 * r * r; },
        Rect(w, h) => { return w * h; },
        Empty => { return 0; }
    }
}
function main(): i32 { return area(Circle(2)) + area(Rect(3, 4)) + area(Empty); }
`, 24},
	{"struct-array-strings", `
struct P { name: string, n: i32 }
function label(i: i32): string {
    if (i % 2 == 0) { return "ab"; }
    return "xyz";
}
function build(n: i32): P[] {
    let out: P[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(P { name: label(i) + "!", n: i }); i = i + 1; }
    return out;
}
function main(): i32 {
    let ps: P[] = build(5);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < ps.len()) { if (ps[i].name.len() == 3) { t = t + ps[i].n + 1; } i = i + 1; }
    return t;
}
`, 9},
	{"generics-tuples-try", `
function pair[K, V](k: K, v: V): (K, V) { return (k, v); }
function first(t: (i32, string)): i32 { return t.0; }
function parse(s: string): Result[i32, string] {
    if (s == "ok") { return Ok(1); }
    return Err("bad");
}
function chain(s: string): Result[i32, string] {
    let v: i32 = parse(s)?;
    return Ok(v + 41);
}
function main(): i32 {
    let t: (i32, string) = pair(1, "x");
    match (chain("ok")) { Ok(v) => { return v + first(t); }, Err(_) => { return 0; } }
}
`, 43},
	{"match-closure-local-opt", `
function main(): i32 {
    let f: () => Option[i32] = () => Some(7);
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-capturing-closure-local-opt", `
function main(): i32 {
    let n: i32 = 7;
    let f: () => Option[i32] = () => Some(n);
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-fnlocal-named-opt", `
function g(): Option[i32] { return Some(7); }
function main(): i32 {
    let f: () => Option[i32] = g;
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-fnlocal-named-result", `
function g(): Result[i32, i32] { return Ok(5); }
function main(): i32 {
    let f: () => Result[i32, i32] = g;
    match (f()) { Ok(v) => { return v; }, Err(_) => { return 9; } }
}
`, 5},
	{"try-generic-payload", `
function f(n: i32): Result[Option[i32], i32] { return Ok(Some(n)); }
function g(n: i32): Result[i32, i32] {
    let x: Option[i32] = f(n)?;
    match (x) { Some(v) => { return Ok(v); }, None => { return Ok(0); } }
}
function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }
`, 5},
	{"try-generic-payload-option", `
function f(n: i32): Option[Option[i32]] { if (n > 3) { return Some(Some(n)); } return Some(None); }
function g(n: i32): Option[i32] {
    let x: Option[i32] = f(n)?;
    match (x) { Some(v) => { return Some(v + 1); }, None => { return Some(50); } }
}
function main(): i32 {
    let a: i32 = 0;
    match (g(7)) { Some(v) => { a = v; }, None => { a = 99; } }
    match (g(1)) { Some(v) => { a = a + v; }, None => { a = a + 99; } }
    return a;
}
`, 58},
	{"try-generic-payload-chain", `
function inner(n: i32): Result[Result[i32, i32], i32] { if (n > 0) { return Ok(Ok(n)); } return Ok(Err(3)); }
function outer(n: i32): Result[i32, i32] {
    let o: Result[i32, i32] = inner(n)?;
    let v: i32 = o?;
    return Ok(v * 2);
}
function main(): i32 { match (outer(9)) { Ok(v) => { return v; }, Err(_) => { return 88; } } }
`, 18},
	{"i32-min-max-clamp", `import "std/i32";
function main(): i32 {
    let a: i32 = 8;
    let b: i32 = 3;
    return a.min(b) + a.max(b) + (99).clamp(0, 10) + (0 - 5).clamp(0, 10);
}
`, 21},
	{"arr-first-last", `import "std/array";
import "std/option";
struct P { name: string, n: i32 }
function build(): i32[] {
    let out: i32[] = [];
    out = out.append(4);
    out = out.append(6);
    return out;
}
function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ss: string[] = ["ab", "cde"];
    let ps: P[] = [P { name: "x", n: 3 }, P { name: "yy", n: 4 }];
    let m: i32[][] = [[1, 2], [3, 4]];
    let mm: string[][] = [["a", "bb"], ["ccc"]];
    let none: P = P { name: "", n: 0 };
    let t: i32 = xs.first().unwrap_or(0) + xs.last().unwrap_or(0);            // 40
    t = t + ss.first().unwrap_or("").len() + ss.last().unwrap_or("").len();   // +5
    t = t + ps.first().unwrap_or(none).n + ps.last().unwrap_or(none).name.len(); // +5
    t = t + m.first().unwrap_or([])[1] + m.last().unwrap_or([])[0];           // +5
    t = t + mm.first().unwrap_or([])[1].len() + mm.last().unwrap_or([]).first().unwrap_or("").len(); // +5
    return t + build().last().unwrap_or(0) + build().first().unwrap_or(0);    // +10
}
`, 70},
	{"ascii-byte-methods", `import "std/i32";
function main(): i32 {
    let t: i32 = 0;
    let A: u8 = 65;
    let z: u8 = 122;
    let d: u8 = 53;
    let f: u8 = 70;
    let g: u8 = 71;
    let sp: u8 = 32;
    if (A.to_ascii_lower() as i32 == 97) { t = t + 1; }
    if (z.to_ascii_upper() as i32 == 90) { t = t + 2; }
    if (A.to_ascii_upper() as i32 == 65) { t = t + 4; }   // already upper: unchanged
    if (z.to_ascii_lower() as i32 == 122) { t = t + 8; }  // already lower: unchanged
    if (d.is_ascii_digit() && !A.is_ascii_digit()) { t = t + 16; }
    if (z.is_ascii_lower() && A.is_ascii_upper()) { t = t + 32; }
    if (A.is_ascii_alpha() && A.is_ascii_letter() && d.is_ascii_alnum()) { t = t + 1; }
    if (!sp.is_ascii_alnum() && !sp.is_ascii_alpha()) { t = t + 2; }
    if (f.is_ascii_hex_digit() && d.is_ascii_hex_digit() && !g.is_ascii_hex_digit()) { t = t + 4; }
    if ((66 as u8).to_ascii_lower() as i32 == 98) { t = t + 8; }
    let s: string = "Q";
    if (s[0].is_ascii_upper() && s[0].to_ascii_lower() as i32 == 113) { t = t + 16; }
    return t;
}
`, 94},
	{"ascii-to-string", `import "std/i32";
function main(): i32 {
    let c: u8 = 65;
    let s: string = c.to_ascii_string();
    let t: i32 = 0;
    if (s[0] as i32 == 65) { t = t + 1; }
    if (s.len() == 1) { t = t + 2; }
    if ((66 as u8).to_ascii_string()[0] as i32 == 66) { t = t + 4; }
    if (c.to_ascii_lower().to_ascii_string()[0] as i32 == 97) { t = t + 8; }
    return t;
}
`, 15},
	{"cell-param", `
function bump(c: Cell[i32]): void { c.set(c.get() + 1); }
function slen(c: Cell[string]): i32 { return c.get().len(); }
function main(): i32 {
    let c: Cell[i32] = cell_new(10);
    bump(c);
    bump(c);
    let s: Cell[string] = cell_new("abc");
    s.set("de");
    return c.get() + slen(s) + s.get().len();
}
`, 16},
	{"iife-match-nested-result-payload", `
function g(o: Option[Result[i32, i32]]): i32 {
    return match (o) { Some(r) => 7, None => 0 - 1 };
}
function h(o: Option[Result[i32, i32]]): i32 {
    match (o) { Some(r) => { match (r) { Ok(n) => { return n + 100; }, Err(e) => { return e; } } }, None => { return 0; } }
}
function main(): i32 { return g(Some(Ok(5))) + h(Some(Ok(5))); }
`, 112},
	{"iife-match-nested-pattern", `
function g(o: Option[Result[i32, i32]]): i32 {
    return match (o) {
        Some(Ok(n)) => n + 100,
        Some(Err(e)) => e,
        None => 0 - 1,
    };
}
function main(): i32 { return g(Some(Ok(5))) + g(Some(Err(2))) + g(None); }
`, 106},
	{"generic-array-return-unannotated", `
struct P { k: i32 }
enum C { Red, Blue }
function idf[T](arr: T[]): T[] { return arr; }
function sort_by_key[T](arr: T[], key: (T) => i32): T[] {
    let out: T[] = arr;
    let i: i32 = 1;
    while (i < out.len()) {
        let j: i32 = i;
        while (j > 0 && key(out[j]) < key(out[j - 1])) {
            let tv: T = out[j];
            out = out.with(j, out[j - 1]);
            out = out.with(j - 1, tv);
            j = j - 1;
        }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let ps: P[] = [P { k: 3 }, P { k: 1 }, P { k: 2 }];
    let s = sort_by_key(ps, (p: P): i32 => { return p.k; });
    let t: i32 = s[0].k * 10 + s[2].k;          // 1*10 + 3
    let qs: P[] = [P { k: 7 }];
    let d = idf(qs);
    t = t + d[0].k;                              // + 7
    let cs: C[] = [Blue, Red];
    let e = idf(cs);
    match (e[0]) { Red => { t = t + 1; }, Blue => { t = t + 2; } }
    return t;
}
`, 22},
	{"direct-iife", `
struct P { n: i32 }
function g(n: i32): i32 { return n * 2; }
function ret(): i32 { return ((): i32 => { return 7; })(); }
function main(): i32 {
    let t: i32 = ret();                                              // 7
    let n: i32 = 5;
    t = t + ((): i32 => { return n + 2; })();                   // +7
    t = t + ((): string => { return "ab" + "cd"; })().len();    // +4
    t = t + ((): P => { return P { n: 6 }; })().n;              // +6
    t = t + ((): i32 => { return ((): i32 => { return 3; })() + 4; })(); // +7
    let i: i32 = 0;
    while (i < 3) { t = t + ((): i32 => { return g(i); })(); i = i + 1; }      // +6
    return t;
}
`, 37},
	{"iife-if-match-expression", `
function main(): i32 {
    let a: i32 = if (3 > 2) { 5 } else { 1 };
    let b: i32 = match (a) { 5 => { 20 }, _ => { 0 } };
    return a + b;
}
`, 25},
	{"arr-first-last-user-method", `
struct Box { n: i32 }
function (b: Box) first(): i32 { return b.n + 1; }
function (b: Box) last(): string { return "zz"; }
function main(): i32 {
    let b: Box = Box { n: 4 };
    return b.first() + b.last().len();
}
`, 7},
	{"arr-user-method-option-inline", `
function (arr: i32[]) pick(): Option[i32] {
    if (arr.len() == 0) { return None; }
    return Some(arr[0]);
}
function (arr: string[]) tail_str(): Option[string] {
    if (arr.len() < 2) { return None; }
    return Some(arr[arr.len() - 1]);
}
function main(): i32 {
    let xs: i32[] = [12, 18];
    let t: i32 = 0;
    match (xs.pick()) { Some(g) => { t = g; }, None => { t = 99; } }
    let bound: Option[i32] = xs.pick();
    match (bound) { Some(g) => { t = t + g; }, None => { t = t + 99; } }
    let empty: i32[] = [];
    match (empty.pick()) { Some(g) => { t = t + g; }, None => { t = t + 1; } }
    let ss: string[] = ["a", "bcd"];
    match (ss.tail_str()) { Some(v) => { t = t + v.len(); }, None => { t = t + 50; } }
    return t;                                    // 12 + 12 + 1 + 3
}
`, 28},
	{"cell-unannotated", `
function main(): i32 {
    let ci = cell_new(7);
    ci.set(ci.get() + 3);
    let cs = cell_new("ab");
    cs.set(cs.get() + "cd");
    let cf = cell_new(1.5);
    cf.set(cf.get() + 0.5);
    let acc: i32 = ci.get() + cs.get().len();
    if (cf.get() == 2.0) { acc = acc + 10; }
    return acc;                                  // 10 + 4 + 10
}
`, 24},
	{"wide-cast-to-string", `import "std/i64";
import "std/u64";
function main(): i32 {
    let a: i32 = (1234567890123 as i64).to_string().len();      // 13
    let b: i32 = (42 as i64).to_string().len();                 //  2
    let c: i32 = (18446744073709551615 as u64).to_string().len();// 20
    let v: i64 = 1234567890123 as i64;
    let d: i32 = v.to_string().len();                           // 13
    return a + b + c + d;
}
`, 48},
	{"tuple-annotation-from-call", `
function unwrap_or_i(o: Option[i32], d: i32): i32 { match (o) { Some(v) => { return v; }, None => { return d; } } }
function unwrap_or_s(o: Option[string], d: string): string { match (o) { Some(v) => { return v; }, None => { return d; } } }
function split_pair(t: (i32, string)): (Option[i32], Option[string]) { return (Some(t.0), Some(t.1)); }
function main(): i32 {
    let sa: (Option[i32], Option[string]) = split_pair((7, "hi"));
    return unwrap_or_i(sa.0, 0) + unwrap_or_s(sa.1, "").len();   // 7 + 2
}
`, 9},
	{"nested-for", `function main(): i32 {
    let hyper: i32[][][][] = [[[[1]], [[2, 3]]]];
    let sum = 0;
    for cube in hyper { for plane in cube { for row in plane { for v in row { sum = sum + v; } } } }
    return sum;
}
`, 6},
	{"iife-value-block", `enum W { Wide(i64[]), Empty }
function main(): i32 {
    let w: W = Wide([5i64, 6i64]);
    let u: i64 = (match (w) { Wide(xs) => xs[0], Empty => 9i64 });
    return (u as i32) & 255i32;
}
`, 5},
}

func runDriver(t *testing.T, runner []string, bin string, src []byte, strict bool, args ...string) ([]byte, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		a := append([]string{}, runner[1:]...)
		a = append(a, bin)
		a = append(a, args...)
		cmd = exec.Command(runner[0], a...)
	}
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_STRICT_IR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if strict {
		cmd.Env = append(cmd.Env, "FERN_STRICT_IR=1")
	}
	_ = cmd.Run()
	return stdout.Bytes(), stderr.String(), cmd.ProcessState.ExitCode()
}

func strictIRDriver(t *testing.T) (string, []string, string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	return gcc, runner, buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
}

func strictExit(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("CLI did not run: %v", err)
	}
	return exit.ExitCode()
}

func TestSelfHostStrictIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			on := cli.emit(t, "x86-64-linux", tc.src)
			if code, _ := cli.runX86(t, on); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrictIRRefusesBail(t *testing.T) {
	cli := newStrictCLI(t)
	tc := strictIRBailReasons[0]
	_, diags, err := cli.tryEmit(t, "x86-64-linux", tc.src)
	if code := strictExit(t, err); code != 3 {
		t.Fatalf("exited %d, want a refusal (3)\n%s", code, diags)
	}
	if !strings.Contains(diags, "FERN_SEM_IR: "+tc.fn+": ") || !strings.Contains(diags, "FERN_SEM_IR: the typed lowering refused") {
		t.Errorf("refusal did not name the refused function:\n%s", diags)
	}
}

var strictIRBailReasons = []struct {
	name   string
	src    string
	fn     string
	reason string
}{
	{"template-bound-to-a-view", `pub function first[T](f: () => T): T {
    let xs: T[] = [f()];
    return xs[0];
}

function main(): i32 {
    let b: string = "abcdefgh";
    print(first((): str => slice_unchecked(b, 2, 5)));
    return 0;
}
`, "first$str", "a view is lent, never retained"},
}

func TestSelfHostStrictIRNamesBailReason(t *testing.T) {
	cli := newStrictCLI(t)
	langBin := buildLangBinForInterp(t)

	seen := map[string]string{}
	for _, tc := range strictIRBailReasons {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := nativeCheck(t, langBin, tc.src); err != nil {
				t.Fatalf("the native compiler rejects this fixture, so its refusal says nothing about the typed lowering: %v\n%s", err, out)
			}
			_, diags, err := cli.tryEmit(t, "x86-64-linux", tc.src)
			if code := strictExit(t, err); code != 3 {
				t.Fatalf("exited %d, want a refusal (3)\n%s", code, diags)
			}
			if !strings.Contains(diags, "FERN_SEM_IR: "+tc.fn+": ") {
				t.Errorf("refusal did not name %q as the refused function:\n%s", tc.fn, diags)
			}
			if !strings.Contains(diags, tc.reason) {
				t.Errorf("refusal did not carry the reason %q:\n%s", tc.reason, diags)
			}
		})
		if prev, dup := seen[tc.reason]; dup {
			t.Errorf("%s and %s expect the same reason %q — a reason that does not "+
				"distinguish two different refusals is no better than the bare function name",
				prev, tc.name, tc.reason)
		}
		seen[tc.reason] = tc.name
	}
}

func nativeCheck(t *testing.T, langBin, src string) ([]byte, error) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "fixture.fern")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return exec.Command(langBin, "-check", f).CombinedOutput()
}

func TestSelfHostStrictIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("strict wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStrictIRNamesUnresolvedFunctionValue(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "airun")

	const apply = "function apply(f: (i32) => i32, x: i32): i32 { return f(x); }\n" +
		"function main(): i32 { return apply(bfoo, 6); }\n"

	t.Run("missing", func(t *testing.T) {
		out, stderr, code := runDriver(t, runner, driverBin, []byte(apply), true)
		if code != 3 {
			t.Fatalf("driver exited %d with %d bytes, want a refusal (3)\n%s", code, len(out), stderr)
		}
		if !strings.Contains(stderr, "FERN_SEM_IR: main: ") {
			t.Errorf("refusal did not name main as the bailing function:\n%s", stderr)
		}
		if !strings.Contains(stderr, "unbound name is not a semantic value: bfoo") {
			t.Errorf("refusal did not name the offending function value:\n%s", stderr)
		}
	})

	t.Run("defined", func(t *testing.T) {
		src := "function bfoo(x: i32): i32 { return x * 7; }\n" + apply
		out, stderr, code := runDriver(t, runner, driverBin, []byte(src), true)
		if code != 0 || len(out) == 0 {
			t.Fatalf("the same program with bfoo defined must emit, else the refusal above "+
				"is about the call shape rather than the missing definition: exited %d with %d bytes\n%s",
				code, len(out), stderr)
		}
	})
}
