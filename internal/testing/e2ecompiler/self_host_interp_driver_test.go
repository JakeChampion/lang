package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const interpDriverMod = "import \"std/io\";\n" +
	"import \"./lexer\";\n" +
	"import \"./parser\";\n" +
	"import \"./interp\";\n" +
	"function main(): i32 {\n" +
	"    let src: string = \"\";\n" +
	"    match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }\n" +
	"    let mod: parser.Module = parser.parse_module(lexer.tokenize(src));\n" +
	"    let (is_status, status) = interp.exit_status(interp.eval_module(mod));\n" +
	"    if (is_status) { return status; }\n" +
	"    return 254;\n" +
	"}\n"

var interpProgs = []struct {
	name string
	src  string
	exit int
}{
	{"raw-filesystem-byte-results", `function main(): i32 {
  let Ok(root) = temp_dir("fern-byte-results") else { return 1; };
  defer { remove_dir_all(root); }
  let raw = string_from_bytes_unchecked([255u8, 254u8]);
  let Ok(_) = write_file(root + "/" + raw, "x") else { return 2; };
  match (read_dir(root)) { Err(InvalidUtf8(_)) => {}, _ => { return 3; } }
  match (read_dir_bytes(root)) {
    Ok(names) => { if (names.len() != 1 || names[0].len() != 2 || names[0][0] != 255u8 || names[0][1] != 254u8) { return 4; } },
    Err(_) => { return 5; }
  }
  let Ok(d) = open_dir(root) else { return 6; };
  let good: boolean = false;
  match (d.entries_bytes()) {
    Ok(names) => { good = names.len() == 1 && names[0].len() == 2 && names[0][0] == 255u8 && names[0][1] == 254u8; },
    Err(_) => {}
  }
  d.close();
  if (!good) { return 7; }
  let Ok(_) = create_symlink(raw, root + "/link") else { return 8; };
  match (read_link_bytes(root + "/link")) {
    Ok(bytes) => { if (bytes.len() != 2 || bytes[0] != 255u8 || bytes[1] != 254u8) { return 9; } },
    Err(_) => { return 10; }
  }
  return 0;
}`, 0},

	{"return-literal", "function main(): i32 { return 42; }", 42},
	{"arith", "function main(): i32 { return 6 * 7; }", 42},
	{"hex-literal", "function main(): i32 { return 0x1F; }", 31},
	{"hex-arith", "function main(): i32 { return 0x10 + 1; }", 17},
	{"sci-float-exp", "function main(): i32 { if (1e3 == 1000.0) { return 7; } return 0; }", 7},
	{"sci-float-frac", "function main(): i32 { if (1.5e2 == 150.0) { return 7; } return 0; }", 7},
	{"sci-float-neg-exp", "function main(): i32 { let b: f64 = 1e-2; if (b > 0.009 && b < 0.011) { return 7; } return 0; }", 7},
	{"float-lit-overflow-rejected", "function main(): i32 { let x: f64 = 1e309; if (x > 1.0e308) { return 7; } return 0; }", 254},
	{"float-lit-underflow-accepted", "function main(): i32 { let x: f64 = 1e-400; if (x == 0.0) { return 7; } return 0; }", 7},
	{"bool-literal-postfix", "function (b: boolean) to_i(): i32 { if (b) { return 1; } return 0; } function main(): i32 { return true.to_i() + 41; }", 42},
	{"locals", "function main(): i32 { let x: i32 = 10; let y: i32 = 32; return x + y; }", 42},
	{"if", "function main(): i32 { if (5 > 3) { return 1; } return 0; }", 1},
	{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(19, 23); }", 42},
	{"default-one", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }", 42},
	{"default-multi", "function box(w: i32, h: i32 = 2, d: i32 = 3): i32 { return w * 100 + h * 10 + d; } function main(): i32 { return box(1) - 81; }", 42},
	{"float", "function main(): i32 { let f: f64 = 3.5; let g: f64 = 2.5; if (f + g > 5.0) { return 7; } return 0; }", 7},
	{"cast-i64-to-i32", "function main(): i32 { let v: i64 = 9; return v as i32; }", 9},
	{"cast-f64-to-i32", "function main(): i32 { let f: f64 = 3.9; return f as i32; }", 3},
	{"cast-i32-to-f64", "function main(): i32 { let n: i32 = 5; let f: f64 = n as f64; return (f + 0.5) as i32; }", 5},
	{"cast-in-i64-array-sum", "function main(): i32 { let xs: i64[] = [3, 5, 90]; let s: i64 = 0; for v in xs { s = s + v; } return s as i32; }", 98},
	{"asc-array-identity", "function main(): i32 { let a = [3, 4] as i32[]; return a[0] + a[1]; }", 7},
	{"range-sum", "function main(): i32 { let s = 0; for i in 0..5 { s = s + i; } return s; }", 10},
	{"range-continue", "function main(): i32 { let s = 0; for i in 0..10 { if (i % 2 == 1) { continue; } s = s + i; } return s; }", 20},
	{"range-break", "function main(): i32 { let s = 0; for i in 0..100 { if (i == 5) { break; } s = s + i; } return s; }", 10},
	{"range-empty", "function main(): i32 { let c = 7; for i in 5..5 { c = c + 1; } return c; }", 7},
	{"range-nested", "function main(): i32 { let t = 0; for i in 0..3 { for j in 0..3 { t = t + 1; } } return t; }", 9},
	{"range-incl-sum", "function main(): i32 { let s = 0; for i in 0..=5 { s = s + i; } return s; }", 15},
	{"range-incl-continue", "function main(): i32 { let s = 0; for i in 0..=10 { if (i % 2 == 1) { continue; } s = s + i; } return s; }", 30},
	{"range-incl-break", "function main(): i32 { let s = 0; for i in 0..=100 { if (i == 5) { break; } s = s + i; } return s; }", 10},
	{"range-incl-single", "function main(): i32 { let c = 7; for i in 5..=5 { c = c + 1; } return c; }", 8},
	{"range-incl-reversed", "function main(): i32 { let c = 7; for i in 9..=3 { c = c + 1; } return c; }", 7},
	{"range-incl-i32-max", "function main(): i32 { let c = 0; for i in 2147483646..=2147483647 { c = c + 1; } return c; }", 2},
	{"range-incl-i32-max-last", "function main(): i32 { let t: i32 = 2147483647; let last = 0; for i in 2147483640..=t { last = i; } if (last == t) { return 7; } return 0; }", 7},
	{"range-incl-i32-min-single", "function main(): i32 { let lo: i32 = -2147483647 - 1; let c = 0; for i in lo..=lo { c = c + 1; } return c; }", 1},
	{"range-incl-i32-max-continue", "function main(): i32 { let c = 0; for i in 2147483640..=2147483647 { if (i < 2147483646) { continue; } c = c + 1; } return c; }", 2},
	{"range-incl-i32-max-break", "function main(): i32 { let c = 0; for i in 2147483645..=2147483647 { c = c + 1; if (i == 2147483646) { break; } } return c; }", 2},
	{"defer-binding-in-a-conditional", "function observe(on: boolean): i32 { let seen = 1; loop { if (on) { let items: i32[] = [2]; defer seen = items[0]; items = [9]; } break; } return seen; }\nfunction main(): i32 { return observe(true) * 10 + observe(false); }", 91},
	{"defer-binding-in-a-loop-body", "function main(): i32 { let seen = 0; let i = 0; while (i < 3) { let k: i32[] = [i]; defer seen = seen + k[0]; i = i + 1; } return seen; }", 3},
	{"range-in-lambda", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { return run((): i32 => { let s = 0; for i in 0..4 { s = s + i; } return s; }); }", 6},
	{"range-in-lambda-var", "function main(): i32 { let f = (): i32 => { let s = 0; for i in 0..4 { s = s + i; } return s; }; return f(); }", 6},
	{"range-in-lambda-nested", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { return run((): i32 => { return run((): i32 => { let s = 0; for i in 0..4 { s = s + i; } return s; }); }); }", 6},
	{"labeled-break-in-lambda", "function main(): i32 {\n" +
		"    let f: () => i32 = (): i32 => {\n" +
		"        let n: i32 = 0;\n" +
		"        outer: while (n < 100) {\n" +
		"            inner: while (true) { n = n + 1; break outer; }\n" +
		"            n = n + 100;\n" +
		"        }\n" +
		"        return n;\n" +
		"    };\n" +
		"    return f() + 41; }", 42},
	{"labeled-continue-in-lambda", "function main(): i32 {\n" +
		"    let f: () => i32 = (): i32 => {\n" +
		"        let n: i32 = 0;\n" +
		"        outer: while (n < 3) {\n" +
		"            n = n + 1;\n" +
		"            inner: while (true) { continue outer; }\n" +
		"        }\n" +
		"        return n;\n" +
		"    };\n" +
		"    return f() + 39; }", 42},
	{"labeled-break-shadowed-in-lambda", "function main(): i32 {\n" +
		"    let acc: i32 = 0;\n" +
		"    outer: while (acc < 1) {\n" +
		"        acc = acc + 1;\n" +
		"        let f: () => i32 = (): i32 => {\n" +
		"            let n: i32 = 0;\n" +
		"            outer: while (n < 100) { n = n + 1; break outer; }\n" +
		"            return n;\n" +
		"        };\n" +
		"        acc = acc + f();\n" +
		"    }\n" +
		"    return acc + 40; }", 42},
	{"defer-in-lambda", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { return run((): i32 => { let n = 0; defer { n = n + 7; } return n; }); }", 0},
	{"defer-in-lambda-runs", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { let out = 0; let r = run((): i32 => { defer { out = out + 7; } return 1; }); return out + r; }", 8},
	{"defer-in-lambda-and-fn", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { let log = 0; defer { log = log * 10; }\n" +
		"    let r = run((): i32 => { defer { log = log + 3; } return 1; });\n" +
		"    if (log != 3) { return 90; } return r; }", 1},
	{"range-in-lambda-match-arm", "enum E { A(i32) }\n" +
		"function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { let e: E = E.A(1); match (e) { E.A(v) => { return run((): i32 => { let s = 0; for i in 0..4 { s = s + i; } return s; }); }, _ => { return 0; } } return 0; }", 6},
	{"range-in-if-branches", "function f(take: boolean): i32 { let s = 0; if (take) { for i in 0..4 { s = s + i; } } else { for j in 1..=3 { s = s + j; } } return s; }\n" +
		"function main(): i32 { return f(true) + f(false); }", 12},
	{"range-in-match-arm", "enum Shape { Run(i32), Pair }\n" +
		"function f(sh: Shape): i32 { let s = 0; match (sh) { Shape.Run(n) => { for i in 0..n { s = s + i; } }, Shape.Pair => { for k in 1..=2 { s = s + 1; } } } return s; }\n" +
		"function main(): i32 { return f(Shape.Run(4)) + f(Shape.Pair); }", 8},
	{"range-in-defer", "function main(): i32 { let t = 0; loop { defer { for k in 0..3 { t = t + 1; } } break; } return t; }", 3},
	{"generic-trait-default-method", "trait Greet[T] { function greet(self: Self): i32 { return 42; } } struct P { x: i32 } impl Greet[i32] for P {} function main(): i32 { let p = P { x: 1 }; return p.greet(); }", 42},
	{"slice-open-low", "function main(): i32 { let a = [10, 20, 30]; let b = a[:2]; return b[0] + b[1]; }", 30},
	{"cfor-expr-init", "function main(): i32 { let i = 0; let s = 0; for (i = 0; i < 4; i = i + 1) { s = s + i; } return s; }", 6},
	{"cfor-empty-init", "function main(): i32 { let i = 0; let s = 0; for (; i < 4; i = i + 1) { s = s + i; } return s; }", 6},
	{"lexer-formfeed-ws", "function main(): i32 {\freturn 42; }", 42},
	{"bit-and", "function main(): i32 { return 5 & 3; }", 1},
	{"bit-or", "function main(): i32 { return 5 | 2; }", 7},
	{"bit-xor", "function main(): i32 { return 6 ^ 3; }", 5},
	{"bit-shl", "function main(): i32 { return 1 << 4; }", 16},
	{"bit-shr", "function main(): i32 { return 256 >> 2; }", 64},
	{"div-by-zero-total", "function main(): i32 { return 7 / 0; }", 0},
	{"mod-by-zero-total", "function main(): i32 { return 7 % 0; }", 7},
	{"fdiv-inf", "function main(): i32 { let x: f64 = 1.0 / 0.0; if (x > 1000000000.0) { return 7; } return 0; }", 7},
	{"fdiv-nan", "function main(): i32 { let y: f64 = 0.0 / 0.0; if (y != y) { return 7; } return 0; }", 7},
	{"and-short-circuit", "function main(): i32 { let a = [1, 2, 3]; let i = 5; if (i < 3 && a[i] == 1) { return 0; } return 42; }", 42},
	{"or-short-circuit", "function main(): i32 { let a = [1, 2, 3]; let i = 5; if (i >= 3 || a[i] == 1) { return 42; } return 0; }", 42},
	{"oob-var-init-not-swallowed", "function main(): i32 { let a = [1, 2, 3]; let x = a[5]; return 7; }", 254},
	{"braceless-if-else", "function main(): i32 { let x = 0; if (x == 0) x = 40; else x = 1; return x; }", 40},
	{"braceless-while", "function main(): i32 { let x = 0; while (x < 42) x = x + 1; return x; }", 42},
	{"braceless-for", "function main(): i32 { let s = 0; for i in 0..5 s = s + i; return s; }", 10},
	{"chained-assign-2", "function main(): i32 { let x = 0; let y = 0; x = y = 20; return x + y; }", 40},
	{"chained-assign-3", "function main(): i32 { let x = 0; let y = 0; let z = 0; x = y = z = 14; return x + y + z; }", 42},
	{"use-monadic-bind", "function apply(n: i32, cb: (i32) => i32): i32 { return cb(n); } function main(): i32 { use r <- apply(41); return r + 1; }", 42},
	{"cast-u8-wrap", "function main(): i32 { let x = 255; return ((x + 1) as u8) as i32; }", 0},
	{"cast-u8-inrange", "function main(): i32 { let x = 200; return (x as u8) as i32; }", 200},

	{"str-len-method", "function main(): i32 { return \"hello\".len() as i32; }", 5},
	{"arr-len-method", "function main(): i32 { let a: i32[] = [1, 2, 3]; return a.len() as i32; }", 3},
	{"arr-len-empty", "function main(): i32 { let a: i32[] = []; return a.len() as i32; }", 0},
	{"arr-append-len", "function main(): i32 { let a: i32[] = [1, 2]; let b: i32[] = a.append(9); return b.len() as i32; }", 3},
	{"arr-append-value", "function main(): i32 { let a: i32[] = [1, 2]; let b: i32[] = a.append(9); return b[2]; }", 9},
	{"arr-append-immutable", "function main(): i32 { let a: i32[] = [1, 2]; let b: i32[] = a.append(9); return a.len() as i32 * 10 + b.len() as i32; }", 23},

	{"const-ref", "const N: i32 = 42; function main(): i32 { return N; }", 42},
	{"const-in-expr", "const N: i32 = 10; function main(): i32 { return N + N * 2; }", 30},
	{"const-two", "const A: i32 = 3; const B: i32 = 4; function main(): i32 { return A * B; }", 12},
	{"const-in-callee", "const K: i32 = 5; function helper(): i32 { return K * 2; } function main(): i32 { return helper(); }", 10},
	{"const-string", "const S: string = \"hello\"; function main(): i32 { let s = S; return s.len() as i32; }", 5},

	{"enum-nullary-match", "enum E { A, B } function main(): i32 { let e = E.B; match (e) { E.A => { return 1; }, E.B => { return 2; } } }", 2},
	{"enum-nullary-first", "enum E { A, B } function main(): i32 { let e = E.A; match (e) { E.A => { return 1; }, E.B => { return 2; } } }", 1},
	{"enum-three-way", "enum Color { Red, Green, Blue } function main(): i32 { let c = Color.Green; match (c) { Color.Red => { return 1; }, Color.Green => { return 2; }, Color.Blue => { return 3; } } }", 2},
	{"enum-wildcard-arm", "enum E { A, B, C } function main(): i32 { let e = E.C; match (e) { E.A => { return 1; }, _ => { return 9; } } }", 9},
	{"enum-return-from-fn", "enum St { On, Off } function flip(s: St): St { match (s) { St.On => { return St.Off; }, St.Off => { return St.On; } } } function main(): i32 { let r = flip(St.On); match (r) { St.On => { return 1; }, St.Off => { return 0; } } }", 0},
	{"enum-in-array", "enum E { A, B } function main(): i32 { let a: E[] = [E.A, E.B, E.A]; let n = 0; for x in a { match (x) { E.A => { n = n + 1; }, E.B => { n = n + 10; } } } return n; }", 12},

	{"enum-payload-bind", "enum E { A(i32), B } function main(): i32 { let e = E.A(7); match (e) { E.A(n) => { return n; }, E.B => { return 0; } } }", 7},
	{"enum-payload-other-arm", "enum E { A(i32), B } function main(): i32 { let e = E.B; match (e) { E.A(n) => { return n; }, E.B => { return 99; } } }", 99},
	{"enum-option-unwrap", "enum Opt { Some(i32), None } function unwrap(o: Opt): i32 { match (o) { Opt.Some(v) => { return v; }, Opt.None => { return 0; } } } function main(): i32 { return unwrap(Opt.Some(42)) + unwrap(Opt.None); }", 42},
	{"enum-result-err", "enum R { Ok(i32), Err(i32) } function main(): i32 { let r = R.Err(3); match (r) { R.Ok(v) => { return v; }, R.Err(e) => { return 100 + e; } } }", 103},
	{"enum-payload-ignore", "enum E { A(i32), B } function main(): i32 { let e = E.A(5); match (e) { E.A(_) => { return 1; }, E.B => { return 2; } } }", 1},
	{"enum-payload-in-array", "enum E { N(i32) } function main(): i32 { let a: E[] = [E.N(1), E.N(2), E.N(3)]; let s = 0; for x in a { match (x) { E.N(v) => { s = s + v; } } } return s; }", 6},

	{"enum-method-payload", "enum Sh { Circle(i32), Sq(i32) } function (s: Sh) area(): i32 { match (s) { Sh.Circle(r) => { return r * r * 3; }, Sh.Sq(w) => { return w * w; } } } function main(): i32 { let s = Sh.Sq(4); return s.area(); }", 16},
	{"enum-method-nullary", "enum Dir { N, S, E, W } function (d: Dir) dx(): i32 { match (d) { Dir.E => { return 1; }, Dir.W => { return 0 - 1; }, _ => { return 0; } } } function main(): i32 { let d = Dir.E; return d.dx(); }", 1},
	{"enum-method-with-arg", "enum Opt { Some(i32), None } function (o: Opt) unwrap_or(dflt: i32): i32 { match (o) { Opt.Some(x) => { return x; }, Opt.None => { return dflt; } } } function main(): i32 { return Opt.Some(7).unwrap_or(0) + Opt.None.unwrap_or(5); }", 12},

	{"builtin-ioerror-method", "function (e: IoError) count(): i32 { match (e) { Interrupted => { return 7; }, _ => { return 0; } } } function main(): i32 { return Interrupted.count(); }", 7},
	{"builtin-variant-shadow-method", "enum Own { BodyBytes(i32) } function (b: Body) count(): i32 { return 99; } function (b: Own) count(): i32 { match (b) { BodyBytes(n) => { return n; } } } function main(): i32 { return Own.BodyBytes(4).count(); }", 4},

	{"i64-literal-div", "function main(): i32 { let x: i64 = 5000000000; return (x / 1000000000) as i32; }", 5},
	{"i64-mul-fits-operands", "function main(): i32 { let x: i64 = 100000; let y: i64 = x * 100000; return (y / 1000000000) as i32; }", 10},
	{"i64-accumulator", "function main(): i32 { let s: i64 = 0; let i: i32 = 0; while (i < 5) { s = s + 1000000000; i = i + 1; } return (s / 1000000000) as i32; }", 5},
	{"i64-negative-div", "function main(): i32 { let x: i64 = 8000000000; return (x / 1000000000) as i32; }", 8},
	{"i64-mod", "function main(): i32 { let x: i64 = 5000000007; return (x % 1000000000) as i32; }", 7},
	{"i64-param-promote", "function scale(n: i64): i64 { return n * 1000000; } function main(): i32 { return (scale(5000) / 1000000000) as i32; }", 5},
	{"i64-array-sum", "function main(): i32 { let xs: i64[] = [5000000000, 3000000000]; let s: i64 = 0; for v in xs { s = s + v; } return (s / 1000000000) as i32; }", 8},
	{"i64-compare", "function main(): i32 { let x: i64 = 5000000000; if (x > 4000000000) { return 1; } return 0; }", 1},
	{"i32-overflow-unchanged", "function main(): i32 { let x: i32 = 100000; return x * 100000; }", 0},
	{"u32-shr-logical", "function main(): i32 { let x: u32 = 4294967288 as u32; let y = x >> 2; if (y == (1073741822 as u32)) { return 42; } return 7; }", 42},
	{"u32-div-unsigned", "function main(): i32 { let x: u32 = 4294967290 as u32; let y: u32 = 5 as u32; if (x / y > (100 as u32)) { return 42; } return 7; }", 42},
	{"u32-cmp-unsigned", "function main(): i32 { let x: u32 = 4294967290 as u32; if (x > (5 as u32)) { return 42; } return 7; }", 42},
	{"f32-round-2p24", "function main(): i32 { let a: f32 = 16777216.0 as f32; let b = a + (1.0 as f32); if ((b as f64) > 16777215.5 && (b as f64) < 16777216.5) { return 42; } return 7; }", 42},
	{"f32-sticky-chain", "function main(): i32 { let a: f32 = 16777216.0; let b = a + 1.5; let c = b + (1.0 as f32); if ((c as f64) == 16777220.0) { return 42; } return 7; }", 42},
	{"defer-scope-exit", "function main(): i32 { let r = 2; defer { r = 101; } if (r == 2) { return 2; } return r; }", 2},
	{"defer-not-eager-mid-body", "function main(): i32 { let r = 1; defer { r = 100; } let x = r + 1; if (x == 2) { return 2; } return 3; }", 2},
	{"labeled-break-two-level", "function main(): i32 { let s = 0; outer: while (true) { let i = 0; while (i < 10) { s = s + 1; if (s >= 11) { break outer; } i = i + 1; } } return s; }", 11},
	{"labeled-continue-two-level", "function main(): i32 { let s = 0; outer: for i in [1, 2, 3] { let j = 0; while (j < 5) { s = s + 1; if (j == 1) { continue outer; } j = j + 1; } } return s; }", 6},
	{"unlabeled-break-innermost", "function main(): i32 { let s = 0; let i = 0; while (i < 3) { let j = 0; while (j < 10) { if (j == 2) { break; } s = s + 1; j = j + 1; } i = i + 1; } return s; }", 6},
	{"i32-wrap-add-negative", "function main(): i32 { let a: i32 = 2147483647; if ((a + 1) < 0) { return 7; } return 5; }", 7},
	{"i32-wrap-add-eq-min", "function main(): i32 { let a: i32 = 2147483647; if ((a + 1) == (0 - 2147483647 - 1)) { return 7; } return 5; }", 7},
	{"i32-wrap-add-via-locals", "function main(): i32 { let a: i32 = 2147483647; let b: i32 = a + 1; let c: i32 = 0 - 2147483647 - 1; if (b == c) { return 7; } return 5; }", 7},
	{"i32-wrap-add-literals", "function main(): i32 { if ((2147483647 + 1) == (0 - 2147483647 - 1)) { return 7; } return 5; }", 7},
	{"i32-wrap-add-not-greater", "function main(): i32 { let a: i32 = 2147483647; if ((a + 1) > a) { return 5; } return 7; }", 7},
	{"i32-wrap-sub", "function main(): i32 { let a: i32 = 0 - 2147483647 - 1; if ((a - 1) == 2147483647) { return 7; } return 5; }", 7},
	{"i32-wrap-mul", "function main(): i32 { let a: i32 = 100000; if ((a * 100000) == 1410065408) { return 7; } return 5; }", 7},
	{"i32-wrap-shl", "function main(): i32 { let a: i32 = 1; if ((a << 31) == (0 - 2147483647 - 1)) { return 7; } return 5; }", 7},
	{"sat-add-hi", "function main(): i32 { let a: i32 = 2147483647; if ((a +| 1) == a) { return 7; } return 0; }", 7},
	{"sat-sub-lo", "function main(): i32 { let a: i32 = 0 - 2147483647 - 1; if ((a -| 1) == a) { return 7; } return 0; }", 7},
	{"sat-mul-hi", "function main(): i32 { if ((100000 *| 100000) == 2147483647) { return 7; } return 0; }", 7},
	{"sat-mul-lo", "function main(): i32 { if (((0 - 100000) *| 100000) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	{"sat-plain", "function main(): i32 { return 40 +| 2; }", 42},
	{"sat-shl-hi", "function main(): i32 { let a: i32 = 1; if ((a <<| 31) == 2147483647) { return 7; } return 0; }", 7},
	{"sat-shl-lo", "function main(): i32 { let a: i32 = 0 - 2; if ((a <<| 31) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	{"sat-shl-exact-min", "function main(): i32 { let a: i32 = 0 - 1; if ((a <<| 31) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	{"sat-shl-plain", "function main(): i32 { return 21 <<| 1; }", 42},
	{"sat-shl-oor", "function main(): i32 { let a: i32 = 42; if ((a <<| 32) == 2147483647) { return 7; } return 0; }", 7},
	{"sat-shl-oor-zero", "function main(): i32 { let a: i32 = 0; if ((a <<| 40) == 0) { return 7; } return 0; }", 7},
	{"sat-shl-neg-count", "function main(): i32 { let a: i32 = 0 - 3; if ((a <<| (0 - 1)) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	{"slice-unchecked-builtin", "function main(): i32 { let s: string = \"hello world\"; let n: i32 = s.len(); return slice_unchecked(s, 6, n).len(); }", 5},
	{"slice-unchecked-oob", "function main(): i32 { let s: string = \"hi\"; return slice_unchecked(s, 0, 3).len(); }", 254},
}

func interpDriverFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{"main.fern": interpDriverMod}
	stdDir := writeSourceModloadProject(t, "import \"std/io\";\nfunction main(): i32 { return 0; }\n")
	stdMods, err := filepath.Glob(filepath.Join(stdDir, "*.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range stdMods {
		base := filepath.Base(p)
		if base == "main.fern" || base == "builtins.fern" {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		files[base] = string(src)
	}
	for _, root := range []string{"lexer.fern", "parser.fern", "interp.fern"} {
		for _, p := range selfHostImportClosure(t, "../../../compiler", root) {
			base := filepath.Base(p)
			if _, ok := files[base]; ok {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			files[base] = string(src)
		}
	}
	return files
}

func TestSelfHostInterpDriverX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	files := interpDriverFiles(t)
	interpAsm, progDir := compileFilesModload(t, runner, driverBin, files)
	if len(interpAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the interp driver")
	}
	interpBin := buildBin(t, gcc, progDir, "interp", interpAsm)

	for _, tc := range interpProgs {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(interpBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], interpBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("interp(%q) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostInterpDriverArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	files := interpDriverFiles(t)
	interpAsm, progDir := compileFilesModload(t, x86runner, driverBin, files, "-target", "arm64-linux")
	interpBin := buildBin(t, arm64gcc, progDir, "interp", interpAsm)

	for _, tc := range interpProgs {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runArm64Bin(qemu, interpBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("interp(%q) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
