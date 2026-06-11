package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostAsmIRPath is the differential gate for integrating the stack IR
// into the PRODUCTION x86-64 backend. The asm_ir_run driver's `-ir` flag, when
// the module is fully i32-eligible, emits via the IR path (asm_ir.emit_module_ir:
// AST -> stack IR -> asm, ABI-identical to asm.fern's output); otherwise it uses
// the unchanged AST backend (asm.emit_module). This compiles each program BOTH
// ways and asserts identical exit codes — proving the IR path is behaviour-
// equivalent to the production AST path, the rollout prerequisite
// (docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md §3) before the IR path can become the
// default. asm.fern and the asm_run driver are UNCHANGED, so the byte-identical
// self-bootstrap and the ~50 asm_run harnesses are unaffected.
//
// Slice 16 eligibility is pure i32 functions (no params, no user calls, no
// arrays), so single-function i32 programs exercise the IR path; multi-
// function / array programs fall back to AST under `-ir` and must still match.
func TestSelfHostAsmIRPath(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the asm_run driver once via the production x86-64 backend.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	// emitAndRun pipes src to the driver (optionally with `-ir`), assembles
	// the emitted asm, runs it, returns the inner exit code.
	emitAndRun := func(t *testing.T, src string, ir bool) int {
		t.Helper()
		args := []string{}
		if ir {
			args = append(args, "-ir")
		}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed (ir=%v) for %q: %v", ir, src, err)
		}
		tag := "ast"
		if ir {
			tag = "ir"
		}
		innerAsm := filepath.Join(dir, tag+"_inner.s")
		innerBin := filepath.Join(dir, tag+"_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc (ir=%v): %v\n%s\n--- asm ---\n%s", ir, err, out, emitted)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally (ir=%v) for %q", ir, src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		// Pure i32 single functions -> exercised by the IR path under -ir.
		{"const", "function main(): i32 { return 42; }"},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }"},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }"},
		{"locals", "function main(): i32 { var x = 2 + 3 * 4; var y = x - 5; return y * 2; }"},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }"},
		{"modulo", "function main(): i32 { return 23 % 5; }"},
		{"division", "function main(): i32 { return 84 / 2; }"},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }"},
		{"shift", "function main(): i32 { return 1 << 4; }"},
		{"compare", "function main(): i32 { return 5 < 10; }"},
		{"unary-not", "function main(): i32 { return !(5 > 10); }"},
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }"},
		{"if-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }"},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }"},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }"},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }"},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }"},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }"},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }"},
		// Params + direct calls (slice 17) -> now IR-eligible; the IR path must
		// match the AST path through asm.fern's stack-arg ABI.
		{"call-one-arg", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return inc(41); }"},
		{"call-two-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }"},
		{"call-three-args", "function f(a: i32, b: i32, c: i32): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(1, 2, 3); }"},
		{"call-arg-order", "function sub(a: i32, b: i32): i32 { return a - b; } function main(): i32 { return sub(50, 8); }"},
		{"call-nested-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(add(10, 20), add(5, 7)); }"},
		{"recursion-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }"},
		{"recursion-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }"},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }"},
		{"call-in-loop", "function sq(x: i32): i32 { return x * x; } function main(): i32 { var i = 1; var s = 0; while (i <= 4) { s = s + sq(i); i = i + 1; } return s; }"},
		{"compute-via-call", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }"},
		// Within-function i32 arrays (slice 18) -> IR path with the freestanding
		// allocator + Perceus RC; values must match the AST array runtime.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }"},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }"},
		{"arr-expr-elems", "function main(): i32 { var x = 4; var a = [x, x * 2, x + 100]; return a[1] + a[2]; }"},
		{"arr-set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }"},
		{"arr-set-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }"},
		{"arr-len", "function main(): i32 { var a = [1, 2, 3, 4]; return a.len(); }"},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }"},
		{"arr-alias", "function main(): i32 { var a = [10, 20, 30]; var b = a; return b[0] + b[2] + a.len(); }"},
		// Cross-function arrays (slice 19): borrowed array params + array
		// returns (move-on-return). Whole module is IR-eligible, so caller and
		// callee share irlower's layout — the move/borrow paths run end-to-end.
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var arr = [10, 20, 30]; return sum(arr); }"},
		{"arr-param-borrow-noreuse", "function len_of(a: i32[]): i32 { return a.len(); } function main(): i32 { var arr = [3, 4, 5]; var n = len_of(arr); var z = [9, 9, 9]; return arr[0] + arr[2] + n; }"},
		{"arr-return-move", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); var y = [1, 1, 1]; return x[0] + x[2]; }"},
		{"arr-return-then-mutate", "function make(): i32[] { var a = [1, 2, 3]; return a; } function main(): i32 { var x = make(); x[1] = 99; return x[0] + x[1] + x[2]; }"},
		{"arr-param-two", "function pick(a: i32[], b: i32[]): i32 { return a[0] + b[1]; } function main(): i32 { var p = [1, 2]; var q = [10, 20]; return pick(p, q); }"},
		// Array-slot reassignment (Perceus retain-new + cow-guarded release-old
		// in irlower's StmtVar/StmtAssign): `ys = xs` retains xs and releases
		// ys's prior buffer; a fresh-literal / loop-rebind reassignment releases
		// the overwritten buffer. The IR + AST RC accounting must agree.
		{"arr-reassign-alias", "function main(): i32 { var xs = [1, 2, 3]; var ys = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }"},
		{"arr-reassign-source-live", "function main(): i32 { var xs = [7, 8]; var ys = [0, 0]; ys = xs; return xs[1] + ys[1]; }"},
		{"arr-reassign-fresh", "function main(): i32 { var xs = [1, 2]; xs = [9, 9, 9]; return xs[2]; }"},
		{"arr-rebind-loop", "function main(): i32 { var s = 0; var i = 0; while (i < 4) { var r = [i, i * 2, i * 3]; s = s + r[2]; i = i + 1; } return s; }"},
		// Strings (within-function + string params): literal + .len(), concat
		// (+), equality (==/!=). irlower tracks string-ness (local_is_str) to
		// pick str_len / str_concat / str_eq over the array/i32 ops; the IR path
		// reuses asm.fern's 16-byte `[data@0,len@8]` box + __fern_str_concat/_eq
		// helpers, so exit codes must match the AST path exactly.
		{"str-len", `function main(): i32 { var s = "hello"; return s.len(); }`},
		{"str-index-local", `function main(): i32 { var s = "hello"; return s[0]; }`},
		{"str-index-loop", `function main(): i32 { var s = "abc"; var sum = 0; var i = 0; while (i < 3) { sum = sum + s[i]; i = i + 1; } return sum % 200; }`},
		{"str-index-param", `function first(s: string): i32 { return s[0]; } function main(): i32 { return first("Z"); }`},
		{"str-slice-len", `function main(): i32 { var s = "hello"; var t = s[1:4]; return t.len(); }`},
		{"str-slice-idx0", `function main(): i32 { var s = "hello"; var t = s[1:4]; return t[0]; }`},
		{"str-slice-chain", `function main(): i32 { return "hello"[1:4][2]; }`},
		{"str-literal-len", `function main(): i32 { return "world!".len(); }`},
		{"str-empty-len", `function main(): i32 { var s = ""; return s.len(); }`},
		{"str-concat-len", `function main(): i32 { var a = "ab"; var b = "cde"; var c = a + b; return c.len(); }`},
		{"str-concat-direct", `function main(): i32 { return ("foo" + "bar").len(); }`},
		{"str-concat-empty", `function main(): i32 { var a = ""; var b = "xyz"; var c = a + b; return c.len(); }`},
		{"str-concat-chain", `function main(): i32 { var a = "a"; var b = "bb"; var c = "ccc"; return (a + b + c).len(); }`},
		{"str-eq-true", `function main(): i32 { var a = "hi"; var b = "hi"; if (a == b) { return 7; } return 0; }`},
		{"str-eq-false", `function main(): i32 { var a = "hi"; var b = "ho"; if (a == b) { return 7; } return 9; }`},
		{"str-eq-difflen", `function main(): i32 { var a = "hi"; var b = "hii"; if (a == b) { return 1; } return 2; }`},
		{"str-ne-true", `function main(): i32 { var a = "hi"; var b = "ho"; if (a != b) { return 3; } return 0; }`},
		{"str-ne-false", `function main(): i32 { var a = "x"; var b = "x"; if (a != b) { return 3; } return 5; }`},
		{"str-concat-eq", `function main(): i32 { var a = "foo"; var b = "foobar"; if (a + "bar" == b) { return 11; } return 0; }`},
		{"str-param-len", `function slen(s: string): i32 { return s.len(); } function main(): i32 { var x = "abcd"; return slen(x); }`},
		{"str-param-concat", `function jn(a: string, b: string): i32 { return (a + b).len(); } function main(): i32 { return jn("xx", "yyy"); }`},
		{"str-param-eq", `function same(a: string, b: string): i32 { if (a == b) { return 1; } return 0; } function main(): i32 { return same("k", "k"); }`},
		// A string-RETURNING function isn't IR-lowered yet (irlower bails), so the
		// whole module falls back to AST under -ir; must still match.
		// String-returning functions now route through the IR (str_ret_fns tracks the
		// result as a string; the box just leaks). Param + concat + return too.
		{"str-returning", `function greet(): string { return "hi"; } function main(): i32 { var s = greet(); return s.len(); }`},
		{"str-returning-concat", `function shout(s: string): string { return s + "!"; } function main(): i32 { var g = shout("hey"); return g.len(); }`},
		{"str-returning-inline", `function tag(): string { return "abcd"; } function main(): i32 { return tag().len(); }`},
		// String-typed struct/enum fields (leak-safe, no RC).
		{"struct-str-field", `struct Token { text: string, kind: i32 } function main(): i32 { var t = Token { text: "hello", kind: 7 }; return t.text.len() + t.kind; }`},
		{"struct-str-method", `struct N { s: string } function (n: N) sz(): i32 { return n.s.len(); } function main(): i32 { var x = N { s: "abcd" }; return x.sz(); }`},
		{"enum-str-payload", `enum T { Word(string), Eof } function g(t: T): i32 { match (t) { Word(w) => { return w.len(); }, Eof => { return 3; } } return 0; } function main(): i32 { return g(Word("hello")) + g(Eof); }`},
		// Scalar-array struct fields (i32[], fresh-literal, leak-only).
		{"struct-arr-field", `struct Buf { data: i32[], n: i32 } function main(): i32 { var b = Buf { data: [10, 20, 30], n: 3 }; var s = 0; var i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; }`},
		{"struct-arr-param", `struct Buf { data: i32[], n: i32 } function sum(b: Buf): i32 { var s = 0; var i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; } function main(): i32 { var b = Buf { data: [5, 10, 15], n: 3 }; return sum(b); }`},
		{"struct-arr-extract", `struct Buf { data: i32[] } function main(): i32 { var b = Buf { data: [7, 8, 9] }; var a = b.data; return a[0] + a[2]; }`},
		// Typed string[] arrays (literals/indexing/params/loop; elements leak).
		{"strarr-index", `function main(): i32 { var names = ["foo", "bar", "hello"]; return names[0].len() + names[2].len(); }`},
		{"strarr-param", `function f(names: string[]): i32 { return names[0].len(); } function main(): i32 { return f(["abcd"]); }`},
		{"strarr-loop", `function main(): i32 { var names = ["a", "bb", "ccc"]; var s = 0; var i = 0; while (i < 3) { s = s + names[i].len(); i = i + 1; } return s; }`},
		// string[]-returning functions (move-on-return; call site element-types
		// the result as string[] via strarr_ret_fns, so xs[i] is a string).
		{"strarr-ret", `function names(): string[] { return ["a", "bb", "ccc"]; } function main(): i32 { var xs = names(); return xs[1].len(); }`},
		{"strarr-ret-direct-index", `function names(): string[] { return ["a", "bb", "ccc"]; } function main(): i32 { return names()[2].len(); }`},
		{"strarr-ret-len", `function names(): string[] { var a = ["x", "yy"]; return a; } function main(): i32 { var xs = names(); return xs.len() + xs[1].len(); }`},
		{"strarr-ret-param", `function id(a: string[]): string[] { return a; } function main(): i32 { var xs = ["q", "ww", "eee"]; var ys = id(xs); return ys[1].len() + ys.len(); }`},
		{"strarr-ret-loop", `function names(): string[] { return ["a", "bb", "ccc", "dddd"]; } function main(): i32 { var xs = names(); var i = 0; var s = 0; while (i < xs.len()) { s = s + xs[i].len(); i = i + 1; } return s; }`},
		// Scalar-field structs (struct_make / struct_get, leak-only): literal +
		// field read, field-order independence, params, boolean fields.
		{"struct-lit-fields", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 3, y: 4 }; return p.x + p.y; }`},
		{"struct-field-order", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { y: 40, x: 2 }; return p.x + p.y; }`},
		{"struct-three-fields", `struct V { a: i32, b: i32, c: i32 } function main(): i32 { var v = V { a: 1, b: 2, c: 3 }; return v.a * 100 + v.b * 10 + v.c; }`},
		{"struct-param", `struct P { x: i32, y: i32 } function sum(p: P): i32 { return p.x + p.y; } function main(): i32 { var p = P { x: 30, y: 12 }; return sum(p); }`},
		{"struct-bool-field", `struct F { on: boolean, n: i32 } function main(): i32 { var f = F { on: true, n: 7 }; if (f.on) { return f.n; } return 0; }`},
		{"struct-in-loop", `struct P { x: i32, y: i32 } function main(): i32 { var s = 0; var i = 0; while (i < 4) { var p = P { x: i, y: i * 2 }; s = s + p.x + p.y; i = i + 1; } return s; }`},
		{"struct-update-one", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; var q = P { ...p, y: 9 }; return q.x + q.y; }`},
		{"struct-update-keeps-base", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 6 }; var q = P { ...p, x: 50 }; return p.x + q.x; }`},
		// Field mutation `p.x = v` (struct_set).
		{"field-mutate", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p.x = 40; return p.x + p.y; }`},
		{"field-mutate-loop", `struct C { n: i32 } function main(): i32 { var c = C { n: 0 }; var i = 0; while (i < 5) { c.n = c.n + i; i = i + 1; } return c.n; }`},
		{"field-mutate-alias", `struct P { x: i32 } function main(): i32 { var p = P { x: 1 }; var q = p; q.x = 9; return p.x; }`},
		// Tuples (tuple_make / tuple_get; no shape slot, numeric .N access) + 2-elem destructure.
		{"tuple-access", `function main(): i32 { var t = (3, 4); return t.0 + t.1; }`},
		{"tuple-three", `function main(): i32 { var t = (1, 2, 3); return t.0 * 100 + t.1 * 10 + t.2; }`},
		{"tuple-destructure", `function main(): i32 { var (a, b) = (40, 2); return a + b; }`},
		{"tuple-expr-elems", `function main(): i32 { var x = 5; var t = (x * 2, x + 1); return t.0 + t.1; }`},
		// Methods (receiver = arg 0, static dispatch).
		{"method-field", `struct P { x: i32 } function (p: P) get(): i32 { return p.x; } function main(): i32 { var p = P { x: 42 }; return p.get(); }`},
		{"method-with-arg", `struct B { v: i32 } function (b: B) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = B { v: 4 }; return x.scale(3); }`},
		{"method-same-name-two-types", `struct A { n: i32 } struct B { n: i32 } function (a: A) get(): i32 { return a.n + 1; } function (b: B) get(): i32 { return b.n + 100; } function main(): i32 { var a = A { n: 5 }; var b = B { n: 5 }; return a.get() + b.get(); }`},
		// Enums + match (variant construction + variant_is dispatch + payload bind).
		{"enum-payload", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(A(21)); }`},
		{"match-guard-fallthrough", `enum E { Pos(i32), Neg(i32), Zero } function f(e: E): i32 { match (e) { Pos(n) when n > 10 => { return 1; }, Pos(n) => { return 2; }, _ => { return 3; } } return 0; } function main(): i32 { return f(Pos(20)) * 100 + f(Pos(5)) * 10 + f(Zero); }`},
		{"match-guard-mixed", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) when n > 3 => { return n * 2; }, A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return f(A(5)) + f(A(1)) + f(B); }`},
		{"match-guard-wildcard", `enum E { V(i32) } function f(e: E): i32 { match (e) { _ when false => { return 5; }, V(n) => { return n; } } return 0; } function main(): i32 { return f(V(42)); }`},
		{"opt-some-none", `function classify(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (classify(n)) { Some(_) => { return 1; }, None => { return 0; } } return 9; } function main(): i32 { return f(5) * 10 + f(0); }`},
		{"opt-ok-err", `function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(n); } function f(n: i32): i32 { match (chk(n)) { Ok(_) => { return 7; }, Err(_) => { return 3; } } return 9; } function main(): i32 { return f(2) * 10 + f(0); }`},
		{"opt-none-first", `function g(n: i32): Option[i32] { if (n > 5) { return Some(n); } return None; } function f(n: i32): i32 { match (g(n)) { None => { return 4; }, Some(_) => { return 8; } } return 0; } function main(): i32 { return f(9) + f(1); }`},
		{"opt-bind-some", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }`},
		{"opt-bind-result", `function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n * 2); } return Err(n + 50); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e; } } return 0; } function main(): i32 { return f(3) + f(0); }`},
		{"opt-bind-guard", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) when x > 10 => { return 1; }, Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(20) * 100 + f(5) * 10 + f(0); }`},
		{"opt-bind-string", `function name(n: i32): Option[string] { if (n > 0) { return Some("hello"); } return None; } function f(n: i32): i32 { match (name(n)) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; } function main(): i32 { return f(1); }`},
		{"opt-bind-result-strerr", `function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err("fail"); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }`},
		{"opt-bind-local", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { var r = g(n); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }`},
		{"opt-bind-local-strerr", `function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err("oops"); } function f(n: i32): i32 { var r = chk(n); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }`},
		{"opt-bind-param", `function f(o: Option[i32]): i32 { match (o) { Some(x) => { return x * 2; }, None => { return 0; } } return 0; } function main(): i32 { return f(Some(21)) + f(None); }`},
		{"struct-field-nested", `struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { var b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }`},
		{"struct-field-deep", `struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { var o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }`},
		{"struct-field-bind", `struct Point { x: i32, y: i32 } struct Box { p: Point, tag: i32 } function main(): i32 { var b = Box { p: Point { x: 7, y: 8 }, tag: 3 }; var pp = b.p; return pp.x * pp.y + b.tag; }`},
		{"forin-i32", `function main(): i32 { var xs = [10, 20, 30, 40]; var sum = 0; for x in xs { sum = sum + x; } return sum; }`},
		{"forin-i32-param", `function total(xs: i32[]): i32 { var s = 0; for v in xs { s = s + v; } return s; } function main(): i32 { var a = [1, 2, 3, 4, 5]; return total(a); }`},
		{"forin-nested", `function main(): i32 { var xs = [1, 2, 3]; var t = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }`},
		{"forin-string", `function main(): i32 { var ss: string[] = ["a", "bb", "ccc", "dddd"]; var n = 0; for s in ss { n = n + s.len(); } return n; }`},
		{"enum-struct-payload", `struct BinExpr { left: i32, right: i32 } enum Expr { Lit(i32), Binary(BinExpr) } function eval(e: Expr): i32 { match (e) { Lit(n) => { return n; }, Binary(b) => { return b.left + b.right; } } return 0; } function main(): i32 { return eval(Lit(7)) + eval(Binary(BinExpr { left: 3, right: 9 })); }`},
		{"enum-struct-payload-guard", `struct P { x: i32, y: i32 } enum Shape { Rect(P), Dot } function area(s: Shape): i32 { match (s) { Rect(p) when p.x > 0 => { return p.x * p.y; }, _ => { return 0; } } return 0; } function main(): i32 { return area(Rect(P { x: 4, y: 5 })); }`},
		{"enum-struct-payload-nested", `struct Inner { v: i32 } struct Mid { i: Inner } enum E { A(Mid), B } function f(e: E): i32 { match (e) { A(m) => { return m.i.v; }, B => { return 9; } } return 0; } function main(): i32 { return f(A(Mid { i: Inner { v: 42 } })) + f(B); }`},
		{"enum-arr-payload-len", `enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len(); }, Empty => { return 0; } } return 0; } function main(): i32 { return f(Items([10, 20, 30])) * 10 + f(Empty); }`},
		{"enum-arr-payload-forin", `enum E { Items(i32[]), Empty } function sum(e: E): i32 { match (e) { Items(xs) => { var t = 0; for x in xs { t = t + x; } return t; }, Empty => { return 0; } } return 0; } function main(): i32 { return sum(Items([5, 10, 15])); }`},
		{"enum-arr-payload-alias", `enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len() + xs[0]; }, Empty => { return 0; } } return 0; } function main(): i32 { var a = [7, 8, 9]; return f(Items(a)); }`},
		{"enum-strarr-payload-len", `enum E { Words(string[]), None } function f(e: E): i32 { match (e) { Words(w) => { return w.len(); }, None => { return 0; } } return 0; } function main(): i32 { return f(Words(["a", "bb", "ccc"])) * 10 + f(None); }`},
		{"enum-strarr-payload-forin", `enum E { Words(string[]), None } function f(e: E): i32 { match (e) { Words(w) => { var n = 0; for s in w { n = n + s.len(); } return n; }, None => { return 0; } } return 0; } function main(): i32 { return f(Words(["a", "bb", "ccc"])); }`},
		{"struct-strarr-field-len", `struct Doc { lines: string[] } function nl(d: Doc): i32 { return d.lines.len(); } function main(): i32 { var d = Doc { lines: ["x", "y", "z"] }; return nl(d); }`},
		{"struct-strarr-field-index", `struct Doc { lines: string[] } function f(d: Doc): i32 { return d.lines[1].len(); } function main(): i32 { var d = Doc { lines: ["a", "bb", "ccc"] }; return f(d); }`},
		{"tuple-str-i32-dotn", `function main(): i32 { var t = ("hello", 7); return t.0.len() + t.1; }`},
		{"tuple-str-i32-destructure", `function main(): i32 { var (a, b) = ("world", 3); return a.len() + b; }`},
		{"tuple-struct-dotn", `struct P { x: i32, y: i32 } function main(): i32 { var t = (P { x: 4, y: 5 }, 2); return t.0.x * t.0.y + t.1; }`},
		{"tuple-local-destructure", `function main(): i32 { var t = ("ab", 10); var (s, n) = t; return s.len() + n; }`},
		{"map-i32-len3", `function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }`},
		{"map-i32-overwrite", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); return m.len(); }`},
		{"map-i32-loop", `function main(): i32 { var m: Map[i32, i32] = map_new(4); var i = 0; while (i < 5) { m = m.insert(i, i*10); i = i + 1; } return m.len(); }`},
		{"map-str-keys", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert("a", 1); m = m.insert("bb", 2); m = m.insert("a", 9); return m.len(); }`},
		{"enum-unit", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(B); }`},
		{"enum-three", `enum Shape { Circle(i32), Square(i32), Empty } function area(s: Shape): i32 { match (s) { Circle(r) => { return r + 1; }, Square(w) => { return w * 2; }, Empty => { return 7; } } return 99; } function main(): i32 { return area(Circle(4)) + area(Square(5)) + area(Empty); }`},
		{"enum-wildcard", `enum E { A(i32), B, C } function f(e: E): i32 { match (e) { A(n) => { return n; }, _ => { return 100; } } return 0; } function main(): i32 { return f(B); }`},
		// Out of the IR subset -> falls back to the AST emitter under -ir; must
		// still match (proves the fallback path is intact).
		{"method-falls-back", "struct P { x: i32 } pub function (p: P) get(): i32 { return p.x; } function main(): i32 { var p = P { x: 42 }; return p.get(); }"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			astCode := emitAndRun(t, tc.src, false)
			irCode := emitAndRun(t, tc.src, true)
			if astCode != irCode {
				t.Errorf("AST-path vs IR-path mismatch for %q: AST=%d IR=%d", tc.name, astCode, irCode)
			}
		})
	}
}
