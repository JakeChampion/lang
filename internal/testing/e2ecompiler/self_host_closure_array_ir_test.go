package e2ecompiler

import "testing"

var closureArrayIRCases = []struct {
	name string
	main string
	want int
}{
	{"loop-sum", `function main(): i32 { let n = 3; let fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2]; let s = 0; let i = 0; while (i < 3) { s = s + fns[i](); i = i + 1; } return s; }`, 13},
	{"index-const", `function main(): i32 { let n = 3; let fns: (() => i32)[] = [() => n, () => n + 1]; return fns[1](); }`, 4},
	{"two-arg-cap", `function main(): i32 { let k = 2; let fns: ((i32) => i32)[] = [(a: i32) => a + k, (a: i32) => a * k]; return fns[0](5) + fns[1](5); }`, 17},
	{"single-cap", `function main(): i32 { let n = 9; let fns: (() => i32)[] = [() => n]; return fns[0](); }`, 9},
	{"append-empty", `function main(): i32 { let n = 4; let fns: (() => i32)[] = []; fns = fns.append(() => n); return fns[0](); }`, 4},
	{"append-two", `function main(): i32 { let a = 10; let b = 20; let fns: (() => i32)[] = []; fns = fns.append(() => a); fns = fns.append(() => b); return fns[0]() + fns[1](); }`, 30},
	{"append-after-literal", `function main(): i32 { let n = 4; let fns: (() => i32)[] = [() => n]; fns = fns.append(() => n + 1); return fns[1](); }`, 5},
	{"append-loop", `function main(): i32 { let a = 1; let b = 2; let c = 3; let fns: (() => i32)[] = []; fns = fns.append(() => a); fns = fns.append(() => b); fns = fns.append(() => c); let s = 0; for f in fns { s = s + f(); } return s; }`, 6},
	{"escape-append-capture", `function make(): (() => i32)[] { let fns: (() => i32)[] = []; let n = 5; fns = fns.append(() => n); return fns; } function main(): i32 { let fns = make(); return fns[0](); }`, 5},
	{"escape-append-noncap", `function make(): ((i32) => i32)[] { let fns: ((i32) => i32)[] = []; fns = fns.append((x: i32) => x + 5); return fns; } function main(): i32 { let fns = make(); return fns[0](10); }`, 15},
	{"escape-loop-capture", `function adders(n: i32): ((i32) => i32)[] { let fs: ((i32) => i32)[] = []; let i = 0; while (i < n) { let k = i; fs = fs.append((x: i32) => x + k); i = i + 1; } return fs; } function main(): i32 { let fs = adders(3); let t = 0; for f in fs { t = t + f(10); } return t; }`, 33},
	{"namedfn-append-empty", `function f(): i32 { return 7; } function main(): i32 { let fns: (() => i32)[] = []; fns = fns.append(f); return fns[0](); }`, 7},
	{"namedfn-append-two", `function f(): i32 { return 7; } function g(): i32 { return 5; } function main(): i32 { let fns: (() => i32)[] = []; fns = fns.append(f); fns = fns.append(g); return fns[0]() + fns[1](); }`, 12},
	{"namedfn-append-after-literal", `function f(): i32 { return 7; } function g(): i32 { return 5; } function main(): i32 { let fns: (() => i32)[] = [f]; fns = fns.append(g); return fns[1](); }`, 5},
	{"namedfn-append-loop", `function a(): i32 { return 1; } function b(): i32 { return 2; } function c(): i32 { return 4; } function main(): i32 { let fns: (() => i32)[] = []; fns = fns.append(a); fns = fns.append(b); fns = fns.append(c); let s = 0; for f in fns { s = s + f(); } return s; }`, 7},
	{"mixed-cap-then-noncap", `function main(): i32 { let base = 2; let fs: ((i32) => i32)[] = [(x: i32) => x + base, (x: i32) => x * x]; return fs[1](5); }`, 25},
	{"mixed-noncap-then-cap", `function main(): i32 { let base = 2; let fs: ((i32) => i32)[] = [(x: i32) => x * x, (x: i32) => x + base]; return fs[1](5); }`, 7},
	{"mixed-loop", `function main(): i32 { let base = 2; let fs: ((i32) => i32)[] = [(x: i32) => x + base, (x: i32) => x * x]; let s = 0; let i = 0; while (i < fs.len()) { s = s + fs[i](5); i = i + 1; } return s; }`, 32},
	{"mixed-append-noncap", `function main(): i32 { let base = 2; let fs: ((i32) => i32)[] = [(x: i32) => x + base]; fs = fs.append((y: i32) => y * y); return fs[1](5); }`, 25},
	{"mixed-namedfn-then-cap", `function inc(x: i32): i32 { return x + 100; } function main(): i32 { let a = 3; let fs: ((i32) => i32)[] = [inc, (x: i32) => x + a]; return fs[1](5); }`, 8},
	{"mixed-cap-then-namedfn", `function inc(x: i32): i32 { return x + 100; } function main(): i32 { let a = 3; let fs: ((i32) => i32)[] = [(x: i32) => x + a, inc]; return fs[1](5); }`, 105},
	{"mixed-namedfn-loop", `function inc(x: i32): i32 { return x + 10; } function main(): i32 { let a = 3; let fs: ((i32) => i32)[] = [inc, (x: i32) => x + a]; let s = 0; let i = 0; while (i < fs.len()) { s = s + fs[i](5); i = i + 1; } return s; }`, 23},
	{"param-enum-match-scrutinee", `function get(fs: ((i32) => i32)[], v: i32): Option[i32] { let f = fs[0]; return Some(f(v)); } function main(): i32 { let k = 10; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; match (get(fs, 4)) { Some(v) => { return v; }, None => { return 0; } } }`, 14},
	{"param-scalar-match-scrutinee", `function get(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function main(): i32 { let k = 10; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; match (get(fs, 4)) { 14 => { return 100; }, _ => { return 99; } } }`, 100},
	{"param-if-condition", `function get(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function main(): i32 { let k = 10; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; if (get(fs, 4) == 14) { return 100; } return 99; }`, 100},
	{"param-call-argument", `function id(x: i32): i32 { return x; } function get(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function main(): i32 { let k = 10; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; return id(get(fs, 4)); }`, 14},
	{"param-namedfn-plain-in-match", `function inc(x: i32): i32 { return x + 1; } function apply(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function main(): i32 { let fs: ((i32) => i32)[] = [inc]; match (apply(fs, 5)) { 6 => { return 100; }, _ => { return 99; } } }`, 100},
	{"param-forward-one-hop", `function apply(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function chain(fs: ((i32) => i32)[]): i32 { return apply(fs, 4); } function main(): i32 { let k = 2; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; return chain(fs); }`, 6},
	{"param-forward-two-hops", `function apply(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function mid(fs: ((i32) => i32)[]): i32 { return apply(fs, 4); } function outer(fs: ((i32) => i32)[]): i32 { return mid(fs); } function main(): i32 { let k = 2; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; return outer(fs); }`, 6},
	{"param-forward-and-dispatch", `function apply(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function chain(fs: ((i32) => i32)[]): i32 { let a = fs[0](1); return a + apply(fs, 4); } function main(): i32 { let k = 2; let fs: ((i32) => i32)[] = [(x: i32) => x + k]; return chain(fs); }`, 9},
	{"param-forward-namedfn-plain", `function inc(x: i32): i32 { return x + 1; } function apply(fs: ((i32) => i32)[], v: i32): i32 { let f = fs[0]; return f(v); } function chain(fs: ((i32) => i32)[]): i32 { return apply(fs, 5); } function main(): i32 { let fs: ((i32) => i32)[] = [inc]; return chain(fs); }`, 6},
	{"local-elem-scalar-cap", `function main(): i32 { let n = 42; let f: () => i32 = (): i32 => { return n; }; let fs: (() => i32)[] = [f]; return fs[0](); }`, 42},
	{"local-elem-array-cap", `function main(): i32 { let a: i32[] = [42, 1]; let f: () => i32 = (): i32 => { return a[0]; }; let fs: (() => i32)[] = [f]; return fs[0](); }`, 42},
	{"local-elem-both-names", `function main(): i32 { let n = 20; let f: () => i32 = (): i32 => { return n; }; let fs: (() => i32)[] = [f]; let x = f(); return x + fs[0]() + 2; }`, 42},
	{"local-elem-two", `function main(): i32 { let n = 20; let f: () => i32 = (): i32 => { return n; }; let g: () => i32 = (): i32 => { return n + 1; }; let fs: (() => i32)[] = [f, g]; return fs[0]() + fs[1]() + 1; }`, 42},
	{"local-elem-namedfn", `function h(): i32 { return 42; } function main(): i32 { let fs: (() => i32)[] = [h]; return fs[0](); }`, 42},

	{"iife-cond-closure-arg", `function g(fs: ((i32) => i32)[], n: i64): boolean { return fs[0i32](1i32) > n as i32; }
function main(): i32 { let v2: i32 = 5i32; return (if (g([((x: i32) => (x + v2))], 1i64)) { 9i32 } else { 1i32 }); }`, 9},
	{"stmt-cond-closure-arg", `function g(fs: ((i32) => i32)[], n: i64): boolean { return fs[0i32](1i32) > n as i32; }
function main(): i32 { let v2: i32 = 5i32; if (g([((x: i32) => (x + v2))], 1i64)) { return 9i32; } return 1i32; }`, 9},
	{"passthrough-iife-elem-mixed", `function id[T](x: T): T { return x; }
function main(): i32 { let fs: ((i32) => i32)[] = [id((if (false) { ((x: i32) => 1i32) } else { ((x: i32) => 2i32) })), ((x: i32) => 40i32)]; return fs[1i32](0i32) + fs[0i32](0i32); }`, 42},
	{"elem-bound-passed-to-fn-param", `function apply(f: (i32) => i32, n: i32): i32 { return f(n); }
function main(): i32 { let fs: ((i32) => i32)[] = [((x: i32) => (x + 2i32))]; let f: (i32) => i32 = fs[0i32]; return apply(f, 40i32); }`, 42},

	{"fnarr-param-two-call-sites", `function apply_all(fs: ((i32) => i32)[]): i32 { let acc: i32 = 0i32; for f in fs { acc = acc + f(1i32); } return acc; }
function main(): i32 { return apply_all([((x: i32) => 1i32), ((y: i32) => apply_all([((z: i32) => (y + 40i32))]))]); }`, 42},
	{"iife-arm-call-fn-argument", `enum Color { Red, Green, Blue }
enum Status { Active, Inactive, Pending }
function gen_f0(p0: Color, p1: Status, p2: (i32, i64), p3: (i32) => i32): i32 { return p3(2i32); }
function main(): i32 {
  let xs: i32[] = [(0i32 ^ (if (false) { 109i32 } else { gen_f0(Blue, Active, (733i32, 747i64), ((x: i32) => (x + 40i32))) }))];
  return xs[0i32];
}`, 42},
	{"fnarr-param-single-site-no-capture", `function apply_all(fs: ((i32) => i32)[]): i32 { let acc: i32 = 0i32; for f in fs { acc = acc + f(1i32); } return acc; }
function main(): i32 { return apply_all([((x: i32) => 1i32), ((y: i32) => 41i32)]); }`, 42},

	{"iife-scrutinee-closure-arg", `function g(fs: ((i32) => i32)[], n: i64): boolean { return fs[0i32](1i32) > n as i32; }
function main(): i32 { let v2: i32 = 5i32; return (match (g([((x: i32) => (x + v2))], 1i64)) { true => 7i32, _ => 1i32 }); }`, 7},
}

func TestSelfHostClosureArrayIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range closureArrayIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
