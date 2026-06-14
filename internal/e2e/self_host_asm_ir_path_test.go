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
		// Top-level `const` references — a bare ident naming a zero-arg fn lowers
		// to a call (the const's value), no longer bailing to AST (#2954).
		{"const-ref", "const LIMIT: i32 = 100; function main(): i32 { return LIMIT + 1; }"},
		{"const-loop-bound", "const N: i32 = 5; function main(): i32 { var s = 0; var i = 0; while (i < N) { s = s + i; i = i + 1; } return s; }"},
		{"const-two", "const A: i32 = 40; const B: i32 = 2; function main(): i32 { return A + B; }"},
		// Bare reference to a module function WITH params is a function VALUE
		// (a plain function pointer): `var f = namedfn; f(args)`, a fn-value as
		// a call argument, and a fn-value-returning function. Lowers to
		// const_func + the existing call_indirect path, no longer bailing.
		{"fnval-local", `function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var f = dbl; return f(21); }`},
		{"fnval-local-arg", `function dbl(n: i32): i32 { return n * 2; } function apply(f: (i32) => i32, n: i32): i32 { return f(n); } function main(): i32 { var g = dbl; return apply(g, 21); }`},
		{"fnval-two", `function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var f = inc; var g = dbl; return f(10) + g(10); }`},
		{"fnval-return", `function dbl(n: i32): i32 { return n * 2; } function getf(): (i32) => i32 { return dbl; } function main(): i32 { var g = getf(); return g(21); }`},
		// Calling a function-VALUE stored in a struct field (`h.f(args)`) — the
		// "fn"-typed field is a plain fn pointer, called via struct_get +
		// call_indirect (not a `<Type>.method` dispatch).
		{"fnval-struct-field", `struct H { f: (i32) => i32 } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var h = H { f: dbl }; return h.f(21); }`},
		{"fnval-struct-field-mixed", `struct H { f: (i32) => i32, n: i32 } function inc(n: i32): i32 { return n + 1; } function main(): i32 { var h = H { f: inc, n: 100 }; return h.f(h.n); }`},
		// No-capture LAMBDA as a struct-literal field value (#2994): the lambda is
		// hoisted to a top-level fn, so the field holds a function pointer — the
		// same shape a named-function field above lowers, and `b.f(args)` rides the
		// existing fn-value-field call path. Capturing lambda fields still bail.
		{"clo-struct-field", `struct Box { f: (i32) => i32 } function main(): i32 { var b = Box { f: function(x: i32): i32 { return x * 3; } }; return b.f(7); }`},
		{"clo-struct-field-mixed", `struct H { f: (i32) => i32, n: i32 } function main(): i32 { var h = H { f: function(x: i32): i32 { return x + 1; }, n: 100 }; return h.f(h.n); }`},
		{"clo-struct-field-2fn", `struct Ops { add1: (i32) => i32, dbl: (i32) => i32 } function main(): i32 { var o = Ops { add1: function(x: i32): i32 { return x + 1; }, dbl: function(x: i32): i32 { return x * 2; } }; return o.add1(10) + o.dbl(10); }`},
		// Calling an element of a function-value ARRAY inline (`fns[i](args)`):
		// a plain fn-pointer array element lowers to args + the element + call_
		// indirect (the local-bind form `var f = fns[i]; f()` already lowered).
		{"fnarr-elem-call", `function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var fns = [inc, dbl]; return fns[0](10) + fns[1](10); }`},
		{"fnarr-elem-call-loop", `function apply(fns: ((i32) => i32)[], n: i32): i32 { var s = 0; var i = 0; while (i < fns.len()) { s = s + fns[i](n); i = i + 1; } return s; } function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { return apply([inc, dbl], 10); }`},
		{"fnarr-elem-call-2arg", `function add(a: i32, b: i32): i32 { return a + b; } function mul(a: i32, b: i32): i32 { return a * b; } function main(): i32 { var ops = [add, mul]; return ops[0](3, 4) + ops[1](3, 4); }`},
		// Array literals of NO-CAPTURE LAMBDAS (#2994): each lambda element is
		// hoisted to a top-level fn (the lift a no-capture lambda arg gets), so the
		// array is a function-pointer array and `fs[i](args)` / `for f in fs`
		// ride the existing fn-pointer-array call path. (Named-function arrays
		// above already lowered; this adds the inline-lambda element form.)
		{"clo-arr-call", `function main(): i32 { var fs = [function(x: i32): i32 { return x * 2; }, function(x: i32): i32 { return x + 100; }]; return fs[0](5) + fs[1](5); }`},
		{"clo-arr-len", `function main(): i32 { var fs = [function(x: i32): i32 { return x + 1; }]; return fs.len() + 9; }`},
		{"clo-arr-idxvar", `function main(): i32 { var fs = [function(x: i32): i32 { return x * 10; }]; var i = 0; return fs[i](7); }`},
		{"clo-arr-forin", `function main(): i32 { var fs = [function(x: i32): i32 { return x + 1; }, function(x: i32): i32 { return x + 2; }]; var s = 0; for f in fs { s = s + f(10); } return s; }`},
		{"clo-arr-mixed", `function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var fs = [dbl, function(x: i32): i32 { return x + 5; }]; return fs[0](10) + fs[1](10); }`},
		{"modulo", "function main(): i32 { return 23 % 5; }"},
		{"division", "function main(): i32 { return 84 / 2; }"},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }"},
		{"shift", "function main(): i32 { return 1 << 4; }"},
		// Hex literals: the IR path used to lower these via a decimal-only
		// parser (digits_to_i32), so every `0x..` constant became 0. Now the
		// literal TEXT is spliced like the AST path (op_const_i32_text), so the
		// assembler parses the base. Exit codes are mod 256, so these probe the
		// low byte through shifts/masks where the high bits matter.
		{"hex-small", "function main(): i32 { return 0xFF & 0x0F; }"},
		{"hex-shift", "function main(): i32 { return (0x61626380 >> 8) & 255; }"},
		{"hex-mask-high", "function main(): i32 { return (0x12345678 >> 16) & 255; }"},
		{"hex-local", "function main(): i32 { var x = 0x100; return (x + 5) & 255; }"},
		{"hex-or", "function main(): i32 { return (0x40 | 0x01) & 255; }"},
		// Int→int casts (op_int_cast). Non-overflowing where they'd differ from
		// native, so the AST path agrees — masking matches asm.fern's as_<ty>.
		{"cast-u8-mask", "function main(): i32 { return (300 as u8) as i32; }"}, // 300 & 255 = 44
		{"cast-u16-mask", "function main(): i32 { return ((70000 as u16) as i32) & 255; }"},
		{"cast-i8-sext", "function main(): i32 { return ((200 as i8) as i32) & 255; }"}, // 200 -> -56 -> &255 = 200
		{"cast-chain", "function main(): i32 { var x: i32 = 65; return ((x as u8) as i32); }"},
		// Array builder methods: .with (reassign-self -> in-place arr_set) and
		// .append (-> __fern_arr_push), plus __alloc_u8 / string_from_bytes. These
		// don't overflow u32, so IR matches the AST path.
		{"with-reassign", "function main(): i32 { var a = [10, 20, 30]; a = a.with(1, 99); return a[0] + a[1] + a[2]; }"},
		{"with-loop", "function main(): i32 { var a = [0, 0, 0, 0]; var i = 0; while (i < 4) { a = a.with(i, i * i); i = i + 1; } return a[0] + a[1] + a[2] + a[3]; }"},
		{"append-build", "function main(): i32 { var a: i32[] = []; var i = 0; while (i < 5) { a = a.append(i * 2); i = i + 1; } return a[0] + a[4]; }"},
		// NB: __alloc_u8 / string_from_bytes programs are NOT differential cases —
		// the standalone asm_ir_run AST fallback references __fern_alloc_u8 without
		// emitting it (a legacy-driver gap), so the AST side won't link. The IR
		// path compiles them correctly; they're validated against the native
		// compiler in TestSelfHostU32WrapIR (alloc-u8 / str-from-bytes).
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
		// Default parameter values: an omitted trailing argument is filled from
		// the parameter's declared default (parser.fill_default_args_module, run
		// in lift_lambdas for the IR path), so the call reaches the IR complete.
		{"default-one", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }"},
		{"default-override", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(40, 2); }"},
		{"default-multi", "function box(w: i32, h: i32 = 2, d: i32 = 3): i32 { return w * 100 + h * 10 + d; } function main(): i32 { return box(1); }"},
		{"default-expr", "function add(a: i32, b: i32 = 5 + 5): i32 { return a + b; } function main(): i32 { return add(32); }"},
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
		// Aliasing a struct/enum-element array local (`var qs = ps`) carries the
		// element type over, so `qs[i].field` / `qs[i].method()` dispatch.
		{"struct-arr-alias-field", `struct P { x: i32 } function main(): i32 { var ps = [P{x: 5}, P{x: 6}]; var qs = ps; return qs[1].x; }`},
		{"struct-arr-alias-method", `struct P { x: i32 } function (p: P) g(): i32 { return p.x; } function main(): i32 { var ps = [P{x: 1}, P{x: 2}]; var qs = ps; return qs[0].g() + qs[1].g(); }`},
		{"enum-arr-alias-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { var a = [R, G]; var b = a; return b[0].k() * 10 + b[1].k(); }`},
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
		// Option/Result payload that is itself an ENUM value — bound by pointer
		// and typed with the enum name, so a nested `match (c)` / `c.method()`
		// resolves (the Option/Result-path analog of #2979).
		{"opt-bind-enum", `enum C { R, G } function g(b: i32): Option[C] { if (b > 0) { return Some(G); } return None; } function main(): i32 { match (g(1)) { Some(c) => { match (c) { R => { return 1; }, G => { return 2; } } }, None => { return 0; } } return 0; }`},
		{"opt-bind-enum-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function g(): Option[C] { return Some(G); } function main(): i32 { match (g()) { Some(c) => { return c.k(); }, None => { return 0; } } return 0; }`},
		{"result-bind-enum", `enum C { R, G } function g(): Result[C, i32] { return Ok(G); } function main(): i32 { match (g()) { Ok(c) => { match (c) { R => { return 1; }, G => { return 2; } } }, Err(e) => { return e; } } return 0; }`},
		// NESTED Option/Result payload — `Some(inner)` where inner is itself an
		// Option/Result: bound by pointer + typed so the inner match recovers.
		{"opt-bind-nested-opt", `function g(n: i32): Option[Option[i32]] { if (n > 0) { return Some(Some(n)); } return None; } function main(): i32 { match (g(5)) { Some(inner) => { match (inner) { Some(x) => { return x; }, None => { return 99; } } }, None => { return 0; } } return 0; }`},
		{"opt-bind-nested-none", `function g(n: i32): Option[Option[i32]] { if (n > 3) { return Some(None); } return None; } function main(): i32 { match (g(5)) { Some(inner) => { match (inner) { Some(x) => { return x; }, None => { return 99; } } }, None => { return 0; } } return 0; }`},
		{"opt-bind-nested-result", `function g(n: i32): Option[Result[i32, i32]] { return Some(Ok(n)); } function main(): i32 { match (g(7)) { Some(r) => { match (r) { Ok(x) => { return x; }, Err(e) => { return e; } } }, None => { return 0; } } return 0; }`},
		// `match (a[i])` on an Option/Result ARRAY element — the element type is
		// recovered from the array slot's annotated `Option[T][]` / `Result[…][]`
		// (stripping the trailing `[]`), incl. via a local bind, a manual
		// while-loop, and an array alias. (`for o in a { match(o) }` is blocked
		// upstream by an asmcore checker mis-inference, tracked separately.)
		{"optarr-index-match", `function main(): i32 { var a: Option[i32][] = [Some(7), None]; match (a[0]) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"optarr-index-via-local", `function main(): i32 { var a: Option[i32][] = [Some(7), None]; var o = a[0]; match (o) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"optarr-while-match", `function main(): i32 { var a: Option[i32][] = [Some(5), None, Some(3)]; var i = 0; var s = 0; while (i < a.len()) { match (a[i]) { Some(x) => { s = s + x; }, None => {} } i = i + 1; } return s; }`},
		{"resultarr-index-match", `function main(): i32 { var a: Result[i32, i32][] = [Ok(5), Err(3)]; match (a[1]) { Ok(x) => { return x; }, Err(e) => { return e * 10; } } return 0; }`},
		// Option/Result-ARRAY struct field — leak-safe, so construction +
		// `.len()` + `match (b.o[i])` (field-array element) lower.
		{"optarr-field-match", `struct B { o: Option[i32][] } function main(): i32 { var b = B { o: [Some(7), None] }; match (b.o[0]) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"resultarr-field-match", `struct B { o: Result[i32, i32][] } function main(): i32 { var b = B { o: [Ok(5), Err(3)] }; match (b.o[1]) { Ok(x) => { return x; }, Err(e) => { return e * 10; } } return 0; }`},
		{"optarr-alias-index-match", `function main(): i32 { var a: Option[i32][] = [Some(9), None]; var b = a; var o = b[0]; match (o) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		// `for o in optArray { match (o) }` — the asmcore type checker no longer
		// mis-parses the `Option[T][]` / `Result[…][]` annotation as
		// `Option[unknown]` (#3000): `ty_from_name` strips the trailing array
		// `[]` before the Option[/Result[ prefix. (Lowering still routes the
		// foreach through AST, so this guards the checker fix via the gate.)
		{"foreach-optarr-match", `function main(): i32 { var a: Option[i32][] = [Some(1), Some(2), None]; var s = 0; for o in a { match (o) { Some(x) => { s = s + x; }, None => { s = s + 100; } } } return s; }`},
		{"foreach-resultarr-match", `function main(): i32 { var a: Result[i32, i32][] = [Ok(5), Err(3)]; var s = 0; for r in a { match (r) { Ok(x) => { s = s + x; }, Err(e) => { s = s + e * 10; } } } return s; }`},
		{"opt-bind-result-strerr", `function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err("fail"); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }`},
		{"opt-bind-local", `function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { var r = g(n); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }`},
		{"opt-bind-local-strerr", `function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err("oops"); } function f(n: i32): i32 { var r = chk(n); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }`},
		{"opt-bind-param", `function f(o: Option[i32]): i32 { match (o) { Some(x) => { return x * 2; }, None => { return 0; } } return 0; } function main(): i32 { return f(Some(21)) + f(None); }`},
		// match on a STRUCT-METHOD call returning Option/Result, binding the
		// payload — the method's return type is recovered via the qualified
		// "<Type>.<method>" key in opt_ret_fns (#2969 follow-up). Direct and
		// via-local forms, Option + Result, i32 + string payloads.
		{"opt-method-bind", `struct Box { v: i32 } function (b: Box) get(): Option[i32] { if (b.v > 0) { return Some(b.v); } return None; } function main(): i32 { var x = Box { v: 5 }; match (x.get()) { Some(n) => { return n; }, None => { return 0; } } return 0; }`},
		{"opt-method-bind-local", `struct Box { v: i32 } function (b: Box) get(): Option[i32] { if (b.v > 0) { return Some(b.v); } return None; } function main(): i32 { var x = Box { v: 5 }; var o = x.get(); match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`},
		{"result-method-bind", `struct Box { v: i32 } function (b: Box) chk(): Result[i32, i32] { if (b.v > 0) { return Ok(b.v + 30); } return Err(b.v); } function main(): i32 { var x = Box { v: 5 }; match (x.chk()) { Ok(n) => { return n; }, Err(e) => { return e; } } return 0; }`},
		{"opt-method-bind-string", `struct Box { v: i32 } function (b: Box) name(): Option[string] { if (b.v > 0) { return Some("hello"); } return None; } function main(): i32 { var x = Box { v: 5 }; match (x.name()) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; }`},
		// Enum-receiver method calls `c.method()` — an unannotated enum-value local
		// (`var c = Green`) dispatches to `<Enum>.<method>` (#2947).
		{"enum-method-payloadless", `enum Color { Red, Green } function (c: Color) code(): i32 { match (c) { Red => { return 1; }, Green => { return 2; } } return 0; } function main(): i32 { var c = Green; return c.code(); }`},
		{"enum-method-payload", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 0; } } return 0; } function main(): i32 { var e = A(9); return e.v(); }`},
		{"enum-method-args", `enum Op2 { Add, Mul } function (o: Op2) ap(a: i32, b: i32): i32 { match (o) { Add => { return a + b; }, Mul => { return a * b; } } return 0; } function main(): i32 { var o = Add; var p = Mul; return o.ap(5, 7) * 100 + p.ap(5, 7); }`},
		{"enum-method-from-ctor", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 5; } } return 0; } function main(): i32 { var e = A(30); return e.v() + B.v(); }`},
		// Method call on a bound ENUM-typed match payload — `Node(l, r) =>
		// l.sum() + r.sum()` dispatches `<Enum>.<method>` because the payload
		// slot is typed with its enum name. Recursive enum (binary tree) +
		// single recursive payload.
		{"enum-method-recursive-tree", `enum Tree { Leaf(i32), Node(Tree, Tree) } function (t: Tree) sum(): i32 { match (t) { Leaf(n) => { return n; }, Node(l, r) => { return l.sum() + r.sum(); } } return 0; } function main(): i32 { return Node(Leaf(3), Node(Leaf(4), Leaf(5))).sum(); }`},
		{"enum-method-recursive-single", `enum Box { Wrap(Box), Base(i32) } function (b: Box) v(): i32 { match (b) { Base(n) => { return n; }, Wrap(inner) => { return inner.v(); } } return 0; } function main(): i32 { return Wrap(Wrap(Base(7))).v(); }`},
		// Enum-ARRAY element method calls `a[i].method()` — the element slot is
		// typed with the enum, so dispatch resolves to `<Enum>.<method>` (#2954 item 2).
		{"enum-array-method-annot", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { var a: C[] = [R, G]; return a[1].k(); }`},
		{"enum-array-method-literal", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { var a = [R, G]; return a[0].k() * 10 + a[1].k(); }`},
		{"enum-array-method-payload", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 9; } } return 0; } function main(): i32 { var a: E[] = [A(7), B]; return a[0].v() + a[1].v(); }`},
		{"enum-array-elem-local-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { var a: C[] = [R, G]; var c = a[1]; return c.k(); }`},
		{"enum-arr-forin", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } function main(): i32 { var a = [R, G, G]; var s = 0; for x in a { s = s + x.k(); } return s; }`},
		{"enum-arr-match", `enum C { R, G } function main(): i32 { var a = [R, G]; match (a[1]) { R => { return 10; }, G => { return 20; } } return 0; }`},
		// A struct with an enum-ARRAY field (`Box { items: C[] }`) is leak-safe,
		// so construction + `.len()` + element index/match lower (the enum boxes
		// leak with the struct like struct-element arrays).
		{"struct-enumarr-len", `enum C { R, G } struct Box { items: C[] } function main(): i32 { var b = Box { items: [R, G] }; return b.items.len(); }`},
		{"struct-enumarr-index-match", `enum C { R, G } struct Box { items: C[] } function main(): i32 { var b = Box { items: [R, G, R] }; match (b.items[1]) { R => { return 1; }, G => { return 2; } } return 0; }`},
		// Method dispatch on an ENUM-array field element (`b.items[i].method()`)
		// — the field-array index recovers the enum element type so it dispatches
		// `<Enum>.<method>` (the field analog of the local enum-array case).
		{"struct-enumarr-elem-method", `enum C { R, G } function (c: C) k(): i32 { match (c) { R => { return 1; }, G => { return 2; } } return 0; } struct Box { items: C[] } function main(): i32 { var b = Box { items: [R, G] }; return b.items[0].k() * 10 + b.items[1].k(); }`},
		{"struct-enumarr-elem-method-payload", `enum E { A(i32), B } function (e: E) v(): i32 { match (e) { A(n) => { return n; }, B => { return 9; } } return 0; } struct Box { items: E[] } function main(): i32 { var b = Box { items: [A(7), B] }; return b.items[0].v() + b.items[1].v(); }`},
		// A struct with a NESTED (array-of-array) field `i32[][]` is leak-safe, so
		// construction + `.len()` + element index (incl. via a param) lower (the
		// whole nested structure leaks with the struct).
		{"struct-nested-arr-index", `struct G { rows: i32[][] } function main(): i32 { var g = G { rows: [[1, 2], [3, 4]] }; return g.rows[1][0]; }`},
		{"struct-nested-arr-len", `struct G { rows: i32[][] } function main(): i32 { var g = G { rows: [[1, 2], [3, 4]] }; return g.rows.len() + g.rows[0].len(); }`},
		{"struct-nested-arr-param", `struct G { rows: i32[][] } function first(g: G): i32 { return g.rows[0][0]; } function main(): i32 { var g = G { rows: [[5, 6]] }; return first(g); }`},
		{"struct-field-nested", `struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { var b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }`},
		{"struct-field-deep", `struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { var o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }`},
		{"struct-field-bind", `struct Point { x: i32, y: i32 } struct Box { p: Point, tag: i32 } function main(): i32 { var b = Box { p: Point { x: 7, y: 8 }, tag: 3 }; var pp = b.p; return pp.x * pp.y + b.tag; }`},
		{"forin-i32", `function main(): i32 { var xs = [10, 20, 30, 40]; var sum = 0; for x in xs { sum = sum + x; } return sum; }`},
		{"forin-i32-param", `function total(xs: i32[]): i32 { var s = 0; for v in xs { s = s + v; } return s; } function main(): i32 { var a = [1, 2, 3, 4, 5]; return total(a); }`},
		{"forin-nested", `function main(): i32 { var xs = [1, 2, 3]; var t = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }`},
		{"forin-string", `function main(): i32 { var ss: string[] = ["a", "bb", "ccc", "dddd"]; var n = 0; for s in ss { n = n + s.len(); } return n; }`},
		// Array-of-arrays (#2987): `var a: T[][]` / `[[…], …]` records the slot as
		// an array-of-arrays, so the inner binding (`var row = a[i]` or the loop
		// var of `for row in a`) types as an array and `for x in row` flows.
		{"arr2d-forin-annot", `function main(): i32 { var a: i32[][] = [[1, 2], [3, 4]]; var s = 0; for row in a { for x in row { s = s + x; } } return s; }`},
		{"arr2d-forin-literal", `function main(): i32 { var a = [[1, 2], [3, 4]]; var s = 0; for row in a { for x in row { s = s + x; } } return s; }`},
		{"arr2d-manual-bind", `function main(): i32 { var a: i32[][] = [[1, 2], [3, 4]]; var row = a[1]; var s = 0; for x in row { s = s + x; } return s; }`},
		{"arr2d-strarr", `function main(): i32 { var a: string[][] = [["a", "bb"], ["c"]]; var s = 0; for row in a { for w in row { s = s + w.len(); } } return s; }`},
		{"arr2d-alias", `function main(): i32 { var a = [[1, 2], [3, 4]]; var b = a; var s = 0; for row in b { for x in row { s = s + x; } } return s; }`},
		{"arr2d-rowlen", `function main(): i32 { var a = [[1, 2, 3], [4]]; var s = 0; for row in a { s = s + row.len(); } return s; }`},
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
		// `for c in r.field` over a leak-safe array-typed struct field (string[] /
		// struct[] / enum[] — element types that aren't reclaimed). The field
		// access is snapshotted into a hidden BORROW local (never swept), so the
		// buffer's lifetime stays with the owning struct (#3003 leak-safe slice).
		{"struct-strarr-field-forin", `struct R { tags: string[] } function main(): i32 { var r = R { tags: ["ab", "cde"] }; var n = 0; for t in r.tags { n = n + t.len(); } return n; }`},
		{"struct-structarr-field-forin", `struct P { x: i32 } struct R { items: P[] } function (p: P) dbl(): i32 { return p.x * 2; } function main(): i32 { var r = R { items: [P { x: 3 }, P { x: 4 }] }; var n = 0; for p in r.items { n = n + p.dbl(); } return n; }`},
		{"struct-enumarr-field-forin", `enum C { A, B } struct R { cells: C[] } function main(): i32 { var r = R { cells: [C.A, C.B] }; var n = 0; for c in r.cells { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } return n; }`},
		// The owning struct is read AFTER the loop — the borrow must not free its
		// field buffer (the exit-sweep never decs a non-array-marked snapshot).
		{"struct-strarr-field-forin-after", `struct R { tags: string[] } function main(): i32 { var r = R { tags: ["ab", "cd", "e"] }; var n = 0; for t in r.tags { n = n + t.len(); } return n + r.tags.len(); }`},
		// A reclaimable scalar-array field (i32[]) STAYS on the AST path — aliasing
		// it is an RC hazard (deferred to the Perceus self-host port, #3003). The
		// AST emitter handles it, so the differential still matches.
		{"struct-i32arr-field-forin", `struct R { nums: i32[] } function main(): i32 { var r = R { nums: [3, 4] }; var n = 0; for v in r.nums { n = n + v; } return n; }`},
		{"tuple-str-i32-dotn", `function main(): i32 { var t = ("hello", 7); return t.0.len() + t.1; }`},
		{"tuple-str-i32-destructure", `function main(): i32 { var (a, b) = ("world", 3); return a.len() + b; }`},
		{"tuple-struct-dotn", `struct P { x: i32, y: i32 } function main(): i32 { var t = (P { x: 4, y: 5 }, 2); return t.0.x * t.0.y + t.1; }`},
		// A function-VALUE tuple element call `t.N(args)` — the element is tagged
		// "fn" at construction (elem_type_tag), so the call lowers to tuple_get +
		// call_indirect, mirroring the "fn"-typed struct field (#3016).
		{"tuple-fn-value-call", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var t = (inc, 5); return t.0(t.1); }`},
		{"tuple-fn-value-call-multi", `function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var t = (inc, dbl, 5); return t.0(t.2) + t.1(t.2); }`},
		{"tuple-fn-value-call-2args", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { var t = ("x", add); return t.1(3, 4); }`},
		// An Option value in a tuple, matched via `t.N` — the element is tagged
		// "Option[T]" at construction (elem_type_tag), admitted by the tuple-make
		// eligibility check, and the match-scrutinee recovers the payload from the
		// element tag (#3018). Result elements (a comma in the tag) stay on AST.
		{"tuple-option-i32-match", `function main(): i32 { var t = (Some(7), 3); match (t.0) { Some(x) => { return x + t.1; }, None => { return 0; } } return 0; }`},
		{"tuple-option-i32-idx1-match", `function main(): i32 { var t = (3, Some(7)); match (t.1) { Some(x) => { return x + t.0; }, None => { return 0; } } return 0; }`},
		{"tuple-option-string-match", `function main(): i32 { var t = (Some("hello"), 3); match (t.0) { Some(s) => { return s.len() + t.1; }, None => { return 0; } } return 0; }`},
		{"tuple-option-from-call-none", `function f(b: boolean): Option[i32] { if (b) { return Some(7); } return None; } function main(): i32 { var t = (f(false), 5); match (t.0) { Some(x) => { return x + t.1; }, None => { return t.1 + 100; } } return 0; }`},
		// A direct `Some(x)` construction matched/bound — `some_opt_type` types
		// the local / scrutinee so the match recovers the payload, the
		// construction analogue of the Option-returning-call path (#3024).
		{"some-local-i32-match", `function main(): i32 { var o = Some(7); match (o) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"some-local-string-match", `function main(): i32 { var o = Some("hello"); match (o) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; }`},
		{"some-local-struct-match", `struct S { x: i32 } function main(): i32 { var o = Some(S { x: 5 }); match (o) { Some(s) => { return s.x; }, None => { return 0; } } return 0; }`},
		{"some-direct-match", `function main(): i32 { match (Some(9)) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"some-local-reassign-none", `function pick(b: boolean): i32 { var o = Some(7); if (b) { o = None; } match (o) { Some(x) => { return x; }, None => { return 99; } } return 0; } function main(): i32 { return pick(true) + pick(false); }`},
		// An unannotated array literal of Option values — the element opt-type is
		// inferred from the first Some(...) element (#3027, array sibling of #3024).
		{"some-array-foreach", `function main(): i32 { var a = [Some(1), Some(2), None]; var n = 0; for o in a { match (o) { Some(x) => { n = n + x; }, None => {} } } return n; }`},
		{"some-array-index", `function main(): i32 { var a = [Some(4), Some(2)]; match (a[0]) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"some-array-string", `function main(): i32 { var a = [Some("ab"), None, Some("c")]; var n = 0; for o in a { match (o) { Some(s) => { n = n + s.len(); }, None => {} } } return n; }`},
		// A function returning a tuple with an Option element (#3029) — admitted
		// by tuple_elems_lowerable; var-bind / destructure recover the payload.
		{"tuple-ret-opt-var", `function mk(): (Option[i32], i32) { return (Some(3), 4); } function main(): i32 { var t = mk(); match (t.0) { Some(x) => { return x + t.1; }, None => { return 0; } } return 0; }`},
		{"tuple-ret-opt-destr", `function mk(): (Option[i32], i32) { return (Some(3), 4); } function main(): i32 { var (o, n) = mk(); match (o) { Some(x) => { return x + n; }, None => { return 0; } } return 0; }`},
		{"tuple-ret-opt-string", `function mk(): (Option[string], i32) { return (Some("ab"), 4); } function main(): i32 { var t = mk(); match (t.0) { Some(s) => { return s.len() + t.1; }, None => { return 0; } } return 0; }`},
		{"tuple-ret-opt-none", `function mk(b: boolean): (Option[i32], i32) { if (b) { return (None, 9); } return (Some(3), 4); } function main(): i32 { var t = mk(true); match (t.0) { Some(x) => { return x; }, None => { return t.1; } } return 0; }`},
		// A method with an Option/Result receiver (#3033) — slot 0 is opt-typed so
		// match(self) recovers the payload; the call dispatches to Option.<method>.
		{"opt-recv-method-bound", `function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { var o = Some(7); return o.unwrap_or(0); }`},
		{"opt-recv-method-direct", `function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { return Some(7).unwrap_or(0); }`},
		{"opt-recv-method-none", `function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { var o: Option[i32] = None; return o.unwrap_or(99); }`},
		{"opt-recv-method-string", `function (o: Option[string]) ln(): i32 { match (o) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; } function main(): i32 { return Some("hello").ln(); }`},
		{"opt-recv-method-callrecv", `function get(b: boolean): Option[i32] { if (b) { return Some(8); } return None; } function (o: Option[i32]) unwrap_or(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { return get(true).unwrap_or(0) + get(false).unwrap_or(5); }`},
		// matching/binding the result of an Option-receiver method (#3051) —
		// opt_recv_base_type keys "Option.<m>" so the result type is recovered.
		{"opt-recv-method-chain-direct", `function (o: Option[i32]) mi(): Option[i32] { match (o) { Some(x) => { return Some(x + 1); }, None => { return None; } } return None; } function main(): i32 { match (Some(5).mi()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"opt-recv-method-chain-bind", `function (o: Option[i32]) mi(): Option[i32] { match (o) { Some(x) => { return Some(x + 1); }, None => { return None; } } return None; } function main(): i32 { var r = Some(5).mi(); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"opt-recv-method-chain-local", `function (o: Option[i32]) mi(): Option[i32] { match (o) { Some(x) => { return Some(x + 1); }, None => { return None; } } return None; } function main(): i32 { var o = Some(5); match (o.mi()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		// An Option-receiver method on a struct-method's Option result, and the
		// chain matched — opt_recv_base_type recovers a method-result receiver (#3067).
		{"opt-chain-on-struct-method", `struct B { v: i32 } function (b: B) find(): Option[i32] { return Some(b.v); } function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { var b = B { v: 7 }; return b.find().uo(0); }`},
		{"opt-chain-on-struct-method-match", `struct B { v: i32 } function (b: B) find(): Option[i32] { return Some(b.v); } function main(): i32 { var b = B { v: 9 }; match (b.find()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		// An Option-receiver method on a struct's Option field or a tuple's Option
		// element — opt_recv_base_type's ExprFieldAccess arm recovers it (#3070).
		{"opt-method-on-struct-field", `struct B { v: Option[i32] } function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { var b = B { v: Some(7) }; return b.v.uo(0); }`},
		{"opt-method-on-tuple-elem", `function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { var t = (Some(5), 3); return t.0.uo(0) + t.1; }`},
		// An enum-receiver method returning Option, matched/chained — the opt-result
		// recovery sites gained an expr_enum_type fallback (#3077).
		{"enum-method-opt-result-match", `enum E { V(i32), N } function (e: E) get(): Option[i32] { match (e) { V(x) => { return Some(x); }, N => { return None; } } return None; } function main(): i32 { match (V(7).get()) { Some(x) => { return x; }, None => { return 0; } } return 0; }`},
		{"enum-method-opt-result-chain", `enum E { V(i32), N } function (e: E) get(): Option[i32] { match (e) { V(x) => { return Some(x); }, N => { return None; } } return None; } function (o: Option[i32]) uo(d: i32): i32 { match (o) { Some(x) => { return x; }, None => { return d; } } return d; } function main(): i32 { return V(6).get().uo(0) + N.get().uo(9); }`},
		// A match-EXPRESSION in value position (`return match (...) { arm => E }`)
		// on a call-returning Option/Result. lower_iife_match now recovers the
		// scrutinee's Option/Result type via try_opt_type (not ExprIdent-only), so
		// the call scrutinee lowers instead of bailing to AST (#3081).
		{"match-expr-call-result-ok", `function f(n: i32): Result[i32, i32] { return Ok(n); } function main(): i32 { return match (f(5)) { Ok(v) => v, Err(e) => e }; }`},
		{"match-expr-call-result-err", `function f(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(99); } function main(): i32 { return match (f(0)) { Ok(v) => v, Err(e) => e }; }`},
		{"match-expr-call-option", `function f(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function main(): i32 { return match (f(7)) { Some(v) => v, None => 13 }; }`},
		// An UNANNOTATED nested Option local (`var a = Some(Some(5))`) records its
		// "Option[Option[i32]]" type via some_opt_type (the nested-Option bail was
		// lifted), so the outer match binds `b` as Option[i32] (mark_opt_type) and the
		// inner `match (b)` recovers its payload — the whole thing lowers (#3106).
		{"nested-opt-unannot", `function main(): i32 { var a = Some(Some(5)); match (a) { Some(b) => { return match (b) { Some(v) => v, None => 1 }; }, None => { return 2; } } return 9; }`},
		{"nested-opt-unannot-inner-expr", `function main(): i32 { var a = Some(Some(42)); match (a) { Some(b) => { return match (b) { Some(v) => v * 2, None => 1 }; }, None => { return 2; } } return 9; }`},
		// The value-position (match-EXPRESSION) form of the nested-Option match: the
		// outer `Some(b)` binds b: Option[i32]. lower_iife_match now admits a nested-
		// Option payload into an i32 temp for an ident scrutinee, so the inner
		// `match (b)` lowers instead of bailing (#3111).
		{"nested-opt-expr-ident", `function main(): i32 { var a = Some(Some(5)); return match (a) { Some(b) => match (b) { Some(v) => v, None => 1 }, None => 2 }; }`},
		{"nested-opt-expr-ident-derived", `function main(): i32 { var a = Some(Some(21)); return match (a) { Some(b) => match (b) { Some(v) => v * 2, None => 1 }, None => 2 }; }`},
		// A match-EXPRESSION on a direct `Some(x)` construction scrutinee. try_opt_type
		// (shared by lower_iife_match and the `?` operator) now falls back to
		// some_opt_type for a direct Some construction, so it lowers instead of
		// bailing (#3115).
		{"match-expr-some-construct", `function main(): i32 { return match (Some(6)) { Some(w) => w, None => 0 }; }`},
		{"match-expr-some-construct-derived", `function main(): i32 { return match (Some(20)) { Some(w) => w + 1, None => 0 }; }`},
		{"match-expr-arm-some-construct", `function main(): i32 { var o = Some(5); return match (o) { Some(v) => match (Some(v + 1)) { Some(w) => w, None => 0 }, None => 0 }; }`},
		// Iterating an Option-array struct field — the leak-safe-field foreach
		// opt-types the loop var so match(o) recovers the payload (#3056).
		{"opt-arr-field-foreach-i32", `struct B { xs: Option[i32][] } function main(): i32 { var b = B { xs: [Some(1), Some(2), None] }; var n = 0; for o in b.xs { match (o) { Some(x) => { n = n + x; }, None => {} } } return n; }`},
		{"opt-arr-field-foreach-string", `struct B { xs: Option[string][] } function main(): i32 { var b = B { xs: [Some("ab"), None, Some("c")] }; var n = 0; for o in b.xs { match (o) { Some(s) => { n = n + s.len(); }, None => {} } } return n; }`},
		// A 2D struct/enum array — the annotation records the innermost element
		// type so the nested foreach propagates it to p (#3058).
		{"arr2d-struct", `struct P { x: i32 } function main(): i32 { var a: P[][] = [[P { x: 1 }], [P { x: 2 }, P { x: 3 }]]; var n = 0; for row in a { for p in row { n = n + p.x; } } return n; }`},
		{"arr2d-struct-method", `struct P { x: i32 } function (p: P) g(): i32 { return p.x * 2; } function main(): i32 { var a: P[][] = [[P { x: 1 }], [P { x: 2 }]]; var n = 0; for row in a { for p in row { n = n + p.g(); } } return n; }`},
		{"arr2d-enum", `enum C { A, B } function main(): i32 { var a: C[][] = [[C.A], [C.B, C.A]]; var n = 0; for row in a { for c in row { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } } return n; }`},
		// Unannotated 2D struct/enum array literal — element type inferred by
		// recursing into the inner literal (#3061, unannotated sibling of #3058).
		{"arr2d-struct-unannot", `struct P { x: i32 } function main(): i32 { var a = [[P { x: 1 }], [P { x: 2 }, P { x: 3 }]]; var n = 0; for row in a { for p in row { n = n + p.x; } } return n; }`},
		{"arr2d-enum-unannot", `enum C { A, B } function main(): i32 { var a = [[C.A], [C.B, C.A]]; var n = 0; for row in a { for c in row { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } } return n; }`},
		// Unannotated 2D Option-array literal — element opt-type inferred by
		// recursing into the inner literal (#3074, depth-2 sibling of #3027).
		{"arr2d-opt-unannot-i32", `function main(): i32 { var a = [[Some(1)], [Some(2), None]]; var n = 0; for row in a { for o in row { match (o) { Some(x) => { n = n + x; }, None => {} } } } return n; }`},
		{"arr2d-opt-unannot-string", `function main(): i32 { var a = [[Some("ab")], [None, Some("c")]]; var n = 0; for row in a { for o in row { match (o) { Some(s) => { n = n + s.len(); }, None => {} } } } return n; }`},
		// A 2D-array param — the param setup marks it is_arrarr and extracts the
		// innermost struct/enum element type for the nested foreach (#3064).
		{"arr2d-param-i32", `function sum(a: i32[][]): i32 { var n = 0; for row in a { for x in row { n = n + x; } } return n; } function main(): i32 { return sum([[1, 2], [3]]); }`},
		{"arr2d-param-struct", `struct P { x: i32 } function sum(a: P[][]): i32 { var n = 0; for row in a { for p in row { n = n + p.x; } } return n; } function main(): i32 { return sum([[P { x: 5 }], [P { x: 6 }]]); }`},
		{"arr2d-param-enum", `enum C { A, B } function cnt(a: C[][]): i32 { var n = 0; for row in a { for c in row { match (c) { C.A => { n = n + 1; }, C.B => { n = n + 2; } } } } return n; } function main(): i32 { return cnt([[C.A], [C.B, C.A]]); }`},
		// A function returning a struct array — the element struct type is recorded
		// so a[i].field / foreach over the result resolve (#3037).
		{"ret-struct-arr-index", `struct P { x: i32 } function mk(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { var a = mk(); return a[0].x + a[1].x; }`},
		{"ret-struct-arr-foreach", `struct P { x: i32 } function mk(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { var a = mk(); var n = 0; for p in a { n = n + p.x; } return n; }`},
		{"ret-struct-arr-method", `struct P { x: i32 } function (p: P) g(): i32 { return p.x * 2; } function mk(): P[] { return [P { x: 3 }, P { x: 4 }]; } function main(): i32 { var a = mk(); var n = 0; for p in a { n = n + p.g(); } return n; }`},
		{"ret-struct-arr-twofield", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 10 }, P { x: 2, y: 20 }]; } function main(): i32 { var a = mk(); return a[1].x + a[1].y; }`},
		// A method returning a struct array (#3042, method sibling of #3037) — the
		// call-site marks the result is_arr so a[i].field / foreach resolve.
		{"method-ret-struct-arr-index", `struct P { x: i32 } struct B { n: i32 } function (b: B) items(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { var b = B { n: 5 }; var a = b.items(); return a[0].x + a[1].x; }`},
		{"method-ret-struct-arr-foreach", `struct P { x: i32 } struct B { n: i32 } function (b: B) items(): P[] { return [P { x: b.n }, P { x: b.n + 1 }]; } function main(): i32 { var b = B { n: 5 }; var a = b.items(); var s = 0; for p in a { s = s + p.x; } return s; }`},
		{"method-ret-struct-arr-method", `struct P { x: i32 } struct B { n: i32 } function (p: P) g(): i32 { return p.x * 2; } function (b: B) items(): P[] { return [P { x: 1 }, P { x: 2 }]; } function main(): i32 { var b = B { n: 5 }; var a = b.items(); var s = 0; for p in a { s = s + p.g(); } return s; }`},
		// A struct-/enum-array enum payload — the match binding marks the slot
		// is_arr + element type so ps[i].field / foreach resolve (#3046).
		{"enum-payload-struct-arr-index", `struct P { x: i32 } enum E { Items(P[]), Nil } function f(e: E): i32 { match (e) { Items(ps) => { return ps[0].x; }, Nil => { return 0; } } return 0; } function main(): i32 { return f(Items([P { x: 7 }])); }`},
		{"enum-payload-struct-arr-foreach", `struct P { x: i32 } enum E { Items(P[]), Nil } function f(e: E): i32 { match (e) { Items(ps) => { var n = 0; for p in ps { n = n + p.x; } return n; }, Nil => { return 0; } } return 0; } function main(): i32 { return f(Items([P { x: 3 }, P { x: 4 }])); }`},
		{"enum-payload-enum-arr", `enum C { A, B } enum E { Cells(C[]), Nil } function f(e: E): i32 { match (e) { Cells(cs) => { match (cs[0]) { C.A => { return 1; }, C.B => { return 2; } } }, Nil => { return 0; } } return 0; } function main(): i32 { return f(Cells([C.B])); }`},
		{"tuple-local-destructure", `function main(): i32 { var t = ("ab", 10); var (s, n) = t; return s.len() + n; }`},
		{"tuple-3-destructure", `function main(): i32 { var (a, b, c) = (1, 2, 3); return a * 100 + b * 10 + c; }`},
		{"tuple-4-destructure", `function main(): i32 { var (a, b, c, d) = (1, 2, 3, 4); return a + b + c + d; }`},
		{"tuple-3-mixed-destructure", `function main(): i32 { var (s, n, m) = ("hi", 5, 10); return s.len() + n + m; }`},
		{"tuple-3-local-destructure", `function main(): i32 { var t = (7, 8, 9); var (a, b, c) = t; return a + b * c; }`},
		{"tuple-3-ret-destructure", `function three(): (i32, i32, i32) { return (4, 5, 6); } function main(): i32 { var (a, b, c) = three(); return a * 100 + b * 10 + c; }`},
		{"struct-ret-basic", `struct P { x: i32, y: i32 } function mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { var p = mk(); return p.x * 10 + p.y; }`},
		{"struct-ret-param", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a * 2 }; } function main(): i32 { var p = mk(5); return p.x + p.y; }`},
		{"struct-ret-direct-field", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { return mk(7).x + mk(7).y; }`},
		{"f64-struct-field-read", `struct P { x: f64, n: i32 } function main(): i32 { var p = P { x: 3.5, n: 2 }; var y: f64 = p.x + 1.0; if (y > 4.0) { return p.n + 5; } return 0; }`},
		{"f64-struct-field-mixed", `struct V { a: i32, d: f64, b: i32 } function main(): i32 { var v = V { a: 1, d: 2.5, b: 3 }; var s: f64 = v.d * 2.0; if (s > 4.0) { return v.a + v.b; } return 0; }`},
		{"f64-struct-field-write", `struct P { x: f64, n: i32 } function main(): i32 { var p = P { x: 1.0, n: 4 }; p.x = 5.5; if (p.x > 5.0) { return p.n + 1; } return 0; }`},
		{"method-struct-ret", `struct P { x: i32, y: i32 } struct B { } function (b: B) mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { var b = B { }; var p = b.mk(); return p.x * 10 + p.y; }`},
		{"method-struct-ret-direct", `struct P { x: i32, y: i32 } struct B { base: i32 } function (b: B) mk(): P { return P { x: b.base, y: b.base + 1 }; } function main(): i32 { var b = B { base: 5 }; return b.mk().x + b.mk().y; }`},
		{"method-tuple-ret", `struct B { } function (b: B) pair(): (i32, i32) { return (3, 4); } function main(): i32 { var b = B { }; var (x, y) = b.pair(); return x * 10 + y; }`},
		{"method-tuple-ret-str", `struct B { } function (b: B) pair(): (string, i32) { return ("hi", 5); } function main(): i32 { var b = B { }; var (s, n) = b.pair(); return s.len() + n; }`},
		{"tuple-struct-elem-ret", `struct P { x: i32, y: i32 } function mk(): (P, i32) { return (P { x: 3, y: 4 }, 9); } function main(): i32 { var (p, n) = mk(); return p.x * 10 + p.y + n; }`},
		{"tuple-struct-elem-dotn", `struct P { x: i32, y: i32 } function mk(): (P, i32) { return (P { x: 6, y: 7 }, 2); } function main(): i32 { var t = mk(); return t.0.x + t.0.y + t.1; }`},
		{"f64-add-cmp", `function main(): i32 { var a: f64 = 1.5; var b: f64 = 2.25; var c: f64 = a + b; if (c > 3.0) { return 7; } return 0; }`},
		{"f64-sub-mul-eq", `function main(): i32 { var a: f64 = 10.0; var b: f64 = 4.0; var c: f64 = (a - b) * 2.0; if (c == 12.0) { return 9; } return 0; }`},
		{"f64-div-lt", `function main(): i32 { var a: f64 = 7.0; var c: f64 = a / 2.0; if (c < 4.0) { return 5; } return 0; }`},
		{"f64-neg-ge", `function main(): i32 { var a: f64 = 3.0; var b: f64 = -a; if (b <= 0.0) { return 4; } return 0; }`},
		{"f64-chain", `function main(): i32 { var x: f64 = 1.0; var y: f64 = 2.0; var z: f64 = 3.0; var r: f64 = x + y * z; if (r >= 7.0) { return 6; } if (r >= 6.0) { return 8; } return 0; }`},
		{"f64-param-ret", `function scale(x: f64, k: f64): f64 { return x * k; } function main(): i32 { var r: f64 = scale(3.0, 2.5); if (r > 7.0) { return 7; } return 0; }`},
		{"f64-ret-unannotated", `function mk(): f64 { return 4.5; } function main(): i32 { var a = mk(); var b = mk(); var c: f64 = a + b; if (c > 8.0) { return 9; } return 0; }`},
		{"f64-call-both-operands", `function one(): f64 { return 2.0; } function two(): f64 { return 3.0; } function main(): i32 { var p: f64 = one() * two(); if (p == 6.0) { return 5; } return 0; }`},
		{"f64-cast-to-int", `function main(): i32 { var x: f64 = 7.9; return x as i32; }`},
		{"f64-cast-from-int", `function main(): i32 { var n: i32 = 3; var x: f64 = (n as f64) + 0.5; if (x > 3.0) { return 8; } return 0; }`},
		{"f64-cast-roundtrip", `function main(): i32 { var n: i32 = 10; var x: f64 = n as f64; var y: f64 = x / 4.0; return y as i32; }`},
		{"f64-cast-mixed-param", `function f(a: f64, n: i32): f64 { return a + (n as f64); } function main(): i32 { var r: f64 = f(1.5, 2); return r as i32; }`},
		{"map-i32-len3", `function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }`},
		{"map-i32-overwrite", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); return m.len(); }`},
		{"map-i32-loop", `function main(): i32 { var m: Map[i32, i32] = map_new(4); var i = 0; while (i < 5) { m = m.insert(i, i*10); i = i + 1; } return m.len(); }`},
		{"map-str-keys", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert("a", 1); m = m.insert("bb", 2); m = m.insert("a", 9); return m.len(); }`},
		{"map-get-hit", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(7)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`},
		{"map-get-miss", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(999)) { Some(v) => { return v; }, None => { return 5; } } return 9; }`},
		{"map-has", `function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 1); var r = 0; if (m.has(1)) { r = r + 1; } if (m.has(2)) { r = r + 10; } return r; }`},
		{"map-get-strkey", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert("hi", 11); match (m.get("hi")) { Some(v) => { return v; }, None => { return 0; } } return 9; }`},
		{"map-get-or-hit", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(7, 0); }`},
		{"map-get-or-miss", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(999, 5); }`},
		{"map-get-or-strhit", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert("hi", 11); return m.get_or("hi", 0); }`},
		{"map-get-or-strmiss", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert("hi", 11); return m.get_or("no", 7); }`},
		{"map-keys-sum", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var ks: i32[] = m.keys(); var s = 0; var i = 0; while (i < ks.len()) { s = s + ks[i]; i = i + 1; } return s; }`},
		{"map-values-sum", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var vs: i32[] = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s; }`},
		{"map-forkv-values", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + v; } return s; }`},
		{"map-forkv-keys", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + k; } return s; }`},
		{"map-forkv-pair", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.insert(2, 3); m = m.insert(3, 4); var s = 0; for (k, v) in m { s = s + k * v; } return s; }`},
		{"map-forkv-strkey", `function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("ab", 1); m = m.insert("cde", 2); var s = 0; for (k, v) in m { s = s + k.len() + v; } return s; }`},
		// `.set` is the PUBLIC map mutator (the existing cases above use the
		// internal `.insert`); it lowers through the IR path identically (#2926).
		{"map-set-i32-len", `function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.set(1, 100); m = m.set(2, 200); m = m.set(3, 300); return m.len(); }`},
		{"map-set-i32-getor", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.set(7, 42); m = m.set(9, 13); return m.get_or(7, 0) + m.get_or(9, 0); }`},
		{"map-set-str-getor", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.set("a", 1); m = m.set("bb", 2); return m.get_or("bb", 0) + m.len(); }`},
		{"map-set-overwrite", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.set(7, 40); m = m.set(7, 42); return m.get_or(7, 0) + m.len(); }`},
		{"map-set-chained", `function main(): i32 { var m: Map[string, i32] = map_new(8).set("x", 5).set("y", 7); return m.get_or("y", 0) + m.len(); }`},
		{"map-set-keyword-literal", `function main(): i32 { var m: Map[string, i32] = Map { "a": 1, "b": 2 }; return m.get_or("b", 0) + m.len(); }`},
		{"map-set-has", `function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.set("k", 9); var r = 0; if (m.has("k")) { r = r + 1; } if (m.has("z")) { r = r + 10; } return r; }`},
		// m.without(k) -> (Map, existed). The destructured map re-marks so later
		// ops on it work; both AST and IR share __fern_map_delete (#2926).
		{"map-without-len", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var (m2, e) = m.without(1); return m2.len(); }`},
		{"map-without-existed", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); var (m2, e) = m.without(1); if (e) { return 1; } return 0; }`},
		{"map-without-miss", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); var (m2, e) = m.without(99); if (e) { return 1; } return m2.len() + 5; }`},
		{"map-without-survivor", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var (m2, e) = m.without(1); return m2.get_or(2, 0); }`},
		{"map-without-removed-gone", `function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); var (m2, e) = m.without(1); if (m2.has(1)) { return 9; } return 0; }`},
		{"map-without-strkey", `function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("a", 1); m = m.insert("b", 2); var (m2, e) = m.without("a"); return m2.len() + m2.get_or("b", 0); }`},
		{"map-without-then-insert", `function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("a", 1); var (m2, e) = m.without("a"); m2 = m2.insert("c", 5); return m2.get_or("c", 0); }`},
		// if-EXPRESSION in value position (#2938): the parser desugars it to a
		// 0-arg IIFE that the IR path now inlines as a value-producing void `if`
		// (a temp local per branch); previously the whole module bailed to AST.
		{"ifexpr-var", `function main(): i32 { var x = 5; var y = if (x > 3) { 10 } else { 20 }; return y; }`},
		{"ifexpr-else", `function main(): i32 { var x = 2; var y = if (x > 3) { 10 } else { 20 }; return y; }`},
		{"ifexpr-return", `function main(): i32 { var x = 5; return if (x > 3) { 10 } else { 20 }; }`},
		{"ifexpr-else-if", `function main(): i32 { var x = 2; var y = if (x == 1) { 10 } else if (x == 2) { 20 } else { 30 }; return y; }`},
		{"ifexpr-capture-expr", `function main(): i32 { var n = 7; var y = if (n > 5) { n + 1 } else { 0 }; return y; }`},
		{"ifexpr-nested-in-binary", `function main(): i32 { var a = 3; return (if (a > 0) { 5 } else { 6 }) + (if (a > 10) { 1 } else { 2 }); }`},
		{"ifexpr-as-arg", `function add1(v: i32): i32 { return v + 1; } function main(): i32 { var x = 5; return add1(if (x > 3) { 10 } else { 20 }); }`},
		{"matchexpr-literal", `function main(): i32 { var n = 2; var y = match (n) { 1 => 10, 2 => 20, _ => 0 }; return y; }`},
		// ENUM match-EXPRESSION in value position: the same IIFE inlining, with a
		// StmtMatch body lowered through the full variant dispatch (arms'
		// `return E` rewritten to a temp store). Unit-variant arms with an i32
		// result (#2938 follow-up); payload-binding arms still bail to AST.
		{"matchexpr-enum-unit", `enum C { A, B, X } function main(): i32 { var c: C = X; var y = match (c) { A => 1, B => 2, X => 3 }; return y; }`},
		{"matchexpr-enum-first", `enum C { A, B, X } function main(): i32 { var c: C = A; var y = match (c) { A => 1, B => 2, X => 3 }; return y; }`},
		{"matchexpr-enum-in-binary", `enum C { A, B } function main(): i32 { var c: C = A; return match (c) { A => 5, B => 6 } + 100; }`},
		{"matchexpr-enum-return-arg", `enum C { Red, Green, Blue } function pick(c: C): i32 { return match (c) { Red => 1, Green => 2, Blue => 3 }; } function main(): i32 { return pick(Green) * 10; }`},
		// Option/Result match-EXPRESSION with an i32 PAYLOAD binding (`Some(n) => n`):
		// admitted because the bound payload is i32, so the temp stays i32-wide.
		{"matchexpr-opt-unwrap", `function main(): i32 { var o: Option[i32] = Some(7); var y = match (o) { Some(n) => n, None => 0 }; return y; }`},
		{"matchexpr-opt-none", `function main(): i32 { var o: Option[i32] = None; var y = match (o) { Some(n) => n, None => 42 }; return y; }`},
		{"matchexpr-opt-expr", `function main(): i32 { var o: Option[i32] = Some(5); return match (o) { Some(n) => n * 2 + 1, None => 0 } + 100; }`},
		{"matchexpr-result-bind", `function main(): i32 { var r: Result[i32, i32] = Err(3); var y = match (r) { Ok(n) => n, Err(e) => e * 10 }; return y; }`},
		// USER-enum match-expression with an i32 payload binding (`Has(n) => n`):
		// the payload type is read from the variant's __ev field.
		{"matchexpr-userenum-bind", `enum O { Has(i32), Nil } function main(): i32 { var o: O = Has(7); var y = match (o) { Has(n) => n, Nil => 0 }; return y; }`},
		{"matchexpr-userenum-3var", `enum E { Num(i32), Word, Nil } function main(): i32 { var e: E = Num(5); return match (e) { Num(n) => n * 3, Word => 1, Nil => 0 }; }`},
		// STRING-valued if / match expressions: the inlined temp holds a string
		// pointer and is marked a string, so `.len()` / concat dispatch and the
		// outer binding tracks it as a string (extends the i32-gated IIFE inline).
		{"ifexpr-str", `function main(): i32 { var n = 5; var s = if (n > 3) { "big" } else { "small" }; return s.len(); }`},
		{"ifexpr-str-return", `function classify(n: i32): string { return if (n > 0) { "pos" } else { "nonpos" }; } function main(): i32 { return classify(5).len() + classify(0 - 1).len(); }`},
		{"ifexpr-str-elseif", `function main(): i32 { var n = 5; var s = if (n > 10) { "big" } else if (n > 3) { "mid" } else { "low" }; return s.len(); }`},
		{"ifexpr-str-concat", `function main(): i32 { var n = 2; var s = if (n > 3) { "a" } else { "bb" }; return (s + "!").len(); }`},
		{"matchexpr-str-unit", `enum C { A, B } function main(): i32 { var c: C = A; var s = match (c) { A => "xx", B => "y" }; return s.len(); }`},
		{"matchexpr-str-3arm", `enum C { R, G, B } function pick(c: C): string { return match (c) { R => "red", G => "green", B => "blue" }; } function main(): i32 { return pick(G).len(); }`},
		{"matchexpr-str-payload", `enum E { N(i32), Z } function f(e: E): string { return match (e) { N(n) => if (n > 0) { "pos" } else { "neg" }, Z => "zero" }; } function main(): i32 { return f(N(5)).len() + f(Z).len(); }`},
		// f64-valued if / match expressions: the inline temp is an 8-byte f64 temp
		// (the binding tracks the result as f64). i64 results stay on the AST path.
		{"ifexpr-f64", `function main(): i32 { var n = 5; var f = if (n > 3) { 1.5 } else { 2.5 }; return (f * 2.0) as i32; }`},
		{"ifexpr-f64-return", `function pick(n: i32): f64 { return if (n > 0) { 1.5 } else { 0.5 }; } function main(): i32 { return (pick(5) * 10.0) as i32; }`},
		{"ifexpr-f64-elseif", `function main(): i32 { var n = 5; var f = if (n > 10) { 1.0 } else if (n > 3) { 2.5 } else { 9.0 }; return (f * 2.0) as i32; }`},
		{"matchexpr-f64", `enum C { A, B } function main(): i32 { var c: C = A; var f = match (c) { A => 1.5, B => 2.5 }; return (f * 10.0) as i32; }`},
		{"matchexpr-f64-3arm", `enum C { R, G, B } function w(c: C): f64 { return match (c) { R => 1.5, G => 2.5, B => 3.5 }; } function main(): i32 { return (w(G) * 10.0) as i32; }`},
		// i64-valued if / match expressions: the inline temp is an 8-byte i64 temp
		// (any branch with an i64-width value classifies it — annotated, unannotated,
		// either branch order). A fully-small-literal i64 expression stays on AST.
		{"ifexpr-i64-annot", `function main(): i32 { var n = 5; var x: i64 = if (n > 3) { 5000000000 } else { 1 }; return (x % 7) as i32; }`},
		{"ifexpr-i64-unannot", `function main(): i32 { var n = 5; var x = if (n > 3) { 5000000000 } else { 1 }; return (x % 7) as i32; }`},
		{"ifexpr-i64-elsebig", `function main(): i32 { var n = 1; var x: i64 = if (n > 3) { 1 } else { 5000000000 }; return (x % 7) as i32; }`},
		{"ifexpr-i64-return", `function pick(n: i32): i64 { return if (n > 0) { 9000000000 } else { 1 }; } function main(): i32 { return (pick(5) % 1000) as i32; }`},
		{"matchexpr-i64", `enum C { A, B } function main(): i32 { var c: C = A; var x: i64 = match (c) { A => 8000000000, B => 1 }; return (x % 1000) as i32; }`},
		{"i64-cmp", `function main(): i32 { var x: i64 = 5000000000; var y: i64 = 4000000000; if (x > y) { return 7; } return 0; }`},
		{"i64-add", `function main(): i32 { var a: i64 = 3000000000; var b: i64 = 3000000000; var c: i64 = a + b; if (c > 5000000000) { return 11; } return 0; }`},
		{"i64-mul", `function main(): i32 { var a: i64 = 100000; var b: i64 = 100000; var c: i64 = a * b; if (c > 4000000000) { return 5; } return 0; }`},
		{"i64-sub-neg", `function main(): i32 { var a: i64 = 1000000000; var b: i64 = 2000000000; var c: i64 = a - b; if (c < 0) { return 9; } return 0; }`},
		{"i64-loop", `function main(): i32 { var s: i64 = 0; var i: i32 = 0; while (i < 100000) { s = s + 100000; i = i + 1; } if (s > 4000000000) { return 13; } return 0; }`},
		{"and-true", `function main(): i32 { var x = 5; if (x > 0 && x < 10) { return 7; } return 0; }`},
		{"and-false", `function main(): i32 { var x = 15; if (x > 0 && x < 10) { return 7; } return 0; }`},
		{"or-true", `function main(): i32 { var x = 15; if (x < 0 || x > 0) { return 3; } return 0; }`},
		{"and-or-nest", `function main(): i32 { var a = 1; var b = 0; var c = 5; if (a > 0 && b > 0 || c > 0) { return 9; } return 0; }`},
		{"and-not-operand", `function main(): i32 { var x = 5; if (!(x > 10) && x > 0) { return 4; } return 0; }`},
		{"and-bool-vars", `function main(): i32 { var f = 5 > 3; var g = 2 > 8; if (f && !g) { return 6; } return 0; }`},
		{"strcmp-lt", `function main(): i32 { var a = "apple"; var b = "banana"; if (a < b) { return 7; } return 0; }`},
		{"strcmp-gt", `function main(): i32 { var a = "banana"; var b = "apple"; if (a > b) { return 3; } return 0; }`},
		{"strcmp-le-eq", `function main(): i32 { var a = "abc"; var b = "abc"; if (a <= b) { return 5; } return 0; }`},
		{"strcmp-prefix", `function main(): i32 { var a = "ab"; var b = "abc"; if (a < b) { return 9; } return 0; }`},
		{"strcmp-ge-false", `function main(): i32 { var a = "a"; var b = "b"; if (a >= b) { return 11; } return 0; }`},
		{"while-break", `function main(): i32 { var s = 0; var i = 0; while (i < 10) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }`},
		{"while-continue", `function main(): i32 { var s = 0; var i = 0; while (i < 10) { i = i + 1; if (i % 2 == 1) { continue; } s = s + i; } return s; }`},
		{"while-break-nested", `function main(): i32 { var t = 0; var i = 0; while (i < 3) { var j = 0; while (j < 5) { if (j == 2) { break; } t = t + j; j = j + 1; } i = i + 1; } return t; }`},
		{"while-break-deep-if", `function main(): i32 { var s = 0; var i = 0; while (i < 10) { if (i > 3) { if (i == 4) { break; } } s = s + i; i = i + 1; } return s; }`},
		{"cast-widen", `function main(): i32 { var n = 100000; var x: i64 = n as i64; var y: i64 = x * x; if (y > 4000000000) { return 5; } return 0; }`},
		{"cast-narrow", `function main(): i32 { var big: i64 = 5000000007; var lo = (big as i32); return lo % 100; }`},
		{"cast-mixed", `function main(): i32 { var base: i64 = 4000000000; var i = 5; var s: i64 = base + (i as i64); if (s > 4000000000) { return 7; } return 0; }`},
		{"cast-roundtrip", `function main(): i32 { var n = 42; var x: i64 = n as i64; return (x as i32); }`},
		{"call-8-args", `function add8(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a+b+c+d+e+f+g+h; } function main(): i32 { return add8(1,2,3,4,5,6,7,8); }`},
		{"call-7-args-order", `function f(a:i32,b:i32,c:i32,d:i32,e:i32,g:i32,h:i32):i32 { return a - b - c - d - e - g - h; } function main(): i32 { return f(100,1,2,3,4,5,6); }`},
		{"method-7-args", `struct P { base: i32 } function (p: P) sum7(a:i32,b:i32,c:i32,d:i32,e:i32,f:i32,g:i32): i32 { return p.base + a+b+c+d+e+f+g; } function main(): i32 { var p = P { base: 10 }; return p.sum7(1,2,3,4,5,6,7); }`},
		{"i64-param", `function dbl(x: i64): i64 { return x * 2; } function main(): i32 { var r: i64 = dbl(3000000000); if (r > 5000000000) { return 7; } return 0; }`},
		{"i64-return", `function big(): i64 { return 4000000000; } function main(): i32 { var x: i64 = big() + 1000000000; if (x > 4000000000) { return 5; } return 0; }`},
		{"i64-param-mixed", `function f(a: i64, b: i32): i64 { return a + (b as i64); } function main(): i32 { var r: i64 = f(4000000000, 5); if (r > 4000000000) { return 9; } return 0; }`},
		{"i64-return-recursion", `function pow2(n: i32): i64 { if (n <= 0) { return 1; } return pow2(n - 1) * 2; } function main(): i32 { if (pow2(33) > 4000000000) { return 13; } return 0; }`},
		{"i64-div", `function main(): i32 { var a: i64 = 12000000000; var b: i64 = 4; var c: i64 = a / b; if (c > 2000000000) { return 7; } return 0; }`},
		{"i64-rem", `function main(): i32 { var a: i64 = 12000000007; var r = (a % 10) as i32; return r; }`},
		{"i64-div-trunc", `function main(): i32 { var a: i64 = 10000000000; var c: i64 = a / 3; if (c > 3000000000) { return 5; } return 0; }`},
		{"i64-div-signed", `function main(): i32 { var a: i64 = 0 - 12000000000; var c: i64 = a / 4; if (c < 0) { return 9; } return 0; }`},
		{"arr-slice", `function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[1:4]; return b[0] + b[2]; }`},
		{"arr-slice-len", `function main(): i32 { var a = [1, 2, 3, 4, 5]; var b = a[1:4]; return b.len(); }`},
		{"arr-slice-strarr", `function main(): i32 { var a = ["x", "yy", "zzz", "w"]; var b = a[1:3]; return b[0].len() + b[1].len(); }`},
		{"arr-slice-full", `function main(): i32 { var a = [5, 10, 15, 20]; var b = a[0:2]; return b[0] + b[1]; }`},
		{"enum-unit", `enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(B); }`},
		{"enum-three", `enum Shape { Circle(i32), Square(i32), Empty } function area(s: Shape): i32 { match (s) { Circle(r) => { return r + 1; }, Square(w) => { return w * 2; }, Empty => { return 7; } } return 99; } function main(): i32 { return area(Circle(4)) + area(Square(5)) + area(Empty); }`},
		{"enum-wildcard", `enum E { A(i32), B, C } function f(e: E): i32 { match (e) { A(n) => { return n; }, _ => { return 100; } } return 0; } function main(): i32 { return f(B); }`},
		// `@derive(Debug)` (#2708) — the self-host synthesizes a type-directed
		// `to_debug` (numbers → to_string, strings → quoted, nominal → to_debug),
		// matching the native structural output. The AST and IR paths must agree
		// on the rendered length. (The inline `trait Debug` is discarded by the
		// self-host; it keeps the program valid for the native compiler.)
		{"derive-debug-struct", `trait Debug { function to_debug(self: Self): string; } @derive(Debug) struct P { x: i32, name: string } function main(): i32 { return P { x: 7, name: "hi" }.to_debug().len(); }`},
		{"derive-debug-enum-unit", `trait Debug { function to_debug(self: Self): string; } @derive(Debug) enum E { Dot, Circle(i32), Tag(string) } function main(): i32 { return Dot.to_debug().len(); }`},
		{"derive-debug-enum-payload", `trait Debug { function to_debug(self: Self): string; } @derive(Debug) enum E { Dot, Circle(i32), Tag(string) } function main(): i32 { return Circle(5).to_debug().len() + Tag("ab").to_debug().len(); }`},
		{"derive-debug-nested", `trait Debug { function to_debug(self: Self): string; } @derive(Debug) struct P { x: i32, name: string } @derive(Debug) struct N { p: P, n: i32 } function main(): i32 { return N { p: P { x: 1, name: "z" }, n: 9 }.to_debug().len(); }`},
		// Out of the IR subset -> falls back to the AST emitter under -ir; must
		// still match (proves the fallback path is intact).
		{"method-falls-back", "struct P { x: i32 } pub function (p: P) get(): i32 { return p.x; } function main(): i32 { var p = P { x: 42 }; return p.get(); }"},
		// Byte-source builtins (issue #2747) — DETERMINISTIC shapes only, so the
		// AST and IR paths must agree. random_bytes(n).len() is always n; as_bytes
		// / bytes byte values on a literal are fixed. (random_i32 + the random byte
		// VALUES are non-deterministic, so they ride the IR-only block below.)
		{"random-bytes-len", `function main(): i32 { return random_bytes(8).len(); }`},
		{"random-bytes-len-var", `function main(): i32 { var s: string = random_bytes(13); return s.len(); }`},
		{"as-bytes-len", `function main(): i32 { var b: i32[] = "ABC".as_bytes(); return b.len(); }`},
		{"as-bytes-vals", `function main(): i32 { var b: i32[] = "ABC".as_bytes(); return b[0] + b[1] + b[2]; }`},
		{"bytes-vals", `function main(): i32 { var b: i32[] = "AB".bytes(); return b[0] + b[1]; }`},
		{"as-bytes-heap", `function main(): i32 { var b: i32[] = "ABCDEFGHIJ".as_bytes(); return b.len() + b[9]; }`},
		// string.split(sep) → string[] (op_str_split). The AST path emits
		// __fern_str_split inside emit_runtime (gated on the str_search need that
		// the split dispatch sets), and the IR path emits its own transcribed
		// __fern_str_split — so the segment count / element lengths must match.
		{"split-count", `function main(): i32 { var p = "a,b,c".split(","); return p.len(); }`},
		{"split-first-len", `function main(): i32 { var p = "foo,bar,baz".split(","); return p[0].len(); }`},
		{"split-elem-lens", `function main(): i32 { var p = "a,bb,ccc".split(","); return p[0].len() + p[1].len() + p[2].len(); }`},
		{"split-multichar-sep", `function main(): i32 { var p = "axxbxxc".split("xx"); return p.len() * 10 + p[2].len(); }`},
		{"split-no-match", `function main(): i32 { var p = "abc".split(","); return p.len() * 10 + p[0].len(); }`},
		{"split-empty-sep", `function main(): i32 { var p = "abc".split(""); return p.len() * 10 + p[0].len(); }`},
		{"split-trailing-sep", `function main(): i32 { var p = "a,b,".split(","); return p.len(); }`},
		{"split-leading-sep", `function main(): i32 { var p = ",a,b".split(","); return p.len() * 10 + p[0].len(); }`},
		{"split-loop-sum", `function main(): i32 { var p = "a,bb,ccc,dddd".split(","); var s = 0; var i = 0; while (i < p.len()) { s = s + p[i].len(); i = i + 1; } return s; }`},
		{"split-forin", `function main(): i32 { var s = 0; for part in "x,yy,zzz".split(",") { s = s + part.len(); } return s; }`},
		{"split-param", `function nfields(s: string): i32 { return s.split(",").len(); } function main(): i32 { return nfields("a,b,c,d"); }`},
		{"split-freecall", `function main(): i32 { var p = str_split("a,b,c", ","); return p.len(); }`},
		{"split-then-index-direct", `function main(): i32 { return "one,two,three".split(",")[1].len(); }`},
		// Scalar string search predicates → i32/boolean (op_str_starts_with /
		// _ends_with / _index_of; contains = index_of >= 0). Allocation-free; the
		// AST path emits the __fern_str_* search runtime under the str_search need,
		// and the IR path emits the transcribed bodies — results must match.
		{"starts-with-true", `function main(): i32 { var s = "hello"; if (s.starts_with("he")) { return 7; } return 0; }`},
		{"starts-with-false", `function main(): i32 { var s = "hello"; if (s.starts_with("lo")) { return 7; } return 9; }`},
		{"starts-with-empty", `function main(): i32 { var s = "hi"; if (s.starts_with("")) { return 3; } return 0; }`},
		{"starts-with-longer", `function main(): i32 { var s = "hi"; if (s.starts_with("hill")) { return 1; } return 5; }`},
		{"ends-with-true", `function main(): i32 { var s = "hello"; if (s.ends_with("lo")) { return 7; } return 0; }`},
		{"ends-with-false", `function main(): i32 { var s = "hello"; if (s.ends_with("he")) { return 7; } return 9; }`},
		{"ends-with-empty", `function main(): i32 { var s = "hi"; if (s.ends_with("")) { return 4; } return 0; }`},
		{"index-of-hit", `function main(): i32 { var s = "abcdef"; return s.index_of("cd"); }`},
		{"index-of-zero", `function main(): i32 { var s = "abcdef"; return s.index_of("ab") + 100; }`},
		{"index-of-miss", `function main(): i32 { var s = "abcdef"; var r = s.index_of("zz"); if (r < 0) { return 42; } return 0; }`},
		{"index-of-empty", `function main(): i32 { var s = "abc"; return s.index_of("") + 50; }`},
		{"contains-true", `function main(): i32 { var s = "hello world"; if (s.contains("o w")) { return 7; } return 0; }`},
		{"contains-false", `function main(): i32 { var s = "hello"; if (s.contains("xyz")) { return 7; } return 9; }`},
		{"predicate-param", `function pre(s: string, p: string): i32 { if (s.starts_with(p)) { return 1; } return 0; } function main(): i32 { return pre("foobar", "foo") * 10 + pre("foobar", "bar"); }`},
		{"predicate-freecall", `function main(): i32 { if (str_starts_with("hello", "he")) { return str_index_of("hello", "ll"); } return 0; }`},
		{"predicate-on-literal", `function main(): i32 { if ("abcdef".contains("cde")) { return "abcdef".index_of("d"); } return 0; }`},
		// ASCII case transforms → fresh string (op_str_to_upper / _to_lower). The
		// AST path emits __fern_str_to_upper/_lower (str_search runtime); the IR
		// path emits its own emit_ir_str_case bodies — lengths/bytes must match.
		{"to-upper-len", `function main(): i32 { var s = "Hello"; return s.to_upper().len(); }`},
		{"to-upper-byte", `function main(): i32 { var s = "abc"; var u = s.to_upper(); return u[0]; }`},
		{"to-lower-byte", `function main(): i32 { var s = "ABC"; var l = s.to_lower(); return l[2]; }`},
		{"to-upper-mixed", `function main(): i32 { var u = "aB9z".to_upper(); return u[0] + u[1] + u[2] + u[3]; }`},
		{"to-lower-mixed", `function main(): i32 { var l = "Ab9Z".to_lower(); return l[0] + l[1] + l[2] + l[3]; }`},
		{"to-upper-empty", `function main(): i32 { return "".to_upper().len() + 5; }`},
		{"case-roundtrip", `function main(): i32 { var s = "Hello"; if (s.to_upper().to_lower() == "hello") { return 7; } return 0; }`},
		{"case-param", `function up(s: string): i32 { return s.to_upper()[0]; } function main(): i32 { return up("xyz"); }`},
		{"case-on-literal", `function main(): i32 { return "Mixed".to_lower().len(); }`},
		// String repeat → fresh string (op_str_repeat). AST path emits
		// __fern_str_repeat (str_search runtime); IR path emits emit_ir_str_repeat.
		{"repeat-len", `function main(): i32 { return "ab".repeat(3).len(); }`},
		{"repeat-byte", `function main(): i32 { var r = "xy".repeat(4); return r[0] + r[7]; }`},
		{"repeat-one", `function main(): i32 { return "hello".repeat(1).len(); }`},
		{"repeat-zero", `function main(): i32 { return "hello".repeat(0).len() + 9; }`},
		{"repeat-var", `function main(): i32 { var s = "ab"; var n = 5; return s.repeat(n).len(); }`},
		{"repeat-param", `function rep(s: string, n: i32): i32 { return s.repeat(n).len(); } function main(): i32 { return rep("xyz", 4); }`},
		{"repeat-concat", `function main(): i32 { var r = "a".repeat(3) + "b".repeat(2); return r.len(); }`},
		// String trim → fresh string with leading/trailing whitespace removed
		// (op_str_trim). AST path emits __fern_str_trim (str_search runtime); IR
		// path emits emit_ir_str_trim (both a zero-copy view, same len/bytes).
		{"trim-both", `function main(): i32 { return "  hi  ".trim().len(); }`},
		{"trim-byte", `function main(): i32 { var t = "  hi".trim(); return t[0]; }`},
		{"trim-tabs-nl", `function main(): i32 { return "\t\n ab \r\n".trim().len(); }`},
		{"trim-none", `function main(): i32 { return "abc".trim().len(); }`},
		{"trim-all-ws", `function main(): i32 { return "    ".trim().len() + 5; }`},
		{"trim-empty", `function main(): i32 { return "".trim().len() + 7; }`},
		{"trim-leading", `function main(): i32 { var t = "   xy".trim(); return t.len() * 10 + t[0]; }`},
		{"trim-param", `function tn(s: string): i32 { return s.trim().len(); } function main(): i32 { return tn("  padded  "); }`},
		// String reverse → fresh string with bytes reversed (op_str_reverse). AST
		// path emits __fern_str_reverse (str_reverse runtime); IR path emits
		// emit_ir_str_reverse — same content/length.
		{"reverse-len", `function main(): i32 { return "hello".reverse().len(); }`},
		{"reverse-first", `function main(): i32 { var r = "abc".reverse(); return r[0]; }`},
		{"reverse-last", `function main(): i32 { var r = "abc".reverse(); return r[2]; }`},
		{"reverse-empty", `function main(): i32 { return "".reverse().len() + 4; }`},
		{"reverse-twice", `function main(): i32 { if ("hello".reverse().reverse() == "hello") { return 7; } return 0; }`},
		{"reverse-param", `function rev(s: string): i32 { return s.reverse()[0]; } function main(): i32 { return rev("xyz"); }`},
		// String replace -> fresh string with every occurrence of old swapped for
		// new (op_str_replace). AST path emits __fern_str_replace; IR path emits
		// emit_ir_str_replace -- same content/length.
		{"replace-len", `function main(): i32 { return "a-b-c".replace("-", "_").len(); }`},
		{"replace-grow", `function main(): i32 { return "aaa".replace("a", "bb").len(); }`},
		{"replace-shrink", `function main(): i32 { return "axbxc".replace("x", "").len(); }`},
		{"replace-byte", `function main(): i32 { var r = "hello".replace("l", "L"); return r[2] + r[3]; }`},
		{"replace-nomatch", `function main(): i32 { return "abc".replace("z", "Q").len(); }`},
		{"replace-empty-old", `function main(): i32 { return "abc".replace("", "X").len(); }`},
		{"replace-multichar", `function main(): i32 { return "axxbxxc".replace("xx", "-").len(); }`},
		{"replace-param", `function rp(s: string): i32 { return s.replace("o", "0").len(); } function main(): i32 { return rp("foobar"); }`},
		// String chars -> string[] of 1-char strings (op_str_chars; result is_arr +
		// is_strarr like split). AST emits __fern_str_chars; IR emits emit_ir_str_chars.
		{"chars-len", `function main(): i32 { return "abcde".chars().len(); }`},
		{"chars-elem-len", `function main(): i32 { return "abc".chars()[1].len(); }`},
		{"chars-elem-byte", `function main(): i32 { return "abc".chars()[1][0]; }`},
		{"chars-empty", `function main(): i32 { return "".chars().len() + 4; }`},
		{"chars-forin", `function main(): i32 { var n = 0; for c in "hello".chars() { n = n + c.len(); } return n; }`},
		{"chars-loop-sum", `function main(): i32 { var cs = "abc".chars(); var s = 0; var i = 0; while (i < cs.len()) { s = s + cs[i][0]; i = i + 1; } return s % 200; }`},
		{"chars-param", `function nc(s: string): i32 { return s.chars().len(); } function main(): i32 { return nc("xyzw"); }`},
		// String lines -> string[] split on \n with trailing-empty drop (op_str_lines;
		// result is_arr + is_strarr). AST inlines lines; IR emits emit_ir_str_lines.
		{"lines-3", `function main(): i32 { return "a\nb\nc".lines().len(); }`},
		{"lines-trailing-nl", `function main(): i32 { return "a\nb\nc\n".lines().len(); }`},
		{"lines-none", `function main(): i32 { return "hello".lines().len(); }`},
		{"lines-empty", `function main(): i32 { return "".lines().len() + 4; }`},
		{"lines-just-nl", `function main(): i32 { return "\n".lines().len() + 5; }`},
		{"lines-elem", `function main(): i32 { var ls = "ab\ncd".lines(); return ls[1].len() * 10 + ls[1][0]; }`},
		{"lines-forin", `function main(): i32 { var n = 0; for ln in "a\nbb\nccc".lines() { n = n + ln.len(); } return n; }`},
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

	// IR-ONLY assertions (issue #2747 / uuid #2682). random_i32 has no legacy
	// x86-64 AST counterpart, so it can't ride the differential gate — compile
	// only via -ir and assert structural properties of the IR output. uuid_v4
	// (random_bytes + sliced-hex string building) likewise exercises the full
	// byte-source path through the IR backend.
	irOnly := []struct {
		name string
		src  string
		want int
	}{
		// Two random_i32 draws differ (a stuck/zero generator returns 0/1).
		{"random-i32-varies", `function main(): i32 { var a: i32 = random_i32(); var b: i32 = random_i32(); if (a == 0) { return 0; } if (a == b) { return 1; } return 7; }`, 7},
		// A random byte is in 0..255.
		{"random-bytes-byte-range", `function main(): i32 { var s: string = random_bytes(4); var x: i32 = s[0]; if (x >= 0) { if (x <= 255) { return 1; } } return 0; }`, 1},
		// uuid_v4: 36 chars, '4' at index 14, '-' at 8, distinct draws.
		{"uuid-v4", uuidV4Program, 0},
		// Range-for through the x86-64 self-host IR path (#2699). The legacy
		// AST x86-64 emitter has no range desugar, so these ride the IR-only
		// gate. Half-open `..` and inclusive `..=` (closed interval) — the
		// latter exits on `i <= hi`, so it visits HIGH and runs one more
		// iteration than the half-open form.
		{"range-sum", `function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }`, 10},
		{"rangei-sum", `function main(): i32 { var s = 0; for i in 0..=5 { s = s + i; } return s; }`, 15},
		{"rangei-single", `function main(): i32 { var c = 0; for i in 5..=5 { c = c + 1; } return c; }`, 1},
		{"rangei-reversed", `function main(): i32 { var c = 9; for i in 9..=3 { c = c + 1; } return c; }`, 9},
		{"rangei-continue", `function main(): i32 { var s = 0; for i in 0..=10 { if (i == 3) { continue; } s = s + i; } return s; }`, 52},
		// Multi-payload variant binds: a `Pt(x, y)` arm binds EVERY payload
		// field (struct_get at successive indices), not just the first. The
		// legacy AST x86-64 emitter binds only field 0, so these ride the
		// IR-only gate against the native interp's value.
		{"match-multi-bind", `enum P { Pt(i32, i32), Origin } function f(p: P): i32 { match (p) { Pt(x, y) => { return x * y; }, Origin => { return 0; } } return 0; } function main(): i32 { return f(Pt(6, 7)); }`, 42},
		{"match-multi-bind-three", `enum T { Tri(i32, i32, i32), Empty } function f(t: T): i32 { match (t) { Tri(a, b, c) => { return a + b * c; }, Empty => { return 0; } } return 0; } function main(): i32 { return f(Tri(1, 2, 3)); }`, 7},
		{"match-multi-bind-mixed", `enum M { Kv(string, i32), None2 } function f(m: M): i32 { match (m) { Kv(k, v) => { return k.len() + v; }, None2 => { return 0; } } return 0; } function main(): i32 { return f(Kv("hello", 5)); }`, 10},
		{"match-multi-bind-skip", `enum P { Pt(i32, i32), Origin } function f(p: P): i32 { match (p) { Pt(_, y) => { return y; }, Origin => { return 0; } } return 0; } function main(): i32 { return f(Pt(6, 7)); }`, 7},
	}
	for _, tc := range irOnly {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRun(t, tc.src, true); got != tc.want {
				t.Errorf("IR path %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
