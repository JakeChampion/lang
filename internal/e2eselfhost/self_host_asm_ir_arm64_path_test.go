package e2eselfhost

import (
	"testing"
)

// TestSelfHostAsmIRArm64Path is the arm64 sibling of TestSelfHostAsmIRPath: the
// same kind of program compiled through the self-hosted CLI (`fern.fern`, built
// for the x86-64 host) with `-target arm64-linux -emit asm`, assembled with
// the aarch64 toolchain and run under
// qemu-aarch64. Each case pins its exit code: the native interpreter's
// answer, or 134 for the two slice traps it reports as an error instead.
func TestSelfHostAsmIRArm64Path(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	emitAndRun := func(t *testing.T, src string) int {
		t.Helper()
		code, _ := runArm64(t, arm64gcc, qemu, cli.emit(t, "arm64-linux", src))
		return code
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Pure i32 (single + multi-function, recursion, control flow).
		{"const", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { return 2 + 3 * 4; }`, 14},
		{"locals", `function main(): i32 { let x = 2 + 3 * 4; let y = x - 5; return y * 2; }`, 18},
		{"reassign", `function main(): i32 { let x = 5; x = x + 3; return x; }`, 8},
		// Top-level `const` references lower to a call (the const's value) (#2954).
		{"const-ref", `const LIMIT: i32 = 100; function main(): i32 { return LIMIT + 1; }`, 101},
		{"const-loop-bound", `const N: i32 = 5; function main(): i32 { let s = 0; let i = 0; while (i < N) { s = s + i; i = i + 1; } return s; }`, 10},
		// Bare reference to a module function WITH params is a function VALUE
		// (const_func + the existing call_indirect path), no longer bailing.
		{"fnval-local", `function dbl(n: i32): i32 { return n * 2; } function main(): i32 { let f = dbl; return f(21); }`, 42},
		{"fnval-local-arg", `function dbl(n: i32): i32 { return n * 2; } function apply(f: (i32) => i32, n: i32): i32 { return f(n); } function main(): i32 { let g = dbl; return apply(g, 21); }`, 42},
		{"fnval-two", `function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { let f = inc; let g = dbl; return f(10) + g(10); }`, 31},
		{"fnval-return", `function dbl(n: i32): i32 { return n * 2; } function getf(): (i32) => i32 { return dbl; } function main(): i32 { let g = getf(); return g(21); }`, 42},
		// Calling a function-VALUE stored in a struct field (struct_get +
		// call_indirect, not a method dispatch).
		{"fnval-struct-field", `struct H { f: (i32) => i32 } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { let h = H { f: dbl }; return h.f(21); }`, 42},
		{"fnval-struct-field-mixed", `struct H { f: (i32) => i32, n: i32 } function inc(n: i32): i32 { return n + 1; } function main(): i32 { let h = H { f: inc, n: 100 }; return h.f(h.n); }`, 101},
		// Calling an element of a function-value ARRAY inline (`fns[i](args)`):
		// the element is an env box, so the call is env-first, as the local-bind
		// form `let f = fns[i]; f()` is.
		{"fnarr-elem-call", `function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { let fns = [inc, dbl]; return fns[0](10) + fns[1](10); }`, 31},
		{"fnarr-elem-call-loop", `function apply(fns: ((i32) => i32)[], n: i32): i32 { let s = 0; let i = 0; while (i < fns.len()) { s = s + fns[i](n); i = i + 1; } return s; } function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { return apply([inc, dbl], 10); }`, 31},
		{"fnarr-elem-call-2arg", `function add(a: i32, b: i32): i32 { return a + b; } function mul(a: i32, b: i32): i32 { return a * b; } function main(): i32 { let ops = [add, mul]; return ops[0](3, 4) + ops[1](3, 4); }`, 19},
		{"modulo", `function main(): i32 { return 23 % 5; }`, 3},
		{"division", `function main(): i32 { return 84 / 2; }`, 42},
		{"bitwise", `function main(): i32 { return (6 & 3) | 8; }`, 10},
		{"shift", `function main(): i32 { return 1 << 4; }`, 16},
		// Hex literals: lowered via op_const_i32_text (source text spliced into
		// the literal pool `ldr x0, =TEXT`). A decimal-only parse zeroes every
		// `0x..`. Exit codes are mod 256 —
		// shifts/masks expose the high bits.
		{"hex-small", `function main(): i32 { return 0xFF & 0x0F; }`, 15},
		{"hex-shift", `function main(): i32 { return (0x61626380 >> 8) & 255; }`, 99},
		{"hex-mask-high", `function main(): i32 { return (0x12345678 >> 16) & 255; }`, 52},
		// Int→int casts (op_int_cast) — and/sxtb matching asm_arm64. (i8/i16/u16
		// were retired (#4408); u8 is the only sub-word type left, so the
		// sign-extend (sxth/sxtw) cast case that used to live here is gone
		// rather than force-substituted onto a width that no longer exists.)
		{"cast-u8-mask", `function main(): i32 { let x: i32 = 300; return (x as u8) as i32; }`, 44},
		{"cast-chain", `function main(): i32 { let x: i32 = 65; return (x as u8) as i32; }`, 65},
		{"compare", `function main(): i32 { if (5 < 10) { return 1; } return 0; }`, 1},
		{"if-else", `function main(): i32 { let x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }`, 9},
		{"nested-if", `function main(): i32 { let x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }`, 100},
		{"while-sum", `function main(): i32 { let i = 1; let s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }`, 15},
		{"nested-loop", `function main(): i32 { let i = 0; let t = 0; while (i < 3) { let j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }`, 9},
		{"call-args", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }`, 42},
		// Default parameter values (fill_default_args_module in lift_lambdas).
		{"default-one", `function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }`, 42},
		{"default-multi", `function box(w: i32, h: i32 = 2, d: i32 = 3): i32 { return w * 100 + h * 10 + d; } function main(): i32 { return box(1); }`, 123},
		{"factorial", `function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }`, 120},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }`, 21},
		{"mutual", `function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }`, 1},
		// Arrays: literal, index, len, set-index, alias, two arrays.
		{"arr-index", `function main(): i32 { let a = [10, 20, 30]; return a[0] + a[2]; }`, 40},
		{"arr-loop-sum", `function main(): i32 { let a = [5, 10, 15, 20, 25]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 75},
		{"arr-set-fill", `function main(): i32 { let a = [0, 0, 0, 0, 0]; let i = 0; while (i < 5) { a = a.with(i, i * i); i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }`, 30},
		{"arr-len", `function main(): i32 { let a = [1, 2, 3, 4]; return a.len(); }`, 4},
		{"arr-two", `function main(): i32 { let a = [1, 2]; let b = [100, 200]; return a[1] + b[0]; }`, 102},
		{"arr-alias", `function main(): i32 { let a = [10, 20, 30]; let b = a; return b[0] + b[2] + a.len(); }`, 43},
		// Cross-function arrays: borrowed params + array return (move-on-return).
		{"arr-param-sum", `function sum(a: i32[]): i32 { let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { let arr = [10, 20, 30]; return sum(arr); }`, 60},
		{"arr-return-move", `function make(): i32[] { let a = [10, 20, 30]; return a; } function main(): i32 { let x = make(); let y = [1, 1, 1]; return x[0] + x[2]; }`, 40},
		{"arr-param-two", `function pick(a: i32[], b: i32[]): i32 { return a[0] + b[1]; } function main(): i32 { let p = [1, 2]; let q = [10, 20]; return pick(p, q); }`, 21},
		// Array-slot reassignment Perceus (retain-new + cow-guarded release-old).
		{"arr-reassign-alias", `function main(): i32 { let xs = [1, 2, 3]; let ys = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }`, 4},
		{"arr-reassign-fresh", `function main(): i32 { let xs = [1, 2]; xs = [9, 9, 9]; return xs[2]; }`, 9},
		{"arr-rebind-loop", `function main(): i32 { let s = 0; let i = 0; while (i < 4) { let r = [i, i * 2, i * 3]; s = s + r[2]; i = i + 1; } return s; }`, 18},
		// Strings: literal + .len(), concat (+), equality (==/!=), incl. string
		// params. The IR path reuses asm_arm64's 16-byte `[data@0,len@8]` box +
		// __fern_str_concat/_eq helpers; exit codes must match the AST path.
		{"str-len", `function main(): i32 { let s = "hello"; return s.len(); }`, 5},
		{"str-index-local", `function main(): i32 { let s = "hello"; return s[0] as i32; }`, 104},
		{"str-index-loop", `function main(): i32 { let s = "abc"; let sum = 0; let i = 0; while (i < 3) { sum = sum + (s[i] as i32); i = i + 1; } return sum % 200; }`, 94},
		{"str-index-param", `function first(s: string): i32 { return s[0] as i32; } function main(): i32 { return first("Z"); }`, 90},
		{"str-slice-len", `function main(): i32 { let s = "hello"; let t = slice_unchecked(s, 1, 4); return t.len(); }`, 3},
		{"str-slice-idx0", `function main(): i32 { let s = "hello"; let t = slice_unchecked(s, 1, 4); return t[0] as i32; }`, 101},
		{"str-slice-chain", `function main(): i32 { return slice_unchecked("hello", 1, 4)[2] as i32; }`, 108},
		{"str-literal-len", `function main(): i32 { return "world!".len(); }`, 6},
		{"str-empty-len", `function main(): i32 { let s = ""; return s.len(); }`, 0},
		{"str-concat-len", `function main(): i32 { let a = "ab"; let b = "cde"; let c = a + b; return c.len(); }`, 5},
		{"str-concat-direct", `function main(): i32 { return ("foo" + "bar").len(); }`, 6},
		{"str-concat-chain", `function main(): i32 { let a = "a"; let b = "bb"; let c = "ccc"; return (a + b + c).len(); }`, 6},
		{"str-eq-true", `function main(): i32 { let a = "hi"; let b = "hi"; if (a == b) { return 7; } return 0; }`, 7},
		{"str-eq-false", `function main(): i32 { let a = "hi"; let b = "ho"; if (a == b) { return 7; } return 9; }`, 9},
		{"str-ne-true", `function main(): i32 { let a = "hi"; let b = "ho"; if (a != b) { return 3; } return 0; }`, 3},
		{"str-concat-eq", `function main(): i32 { let a = "foo"; let b = "foobar"; if (a + "bar" == b) { return 11; } return 0; }`, 11},
		{"str-param-len", `function slen(s: string): i32 { return s.len(); } function main(): i32 { let x = "abcd"; return slen(x); }`, 4},
		{"str-param-concat", `function jn(a: string, b: string): i32 { return (a + b).len(); } function main(): i32 { return jn("xx", "yyy"); }`, 5},
		// Builtin i32.to_string() — IR routes to the __fn___fern_i32_to_string
		// stack-ABI wrapper (tail-calls asm_arm64's register-ABI body); AST uses
		// the same decimal helper. Exit codes must match across both paths.
		{"to-string-basic", `import "std/i32";
function main(): i32 { let a = (42).to_string(); return a.len(); }`, 2},
		{"to-string-digits", `import "std/i32";
function main(): i32 { let a = (42).to_string(); if (a[0] != 52) { return 80; } if (a[1] != 50) { return 81; } return a.len(); }`, 2},
		{"to-string-negative", `import "std/i32";
function main(): i32 { let n = (5 - 12).to_string(); if (n[0] != 45) { return 82; } if (n[1] != 55) { return 83; } return n.len(); }`, 2},
		{"to-string-zero", `import "std/i32";
function main(): i32 { let z = (0).to_string(); if (z[0] != 48) { return 84; } return z.len(); }`, 1},
		{"to-string-concat", `import "std/i32";
function main(): i32 { let m = "n=" + (7).to_string(); return m.len(); }`, 3},
		{"to-string-identity", `import "std/string";
function main(): i32 { let s = "hi"; let t = s.to_string(); return t.len(); }`, 2},
		// String-returning functions route through the IR (str_ret_fns tracks the
		// result as a string; the box just leaks). Param + concat + return too.
		{"str-returning", `function greet(): string { return "hi"; } function main(): i32 { let s = greet(); return s.len(); }`, 2},
		{"str-returning-concat", `function shout(s: string): string { return s + "!"; } function main(): i32 { let g = shout("hey"); return g.len(); }`, 4},
		{"str-returning-inline", `function tag(): string { return "abcd"; } function main(): i32 { return tag().len(); }`, 4},
		// String-typed struct/enum fields (leak-safe, no RC).
		{"struct-str-field", `struct Token { text: string, kind: i32 } function main(): i32 { let t = Token { text: "hello", kind: 7 }; return t.text.len() + t.kind; }`, 12},
		{"struct-str-method", `struct N { s: string } function (n: N) sz(): i32 { return n.s.len(); } function main(): i32 { let x = N { s: "abcd" }; return x.sz(); }`, 4},
		{"enum-str-payload", `enum T { Word(string), Eof } function g(t: T): i32 { match (t) { Word(w) => { return w.len(); }, Eof => { return 3; } } return 0; } function main(): i32 { return g(Word("hello")) + g(Eof); }`, 8},
		// Scalar-array struct fields (i32[], fresh-literal, leak-only).
		{"struct-arr-field", `struct Buf { data: i32[], n: i32 } function main(): i32 { let b = Buf { data: [10, 20, 30], n: 3 }; let s = 0; let i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; }`, 60},
		{"struct-arr-param", `struct Buf { data: i32[], n: i32 } function sum(b: Buf): i32 { let s = 0; let i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; } function main(): i32 { let b = Buf { data: [5, 10, 15], n: 3 }; return sum(b); }`, 30},
		{"struct-arr-extract", `struct Buf { data: i32[] } function main(): i32 { let b = Buf { data: [7, 8, 9] }; let a = b.data; return a[0] + a[2]; }`, 16},
		// Aliasing a struct/enum-element array local carries the element type
		// over (`qs[i].field` / `qs[i].method()` dispatch).
		{"struct-arr-alias-method", `struct P { x: i32 } function (p: P) g(): i32 { return p.x; } function main(): i32 { let ps = [P{x: 1}, P{x: 2}]; let qs = ps; return qs[0].g() + qs[1].g(); }`, 3},
		{"enum-arr-alias-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { let a = [R, G]; let b = a; return b[0].k() * 10 + b[1].k(); }`, 12},
		// Typed string[] arrays (literals/indexing/params/loop; elements leak).
		{"strarr-index", `function main(): i32 { let names = ["foo", "bar", "hello"]; return names[0].len() + names[2].len(); }`, 8},
		{"strarr-param", `function f(names: string[]): i32 { return names[0].len(); } function main(): i32 { return f(["abcd"]); }`, 4},
		{"strarr-loop", `function main(): i32 { let names = ["a", "bb", "ccc"]; let s = 0; let i = 0; while (i < 3) { s = s + names[i].len(); i = i + 1; } return s; }`, 6},
		// string[]-returning functions (move-on-return; call site element-types
		// the result as string[] via strarr_ret_fns, so xs[i] is a string).
		{"strarr-ret", `function names(): string[] { return ["a", "bb", "ccc"]; } function main(): i32 { let xs = names(); return xs[1].len(); }`, 2},
		{"strarr-ret-direct-index", `function names(): string[] { return ["a", "bb", "ccc"]; } function main(): i32 { return names()[2].len(); }`, 3},
		{"strarr-ret-len", `function names(): string[] { let a = ["x", "yy"]; return a; } function main(): i32 { let xs = names(); return xs.len() + xs[1].len(); }`, 4},
		{"strarr-ret-param", `function id(a: string[]): string[] { return a; } function main(): i32 { let xs = ["q", "ww", "eee"]; let ys = id(xs); return ys[1].len() + ys.len(); }`, 5},
		{"strarr-ret-loop", `function names(): string[] { return ["a", "bb", "ccc", "dddd"]; } function main(): i32 { let xs = names(); let i = 0; let s = 0; while (i < xs.len()) { s = s + xs[i].len(); i = i + 1; } return s; }`, 10},
		// Scalar-field structs (struct_make / struct_get): the arm64 IR path mirrors
		// x86's `[shape_ptr, f0, f1, …]` 8-byte box. Exit codes must match.
		{"struct-lit-fields", `struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 3, y: 4 }; return p.x + p.y; }`, 7},
		{"struct-field-order", `struct P { x: i32, y: i32 } function main(): i32 { let p = P { y: 40, x: 2 }; return p.x + p.y; }`, 42},
		{"struct-three-fields", `struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; return v.a * 100 + v.b * 10 + v.c; }`, 123},
		{"struct-param", `struct P { x: i32, y: i32 } function sum(p: P): i32 { return p.x + p.y; } function main(): i32 { let p = P { x: 30, y: 12 }; return sum(p); }`, 42},
		{"struct-bool-field", `struct F { on: boolean, n: i32 } function main(): i32 { let f = F { on: true, n: 7 }; if (f.on) { return f.n; } return 0; }`, 7},
		{"struct-in-loop", `struct P { x: i32, y: i32 } function main(): i32 { let s = 0; let i = 0; while (i < 4) { let p = P { x: i, y: i * 2 }; s = s + p.x + p.y; i = i + 1; } return s; }`, 18},
		// Functional struct update (desugars to struct_make + struct_get).
		{"struct-update-mid", `struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; let w = V { ...v, b: 20 }; return w.a + w.b + w.c; }`, 24},
		{"struct-update-keeps-base", `struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 5, y: 6 }; let q = P { ...p, x: 50 }; return p.x + q.x; }`, 55},
		// Field mutation `p.x = v` (struct_set).
		{"field-mutate", `struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 1, y: 2 }; p = P { ...p, x: 40 }; return p.x + p.y; }`, 42},
		{"field-mutate-loop", `struct C { n: i32 } function main(): i32 { let c = C { n: 0 }; let i = 0; while (i < 5) { c = C { ...c, n: c.n + i }; i = i + 1; } return c.n; }`, 10},
		{"field-mutate-alias", `struct P { x: i32 } function main(): i32 { let p = P { x: 1 }; let q = p; q = P { ...q, x: 9 }; return p.x * 10 + q.x; }`, 19},
		// Tuples (tuple_make / tuple_get; no shape slot, numeric .N access) + 2-elem destructure.
		{"tuple-access", `function main(): i32 { let t = (3, 4); return t.0 + t.1; }`, 7},
		{"tuple-three", `function main(): i32 { let t = (1, 2, 3); return t.0 * 100 + t.1 * 10 + t.2; }`, 123},
		{"tuple-destructure", `function main(): i32 { let (a, b) = (40, 2); return a + b; }`, 42},
		{"tuple-expr-elems", `function main(): i32 { let x = 5; let t = (x * 2, x + 1); return t.0 + t.1; }`, 16},
		// Methods (receiver = arg 0, static dispatch to __fn_<Type>.<name>).
		{"method-field", `struct P { x: i32 } function (p: P) get(): i32 { return p.x; } function main(): i32 { let p = P { x: 42 }; return p.get(); }`, 42},
		{"method-with-arg", `struct B { v: i32 } function (b: B) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = B { v: 4 }; return x.scale(3); }`, 12},
		{"method-self-dispatch", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function (p: P) quad(): i32 { return p.dbl() * 2; } function main(): i32 { let p = P { x: 5 }; return p.quad(); }`, 20},
		{"method-same-name-two-types", `struct A { n: i32 } struct B { n: i32 } function (a: A) get(): i32 { return a.n + 1; } function (b: B) get(): i32 { return b.n + 100; } function main(): i32 { let a = A { n: 5 }; let b = B { n: 5 }; return a.get() + b.get(); }`, 111},
		// Enums + match (variant construction + variant_is dispatch + payload bind).
		{"enum-payload", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(A(21)); }`, 42},
		{"match-guard-fallthrough", `enum E { Pos(i32), Neg(i32), Zero } function f(e: E): i32 { match (e) { Pos(n) when n > 10 => { return 1; }, Pos(n) => { return 2; }, _ => { return 3; } } return 0; } function main(): i32 { return f(Pos(20)) * 100 + f(Pos(5)) * 10 + f(Zero); }`, 123},
		{"match-guard-mixed", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) when n > 3 => { return n * 2; }, A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return f(A(5)) + f(A(1)) + f(B); }`, 110},
		{"match-guard-wildcard", `enum E { V(i32) } function f(e: E): i32 { match (e) { V(n) when false => { return 5; }, V(n) => { return n; } } return 0; } function main(): i32 { return f(V(42)); }`, 42},
		{"opt-some-none", `function classify(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (classify(n)) { Some(_) => { return 1; }, None => { return 0; } } return 9; } function main(): i32 { return f(5) * 10 + f(0); }`, 10},
		{"opt-ok-err", `function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(n); } function f(n: i32): i32 { match (chk(n)) { Ok(_) => { return 7; }, Err(_) => { return 3; } } return 9; } function main(): i32 { return f(2) * 10 + f(0); }`, 73},
		{"opt-none-first", `function g(n: i32): Option[i32] { if (n > 5) { return Some(n); } return None; } function f(n: i32): i32 { match (g(n)) { None => { return 4; }, Some(_) => { return 8; } } return 0; } function main(): i32 { return f(9) + f(1); }`, 12},
		{"opt-bind-some", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }`, 105},
		{"opt-bind-result", `function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n * 2); } return Err(n + 50); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e; } } return 0; } function main(): i32 { return f(3) + f(0); }`, 56},
		{"opt-bind-guard", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) when x > 10 => { return 1; }, Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(20) * 100 + f(5) * 10 + f(0); }`, 150},
		{"opt-bind-string", `function name(n: i32): Option[string] { if (n > 0) { return Some("hello"); } return None; } function f(n: i32): i32 { match (name(n)) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; } function main(): i32 { return f(1); }`, 5},
		// Option/Result payload that is itself an ENUM value (the Option/Result-
		// path analog of #2979).
		{"opt-bind-enum", `enum C { R, G } function g(b: i32): Option[C] { if (b > 0) { return Some(G); } return None; } function main(): i32 { match (g(1)) { Some(c) => { match (c) { R => { return 1; }, G => { return 2; } } }, None => { return 0; } } return 0; }`, 2},
		{"opt-bind-enum-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function g(): Option[C] { return Some(G); } function main(): i32 { match (g()) { Some(c) => { return c.k(); }, None => { return 0; } } return 0; }`, 2},
		{"result-bind-enum", `enum C { R, G } function g(): Result[C, i32] { return Ok(G); } function main(): i32 { match (g()) { Ok(c) => { match (c) { R => { return 1; }, G => { return 2; } } }, Err(e) => { return e; } } return 0; }`, 2},
		// NESTED Option/Result payload — bound by pointer + typed so the inner
		// match recovers.
		{"opt-bind-nested-opt", `function g(n: i32): Option[Option[i32]] { if (n > 0) { return Some(Some(n)); } return None; } function main(): i32 { match (g(5)) { Some(inner) => { match (inner) { Some(x) => { return x; }, None => { return 99; } } }, None => { return 0; } } return 0; }`, 5},
		{"opt-bind-nested-result", `function g(n: i32): Option[Result[i32, i32]] { return Some(Ok(n)); } function main(): i32 { match (g(7)) { Some(r) => { match (r) { Ok(x) => { return x; }, Err(e) => { return e; } } }, None => { return 0; } } return 0; }`, 7},
		// `match (a[i])` on an Option/Result ARRAY element (element type from the
		// array's `Option[T][]` / `Result[…][]` annotation).
		{"optarr-index-match", `function main(): i32 { let a: Option[i32][] = [Some(7), None]; match (a[0]) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 7},
		{"optarr-while-match", `function main(): i32 { let a: Option[i32][] = [Some(5), None, Some(3)]; let i = 0; let s = 0; while (i < a.len()) { match (a[i]) { Some(x) => { s = s + x; }, None => {} } i = i + 1; } return s; }`, 8},
		{"resultarr-index-match", `function main(): i32 { let a: Result[i32, i32][] = [Ok(5), Err(3)]; match (a[1]) { Ok(x) => { return x; }, Err(e) => { return e * 10; } } return 0; }`, 30},
		// Option/Result-ARRAY struct field — leak-safe, so construction +
		// `.len()` + `match (b.o[i])` (field-array element) lower.
		{"optarr-field-match", `struct B { o: Option[i32][] } function main(): i32 { let b = B { o: [Some(7), None] }; match (b.o[0]) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 7},
		{"resultarr-field-match", `struct B { o: Result[i32, i32][] } function main(): i32 { let b = B { o: [Ok(5), Err(3)] }; match (b.o[1]) { Ok(x) => { return x; }, Err(e) => { return e * 10; } } return 0; }`, 30},
		// `for o in optArray { match (o) }` — the foreach binds the loop var with
		// the element Option/Result type so the body match recovers the payload.
		{"foreach-optarr-match", `function main(): i32 { let a: Option[i32][] = [Some(1), Some(2), None]; let s = 0; for o in a { match (o) { Some(x) => { s = s + x; }, None => { s = s + 100; } } } return s; }`, 103},
		{"foreach-resultarr-match", `function main(): i32 { let a: Result[i32, i32][] = [Ok(5), Err(3)]; let s = 0; for r in a { match (r) { Ok(x) => { s = s + x; }, Err(e) => { s = s + e * 10; } } } return s; }`, 35},
		{"opt-bind-result-strerr", `function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err("fail"); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }`, 74},
		{"opt-bind-local", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { let r = g(n); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }`, 105},
		{"opt-bind-local-strerr", `function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err("oops"); } function f(n: i32): i32 { let r = chk(n); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }`, 74},
		{"opt-bind-param", `function f(o: Option[i32]): i32 { match (o) { Some(x) => { return x * 2; }, None => { return 0; } } return 0; } function main(): i32 { return f(Some(21)) + f(None); }`, 42},
		// match on a STRUCT-METHOD call returning Option/Result, binding the
		// payload — the method's return type is recovered via the qualified
		// "<Type>.<method>" key in opt_ret_fns (#2969 follow-up).
		{"opt-method-bind", `struct Box { v: i32 } function (b: Box) get(): Option[i32] { if (b.v > 0) { return Some(b.v); } return None; } function main(): i32 { let x = Box { v: 5 }; match (x.get()) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 5},
		{"opt-method-bind-local", `struct Box { v: i32 } function (b: Box) get(): Option[i32] { if (b.v > 0) { return Some(b.v); } return None; } function main(): i32 { let x = Box { v: 5 }; let o = x.get(); match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 5},
		{"result-method-bind", `struct Box { v: i32 } function (b: Box) chk(): Result[i32, i32] { if (b.v > 0) { return Ok(b.v + 30); } return Err(b.v); } function main(): i32 { let x = Box { v: 5 }; match (x.chk()) { Ok(n) => { return n; }, Err(e) => { return e; } } return 0; }`, 35},
		{"opt-method-bind-string", `struct Box { v: i32 } function (b: Box) name(): Option[string] { if (b.v > 0) { return Some("hello"); } return None; } function main(): i32 { let x = Box { v: 5 }; match (x.name()) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; }`, 5},
		// Enum-receiver method calls `c.method()` — unannotated enum-value local
		// dispatches to `<Enum>.<method>` (#2947).
		{"enum-method-payloadless", `enum Color { Red, Green } function (c: Color) code(): i32 { match (c) { Red => { return 1; }, Green => { return 2; } } return 0; } function main(): i32 { let c = Green; return c.code(); }`, 2},
		{"enum-method-payload", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 0; } } return 0; } function main(): i32 { let e = A(9); return e.v(); }`, 9},
		{"enum-method-args", `enum Op2 { Add, Mul } function (o: Op2) ap(a: i32, b: i32): i32 { match (o) { Add => { return a + b; }, Mul => { return a * b; } } return 0; } function main(): i32 { let o = Add; let p = Mul; return o.ap(5, 7) * 100 + p.ap(5, 7); }`, 211},
		// Method call on a bound ENUM-typed match payload (recursive enum).
		{"enum-method-recursive-tree", `enum Tree { Leaf(i32), Node(Tree, Tree) } function (t: Tree) sum(): i32 { match (t) { Leaf(n) => { return n; }, Node(l, r) => { return l.sum() + r.sum(); } } return 0; } function main(): i32 { return Node(Leaf(3), Node(Leaf(4), Leaf(5))).sum(); }`, 12},
		{"enum-method-recursive-single", `enum Box { Wrap(Box), Base(i32) } function (b: Box) v(): i32 { match (b) { Base(n) => { return n; }, Wrap(inner) => { return inner.v(); } } return 0; } function main(): i32 { return Wrap(Wrap(Base(7))).v(); }`, 7},
		// Enum-array element method dispatch `a[i].method()` (#2954 item 2).
		{"enum-array-method-annot", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { let a: C[] = [R, G]; return a[1].k(); }`, 2},
		{"enum-array-method-payload", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 9; } } return 0; } function main(): i32 { let a: E[] = [A(7), B]; return a[0].v() + a[1].v(); }`, 16},
		// A struct with an enum-ARRAY field is leak-safe (construction + `.len()`
		// + element index/match lower).
		{"struct-enumarr-len", `enum C { R, G } struct Box { items: C[] } function main(): i32 { let b = Box { items: [R, G] }; return b.items.len(); }`, 2},
		{"struct-enumarr-index-match", `enum C { R, G } struct Box { items: C[] } function main(): i32 { let b = Box { items: [R, G, R] }; match (b.items[1]) { R => { return 1; }, G => { return 2; } } return 0; }`, 2},
		// Method dispatch on an ENUM-array field element (`b.items[i].method()`)
		// — the field-array index recovers the enum element type so it dispatches
		// `<Enum>.<method>` (the field analog of the local enum-array case).
		{"struct-enumarr-elem-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } struct Box { items: C[] } function main(): i32 { let b = Box { items: [R, G] }; return b.items[0].k() * 10 + b.items[1].k(); }`, 12},
		{"struct-enumarr-elem-method-payload", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 9; } } return 0; } struct Box { items: E[] } function main(): i32 { let b = Box { items: [A(7), B] }; return b.items[0].v() + b.items[1].v(); }`, 16},
		// A struct with a NESTED (array-of-array) field is leak-safe (construction
		// + `.len()` + element index lower).
		{"struct-nested-arr-index", `struct G { rows: i32[][] } function main(): i32 { let g = G { rows: [[1, 2], [3, 4]] }; return g.rows[1][0]; }`, 3},
		{"struct-nested-arr-param", `struct G { rows: i32[][] } function first(g: G): i32 { return g.rows[0][0]; } function main(): i32 { let g = G { rows: [[5, 6]] }; return first(g); }`, 5},
		{"struct-field-nested", `struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { let b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }`, 42},
		{"struct-field-deep", `struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { let o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }`, 105},
		{"struct-field-bind", `struct Point { x: i32, y: i32 } struct Box { p: Point, tag: i32 } function main(): i32 { let b = Box { p: Point { x: 7, y: 8 }, tag: 3 }; let pp = b.p; return pp.x * pp.y + b.tag; }`, 59},
		{"forin-i32", `function main(): i32 { let xs = [10, 20, 30, 40]; let sum = 0; for x in xs { sum = sum + x; } return sum; }`, 100},
		{"forin-i32-param", `function total(xs: i32[]): i32 { let s = 0; for v in xs { s = s + v; } return s; } function main(): i32 { let a = [1, 2, 3, 4, 5]; return total(a); }`, 15},
		{"forin-nested", `function main(): i32 { let xs = [1, 2, 3]; let t = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }`, 36},
		{"forin-string", `function main(): i32 { let ss: string[] = ["a", "bb", "ccc", "dddd"]; let n = 0; for s in ss { n = n + s.len(); } return n; }`, 10},
		{"enum-struct-payload", `struct BinExpr { left: i32, right: i32 } enum Expr { Lit(i32), Binary(BinExpr) } function eval(e: Expr): i32 { match (e) { Lit(n) => { return n; }, Binary(b) => { return b.left + b.right; } } return 0; } function main(): i32 { return eval(Lit(7)) + eval(Binary(BinExpr { left: 3, right: 9 })); }`, 19},
		{"enum-struct-payload-guard", `struct P { x: i32, y: i32 } enum Shape { Rect(P), Dot } function area(s: Shape): i32 { match (s) { Rect(p) when p.x > 0 => { return p.x * p.y; }, _ => { return 0; } } return 0; } function main(): i32 { return area(Rect(P { x: 4, y: 5 })); }`, 20},
		{"enum-struct-payload-nested", `struct Inner { v: i32 } struct Mid { i: Inner } enum E { A(Mid), B } function f(e: E): i32 { match (e) { A(m) => { return m.i.v; }, B => { return 9; } } return 0; } function main(): i32 { return f(A(Mid { i: Inner { v: 42 } })) + f(B); }`, 51},
		{"enum-arr-payload-len", `enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len(); }, Empty => { return 0; } } return 0; } function main(): i32 { return f(Items([10, 20, 30])) * 10 + f(Empty); }`, 30},
		{"enum-arr-payload-forin", `enum E { Items(i32[]), Empty } function sum(e: E): i32 { match (e) { Items(xs) => { let t = 0; for x in xs { t = t + x; } return t; }, Empty => { return 0; } } return 0; } function main(): i32 { return sum(Items([5, 10, 15])); }`, 30},
		{"enum-arr-payload-alias", `enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len() + xs[0]; }, Empty => { return 0; } } return 0; } function main(): i32 { let a = [7, 8, 9]; return f(Items(a)); }`, 10},
		{"enum-strarr-payload-len", `enum E { Words(string[]), Nothing } function f(e: E): i32 { match (e) { Words(w) => { return w.len(); }, Nothing => { return 0; } } return 0; } function main(): i32 { return f(Words(["a", "bb", "ccc"])) * 10 + f(Nothing); }`, 30},
		{"enum-strarr-payload-forin", `enum E { Words(string[]), None } function f(e: E): i32 { match (e) { Words(w) => { let n = 0; for s in w { n = n + s.len(); } return n; }, None => { return 0; } } return 0; } function main(): i32 { return f(Words(["a", "bb", "ccc"])); }`, 6},
		{"struct-strarr-field-len", `struct Doc { lines: string[] } function nl(d: Doc): i32 { return d.lines.len(); } function main(): i32 { let d = Doc { lines: ["x", "y", "z"] }; return nl(d); }`, 3},
		{"struct-strarr-field-index", `struct Doc { lines: string[] } function f(d: Doc): i32 { return d.lines[1].len(); } function main(): i32 { let d = Doc { lines: ["a", "bb", "ccc"] }; return f(d); }`, 2},
		// `for c in r.field` over a leak-safe array-typed struct field (string[] /
		// struct[] / enum[] — element types that aren't reclaimed). The field
		// access is snapshotted into a hidden BORROW local (never swept), so the
		// buffer's lifetime stays with the owning struct (#3003 leak-safe slice).
		{"struct-strarr-field-forin", `struct R { tags: string[] } function main(): i32 { let r = R { tags: ["ab", "cde"] }; let n = 0; for t in r.tags { n = n + t.len(); } return n; }`, 5},
		{"struct-structarr-field-forin", `struct P { x: i32 } struct R { items: P[] } function (p: P) dbl(): i32 { return p.x * 2; } function main(): i32 { let r = R { items: [P { x: 3 }, P { x: 4 }] }; let n = 0; for p in r.items { n = n + p.dbl(); } return n; }`, 14},
		{"struct-enumarr-field-forin", `enum C { A, B } struct R { cells: C[] } function main(): i32 { let r = R { cells: [C.A, C.B] }; let n = 0; for c in r.cells { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } return n; }`, 3},
		// The owning struct is read AFTER the loop — the borrow must not free its
		// field buffer (the exit-sweep never decs a non-array-marked snapshot).
		{"struct-strarr-field-forin-after", `struct R { tags: string[] } function main(): i32 { let r = R { tags: ["ab", "cd", "e"] }; let n = 0; for t in r.tags { n = n + t.len(); } return n + r.tags.len(); }`, 8},
		// A reclaimable scalar-array field (i32[]) is NOT admitted — aliasing it is
		// an RC hazard (deferred to the Perceus self-host port, #3003) — so this
		// exercises the ungated route on both legs.
		{"struct-i32arr-field-forin", `struct R { nums: i32[] } function main(): i32 { let r = R { nums: [3, 4] }; let n = 0; for v in r.nums { n = n + v; } return n; }`, 7},
		{"tuple-str-i32-dotn", `function main(): i32 { let t = ("hello", 7); return t.0.len() + t.1; }`, 12},
		{"tuple-str-i32-destructure", `function main(): i32 { let (a, b) = ("world", 3); return a.len() + b; }`, 8},
		{"tuple-struct-dotn", `struct P { x: i32, y: i32 } function main(): i32 { let t = (P { x: 4, y: 5 }, 2); return t.0.x * t.0.y + t.1; }`, 22},
		// A function-VALUE tuple element call `t.N(args)` — the element is tagged
		// "fn" at construction (elem_type_tag), so the call lowers to tuple_get +
		// call_indirect, mirroring the "fn"-typed struct field (#3016).
		{"tuple-fn-value-call", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { let t = (inc, 5); return t.0(t.1); }`, 6},
		{"tuple-fn-value-call-multi", `function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { let t = (inc, dbl, 5); return t.0(t.2) + t.1(t.2); }`, 16},
		{"tuple-fn-value-call-2args", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { let t = ("x", add); return t.1(3, 4); }`, 7},
		// An Option value in a tuple, matched via `t.N` — the element is tagged
		// "Option[T]" at construction (elem_type_tag), admitted by the tuple-make
		// eligibility check, and the match-scrutinee recovers the payload from the
		// element tag (#3018). Result elements (a comma in the tag) stay on AST.
		{"tuple-option-i32-match", `function main(): i32 { let t = (Some(7), 3); match (t.0) { Some(x) => { return x + t.1; }, None => { return 0; } } return 0; }`, 10},
		{"tuple-option-i32-idx1-match", `function main(): i32 { let t = (3, Some(7)); match (t.1) { Some(x) => { return x + t.0; }, None => { return 0; } } return 0; }`, 10},
		{"tuple-option-string-match", `function main(): i32 { let t = (Some("hello"), 3); match (t.0) { Some(s) => { return s.len() + t.1; }, None => { return 0; } } return 0; }`, 8},
		{"tuple-option-from-call-none", `function f(b: boolean): Option[i32] { if (b) { return Some(7); } return None; } function main(): i32 { let t = (f(false), 5); match (t.0) { Some(x) => { return x + t.1; }, None => { return t.1 + 100; } } return 0; }`, 105},
		// A direct `Some(x)` construction matched/bound — `some_opt_type` types
		// the local / scrutinee so the match recovers the payload, the
		// construction analogue of the Option-returning-call path (#3024).
		{"some-local-i32-match", `function main(): i32 { let o = Some(7); match (o) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 7},
		{"some-local-string-match", `function main(): i32 { let o = Some("hello"); match (o) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; }`, 5},
		{"some-local-struct-match", `struct S { x: i32 } function main(): i32 { let o = Some(S { x: 5 }); match (o) { Some(s) => { return s.x; }, None => { return 0; } } return 0; }`, 5},
		{"some-direct-match", `function main(): i32 { match (Some(9)) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 9},
		{"some-local-reassign-none", `function pick(b: boolean): i32 { let o = Some(7); if (b) { o = None; } match (o) { Some(x) => { return x; }, None => { return 99; } } return 0; } function main(): i32 { return pick(true) + pick(false); }`, 106},
		// An unannotated array literal of Option values — the element opt-type is
		// inferred from the first Some(...) element (#3027, array sibling of #3024).
		{"some-array-foreach", `function main(): i32 { let a = [Some(1), Some(2), None]; let n = 0; for o in a { match (o) { Some(x) => { n = n + x; }, None => {} } } return n; }`, 3},
		{"some-array-index", `function main(): i32 { let a = [Some(4), Some(2)]; match (a[0]) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 4},
		{"some-array-string", `function main(): i32 { let a = [Some("ab"), None, Some("c")]; let n = 0; for o in a { match (o) { Some(s) => { n = n + s.len(); }, None => {} } } return n; }`, 3},
		// A function returning a tuple with an Option element (#3029) — admitted
		// by tuple_elems_lowerable; var-bind / destructure recover the payload.
		{"tuple-ret-opt-var", `function mk(): (Option[i32], i32) { return (Some(3), 4); } function main(): i32 { let t = mk(); match (t.0) { Some(x) => { return x + t.1; }, None => { return 0; } } return 0; }`, 7},
		{"tuple-ret-opt-destr", `function mk(): (Option[i32], i32) { return (Some(3), 4); } function main(): i32 { let (o, n) = mk(); match (o) { Some(x) => { return x + n; }, None => { return 0; } } return 0; }`, 7},
		{"tuple-ret-opt-string", `function mk(): (Option[string], i32) { return (Some("ab"), 4); } function main(): i32 { let t = mk(); match (t.0) { Some(s) => { return s.len() + t.1; }, None => { return 0; } } return 0; }`, 6},
		{"tuple-ret-opt-none", `function mk(b: boolean): (Option[i32], i32) { if (b) { return (None, 9); } return (Some(3), 4); } function main(): i32 { let t = mk(true); match (t.0) { Some(x) => { return x; }, None => { return t.1; } } return 0; }`, 9},
		// A method with an Option/Result receiver (#3033) — slot 0 is opt-typed so
		// match(self) recovers the payload; the call dispatches to Option.<method>.
		{"opt-recv-method-bound", `function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { let o = Some(7); return o.unwrap_or(0); }`, 7},
		{"opt-recv-method-direct", `function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { return Some(7).unwrap_or(0); }`, 7},
		{"opt-recv-method-none", `function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { let o: Option[i32] = None; return o.unwrap_or(99); }`, 99},
		{"opt-recv-method-string", `function (o: Option[string]) ln(): i32 { match (o) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; } function main(): i32 { return Some("hello").ln(); }`, 5},
		{"opt-recv-method-callrecv", `function get(b: boolean): Option[i32] { if (b) { return Some(8); } return None; } function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { return get(true).unwrap_or(0) + get(false).unwrap_or(5); }`, 13},
		// matching/binding the result of an Option-receiver method (#3051) —
		// opt_recv_base_type keys "Option.<m>" so the result type is recovered.
		{"opt-recv-method-chain-direct", `function (o: Option[i32]) mi(): Option[i32] { match (o) { Some(x) => { return Some(x + 1); }, None => { return None; } } return None; } function main(): i32 { match (Some(5).mi()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 6},
		{"opt-recv-method-chain-bind", `function (o: Option[i32]) mi(): Option[i32] { match (o) { Some(x) => { return Some(x + 1); }, None => { return None; } } return None; } function main(): i32 { let r = Some(5).mi(); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 6},
		{"opt-recv-method-chain-local", `function (o: Option[i32]) mi(): Option[i32] { match (o) { Some(x) => { return Some(x + 1); }, None => { return None; } } return None; } function main(): i32 { let o = Some(5); match (o.mi()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 6},
		// An Option-receiver method on a struct-method's Option result, and the
		// chain matched — opt_recv_base_type recovers a method-result receiver (#3067).
		{"opt-chain-on-struct-method", `struct B { v: i32 } function (b: B) find(): Option[i32] { return Some(b.v); } function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { let b = B { v: 7 }; return b.find().uo(0); }`, 7},
		{"opt-chain-on-struct-method-match", `struct B { v: i32 } function (b: B) find(): Option[i32] { return Some(b.v); } function main(): i32 { let b = B { v: 9 }; match (b.find()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 9},
		// An Option-receiver method on a struct's Option field or a tuple's Option
		// element — opt_recv_base_type's ExprFieldAccess arm recovers it (#3070).
		{"opt-method-on-struct-field", `struct B { v: Option[i32] } function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { let b = B { v: Some(7) }; return b.v.uo(0); }`, 7},
		{"opt-method-on-tuple-elem", `function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { let t = (Some(5), 3); return t.0.uo(0) + t.1; }`, 8},
		// An enum-receiver method returning Option, matched/chained — the opt-result
		// recovery sites gained an expr_enum_type fallback (#3077).
		{"enum-method-opt-result-match", `enum E { V(i32), N } function (e: E) get(): Option[i32] { match (e) { V(x) => { return Some(x); }, N => { return None; } } return None; } function main(): i32 { match (V(7).get()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`, 7},
		{"enum-method-opt-result-chain", `enum E { V(i32), N } function (e: E) get(): Option[i32] { match (e) { V(x) => { return Some(x); }, N => { return None; } } return None; } function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { return V(6).get().uo(0) + N.get().uo(9); }`, 15},
		// A match-EXPRESSION in value position (`return match (...) { arm => E }`)
		// on a call-returning Option/Result. lower_iife_match now recovers the
		// scrutinee's Option/Result type via try_opt_type (not ExprIdent-only), so
		// the call scrutinee lowers instead of bailing to AST (#3081).
		{"match-expr-call-result-ok", `function f(n: i32): Result[i32, i32] { return Ok(n); } function main(): i32 { return match (f(5)) { Ok(v) => v, Err(e) => e }; }`, 5},
		{"match-expr-call-result-err", `function f(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(99); } function main(): i32 { return match (f(0)) { Ok(v) => v, Err(e) => e }; }`, 99},
		{"match-expr-call-option", `function f(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function main(): i32 { return match (f(7)) { Some(v) => v, None => 13 }; }`, 7},
		// An UNANNOTATED nested Option local (`let a = Some(Some(5))`) records its
		// "Option[Option[i32]]" type via some_opt_type (the nested-Option bail was
		// lifted), so the outer match binds `b` as Option[i32] (mark_opt_type) and the
		// inner `match (b)` recovers its payload — the whole thing lowers (#3106).
		{"nested-opt-unannot", `function main(): i32 { let a = Some(Some(5)); match (a) { Some(b) => { return match (b) { Some(v) => v, None => 1 }; }, None => { return 2; } } return 9; }`, 5},
		{"nested-opt-unannot-inner-expr", `function main(): i32 { let a = Some(Some(42)); match (a) { Some(b) => { return match (b) { Some(v) => v * 2, None => 1 }; }, None => { return 2; } } return 9; }`, 84},
		// The value-position (match-EXPRESSION) form of the nested-Option match: the
		// outer `Some(b)` binds b: Option[i32]. lower_iife_match now admits a nested-
		// Option payload into an i32 temp for an ident scrutinee, so the inner
		// `match (b)` lowers instead of bailing (#3111).
		{"nested-opt-expr-ident", `function main(): i32 { let a = Some(Some(5)); return match (a) { Some(b) => match (b) { Some(v) => v, None => 1 }, None => 2 }; }`, 5},
		{"nested-opt-expr-ident-derived", `function main(): i32 { let a = Some(Some(21)); return match (a) { Some(b) => match (b) { Some(v) => v * 2, None => 1 }, None => 2 }; }`, 42},
		// A match-EXPRESSION on a direct `Some(x)` construction scrutinee. try_opt_type
		// (shared by lower_iife_match and the `?` operator) now falls back to
		// some_opt_type for a direct Some construction, so it lowers instead of
		// bailing (#3115).
		{"match-expr-some-construct", `function main(): i32 { return match (Some(6)) { Some(w) => w, None => 0 }; }`, 6},
		{"match-expr-some-construct-derived", `function main(): i32 { return match (Some(20)) { Some(w) => w + 1, None => 0 }; }`, 21},
		{"match-expr-arm-some-construct", `function main(): i32 { let o = Some(5); return match (o) { Some(v) => match (Some(v + 1)) { Some(w) => w, None => 0 }, None => 0 }; }`, 6},
		// A match-EXPRESSION whose scrutinee is an Option-typed tuple element (t.0):
		// try_opt_type now resolves a numeric (tuple-element) field via
		// expr_tuple_elem_tag, mirroring the main StmtMatch path (#3118).
		{"match-expr-tuple-elem0", `function main(): i32 { let t = (Some(5), 3); return match (t.0) { Some(v) => v, None => 0 } + t.1; }`, 8},
		{"match-expr-tuple-elem1", `function main(): i32 { let t = (3, Some(8)); return match (t.1) { Some(v) => v, None => 0 } + t.0; }`, 11},
		// A match-EXPRESSION whose scrutinee is an Option-array element (a[i]):
		// try_opt_type gained an ExprIndex case recovering the element type from the
		// array slot's Option[T][] opt-type, mirroring the main StmtMatch path (#3121).
		{"match-expr-arr-elem0", `function main(): i32 { let a = [Some(5)]; return match (a[0]) { Some(v) => v, None => 0 }; }`, 5},
		{"match-expr-arr-elem-idx", `function main(): i32 { let a = [Some(3), Some(8)]; let i = 1; return match (a[i]) { Some(v) => v, None => 0 }; }`, 8},
		{"match-expr-arr-field-elem", `struct B { xs: Option[i32][] } function main(): i32 { let b = B { xs: [Some(4), None] }; return match (b.xs[0]) { Some(v) => v, None => 0 }; }`, 4},
		// An unannotated Option bound from an if-/match-EXPRESSION (which desugars to
		// an IIFE): the StmtVar opt-type inference now recovers o's Option type from
		// the first branch's Some(...) via iife_first_return_expr, so the later
		// match (o) lowers (#3124).
		{"ifexpr-opt-bind-some", `function main(): i32 { let x = 5; let o = if (x > 3) { Some(7) } else { None }; match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 7},
		{"ifexpr-opt-bind-none", `function main(): i32 { let x = 1; let o = if (x > 3) { Some(7) } else { None }; match (o) { Some(v) => { return v; }, None => { return 42; } } return 9; }`, 42},
		{"matchexpr-opt-bind", `function main(): i32 { let e = 2; let o = match (e) { 1 => Some(10), _ => Some(20) }; match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 20},
		// A struct bound from an if-/match-EXPRESSION (IIFE): the StmtVar struct-type
		// inference now recovers p's struct type from the first branch's struct
		// literal, so p.field resolves (the struct sibling of #3124) (#3133).
		{"ifexpr-struct-bind", `struct P { x: i32 } function main(): i32 { let c = 5; let p = if (c > 3) { P { x: 7 } } else { P { x: 1 } }; return p.x; }`, 7},
		{"ifexpr-struct-bind-else", `struct P { x: i32, y: i32 } function main(): i32 { let c = 1; let p = if (c > 3) { P { x: 7, y: 2 } } else { P { x: 1, y: 0 } }; return p.x + p.y; }`, 1},
		{"matchexpr-struct-bind", `struct P { x: i32 } function main(): i32 { let c = 1; let p = match (c) { 1 => P { x: 9 }, _ => P { x: 0 } }; return p.x; }`, 9},
		// A struct ARRAY bound from an if-/match-EXPRESSION (IIFE): the StmtVar
		// inference now records the element struct type and marks the slot is_arr, so
		// ps[i].field / ps.len() resolve (the struct-array sibling of #3133) (#3138).
		{"ifexpr-struct-arr-bind", `struct P { x: i32 } function main(): i32 { let c = 5; let ps = if (c > 3) { [P { x: 7 }] } else { [P { x: 1 }] }; return ps[0].x; }`, 7},
		{"ifexpr-struct-arr-len", `struct P { x: i32 } function main(): i32 { let c = 5; let ps = if (c > 3) { [P { x: 7 }, P { x: 8 }] } else { [P { x: 1 }] }; return ps.len() + ps[1].x; }`, 10},
		{"matchexpr-struct-arr-bind", `struct P { x: i32 } function main(): i32 { let k = 1; let ps = match (k) { 1 => [P { x: 9 }], _ => [P { x: 0 }] }; return ps[0].x; }`, 9},
		// for-in / .len() over an array bound from an if-/match-EXPRESSION: the StmtVar
		// is_arr inference now marks the slot is_arr for an IIFE-array result, so the
		// foreach lowers (indexing already worked without is_arr) (#3141).
		{"ifexpr-arr-foreach", `function main(): i32 { let c = 5; let a = if (c > 3) { [1, 2, 3] } else { [4] }; let s = 0; for x in a { s = s + x; } return s; }`, 6},
		{"ifexpr-arr-len", `function main(): i32 { let c = 5; let a = if (c > 3) { [1, 2, 3] } else { [4] }; return a.len(); }`, 3},
		{"matchexpr-arr-foreach", `function main(): i32 { let k = 1; let a = match (k) { 1 => [10, 20], _ => [1] }; let s = 0; for x in a { s = s + x; } return s; }`, 30},
		// An Option array bound from an if-/match-EXPRESSION (IIFE): the StmtVar
		// opt-array inference now records the slot's Option[T][] from the first
		// branch's array literal, so match (a[i]) / for o in a recover the element
		// payload (the Option-array sibling of #3141) (#3146).
		{"ifexpr-optarr-index", `function main(): i32 { let c = 5; let a = if (c > 3) { [Some(7), None] } else { [Some(1)] }; return match (a[0]) { Some(v) => v, None => 0 }; }`, 7},
		{"ifexpr-optarr-foreach", `function main(): i32 { let c = 5; let a = if (c > 3) { [Some(7), None, Some(3)] } else { [Some(1)] }; let s = 0; for o in a { match (o) { Some(v) => { s = s + v; }, None => {} } } return s; }`, 10},
		{"matchexpr-optarr-index", `function main(): i32 { let k = 1; let a = match (k) { 1 => [Some(9)], _ => [Some(0)] }; return match (a[0]) { Some(v) => v, None => 0 }; }`, 9},
		// A binding from a NESTED if-/match-expression (a branch is itself an
		// if-expression): iife_leaf_value unwraps the nested IIFE chain so the StmtVar
		// type inference sees the leaf struct/Some/array literal (#3156).
		{"nested-ifexpr-opt", `function main(): i32 { let c = 5; let o = if (c > 3) { if (c > 10) { Some(1) } else { Some(7) } } else { None }; return match (o) { Some(v) => v, None => 0 }; }`, 7},
		{"nested-ifexpr-struct", `struct P { x: i32 } function main(): i32 { let c = 5; let p = if (c > 3) { if (c > 10) { P { x: 1 } } else { P { x: 7 } } } else { P { x: 0 } }; return p.x; }`, 7},
		{"nested-ifexpr-arr", `function main(): i32 { let c = 5; let a = if (c > 3) { if (c > 10) { [1] } else { [7, 8] } } else { [0] }; let s = 0; for x in a { s = s + x; } return s; }`, 15},
		// A match whose scrutinee is directly an if-/match-EXPRESSION (a 0-arg IIFE):
		// both the main StmtMatch scrutinee resolution and try_opt_type now recover
		// the Option type via iife_leaf_value + some_opt_type (#3161).
		{"match-scrut-ifexpr-some", `function main(): i32 { let c = 5; return match (if (c > 3) { Some(7) } else { None }) { Some(v) => v, None => 0 }; }`, 7},
		{"match-scrut-ifexpr-none", `function main(): i32 { let c = 1; return match (if (c > 3) { Some(7) } else { None }) { Some(v) => v, None => 9 }; }`, 9},
		{"stmt-match-scrut-ifexpr", `function main(): i32 { let c = 5; match (if (c > 3) { Some(7) } else { None }) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 7},
		// An if-/match-expression binding whose branch returns an Option-typed LOCAL
		// (not a fresh Some): the StmtVar opt-IIFE inference falls back to the leaf
		// ident's tracked opt_type_of_slot (#3165).
		{"ifexpr-ret-optvar", `function f(c: i32): Option[i32] { if (c > 3) { return Some(7); } return None; } function main(): i32 { let o = f(5); let r = if (true) { o } else { None }; return match (r) { Some(v) => v, None => 0 }; }`, 7},
		{"matchexpr-ret-optvar", `function main(): i32 { let o = Some(8); let k = 1; let r = match (k) { 1 => o, _ => o }; return match (r) { Some(v) => v, None => 0 }; }`, 8},
		// A tuple literal with an if-/match-EXPRESSION element: the tuple lowering now
		// classifies each element by its leaf branch value via iife_leaf_value, so an
		// IIFE element is admitted with the right kind tag (#3172).
		{"tuple-ifexpr-elem0", `function main(): i32 { let c = 5; let t = (if (c > 3) { 7 } else { 1 }, 3); return t.0 + t.1; }`, 10},
		{"tuple-ifexpr-elem1", `function main(): i32 { let c = 1; let t = (3, if (c > 3) { 7 } else { 1 }); return t.0 + t.1; }`, 4},
		{"tuple-matchexpr-elem", `function main(): i32 { let k = 1; let t = (match (k) { 1 => 5, _ => 0 }, 3); return t.0 + t.1; }`, 8},
		// A struct array field set from an if-/match-EXPRESSION whose every branch is
		// a fresh array literal (iife_returns_fresh_array): admitted as an owned value
		// (#3179). An aliased branch is refused (verified by probe).
		{"struct-fld-ifexpr-arr", `struct B { xs: i32[] } function main(): i32 { let c = 5; let b = B { xs: if (c > 3) { [1, 2, 3] } else { [4] } }; return b.xs.len(); }`, 3},
		{"struct-fld-ifexpr-arr-else", `struct B { xs: i32[] } function main(): i32 { let c = 1; let b = B { xs: if (c > 3) { [1, 2, 3] } else { [4, 5] } }; return b.xs.len(); }`, 2},
		{"struct-fld-matchexpr-arr", `struct B { xs: i32[] } function main(): i32 { let k = 1; let b = B { xs: match (k) { 1 => [7, 8, 9], _ => [0] } }; return b.xs.len() + b.xs[0]; }`, 10},
		// An array literal whose element is an if-/match-EXPRESSION struct: the StmtVar
		// struct-array inference classifies the first element by its leaf branch via
		// iife_leaf_value, so a[i].field resolves (#3183).
		{"arr-ifexpr-struct-elem", `struct P { x: i32 } function main(): i32 { let c = 5; let a = [if (c > 3) { P { x: 7 } } else { P { x: 1 } }]; return a[0].x; }`, 7},
		{"arr-ifexpr-struct-foreach", `struct P { x: i32 } function main(): i32 { let c = 5; let a = [if (c > 3) { P { x: 7 } } else { P { x: 1 } }, P { x: 2 }]; let s = 0; for p in a { s = s + p.x; } return s; }`, 9},
		{"arr-matchexpr-struct-elem", `struct P { x: i32 } function main(): i32 { let k = 1; let a = [match (k) { 1 => P { x: 9 }, _ => P { x: 0 } }]; return a[0].x; }`, 9},
		// Field access / method dispatch directly on an if-/match-EXPRESSION:
		// expr_struct_type now resolves an IIFE value's struct type via
		// iife_leaf_value, so `(if (c) { P{..} } else { .. }).field` lowers (#3186).
		{"ifexpr-field-direct", `struct P { x: i32 } function main(): i32 { let c = 5; return (if (c > 3) { P { x: 7 } } else { P { x: 1 } }).x; }`, 7},
		{"ifexpr-method-direct", `struct P { x: i32 } function (p: P) g(): i32 { return p.x; } function main(): i32 { let c = 5; return (if (c > 3) { P { x: 7 } } else { P { x: 1 } }).g(); }`, 7},
		{"matchexpr-field-direct", `struct P { x: i32 } function main(): i32 { let k = 1; return (match (k) { 1 => P { x: 9 }, _ => P { x: 0 } }).x; }`, 9},
		// Iterating an Option-array struct field — the leak-safe-field foreach
		// opt-types the loop var so match(o) recovers the payload (#3056).
		{"opt-arr-field-foreach-i32", `struct B { xs: Option[i32][] } function main(): i32 { let b = B { xs: [Some(1), Some(2), None] }; let n = 0; for o in b.xs { match (o) { Some(x) => { n = n + x; }, None => {} } } return n; }`, 3},
		{"opt-arr-field-foreach-string", `struct B { xs: Option[string][] } function main(): i32 { let b = B { xs: [Some("ab"), None, Some("c")] }; let n = 0; for o in b.xs { match (o) { Some(s) => { n = n + s.len(); }, None => {} } } return n; }`, 3},
		// A 2D struct/enum array — the annotation records the innermost element
		// type so the nested foreach propagates it to p (#3058).
		{"arr2d-struct", `struct P { x: i32 } function main(): i32 { let a: P[][] = [[P { x: 1 }], [P { x: 2 }, P { x: 3 }]]; let n = 0; for row in a { for p in row { n = n + p.x; } } return n; }`, 6},
		{"arr2d-struct-method", `struct P { x: i32 } function (p: P) g(): i32 { return p.x * 2; } function main(): i32 { let a: P[][] = [[P { x: 1 }], [P { x: 2 }]]; let n = 0; for row in a { for p in row { n = n + p.g(); } } return n; }`, 6},
		{"arr2d-enum", `enum C { A, B } function main(): i32 { let a: C[][] = [[C.A], [C.B, C.A]]; let n = 0; for row in a { for c in row { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } } return n; }`, 4},
		// Unannotated 2D struct/enum array literal — element type inferred by
		// recursing into the inner literal (#3061, unannotated sibling of #3058).
		{"arr2d-struct-unannot", `struct P { x: i32 } function main(): i32 { let a = [[P { x: 1 }], [P { x: 2 }, P { x: 3 }]]; let n = 0; for row in a { for p in row { n = n + p.x; } } return n; }`, 6},
		{"arr2d-enum-unannot", `enum C { A, B } function main(): i32 { let a = [[C.A], [C.B, C.A]]; let n = 0; for row in a { for c in row { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } } return n; }`, 4},
		// Unannotated 2D Option-array literal — element opt-type inferred by
		// recursing into the inner literal (#3074, depth-2 sibling of #3027).
		{"arr2d-opt-unannot-i32", `function main(): i32 { let a = [[Some(1)], [Some(2), None]]; let n = 0; for row in a { for o in row { match (o) { Some(x) => { n = n + x; }, None => {} } } } return n; }`, 3},
		{"arr2d-opt-unannot-string", `function main(): i32 { let a = [[Some("ab")], [None, Some("c")]]; let n = 0; for row in a { for o in row { match (o) { Some(s) => { n = n + s.len(); }, None => {} } } } return n; }`, 3},
		// A 2D-array param — the param setup marks it is_arrarr and extracts the
		// innermost struct/enum element type for the nested foreach (#3064).
		{"arr2d-param-i32", `function sum(a: i32[][]): i32 { let n = 0; for row in a { for x in row { n = n + x; } } return n; } function main(): i32 { return sum([[1, 2], [3]]); }`, 6},
		{"arr2d-param-struct", `struct P { x: i32 } function sum(a: P[][]): i32 { let n = 0; for row in a { for p in row { n = n + p.x; } } return n; } function main(): i32 { return sum([[P { x: 5 }], [P { x: 6 }]]); }`, 11},
		{"arr2d-param-enum", `enum C { A, B } function cnt(a: C[][]): i32 { let n = 0; for row in a { for c in row { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } } return n; } function main(): i32 { return cnt([[C.A], [C.B, C.A]]); }`, 4},
		// A function returning a struct array — the element struct type is recorded
		// so a[i].field / foreach over the result resolve (#3037).
		{"ret-struct-arr-index", `struct P { x: i32 } function mk(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { let a = mk(); return a[0].x + a[1].x; }`, 3},
		{"ret-struct-arr-foreach", `struct P { x: i32 } function mk(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { let a = mk(); let n = 0; for p in a { n = n + p.x; } return n; }`, 3},
		{"ret-struct-arr-method", `struct P { x: i32 } function (p: P) g(): i32 { return p.x * 2; } function mk(): P[] { return [P { x: 3 }, P { x: 4 }]; } function main(): i32 { let a = mk(); let n = 0; for p in a { n = n + p.g(); } return n; }`, 14},
		{"ret-struct-arr-twofield", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 10 }, P { x: 2, y: 20 }]; } function main(): i32 { let a = mk(); return a[1].x + a[1].y; }`, 22},
		// A method returning a struct array (#3042, method sibling of #3037) — the
		// call-site marks the result is_arr so a[i].field / foreach resolve.
		{"method-ret-struct-arr-index", `struct P { x: i32 } struct B { n: i32 } function (b: B) items(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { let b = B { n: 5 }; let a = b.items(); return a[0].x + a[1].x; }`, 3},
		{"method-ret-struct-arr-foreach", `struct P { x: i32 } struct B { n: i32 } function (b: B) items(): P[] { return [P { x: b.n }, P { x: b.n + 1 }]; } function main(): i32 { let b = B { n: 5 }; let a = b.items(); let s = 0; for p in a { s = s + p.x; } return s; }`, 11},
		{"method-ret-struct-arr-method", `struct P { x: i32 } struct B { n: i32 } function (p: P) g(): i32 { return p.x * 2; } function (b: B) items(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { let b = B { n: 5 }; let a = b.items(); let s = 0; for p in a { s = s + p.g(); } return s; }`, 6},
		// A struct-/enum-array enum payload — the match binding marks the slot
		// is_arr + element type so ps[i].field / foreach resolve (#3046).
		{"enum-payload-struct-arr-index", `struct P { x: i32 } enum E { Items(P[]), Nil } function f(e: E): i32 { match (e) { Items(ps) => { return ps[0].x; }, Nil => { return 0; } } return 0; } function main(): i32 { return f(Items([P { x: 7 }])); }`, 7},
		{"enum-payload-struct-arr-foreach", `struct P { x: i32 } enum E { Items(P[]), Nil } function f(e: E): i32 { match (e) { Items(ps) => { let n = 0; for p in ps { n = n + p.x; } return n; }, Nil => { return 0; } } return 0; } function main(): i32 { return f(Items([P { x: 3 }, P { x: 4 }])); }`, 7},
		{"enum-payload-enum-arr", `enum C { A, B } enum E { Cells(C[]), Nil } function f(e: E): i32 { match (e) { Cells(cs) => { match (cs[0]) { C.A => { return 1; }, C.B => { return 2; } } }, Nil => { return 0; } } return 0; } function main(): i32 { return f(Cells([C.B])); }`, 2},
		{"tuple-local-destructure", `function main(): i32 { let t = ("ab", 10); let (s, n) = t; return s.len() + n; }`, 12},
		{"tuple-3-destructure", `function main(): i32 { let (a, b, c) = (1, 2, 3); return a * 100 + b * 10 + c; }`, 123},
		{"tuple-4-destructure", `function main(): i32 { let (a, b, c, d) = (1, 2, 3, 4); return a + b + c + d; }`, 10},
		{"tuple-3-mixed-destructure", `function main(): i32 { let (s, n, m) = ("hi", 5, 10); return s.len() + n + m; }`, 17},
		{"tuple-3-local-destructure", `function main(): i32 { let t = (7, 8, 9); let (a, b, c) = t; return a + b * c; }`, 79},
		{"tuple-3-ret-destructure", `function three(): (i32, i32, i32) { return (4, 5, 6); } function main(): i32 { let (a, b, c) = three(); return a * 100 + b * 10 + c; }`, 200},
		{"struct-ret-basic", `struct P { x: i32, y: i32 } function mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { let p = mk(); return p.x * 10 + p.y; }`, 34},
		{"struct-ret-param", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a * 2 }; } function main(): i32 { let p = mk(5); return p.x + p.y; }`, 15},
		{"struct-ret-direct-field", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { return mk(7).x + mk(7).y; }`, 15},
		{"f64-struct-field-read", `struct P { x: f64, n: i32 } function main(): i32 { let p = P { x: 3.5, n: 2 }; let y: f64 = p.x + 1.0; if (y > 4.0) { return p.n + 5; } return 0; }`, 7},
		{"f64-struct-field-mixed", `struct V { a: i32, d: f64, b: i32 } function main(): i32 { let v = V { a: 1, d: 2.5, b: 3 }; let s: f64 = v.d * 2.0; if (s > 4.0) { return v.a + v.b; } return 0; }`, 4},
		{"f64-struct-field-write", `struct P { x: f64, n: i32 } function main(): i32 { let p = P { x: 1.0, n: 4 }; p = P { ...p, x: 5.5 }; if (p.x > 5.0) { return p.n + 1; } return 0; }`, 5},
		{"method-struct-ret", `struct P { x: i32, y: i32 } struct B { } function (b: B) mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { let b = B { }; let p = b.mk(); return p.x * 10 + p.y; }`, 34},
		{"method-struct-ret-direct", `struct P { x: i32, y: i32 } struct B { base: i32 } function (b: B) mk(): P { return P { x: b.base, y: b.base + 1 }; } function main(): i32 { let b = B { base: 5 }; return b.mk().x + b.mk().y; }`, 11},
		{"method-tuple-ret", `struct B { } function (b: B) pair(): (i32, i32) { return (3, 4); } function main(): i32 { let b = B { }; let (x, y) = b.pair(); return x * 10 + y; }`, 34},
		{"method-tuple-ret-str", `struct B { } function (b: B) pair(): (string, i32) { return ("hi", 5); } function main(): i32 { let b = B { }; let (s, n) = b.pair(); return s.len() + n; }`, 7},
		{"tuple-struct-elem-ret", `struct P { x: i32, y: i32 } function mk(): (P, i32) { return (P { x: 3, y: 4 }, 9); } function main(): i32 { let (p, n) = mk(); return p.x * 10 + p.y + n; }`, 43},
		{"tuple-struct-elem-dotn", `struct P { x: i32, y: i32 } function mk(): (P, i32) { return (P { x: 6, y: 7 }, 2); } function main(): i32 { let t = mk(); return t.0.x + t.0.y + t.1; }`, 15},
		{"f64-add-cmp", `function main(): i32 { let a: f64 = 1.5; let b: f64 = 2.25; let c: f64 = a + b; if (c > 3.0) { return 7; } return 0; }`, 7},
		{"f64-sub-mul-eq", `function main(): i32 { let a: f64 = 10.0; let b: f64 = 4.0; let c: f64 = (a - b) * 2.0; if (c == 12.0) { return 9; } return 0; }`, 9},
		{"f64-div-lt", `function main(): i32 { let a: f64 = 7.0; let c: f64 = a / 2.0; if (c < 4.0) { return 5; } return 0; }`, 5},
		{"f64-neg-ge", `function main(): i32 { let a: f64 = 3.0; let b: f64 = -a; if (b <= 0.0) { return 4; } return 0; }`, 4},
		{"f64-chain", `function main(): i32 { let x: f64 = 1.0; let y: f64 = 2.0; let z: f64 = 3.0; let r: f64 = x + y * z; if (r >= 7.0) { return 6; } if (r >= 6.0) { return 8; } return 0; }`, 6},
		{"f64-param-ret", `function scale(x: f64, k: f64): f64 { return x * k; } function main(): i32 { let r: f64 = scale(3.0, 2.5); if (r > 7.0) { return 7; } return 0; }`, 7},
		{"f64-ret-unannotated", `function mk(): f64 { return 4.5; } function main(): i32 { let a = mk(); let b = mk(); let c: f64 = a + b; if (c > 8.0) { return 9; } return 0; }`, 9},
		{"f64-call-both-operands", `function one(): f64 { return 2.0; } function two(): f64 { return 3.0; } function main(): i32 { let p: f64 = one() * two(); if (p == 6.0) { return 5; } return 0; }`, 5},
		{"f64-cast-to-int", `function main(): i32 { let x: f64 = 7.9; return x as i32; }`, 7},
		{"f64-cast-from-int", `function main(): i32 { let n: i32 = 3; let x: f64 = (n as f64) + 0.5; if (x > 3.0) { return 8; } return 0; }`, 8},
		{"f64-cast-roundtrip", `function main(): i32 { let n: i32 = 10; let x: f64 = n as f64; let y: f64 = x / 4.0; return y as i32; }`, 2},
		{"f64-cast-mixed-param", `function f(a: f64, n: i32): f64 { return a + (n as f64); } function main(): i32 { let r: f64 = f(1.5, 2); return r as i32; }`, 3},
		{"map-i32-len3", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }`, 3},
		{"map-i32-overwrite", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); return m.len(); }`, 2},
		{"map-i32-loop", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(4); let i = 0; while (i < 5) { m = m.insert(i, i*10); i = i + 1; } return m.len(); }`, 5},
		{"map-str-keys", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(4); m = m.insert("a", 1); m = m.insert("bb", 2); m = m.insert("a", 9); return m.len(); }`, 2},
		{"map-get-hit", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(7)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 42},
		{"map-get-miss", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(999)) { Some(v) => { return v; }, None => { return 5; } } return 9; }`, 5},
		{"map-has", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(4); m = m.insert(1, 1); let r = 0; if (m.has(1)) { r = r + 1; } if (m.has(2)) { r = r + 10; } return r; }`, 1},
		{"map-get-strkey", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(4); m = m.insert("hi", 11); match (m.get("hi")) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 11},
		{"map-get-or-hit", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(7, 0); }`, 42},
		{"map-get-or-miss", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(999, 5); }`, 5},
		{"map-get-or-strhit", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(4); m = m.insert("hi", 11); return m.get_or("hi", 0); }`, 11},
		{"map-get-or-strmiss", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(4); m = m.insert("hi", 11); return m.get_or("no", 7); }`, 7},
		{"map-keys-sum", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); let ks: i32[] = m.keys(); let s = 0; let i = 0; while (i < ks.len()) { s = s + ks[i]; i = i + 1; } return s; }`, 6},
		{"map-values-sum", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); let vs: i32[] = m.values(); let s = 0; let i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s; }`, 60},
		{"map-forkv-values", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); let s = 0; for (k, v) in m { s = s + v; } return s; }`, 60},
		{"map-forkv-keys", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); let s = 0; for (k, v) in m { s = s + k; } return s; }`, 6},
		{"map-forkv-pair", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.insert(2, 3); m = m.insert(3, 4); let s = 0; for (k, v) in m { s = s + k * v; } return s; }`, 20},
		{"map-forkv-strkey", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert("ab", 1); m = m.insert("cde", 2); let s = 0; for (k, v) in m { s = s + k.len() + v; } return s; }`, 8},
		// m.without(k) -> (Map, existed); arm64 reuses asm_arm64's __fern_map_delete (#2926).
		{"map-without-len", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); let (m2, e) = m.without(1); return m2.len(); }`, 1},
		{"map-without-existed", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); let (m2, e) = m.without(1); if (e) { return 1; } return 0; }`, 1},
		{"map-without-survivor", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); let (m2, e) = m.without(1); return m2.get_or(2, 0); }`, 20},
		{"map-without-strkey", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert("a", 1); m = m.insert("b", 2); let (m2, e) = m.without("a"); return m2.len() + m2.get_or("b", 0); }`, 3},
		{"i64-cmp", `function main(): i32 { let x: i64 = 5000000000; let y: i64 = 4000000000; if (x > y) { return 7; } return 0; }`, 7},
		{"i64-add", `function main(): i32 { let a: i64 = 3000000000; let b: i64 = 3000000000; let c: i64 = a + b; if (c > 5000000000) { return 11; } return 0; }`, 11},
		{"i64-mul", `function main(): i32 { let a: i64 = 100000; let b: i64 = 100000; let c: i64 = a * b; if (c > 4000000000) { return 5; } return 0; }`, 5},
		{"i64-sub-neg", `function main(): i32 { let a: i64 = 1000000000; let b: i64 = 2000000000; let c: i64 = a - b; if (c < 0) { return 9; } return 0; }`, 9},
		{"i64-loop", `function main(): i32 { let s: i64 = 0; let i: i32 = 0; while (i < 100000) { s = s + 100000; i = i + 1; } if (s > 4000000000) { return 13; } return 0; }`, 13},
		{"and-true", `function main(): i32 { let x = 5; if (x > 0 && x < 10) { return 7; } return 0; }`, 7},
		{"and-false", `function main(): i32 { let x = 15; if (x > 0 && x < 10) { return 7; } return 0; }`, 0},
		{"or-true", `function main(): i32 { let x = 15; if (x < 0 || x > 0) { return 3; } return 0; }`, 3},
		{"and-or-nest", `function main(): i32 { let a = 1; let b = 0; let c = 5; if (a > 0 && b > 0 || c > 0) { return 9; } return 0; }`, 9},
		{"and-not-operand", `function main(): i32 { let x = 5; if (!(x > 10) && x > 0) { return 4; } return 0; }`, 4},
		{"and-bool-vars", `function main(): i32 { let f = 5 > 3; let g = 2 > 8; if (f && !g) { return 6; } return 0; }`, 6},
		{"strcmp-lt", `function main(): i32 { let a = "apple"; let b = "banana"; if (a < b) { return 7; } return 0; }`, 7},
		{"strcmp-gt", `function main(): i32 { let a = "banana"; let b = "apple"; if (a > b) { return 3; } return 0; }`, 3},
		{"strcmp-le-eq", `function main(): i32 { let a = "abc"; let b = "abc"; if (a <= b) { return 5; } return 0; }`, 5},
		{"strcmp-prefix", `function main(): i32 { let a = "ab"; let b = "abc"; if (a < b) { return 9; } return 0; }`, 9},
		{"strcmp-ge-false", `function main(): i32 { let a = "a"; let b = "b"; if (a >= b) { return 11; } return 0; }`, 0},
		{"while-break", `function main(): i32 { let s = 0; let i = 0; while (i < 10) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }`, 10},
		{"while-continue", `function main(): i32 { let s = 0; let i = 0; while (i < 10) { i = i + 1; if (i % 2 == 1) { continue; } s = s + i; } return s; }`, 30},
		{"while-break-nested", `function main(): i32 { let t = 0; let i = 0; while (i < 3) { let j = 0; while (j < 5) { if (j == 2) { break; } t = t + j; j = j + 1; } i = i + 1; } return t; }`, 3},
		{"while-break-deep-if", `function main(): i32 { let s = 0; let i = 0; while (i < 10) { if (i > 3) { if (i == 4) { break; } } s = s + i; i = i + 1; } return s; }`, 6},
		{"cast-widen", `function main(): i32 { let n = 100000; let x: i64 = n as i64; let y: i64 = x * x; if (y > 4000000000) { return 5; } return 0; }`, 5},
		{"cast-narrow", `function main(): i32 { let big: i64 = 5000000007; let lo = (big as i32); return lo % 100; }`, 11},
		{"cast-mixed", `function main(): i32 { let base: i64 = 4000000000; let i = 5; let s: i64 = base + (i as i64); if (s > 4000000000) { return 7; } return 0; }`, 7},
		{"cast-roundtrip", `function main(): i32 { let n = 42; let x: i64 = n as i64; return (x as i32); }`, 42},
		{"call-8-args", `function add8(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a+b+c+d+e+f+g+h; } function main(): i32 { return add8(1,2,3,4,5,6,7,8); }`, 36},
		{"call-7-args-order", `function f(a:i32,b:i32,c:i32,d:i32,e:i32,g:i32,h:i32):i32 { return a - b - c - d - e - g - h; } function main(): i32 { return f(100,1,2,3,4,5,6); }`, 79},
		{"method-7-args", `struct P { base: i32 } function (p: P) sum7(a:i32,b:i32,c:i32,d:i32,e:i32,f:i32,g:i32): i32 { return p.base + a+b+c+d+e+f+g; } function main(): i32 { let p = P { base: 10 }; return p.sum7(1,2,3,4,5,6,7); }`, 38},
		{"i64-param", `function dbl(x: i64): i64 { return x * 2; } function main(): i32 { let r: i64 = dbl(3000000000); if (r > 5000000000) { return 7; } return 0; }`, 7},
		{"i64-return", `function big(): i64 { return 4000000000; } function main(): i32 { let x: i64 = big() + 1000000000; if (x > 4000000000) { return 5; } return 0; }`, 5},
		{"i64-param-mixed", `function f(a: i64, b: i32): i64 { return a + (b as i64); } function main(): i32 { let r: i64 = f(4000000000, 5); if (r > 4000000000) { return 9; } return 0; }`, 9},
		{"i64-return-recursion", `function pow2(n: i32): i64 { if (n <= 0) { return 1; } return pow2(n - 1) * 2; } function main(): i32 { if (pow2(33) > 4000000000) { return 13; } return 0; }`, 13},
		{"i64-div", `function main(): i32 { let a: i64 = 12000000000; let b: i64 = 4; let c: i64 = a / b; if (c > 2000000000) { return 7; } return 0; }`, 7},
		{"i64-rem", `function main(): i32 { let a: i64 = 12000000007; let r = (a % 10) as i32; return r; }`, 7},
		{"i64-div-trunc", `function main(): i32 { let a: i64 = 10000000000; let c: i64 = a / 3; if (c > 3000000000) { return 5; } return 0; }`, 5},
		{"i64-div-signed", `function main(): i32 { let a: i64 = 0 - 12000000000; let c: i64 = a / 4; if (c < 0) { return 9; } return 0; }`, 9},
		{"arr-slice", `function main(): i32 { let a = [10, 20, 30, 40, 50]; let b = a[1:4]; return b[0] + b[2]; }`, 60},
		{"arr-slice-len", `function main(): i32 { let a = [1, 2, 3, 4, 5]; let b = a[1:4]; return b.len(); }`, 3},
		{"arr-slice-strarr", `function main(): i32 { let a = ["x", "yy", "zzz", "w"]; let b = a[1:3]; return b[0].len() + b[1].len(); }`, 5},
		{"arr-slice-full", `function main(): i32 { let a = [5, 10, 15, 20]; let b = a[0:2]; return b[0] + b[1]; }`, 15},
		{"enum-unit", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(B); }`, 9},
		{"enum-three", `enum Shape { Circle(i32), Square(i32), Empty } function area(s: Shape): i32 { match (s) { Circle(r) => { return r + 1; }, Square(w) => { return w * 2; }, Empty => { return 7; } } return 99; } function main(): i32 { return area(Circle(4)) + area(Square(5)) + area(Empty); }`, 22},
		{"enum-wildcard", `enum E { A(i32), B, C } function f(e: E): i32 { match (e) { A(n) => { return n; }, _ => { return 100; } } return 0; } function main(): i32 { return f(B); }`, 100},
		// Byte-source builtins (issue #2747) — deterministic shapes only.
		{"random-bytes-len", `function main(): i32 { return random_bytes(8).len(); }`, 8},
		{"random-bytes-len-var", `function main(): i32 { let s: u8[] = random_bytes(13); return s.len(); }`, 13},
		{"as-bytes-len", `function main(): i32 { let b: [u8] = "ABC".as_bytes(); return b.len(); }`, 3},
		{"as-bytes-vals", `function main(): i32 { let b: [u8] = "ABC".as_bytes(); return (b[0] as i32) + (b[1] as i32) + (b[2] as i32); }`, 198},
		{"bytes-vals", `import "std/string";
function main(): i32 { let b: u8[] = "AB".bytes(); return (b[0] as i32) + (b[1] as i32); }`, 131},
		{"as-bytes-heap", `function main(): i32 { let b: [u8] = "ABCDEFGHIJ".as_bytes(); return b.len() + (b[9] as i32); }`, 84},
		// A struct-valued if-/match-EXPRESSION binding (lifted to a `__lam_N`
		// whose return type is inferred from its struct-literal body, so the
		// `__lam_N()` call site recovers the struct type for `.field` / method
		// dispatch). The legacy AST path also handles these, so they run in the
		// differential gate.
		{"struct-if-expr-field", `struct P { x: i32, y: i32 } function main(): i32 { let p = if (true) { P{x:1,y:2} } else { P{x:3,y:4} }; return p.x + p.y; }`, 3},
		{"struct-match-expr-field", `struct P { x: i32, y: i32 } function main(): i32 { let p = match (1) { 1 => P{x:10,y:2}, _ => P{x:3,y:4} }; return p.x + p.y; }`, 12},
		{"struct-if-expr-direct-field", `struct P { x: i32, y: i32 } function main(): i32 { return (if (true) { P{x:7,y:2} } else { P{x:3,y:4} }).x; }`, 7},
		{"struct-if-expr-method", `struct P { x: i32, y: i32 } function (p: P) sum(): i32 { return p.x + p.y; } function main(): i32 { let p = if (true) { P{x:1,y:2} } else { P{x:3,y:4} }; return p.sum(); }`, 3},
		// An enum-valued if-/match-EXPRESSION binding stays an inline IIFE (its
		// variant constructors read as captures, so lift_lambdas leaves it as
		// ExprLambda); expr_enum_type sees through the IIFE so the bound local
		// types as the enum and a method call on it dispatches to <Enum>.<method>.
		{"enum-if-expr-method", `enum Shape { Circle(i32), Square(i32) } function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } return 0; } function main(): i32 { let s = if (true) { Circle(2) } else { Square(3) }; return s.area(); }`, 12},
		{"enum-match-expr-method", `enum Shape { Circle(i32), Square(i32) } function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } return 0; } function main(): i32 { let s = match (1) { 1 => Circle(2), _ => Square(3) }; return s.area(); }`, 12},
		{"enum-unit-if-expr-method", `enum Color { Red, Green, Blue } function (c: Color) code(): i32 { match (c) { Red => { return 1; }, Green => { return 2; }, Blue => { return 3; } } return 0; } function main(): i32 { let c = if (false) { Red } else { Green }; return c.code(); }`, 2},
		// A NESTED struct-valued if-/match-EXPRESSION binding: each inner branch
		// is itself lifted to a `__lam_M`, so fn_inferred_struct_ret recurses
		// through the `__lam` chain to the innermost struct literal.
		{"struct-nested-if-expr", `struct P { x: i32, y: i32 } function main(): i32 { let p = if (true) { if (false) { P{x:1,y:2} } else { P{x:5,y:6} } } else { P{x:3,y:4} }; return p.x + p.y; }`, 11},
		{"struct-match-then-if-expr", `struct P { x: i32, y: i32 } function main(): i32 { let p = match (1) { 1 => if (true) { P{x:4,y:5} } else { P{x:0,y:0} }, _ => P{x:3,y:4} }; return p.x + p.y; }`, 9},
		// A struct-returning USER function called in each if-/match-expression
		// branch: the lifted `__lam`'s leaf is a call to `mk`, so the struct type
		// is read from `mk`'s declared return type.
		{"struct-fncall-if-expr", `struct P { x: i32, y: i32 } function mk(v: i32): P { return P{x:v, y:v+1}; } function main(): i32 { let p = if (true) { mk(5) } else { mk(2) }; return p.x + p.y; }`, 11},
		{"struct-fncall-match-expr", `struct P { x: i32, y: i32 } function mk(v: i32): P { return P{x:v, y:v+1}; } function main(): i32 { let p = match (2) { 2 => mk(10), _ => mk(0) }; return p.x + p.y; }`, 21},
		// An if-/match-expression binding whose first branch CALLS an
		// Option/Result-returning function (`if (c) { mkO(7) } else { Some(0) }`):
		// the leaf is a call, so the bound local's opt-type is recovered from the
		// callee's registered return type, letting a later `match (o)` lower.
		{"opt-fncall-if-expr", `function mkO(v: i32): Option[i32] { return Some(v); } function main(): i32 { let o = if (true) { mkO(7) } else { Some(0) }; match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 7},
		{"result-fncall-if-expr", `function div(a: i32, b: i32): Result[i32, i32] { if (b == 0) { return Err(1); } return Ok(a / b); } function main(): i32 { let r = if (true) { div(20, 4) } else { Err(9) }; match (r) { Ok(n) => { return n; }, Err(e) => { return e; } } return 0; }`, 5},
		// A string-CONCAT branch in a value-position if-/match-expression: the
		// lifted `__lam` carries a default i32 ret_type (the desugar doesn't infer
		// `string + string : string`), so before str_ret_fns_of's body-inference a
		// `.len()` on the result mis-dispatched to the array path — a silent
		// miscompile (returned 56, not 4). These assert the IR value directly (the
		// AST==IR differential is blind to a both-paths-wrong case).
		{"str-concat-if-expr-direct", `function main(): i32 { return (if (true) { "ab" + "cd" } else { "x" }).len(); }`, 4},
		{"str-concat-if-expr-var", `function main(): i32 { let s = if (true) { "ab" + "cd" } else { "x" }; return s.len(); }`, 4},
		{"str-concat-if-expr-else", `function main(): i32 { let s = if (false) { "x" } else { "ab" + "cdef" }; return s.len(); }`, 6},
		{"str-concat-match-expr", `function main(): i32 { return (match (1) { 1 => "aa" + "bb", _ => "z" }).len(); }`, 4},
		// A string-ARRAY-valued if-/match-expression: the lifted `__lam` carries a
		// default i32 ret_type, so the binding was mis-treated as a scalar and the
		// 8-byte string elements were read at i32 width — a silent miscompile
		// (`xs[i].len()` returned 1, not the element length). array_ret_fns +
		// strarr_ret_fns_of now infer the array element type from the __lam body.
		// Asserted against the IR value directly (the AST==IR gate was blind).
		{"strarr-if-expr-direct-elem", `function main(): i32 { return (if (true) { ["a", "bb"] } else { ["ccc"] })[1].len(); }`, 2},
		{"strarr-if-expr-var-elems", `function main(): i32 { let xs = if (true) { ["a", "bb"] } else { ["ccc"] }; return xs[0].len() + xs[1].len(); }`, 3},
		{"strarr-if-expr-forin", `function main(): i32 { let xs = if (true) { ["a", "bb", "ccc"] } else { ["z"] }; let t = 0; for s in xs { t = t + s.len(); } return t; }`, 6},
		{"strarr-match-expr-elems", `function main(): i32 { let xs = match (1) { 1 => ["hi", "yo"], _ => ["x"] }; return xs[0].len() + xs[1].len() + xs.len(); }`, 6},
		// f64-ARRAY-valued if-/match-expression: same lifted-__lam scalar miscompile
		// as the string-array case (8-byte f64 elements read at i32 width). #3224.
		{"f64arr-if-expr-elem", `function main(): i32 { let xs = if (true) { [1.5, 2.5] } else { [3.5] }; return xs.len() * 10 + (xs[1] as i32); }`, 22},
		{"f64arr-if-expr-forin", `function main(): i32 { let xs = if (true) { [1.5, 2.5, 3.0] } else { [9.0] }; let t = 0.0; for x in xs { t = t + x; } return t as i32; }`, 7},
		{"f64arr-match-expr-elem", `function main(): i32 { return (match (1) { 1 => [2.5, 4.5], _ => [0.0] })[1] as i32; }`, 4},
		// A lifted if-/match-expression whose branch CALLS an opt-returning fn with
		// a `None`/call other branch is lambda-lifted (None is a keyword) — its
		// `__lam` opt return type is inferred from the body so a later `match (o)`
		// recovers the payload (#3236 sibling for the lifted shape).
		{"opt-fncall-if-none-else", `function mkO(v: i32): Option[i32] { return Some(v); } function main(): i32 { let o = if (true) { mkO(7) } else { None }; match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 7},
		{"opt-fncall-if-call-else", `function mkO(v: i32): Option[i32] { return Some(v); } function main(): i32 { let o = if (true) { mkO(7) } else { mkO(2) }; match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 7},
		{"result-fncall-if-err-else", `function div(a: i32, b: i32): Result[i32, i32] { if (b == 0) { return Err(1); } return Ok(a / b); } function main(): i32 { let r = if (true) { div(20, 4) } else { Err(9) }; match (r) { Ok(n) => { return n; }, Err(e) => { return e; } } return 0; }`, 5},
		// A struct-ARRAY-valued if-/match-expression (literal, or a P[]-returning
		// call in the branch): the lifted __lam's element struct type is inferred
		// so `ps[i].field` / `for p in ps { p.method() }` resolve. P[] sibling of
		// the string[]/f64[] array fixes (#3224).
		{"structarr-if-expr-elem", `struct P { x: i32, y: i32 } function main(): i32 { let ps = if (true) { [P{x:1,y:2}, P{x:3,y:4}] } else { [P{x:0,y:0}] }; return ps[1].x + ps[1].y; }`, 7},
		{"structarr-if-expr-forin-method", `struct P { x: i32, y: i32 } function (p: P) s(): i32 { return p.x + p.y; } function main(): i32 { let ps = if (true) { [P{x:1,y:2}, P{x:3,y:4}] } else { [P{x:0,y:0}] }; let t = 0; for p in ps { t = t + p.s(); } return t; }`, 10},
		{"structarr-match-expr-elem", `struct P { x: i32, y: i32 } function main(): i32 { let ps = match (1) { 1 => [P{x:5,y:6}], _ => [P{x:0,y:0}] }; return ps[0].x * 10 + ps[0].y; }`, 56},
		{"structarr-fncall-if-expr", `struct P { x: i32, y: i32 } function mk(): P[] { return [P{x:5,y:6}]; } function main(): i32 { let ps = if (true) { mk() } else { mk() }; return ps[0].x + ps[0].y; }`, 11},
		// A Map-typed STRUCT FIELD receiver (`c.m.get_or(k, d)`): map-method
		// dispatch resolves the map type from the field declaration, not just a
		// local slot, so reads through a struct field lower (the field read pushes
		// the map pointer). #map-struct-field.
		{"map-field-get_or", `import "core/map";
struct Cache { m: Map[i32, i32], hits: i32 } function main(): i32 { let c = Cache{m: Map { 5: 50, 7: 70 }, hits: 1}; return c.m.get_or(5, 0) + c.m.get_or(7, 0) + c.hits; }`, 121},
		{"map-field-method", `import "core/map";
struct Cfg { table: Map[string, i32] } function (c: Cfg) lookup(k: string): i32 { return c.table.get_or(k, 0); } function main(): i32 { let c = Cfg{table: Map { "a": 3, "b": 4 }}; return c.lookup("a") + c.lookup("b"); }`, 7},
		{"map-field-has-len", `import "core/map";
struct Cache { m: Map[string, i32] } function main(): i32 { let c = Cache{m: Map { "a": 1, "b": 2 }}; let t = 0; if (c.m.has("a")) { t = t + c.m.len(); } return t; }`, 2},
		// A Map[K,V] PARAMETER recovers its map type, so map methods on the param
		// (`m.get_or(k, d)`) dispatch as map ops (the local-annotation path already
		// did this; params lacked it). #map-param.
		{"map-param-get_or", `import "core/map";
function total(m: Map[i32, i32]): i32 { return m.get_or(1, 0) + m.get_or(2, 0); } function main(): i32 { let m: Map[i32, i32] = Map { 1: 10, 2: 20 }; return total(m); }`, 30},
		{"map-param-string-key", `import "core/map";
function look(m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); } function main(): i32 { let m: Map[string, i32] = Map { "x": 7 }; return look(m, "x"); }`, 7},
		// Iterating a Map-typed struct FIELD's keys()/values() (`for k in
		// c.m.keys()`): the foreach resolves the map type from the field decl,
		// like the map-method dispatch. #map-struct-field-iter.
		{"map-field-keys-forin", `import "core/map";
struct Cfg { m: Map[i32, i32] } function main(): i32 { let c = Cfg{m: Map { 1: 10, 2: 20, 3: 30 }}; let t = 0; for k in c.m.keys() { t = t + c.m.get_or(k, 0); } return t; }`, 60},
		{"map-field-values-forin", `import "core/map";
struct Cfg { m: Map[string, i32] } function main(): i32 { let c = Cfg{m: Map { "a": 3, "b": 4 }}; let t = 0; for v in c.m.values() { t = t + v; } return t; }`, 7},
		// An UNANNOTATED binding from a map-returning function (`let m = build()`):
		// the `map_ret_fns` registry recovers the slot's map type so `m.get_or(...)`
		// dispatches without a `: Map[K,V]` annotation. #3317.
		{"map-ret-fn-binding", `import "core/map";
function build(): Map[i32, i32] { return Map { 1: 5, 2: 6 }; } function main(): i32 { let m = build(); return m.get_or(1, 0) + m.get_or(2, 0); }`, 11},
		{"map-ret-method-binding", `import "core/map";
struct Reg { base: i32 } function (r: Reg) table(): Map[i32, i32] { return Map { 1: r.base, 2: r.base + 1 }; } function main(): i32 { let reg = Reg{base: 10}; let m = reg.table(); return m.get_or(1, 0) + m.get_or(2, 0); }`, 21},
		// A Map TUPLE element (`(Map { … }, x)`): the map-literal element is admitted
		// to tuple construction (a leak-only pointer slot) with a `Map[K,V]` tag, so
		// `t.0.get_or(…)` dispatches as a map op, a rebind `let m = t.0` recovers the
		// map type, and a string-VALUE element's get_or tracks as a string. The
		// self-host AST path also mishandled this (returned 4), so these pin the
		// absolute IR value. #3317.
		{"map-tuple-elem-get_or", `import "core/map";
function main(): i32 { let t = (Map { 1: 10 }, 5); return t.0.get_or(1, 0) + t.1; }`, 15},
		{"map-tuple-elem-rebind", `import "core/map";
function main(): i32 { let t = (Map { 1: 10 }, 5); let m = t.0; return m.get_or(1, 0) + t.1; }`, 15},
		{"map-tuple-elem-string-val", `import "core/map";
function main(): i32 { let t = (Map { 1: "abcd" }, 5); return t.0.get_or(1, "z").len() + t.1; }`, 9},
		// An ARRAY of maps (`let ms = [Map { … }, …]`): the array slot carries the
		// ELEMENT map type (the map sibling of the struct-array element-type
		// overload), so `ms[i].get_or(…)` dispatches as a map op, a rebind
		// `let m = ms[i]` recovers the map type, an annotated `Map[K,V][]` binding
		// works, and a string-VALUE element's get_or tracks as a string. The
		// self-host AST path also mishandled this (link error on `i32.get_or`), so
		// these pin the absolute IR value. #3317.
		{"map-array-elem-get_or", `import "core/map";
function main(): i32 { let ms = [Map { 1: 10 }, Map { 1: 20 }]; return ms[0].get_or(1, 0) + ms[1].get_or(1, 0); }`, 30},
		{"map-array-elem-rebind", `import "core/map";
function main(): i32 { let ms = [Map { 1: 10 }, Map { 1: 20 }]; let m = ms[1]; return m.get_or(1, 0) + ms[0].get_or(1, 0); }`, 30},
		{"map-array-elem-annotated", `import "core/map";
function main(): i32 { let ms: Map[i32, i32][] = [Map { 1: 10 }]; return ms[0].get_or(1, 0); }`, 10},
		{"map-array-elem-string-val", `import "core/map";
function main(): i32 { let ms = [Map { 1: "abcd" }]; return ms[0].get_or(1, "z").len(); }`, 4},
		// A struct-ARRAY tuple element (`([P { .. }], x)`): the element's recorded
		// `P[]` tuple tag lets `t.0[i].field` / `t.0[i].method()` recover the
		// element struct type (the array sibling of the struct-field-array case).
		// The struct-array element constructs as a leak-only pointer slot. The
		// self-host AST path also mishandled the indexed field read, so these pin
		// the absolute IR value. #3353.
		{"tuple-structarr-elem-field", `struct P { x: i32 } function main(): i32 { let t = ([P{x:5}], 3); return t.0[0].x + t.1; }`, 8},
		{"tuple-structarr-elem-multi", `struct P { x: i32, y: i32 } function main(): i32 { let t = ([P{x:5,y:6}, P{x:7,y:8}], 100); return t.0[0].x + t.0[1].y + t.1; }`, 113},
		{"tuple-structarr-elem-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function main(): i32 { let t = ([P{x:5}], 3); return t.0[0].dbl() + t.1; }`, 13},
		// A string[] tuple element (`(["a","b"], x)`): the element's recorded
		// `string[]` tuple tag lets `t.0[i]` read as a string (`.len()`) and a
		// rebind `let xs = t.0` recover the string[] type. The element is a heap
		// pointer in one slot; the self-host AST path mishandled it (and refused it
		// at construction), so these pin the absolute IR value. #3353.
		{"tuple-strarr-elem-len", `function main(): i32 { let t = (["ab","cd"], 3); return t.0[1].len() + t.1; }`, 5},
		{"tuple-strarr-elem-two", `function main(): i32 { let t = (["ab","cd"], 3); return t.0[0].len() + t.0[1].len() + t.1; }`, 7},
		{"tuple-strarr-elem-rebind", `function main(): i32 { let t = (["ab","cd"], 3); let xs = t.0; return xs[1].len() + t.1; }`, 5},
		// An f64[] tuple element (`([1.5, 2.5], x)`): the element's recorded `f64[]`
		// tuple tag lets `t.0[i]` read an 8-byte f64 (arr_get width 64) and a rebind
		// `let xs = t.0` recover the f64[] type. The element is a heap pointer in one
		// slot; the self-host AST path mishandled it (and refused it at
		// construction), so these pin the absolute IR value. #3353.
		{"tuple-f64arr-elem-index", `function main(): i32 { let t = ([1.5, 2.5], 3); return (t.0[1] as i32) + t.1; }`, 5},
		{"tuple-f64arr-elem-sum", `function main(): i32 { let t = ([1.5, 2.5, 4.0], 10); let s = 0.0; let i = 0; while (i < 3) { s = s + t.0[i]; i = i + 1; } return (s as i32) + t.1; }`, 18},
		{"tuple-f64arr-elem-rebind", `function main(): i32 { let t = ([1.5, 2.5], 3); let xs = t.0; return (xs[1] as i32) + t.1; }`, 5},
		// An UNANNOTATED i64 array literal binding (`let xs = [10 as i64, …]`): the
		// first element is i64-wide, so the slot is inferred i64[] and lowers the
		// same as the annotated `let xs: i64[] = …` (arr_make_i64 + 8-byte element
		// reads) instead of bailing to AST. #3353.
		{"i64arr-unannot-index", `function main(): i32 { let xs = [10 as i64, 20 as i64]; let q: i64 = xs[0] + xs[1]; return q as i32; }`, 30},
		{"i64arr-unannot-while", `function main(): i32 { let xs = [1 as i64, 2 as i64, 3 as i64]; let s: i64 = 0 as i64; let i = 0; while (i < 3) { s = s + xs[i]; i = i + 1; } return s as i32; }`, 6},
		{"i64arr-unannot-forin", `function main(): i32 { let xs = [10 as i64, 20 as i64]; let s: i64 = 0 as i64; for x in xs { s = s + x; } return s as i32; }`, 30},
		{"random-i32-varies", `function main(): i32 { let a: i32 = random_i32(); let b: i32 = random_i32(); if (a == 0) { return 0; } if (a == b) { return 1; } return 7; }`, 7},
		// A draw is a SIGNED i32 — see the x86-64 sibling: the hand-asm loaded
		// with a zero-extending `ldr w0`, so `< 0` was dead code.
		{"random-i32-signed", `function main(): i32 { let neg: i32 = 0; let i: i32 = 0; while (i < 200) { if (random_i32() < 0) { neg = neg + 1; } i = i + 1; } if (neg == 0) { return 1; } if (neg > 60) { if (neg < 140) { return 7; } } return 2; }`, 7},
		{"random-bytes-byte-range", `function main(): i32 { let s: u8[] = random_bytes(4); let x: i32 = s[0] as i32; if (x >= 0) { if (x <= 255) { return 1; } } return 0; }`, 1},
		// random_bytes over MORE than one chunk (#2649). The Fern helper fills
		// the buffer in <= 256-byte pieces — getentropy's per-call ceiling on
		// Darwin, and on Linux the fix for getrandom short-filling a big n. The
		// chunk address is __raw_addr(p, off), so a truncated heap pointer (what
		// plain `p + off` produces on arm64, where i32 arithmetic sign-extends
		// back to 32 bits) EFAULTs and leaves the tail zeroed -> 2.
		// write -> read -> remove -> confirm-gone, through the Fern fs leaves
		// (#2649). An absolute /tmp path works under qemu-user too, since the
		// syscalls pass through to the host. The read and write are LOOPS over
		// __raw_addr now, so a wrong chunk address shows up as a length or
		// content mismatch rather than a silent partial file.
		// temp_dir + env, the last two leaves that needed only constants (#2649):
		// mkdirat into a monotonic_ns-named /tmp dir, then a write/read/remove
		// inside it, then a set and an unset environment variable. PATH is
		// present under qemu-user too (it forwards the host environment).
		// stat through the Fern helper on the 4-arg fstatat family (#2649):
		// /tmp is a directory on every host and under qemu-user, and the
		// missing sibling exercises the errno -> NotFound classification. A
		// wrong st_mode offset reads st_nlink or st_dev and misclassifies.
		// xs.reverse() / xs.concat(ys) through the Fern helpers (#2649). The
		// string[] cases are the ones that matter: both copy raw 8-byte SLOTS,
		// so an element that is a box POINTER has to survive untouched — the
		// i32[] cases alone would pass even if the copy narrowed to 32 bits.
		// The source array is re-read afterwards to catch a helper that
		// consumed its borrowed parameter.
		{"arr-slice-fern", `function main(): i32 { let a: i32[] = [10,20,30,40,50]; let b = a[1:4]; if (b.len() != 3) { return 1; } if (b[0] != 20) { return 2; } if (b[2] != 40) { return 3; } if (a[1] != 20) { return 4; } if (a.len() != 5) { return 5; } if (a[0:0].len() != 0) { return 6; } if (a[5:5].len() != 0) { return 7; } let f = a[0:5]; if (f.len() != 5) { return 8; } if (f[4] != 50) { return 9; } let s: string[] = ["aa","bbb","c"]; let t = s[1:3]; if (t.len() != 2) { return 10; } if (t[0] != "bbb") { return 11; } if (t[1] != "c") { return 12; } if (s[0] != "aa") { return 13; } return 33; }`, 33},
		{"clocks-fern", `function main(): i32 { let t0: i64 = monotonic_ns(); let w: i64 = now_unix_ms(); let n: i64 = now_ns(); let t1: i64 = monotonic_ns(); if (t1 < t0) { return 1; } if (w < (1577836800000 as i64)) { return 2; } if (w > (4102444800000 as i64)) { return 3; } let nms: i64 = n / (1000000 as i64); let d: i64 = nms - w; if (d < (0 as i64)) { d = 0 - d; } if (d > (5000 as i64)) { return 4; } if (t0 > (4102444800000000000 as i64)) { return 5; } return 71; }`, 71},
		{"clocks-no-heap", `function main(): i32 { let t: i64 = monotonic_ns(); if (t > (0 as i64)) { return 55; } return 1; }`, 55},
		{"dirs-fern", `function main(): i32 { match (temp_dir("dirp")) { Ok(d) => { match (write_file(d + "/a.txt", "aa")) { Ok(_) => {}, Err(_) => { return 1; } } match (write_file(d + "/b.txt", "bb")) { Ok(_) => {}, Err(_) => { return 2; } } match (read_dir(d)) { Ok(ns) => { if (ns.len() != 2) { return 3; } let seen: i32 = 0; let i: i32 = 0; while (i < ns.len()) { if (ns[i] == "a.txt") { seen = seen + 1; } if (ns[i] == "b.txt") { seen = seen + 2; } if (ns[i] == ".") { return 4; } if (ns[i] == "..") { return 5; } i = i + 1; } if (seen != 3) { return 6; } }, Err(_) => { return 7; } } match (remove_dir_all(d)) { Ok(_) => {}, Err(_) => { return 8; } } match (read_dir(d)) { Ok(_) => { return 9; }, Err(_) => {} } match (remove_dir_all(d)) { Ok(_) => {}, Err(_) => { return 10; } } return 83; }, Err(_) => { return 11; } } }`, 83},
		{"rmdirall-on-file", `function main(): i32 { match (temp_dir("rmf")) { Ok(d) => { match (write_file(d + "/f.txt", "z")) { Ok(_) => {}, Err(_) => { return 1; } } match (remove_dir_all(d + "/f.txt")) { Ok(_) => {}, Err(_) => { return 2; } } match (read_file(d + "/f.txt")) { Ok(_) => { return 3; }, Err(_) => {} } match (remove_dir_all(d)) { Ok(_) => {}, Err(_) => { return 4; } } return 84; }, Err(_) => { return 5; } } }`, 84},
		{"arr-slice-oob-trap", `function main(): i32 { let a: i32[] = [1,2,3]; let k: i32 = 3; return a[0:k + 2].len(); }`, 134},
		{"arr-slice-reversed-trap", `function main(): i32 { let a: i32[] = [1,2,3]; let k: i32 = 0; return a[k + 2:k + 1].len(); }`, 134},
		{"stat-dir-and-missing", `function main(): i32 { match (stat("/tmp")) { Ok(d) => { if (d.is_file) { return 1; } if (!d.is_dir) { return 2; } match (stat("/tmp/fern_no_such_path_xyz")) { Ok(_) => { return 3; }, Err(e) => { match (e) { NotFound(_) => { return 37; }, _ => { return 4; } } } } }, Err(_) => { return 5; } } }`, 37},
		{"tempdir-env", `function main(): i32 { match (temp_dir("fernrt")) { Ok(d) => { if (d.len() < 12) { return 1; } match (write_file(d + "/f.txt", "abc")) { Ok(_) => {}, Err(_) => { return 2; } } match (read_file(d + "/f.txt")) { Ok(c) => { if (c != "abc") { return 3; } }, Err(_) => { return 4; } } match (remove_file(d + "/f.txt")) { Ok(_) => {}, Err(_) => { return 5; } } match (env("PATH")) { Some(v) => { if (v.len() == 0) { return 6; } }, None => { return 7; } } match (env("FERN_DEFINITELY_UNSET_XYZ")) { Some(_) => { return 8; }, None => {} } return 39; }, Err(_) => { return 9; } } }`, 39},
		{"fs-roundtrip", `function main(): i32 { let p: string = "/tmp/fern_fsrt_a64.txt"; match (write_file(p, "hello world")) { Ok(_) => {}, Err(_) => { return 1; } } match (read_file(p)) { Ok(c) => { if (c != "hello world") { return 2; } }, Err(_) => { return 3; } } match (remove_file(p)) { Ok(_) => {}, Err(_) => { return 4; } } match (read_file(p)) { Ok(_) => { return 5; }, Err(_) => {} } return 41; }`, 41},
		{"read-file-bytes", `function main(): i32 { let p: string = "/tmp/fern_rfb_a64.bin"; match (write_file(p, "AZ")) { Ok(_) => {}, Err(_) => { return 1; } } match (read_file_bytes(p)) { Ok(b) => { if (b.len() != 2) { return 2; } if ((b[0] as i32) != 65) { return 3; } if ((b[1] as i32) != 90) { return 4; } }, Err(_) => { return 5; } } match (remove_file(p)) { Ok(_) => {}, Err(_) => { return 6; } } match (read_file_bytes(p)) { Ok(_) => { return 7; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 8; } } } } return 45; }`, 45},
		{"read-file-invalid-utf8", `function main(): i32 { let p: string = "/tmp/fern_rfu_a64.bin"; let raw: u8[] = [255 as u8, 254 as u8]; match (write_file(p, string_from_bytes_unchecked(raw))) { Ok(_) => {}, Err(_) => { return 1; } } match (read_file(p)) { Ok(_) => { return 2; }, Err(e) => { match (e) { InvalidUtf8(q) => { if (q != p) { return 3; } }, _ => { return 4; } } } } match (read_file_bytes(p)) { Ok(b) => { if (b.len() != 2) { return 5; } if ((b[0] as i32) != 255) { return 6; } }, Err(_) => { return 7; } } let _r: Result[void, IoError] = remove_file(p); return 46; }`, 46},
		{"random-bytes-chunked", `function main(): i32 { let b: u8[] = random_bytes(600); if (b.len() != 600) { return 1; } let v: i32 = 0; let i: i32 = 256; while (i < 600) { v = v | (b[i] as i32); i = i + 1; } if (v == 0) { return 2; } let w: i32 = 0; let j: i32 = 0; while (j < 256) { w = w | (b[j] as i32); j = j + 1; } if (w == 0) { return 3; } return 42; }`, 42},
		{"uuid-v4", uuidV4Program, 0},
		// Range-for through the arm64 self-host IR path (#2699). The legacy
		// AST arm64 emitter has no range desugar, so these run in the IR-only
		// gate. Half-open `..` and inclusive `..=` (closed interval: it visits
		// HIGH, and ends even when HIGH is i32's maximum, #10359).
		{"range-sum", `function main(): i32 { let s = 0; for i in 0..5 { s = s + i; } return s; }`, 10},
		{"rangei-sum", `function main(): i32 { let s = 0; for i in 0..=5 { s = s + i; } return s; }`, 15},
		{"rangei-single", `function main(): i32 { let c = 0; for i in 5..=5 { c = c + 1; } return c; }`, 1},
		{"rangei-reversed", `function main(): i32 { let c = 9; for i in 9..=3 { c = c + 1; } return c; }`, 9},
		{"rangei-continue", `function main(): i32 { let s = 0; for i in 0..=10 { if (i == 3) { continue; } s = s + i; } return s; }`, 52},
		{"rangei-i32-max", `function main(): i32 { let c = 0; for i in 2147483646..=2147483647 { c = c + 1; } return c; }`, 2},
		{"rangei-i32-max-break", `function main(): i32 { let c = 0; for i in 2147483645..=2147483647 { c = c + 1; if (i == 2147483646) { break; } } return c; }`, 2},
		{"rangei-i32-min", `function main(): i32 { let lo: i32 = -2147483647 - 1; let c = 0; for i in lo..=lo { c = c + 1; } return c; }`, 1},
		{"range-i32-min", `function main(): i32 { let lo: i32 = -2147483647 - 1; let c = 0; for i in lo..(lo + 1) { c = c + 1; } return c; }`, 1},
		// Multi-payload variant binds: a `Pt(x, y)` arm binds EVERY payload
		// field (struct_get at successive indices), not just the first. The
		// legacy AST emitter binds only field 0, so these run in the IR-only
		// gate against the native interp's value.
		{"match-multi-bind", `enum P { Pt(i32, i32), Origin } function f(p: P): i32 { match (p) { Pt(x, y) => { return x * y; }, Origin => { return 0; } } return 0; } function main(): i32 { return f(Pt(6, 7)); }`, 42},
		{"match-multi-bind-three", `enum T { Tri(i32, i32, i32), Empty } function f(t: T): i32 { match (t) { Tri(a, b, c) => { return a + b * c; }, Empty => { return 0; } } return 0; } function main(): i32 { return f(Tri(1, 2, 3)); }`, 7},
		{"match-multi-bind-mixed", `enum M { Kv(string, i32), None2 } function f(m: M): i32 { match (m) { Kv(k, v) => { return k.len() + v; }, None2 => { return 0; } } return 0; } function main(): i32 { return f(Kv("hello", 5)); }`, 10},
		{"match-multi-bind-skip", `enum P { Pt(i32, i32), Origin } function f(p: P): i32 { match (p) { Pt(_, y) => { return y; }, Origin => { return 0; } } return 0; } function main(): i32 { return f(Pt(6, 7)); }`, 7},
		// Multi-payload variant arm in a value-position match-EXPRESSION
		// (`return match (e) { Pair(a, b) => a + b }`): lower_iife_match now admits
		// an arm with extra_bindings when every payload is i32 (#3193). The legacy
		// AST emitter mishandles this (segfaults), so these run in the IR-only gate.
		{"match-expr-multi-bind", `enum E { Pair(i32, i32) } function main(): i32 { let e = E.Pair(3, 4); return match (e) { Pair(a, b) => a + b }; }`, 7},
		{"match-expr-multi-2var", `enum E { Pair(i32, i32), Single(i32) } function main(): i32 { let e = E.Single(9); return match (e) { Pair(a, b) => a + b, Single(x) => x }; }`, 9},
		{"match-expr-multi-wildcard", `enum E { Pair(i32, i32) } function main(): i32 { let e = E.Pair(3, 4); return match (e) { Pair(_, b) => b }; }`, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRun(t, tc.src); got != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
