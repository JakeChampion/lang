package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostAsmIRPath WAS the AST-vs-IR differential gate: it compiled each
// program both ways through asm_ir_run and asserted identical exit codes, proving
// the IR path behaviour-equivalent to the production AST path
// (docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md §3).
//
// That comparison is GONE, because it can no longer compare anything. Since
// #3457 slice 5 the driver has no AST leg: both the `-ir`-on and `-ir`-off paths
// route IR or refuse, so the two legs are the same emitter and the assertion was
// vacuous. Deleting it is what slice-5 step 4 specifies ("DELETE, do not
// re-point") — and re-pointing is genuinely unavailable, since these cases are in
// the driver dialect (parser.module_with_builtins injects map_new / __alloc /
// casts) that native `-interp` rejects, so there is no independent oracle to
// swap in.
//
// What the ~930 cases still buy: every one must compile through the IR path,
// assemble, link and run. That is an ELIGIBILITY + assembly guard at a scale
// nothing else in the suite provides, and it is exactly what keeps slice 5 green.
// What they no longer buy: a wrong exit code passes. The dedicated *_ir_test.go
// corpora carry the oracle role now (each checks values, most against
// `fern -interp`), which is the trade slice 5 accepts.
//
// Slice 16 eligibility is pure i32 functions (no params, no user calls, no
// arrays), so single-function i32 programs exercise the IR path; multi-
// function / array programs fall back to AST under `-ir` and must still match.
func TestSelfHostAsmIRPath(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	// Build the asm_run driver once via the production x86-64 backend.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
		// flat_map shape: `for y in f(x)` where `f` is a closure PARAM whose type
		// `(T) => U[]` returns an array (ParamDecl.ret_arr). lower_func admits
		// such a param into its arr_ret_fns view, so the foreach snapshot
		// `var $forit = f(x)` marks an owned array and `for y in f(x)` lowers like
		// `for x in xs`. Was BAIL lower (the single bail across the functional
		// core); now ir. The bare-fn-name arg `apply(dup)` rides the existing
		// fn-value-arg path (callee_param_is_fn still sees type_name "fn").
		{"fnval-ret-arr-forin", `function apply(xs: i32[], f: (i32) => i32[]): i32 { var out = 0; for x in xs { for y in f(x) { out = out + y; } } return out; } function dup(n: i32): i32[] { return [n, n]; } function main(): i32 { return apply([1, 2, 3], dup); }`},
		{"fnval-ret-arr-varlen", `function apply(xs: i32[], f: (i32) => i32[]): i32 { var c = 0; for x in xs { for y in f(x) { c = c + 1; } } return c; } function upto(n: i32): i32[] { var a: i32[] = []; var i = 0; while (i < n) { a = a.append(i); i = i + 1; } return a; } function main(): i32 { return apply([1, 2, 3], upto); }`},
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
		// (i8/i16/u16 were retired (#4408); u8 is the only sub-word type left,
		// so the u16-mask and i8-sign-extend cast cases that used to live here
		// are gone rather than force-substituted onto a width that no longer
		// exists.)
		{"cast-u8-mask", "function main(): i32 { return (300 as u8) as i32; }"}, // 300 & 255 = 44
		{"cast-chain", "function main(): i32 { var x: i32 = 65; return ((x as u8) as i32); }"},
		// Array builder methods: .with (reassign-self -> in-place arr_set) and
		// .append (-> __fern_arr_push), plus __alloc_u8 / string_from_bytes_unchecked. These
		// don't overflow u32, so IR matches the AST path.
		{"with-reassign", "function main(): i32 { var a = [10, 20, 30]; a = a.with(1, 99); return a[0] + a[1] + a[2]; }"},
		// __memcpy(dst, src, n): raw byte copy — op_memcpy (rep movsb on x86-64).
		// Was BAIL call (no IR op), which kept core/int's int_to_string and
		// everything calling it off the IR path. Cloning a same-size u8[] box
		// (8-byte header + 3*8-byte slots = 32 bytes) makes the copy observable:
		// dst[0..2] become src's 5/7/9 -> 21. IR must match the AST rep-movsb path.
		{"memcpy-clone", "function main(): i32 { var src: u8[] = __alloc_u8(3); src = src.with(0, 5 as u8); src = src.with(1, 7 as u8); src = src.with(2, 9 as u8); var dst: u8[] = __alloc_u8(3); __memcpy(dst as usize, src as usize, 32); return (dst[0] as i32) + (dst[1] as i32) + (dst[2] as i32); }"},
		// Raw-memory load/store intrinsics — op_load_i32 / op_load_i64 /
		// op_load_ptr / op_store_i32 / op_store_ptr (the inline IR siblings of the
		// AST __fn___load_* runtime helpers). Was BAIL (call/lower: no IR op),
		// which kept core/map's __map_hash and friends off the IR path. A
		// store/load round-trip at a raw usize address makes them observable: IR
		// must match the AST runtime-helper path.
		{"rawmem-i32", "function main(): i32 { var buf: u8[] = __alloc_u8(8); var p: usize = buf as usize; __store_i32(p, 1234567); return __load_i32(p); }"},
		{"rawmem-ptr-i64", "function main(): i32 { var buf: u8[] = __alloc_u8(16); var p: usize = buf as usize; __store_ptr(p, 9999); __store_i32(p + 8, 4242); var c: usize = __load_ptr(p); var b: i64 = __load_i64(p + 8); if ((c as i32) == 9999 && (b % 100000) as i32 == 4242) { return 7; } return 1; }"},
		// Array-receiver method call `arr.<m>(args)` -> the std/array auto-discovered
		// helper `__method_Array_<m>(arr, args)`. Was BAIL (the array receiver fell
		// through to the prim path and mis-dispatched to `i32.<m>`); now
		// expr_recv_prim_type returns "" for arrays and find_arr_method resolves the
		// helper. This is the shape std/array distinct/distinct_count/mode use
		// (calling index_of via method syntax). dedup of [x,y,x,z,y] over i32 -> 3.
		{"arr-method-dispatch", "function __method_Array_idx_i(arr: i32[], v: i32): i32 { var i = 0; while (i < arr.len()) { if (arr[i] == v) { return i; } i = i + 1; } return 0 - 1; } function distinct_n(arr: i32[]): i32 { var seen: i32[] = []; var i = 0; while (i < arr.len()) { if (seen.idx_i(arr[i]) < 0) { seen = seen.append(arr[i]); } i = i + 1; } return seen.len(); } function main(): i32 { var a: i32[] = []; a = a.append(3); a = a.append(7); a = a.append(3); a = a.append(9); a = a.append(7); return distinct_n(a); }"},
		// Chained method call `recv.m().n()` — a method on a method-call RESULT.
		// Was BAIL call: the eligibility check (calls_only_known) lowered via
		// lower_func_for_noret, which passed empty str_ret_fns, so the inner
		// `s.m()` result typed as i32 and the outer `.n()` mis-resolved to
		// `i32.n`. lower_func_for_noret now mirrors the emit path's registries, so
		// the chained string-method dispatches `string.n` (the shape i32.to_rgb_hex
		// uses: r.to_hex().pad_start(...)). "ab".dup().dup() -> "abababab", len 8.
		{"chained-str-method", "function (s: string) dup(): string { return s + s; } function f(s: string): i32 { return s.dup().dup().len(); } function main(): i32 { return f(\"ab\"); }"},
		// Method-name collision across receiver types with different return types:
		// `bump` exists on i32 (-> i32) AND string (-> string). str_ret_fns is now
		// keyed "<Type>.<method>" (not bare), so `b.bump()` on an i32 does NOT
		// wrongly type as string (which let the chained `.chr()` mis-dispatch to a
		// nonexistent string.chr -> BAIL). This is the std/string case_separator /
		// to_acronym shape (i32.to_lower vs string.to_lower). f(64): 64+1 -> char
		// 65 'A' -> s[0] -> 65.
		{"method-name-collision", "function (n: i32) bump(): i32 { return n + 1; } function (s: string) bump(): string { return s + \"!\"; } function (n: i32) chr(): string { var a: u8[] = __alloc_u8(1); a = a.with(0, n as u8); return string_from_bytes_unchecked(a); } function f(b: i32): i32 { var s: string = b.bump().chr(); return s[0] as i32; } function main(): i32 { return f(64); }"},
		// Chained ARRAY-method call `arr.m().n()` where the inner m returns an
		// array — `__method_Array_rev` (-> i32[]) then `.sum2()`. Was BAIL call:
		// expr_is_arr_src didn't recognise an array-RETURNING array-method call
		// result as an array source, so the outer `.sum2()` mis-dispatched. Now
		// it resolves the helper + checks is_arr_ret_fn. This is the std/string
		// reverse_words shape (`words.reverse().join(" ")`). rev([1,2,3])=[3,2,1],
		// sum2 -> 6.
		{"chained-arr-method", "function __method_Array_rev(xs: i32[]): i32[] { var out: i32[] = []; var i = xs.len() - 1; while (i >= 0) { out = out.append(xs[i]); i = i - 1; } return out; } function __method_Array_sum2(xs: i32[]): i32 { var s = 0; var i = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; } function f(xs: i32[]): i32 { return xs.rev().sum2(); } function main(): i32 { var a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return f(a); }"},
		// f32 method dispatch: f32 is stored as an 8-byte f64, but its TYPE is
		// tracked distinctly (local_is_f32) so an f32 receiver dispatches
		// "f32.<m>", NOT the f64 twin — they can differ (e.g. to_string precision).
		// Here f32.tag returns 32, f64.tag returns 64; an f32 value must pick 32
		// and an f64 value 64. 32*100+64 = 3264 (exit 192 mod 256). Was BAIL (f32
		// receiver not admitted as prim + conflated with f64).
		{"f32-vs-f64-dispatch", "function (x: f32) tag(): i32 { return 32; } function (x: f64) tag(): i32 { return 64; } function f(): i32 { var v: f32 = 1.0 as f32; var w: f64 = 1.0; return v.tag() * 100 + w.tag(); } function main(): i32 { return f(); }"},
		// f32-only methods (no f64 twin) on f32 locals — the std/float method
		// shape (`__op_f64(x as f64) as f32`). myabs(-5)=5, myclamp via mymax(7,3)=7
		// -> 5*10+7 = 57.
		{"f32-methods", "function (x: f32) myabs(): f32 { var d: f64 = x as f64; if (d < 0.0) { return (0.0 - d) as f32; } return d as f32; } function (x: f32) mymax(y: f32): f32 { if ((x as f64) > (y as f64)) { return x; } return y; } function (x: f32) myclamp(lo: f32, hi: f32): f32 { return lo.mymax(x); } function f(): i32 { var a: f32 = 0.0 - 5.0 as f32; var b = a.myabs(); var c: f32 = 3.0 as f32; var m = c.myclamp(7.0 as f32, 9.0 as f32); return (b as i32) * 10 + (m as i32); } function main(): i32 { return f(); }"},
		// Built-in struct types (std/time's Date / Span etc. are registered in the
		// Go checker, not `struct`-declared) are injected into the IR path's struct
		// table by lift_lambdas (matching the AST path's module_with_builtins), so
		// constructing them + reading fields lowers. d.month*100 + d.day + s.days =
		// 6*100 + 15 + 3 = 618 (exit 106 mod 256). Was BAIL (Date layout unknown).
		{"builtin-struct-date", "function f(): i32 { var d = Date { year: 2026, month: 6, day: 15 }; var s = Span { years: 0, months: 0, weeks: 0, days: 3, hours: 0, minutes: 0, seconds: 0, nanos: 0 }; return d.month * 100 + d.day + s.days; } function main(): i32 { return f(); }"},
		// Sub-word (u8[]) array as a STRUCT FIELD (is_leaksafe_array_field
		// admits u8[], the only sub-word array type left after #4408) — a
		// byte-buffer struct (the BytesWriter
		// shape from std/io_buffered). The field stores a pointer; elements ride
		// the i32[] 4-byte-slot representation (op_arr_push / op_arr_get(32)), the
		// sub-word value pre-wrapped by `as u8` at the push site. Binding the
		// field to a local (`var data = w.data`) aliases via a Perceus dup,
		// append grows the clone, struct-lit spread rebuilds. 10+20+33 = 63.
		{"subword-arr-field", "struct BW { data: u8[] } function bw_new(): BW { var e: u8[] = []; return BW { data: e }; } function (w: BW) push(b: u8): BW { var data: u8[] = w.data; data = data.append(b); return BW { ...w, data: data }; } function (w: BW) sum(): i32 { var data: u8[] = w.data; var t: i32 = 0; var i: i32 = 0; while (i < data.len()) { t = t + (data[i] as i32); i = i + 1; } return t; } function main(): i32 { var w = bw_new(); w = w.push(10 as u8); w = w.push(20 as u8); w = w.push(33 as u8); return w.sum(); }"},
		// The checker-injected BytesWriter built-in (std/io_buffered) reachable on
		// the IR path: same u8[]-field shape, injected by inject_builtin_enums so
		// its layout + leaf-safety resolve. write_byte spreads + appends; len()
		// reads the buffer length. 3 bytes written -> len 3.
		{"builtin-byteswriter", "function main(): i32 { var w: BytesWriter = BytesWriter { data: [] }; w = BytesWriter { ...w, data: w.data.append(65 as u8) }; w = BytesWriter { ...w, data: w.data.append(66 as u8) }; w = BytesWriter { ...w, data: w.data.append(67 as u8) }; return w.data.len(); }"},
		// A FRESH scalar-array-returning CALL as a struct-lit field value (move):
		// `S { data: gen() }` where gen(): i32[]. Previously only array literals /
		// idents / field-copies / `.with`/`.append` clones were admitted; a plain
		// array-returning call (expr_is_arr_src — an arr_ret_fn move source) now
		// lowers, owned by the struct with no alias-inc. gen() = [3,4,5]; sum 12.
		{"scalar-arr-call-field", "struct S { data: i32[] } function gen(): i32[] { var a: i32[] = []; a = a.append(3); a = a.append(4); a = a.append(5); return a; } function (x: S) sum(): i32 { var d: i32[] = x.data; var t: i32 = 0; var i: i32 = 0; while (i < d.len()) { t = t + d[i]; i = i + 1; } return t; } function main(): i32 { var x: S = S { data: gen() }; return x.sum(); }"},
		// The std/stream `stream_from_string` shape: a string `.bytes()` call (a
		// fresh u8[] move source) as a u8[] struct-lit field value. 65+66+67 = 198.
		{"bytes-call-field", "struct S { data: u8[], pos: i32 } function mk(s: string): S { return S { data: s.bytes(), pos: 0 }; } function (x: S) sum(): i32 { var d: u8[] = x.data; var t: i32 = 0; var i: i32 = 0; while (i < d.len()) { t = t + (d[i] as i32); i = i + 1; } return t; } function main(): i32 { var x: S = mk(\"ABC\"); return x.sum(); }"},
		// A sub-word (u8[]) array as a TUPLE element — the std/stream
		// `(s: Stream) read_all(): (u8[], Stream)` cursor-idiom shape. u8[] (the
		// only sub-word array type left after #4408) rides the same i32[]
		// 4-byte-slot tuple representation as i32[]/u32[], so
		// tuple_elems_lowerable now admits them: construction
		// stores the buffer pointer in one slot, the destructure binds it as an
		// array local (mark_arr), and `bytes[i]` rides the 4-byte arr_get. Builds
		// [10,20,35], reads it back through the (u8[], St) return: 10+20+35 + pos
		// 3 = 68.
		{"subword-arr-tuple-elem", "struct St { data: u8[], pos: i32 } function (s: St) read_all(): (u8[], St) { var out: u8[] = []; var i: i32 = s.pos; var end: i32 = s.data.len(); while (i < end) { out = out.append(s.data[i]); i = i + 1; } return (out, St { ...s, pos: end }); } function main(): i32 { var d: u8[] = []; d = d.append(10 as u8); d = d.append(20 as u8); d = d.append(35 as u8); var s: St = St { data: d, pos: 0 }; var (bytes, s2) = s.read_all(); var total: i32 = 0; var i: i32 = 0; while (i < bytes.len()) { total = total + (bytes[i] as i32); i = i + 1; } return total + s2.pos; }"},
		// 8-byte-element (i64[]/f64[]) and string[] arrays as a TUPLE element,
		// returned from a function + destructured. The buffer is a heap pointer in
		// the tuple slot (width/kind-agnostic at construction); the destructure
		// marks the bound slot i64arr / f64arr / strarr so `xs[i]` reads at the
		// right width / as a string. i64: 7+11+3=21; f64: 2+4+1=7; string[]:
		// len("hello")+len("world!")+2 = 13; `.N` direct access: 100+9=109.
		{"i64-arr-tuple-elem", "function mk(): (i64[], i32) { var a: i64[] = []; a = a.append(7 as i64); a = a.append(11 as i64); return (a, 3); } function main(): i32 { var (xs, n) = mk(); return (xs[0] as i32) + (xs[1] as i32) + n; }"},
		{"f64-arr-tuple-elem", "function mk(): (f64[], i32) { var a: f64[] = []; a = a.append(2.5); a = a.append(4.5); return (a, 1); } function main(): i32 { var (xs, n) = mk(); return (xs[0] as i32) + (xs[1] as i32) + n; }"},
		{"i64-arr-tuple-dotn", "function mk(): (i64[], i32) { var a: i64[] = []; a = a.append(100 as i64); return (a, 9); } function main(): i32 { var t = mk(); return (t.0[0] as i32) + t.1; }"},
		{"strarr-tuple-elem", "function mk(): (string[], i32) { var a: string[] = []; a = a.append(\"hello\"); a = a.append(\"world!\"); return (a, 2); } function main(): i32 { var (ps, n) = mk(); return ps[0].len() + ps[1].len() + n; }"},
		// A nominal-enum value as a TUPLE element — the (JsonValue, parser) cursor
		// shape std/json's parsers use. The enum is a leak-only box in one pointer
		// slot, like a struct/Option element: construction stores the box, the
		// destructure / `.N` read carries the enum name (mark_struct_type) so
		// `match (t.N)` dispatches. payload variant: A(3)+5 = 8; unit variant via
		// `.N` direct access: Green -> t.1+10 = 15.
		{"enum-tuple-elem", "enum E { A(i32), B } function mk(): (E, i32) { return (A(3), 5); } function main(): i32 { var (e, n) = mk(); match (e) { A(x) => { return x + n; }, B => { return n; } } return 0; }"},
		{"enum-tuple-dotn", "enum Color { Red, Green, Blue } function mk(): (Color, i32) { return (Green, 5); } function main(): i32 { var t = mk(); match (t.0) { Red => { return 1; }, Green => { return t.1 + 10; }, Blue => { return 3; } } return 0; }"},
		// A match-EXPRESSION (value position) over a STRUCT / TUPLE scrutinee.
		// Both desugar to a done-flag if-chain wrapped in an IIFE; unlike the enum
		// (StmtMatch) / literal (`_`-terminated chain) forms, that chain has no
		// terminal return, so the IIFE lambda used to fall through and bail the IR
		// path to the AST emitter (#3457 slice 3, #5749's "match-expr over struct"
		// shape). parse_match_expr now flags these (needs_default_return) so the
		// IIFE gets a synthetic terminal return and lowers. struct: 5+1 = 6;
		// tuple: 3+4 = 7.
		{"matchexpr-struct", "struct P { x: i32 } function main(): i32 { var p = P { x: 5 }; var r = match (p) { P { x: v } => v + 1 }; return r; }"},
		{"matchexpr-tuple", "function main(): i32 { var t = (3, 4); var r = match (t) { (a, b) => a + b }; return r; }"},
		// A struct field whose type is a TUPLE containing an fn element
		// (`(i32, () => i32)`). The fn element is a leak-only closure box (one
		// pointer slot, like an Option box), but is_leaksafe_tuple_field_d had no
		// arm for a "clo" element, so the whole struct bailed the IR path to the AST
		// emitter — even reading only the i32 element (#3457 slice 3, #5749's
		// "fn-typed tuple element in a struct field" shape). Admitting a clo tuple
		// element (wide predicate only → IR-eligible, reuse conservatively skipped)
		// closes it: s.p.0 (=3) + s.p.1() (=7) = 10.
		{"structfield-tuple-fn", "struct S { p: (i32, () => i32) } function main(): i32 { var s = S { p: (3, () => 7) }; return s.p.0 + s.p.1(); }"},
		// Binding the tuple field to a LOCAL first (`var t = s.p`) then calling its
		// fn element (`t.1()`). The eligibility gate above admits the struct, but
		// the local slot `t` also has to inherit the field's tuple-element tags or
		// `t.1()` dispatch can't find the "clo" tag and bails on `call`. The
		// lower_stmt_var ExprFieldAccess arm now transfers a tuple field's element
		// tags onto t (the struct-field sibling of its digit-field / array-element
		// binds). Same value: 3 + 7 = 10.
		{"structfield-tuple-fn-local", "struct S { p: (i32, () => i32) } function main(): i32 { var s = S { p: (3, () => 7) }; var t = s.p; return t.0 + t.1(); }"},
		// DESTRUCTURING a tuple read from an array-of-tuples whose element is a
		// STRUCT / STRING / ENUM: `var (p, n) = a[i]` over `(P, i32)[]`. Direct
		// access (`a[i].0.x`) already resolved via the array slot's arrarr_elem,
		// but the destructure's dtag resolution had no ExprIndex arm, so the
		// binding fell through untyped and `p.x` read an unmarked slot → bail. The
		// new arm reads element k's tag from arrarr_elem (the destructure sibling
		// of expr_tuple_elem_tag's ExprIndex arm). struct: 3+4=7; string: 2+4=6;
		// enum: G->5+10=15.
		{"destr-tuparr-struct", "struct P { x: i32 } function main(): i32 { var a: (P, i32)[] = []; a = a.append((P { x: 3 }, 4)); var (p, n) = a[0]; return p.x + n; }"},
		{"destr-tuparr-string", "function main(): i32 { var a: (string, i32)[] = []; a = a.append((\"hi\", 4)); var (s, n) = a[0]; return s.len() + n; }"},
		{"destr-tuparr-enum", "enum C { R, G, B } function main(): i32 { var a: (C, i32)[] = []; a = a.append((G, 5)); var (c, n) = a[0]; match (c) { R => { return 1; }, G => { return n + 10; }, B => { return 3; } } return 0; }"},
		// A RECURSIVE struct — a self-referential `next: Option[Node]` field (linked
		// list / tree / AST shape). The leak-safe eligibility proof recursed
		// decl_is_leaksafe_d -> is_leaksafe_opt_field_d -> opt_payload_ok_d ->
		// decl_is_leaksafe (fresh, visiting reset) -> … forever and SIGSEGV'd the
		// compiler on a valid program. Threading the proof's visiting set through the
		// Option/tuple field predicates lets the back-edge short-circuit (leak-only,
		// safe), admitting recursive structs onto the IR path. Construction +
		// field-read: 5; recursive Option-match sum over a 3-node list: 1+2+3=6.
		{"recursive-struct-opt", "struct Node { v: i32, next: Option[Node] } function main(): i32 { var n = Node { v: 5, next: None }; return n.v; }"},
		{"recursive-list-sum", "struct Node { v: i32, next: Option[Node] } function sum(n: Option[Node]): i32 { match (n) { Some(nd) => { return nd.v + sum(nd.next); }, None => { return 0; } } return 0; } function main(): i32 { var l = Some(Node { v: 1, next: Some(Node { v: 2, next: Some(Node { v: 3, next: None }) }) }); return sum(l); }"},
		// A NESTED Option whose innermost payload is a struct: `Option[Option[P]]`,
		// constructed into a local (`Some(Some(P))`) then matched two levels deep.
		// some_opt_type inferred the local's type by elem_type_tag'ing the inner
		// `Some(P)`, which defaults a nested Some to "i32" — collapsing the type to
		// `Option[i32]`, so the outer match bound `inner` as i32 and `match (inner)`
		// bailed to AST. Recursing some_opt_type recovers `Option[Option[P]]`. = 9.
		{"nested-opt-struct", "struct P { x: i32 } function main(): i32 { var x: Option[Option[P]] = Some(Some(P { x: 9 })); match (x) { Some(inner) => { match (inner) { Some(p) => { return p.x; }, None => { return 1; } } }, None => { return 2; } } return 0; }"},
		// A struct field whose type is a TUPLE with a bare STRUCT element:
		// `Pair { both: (A, A) }`, read `p.both.N.v`. is_leaksafe_tuple_field_d
		// classified scalar / Option / array / struct-array / enum-array / clo /
		// nested-tuple elements but had no arm for a bare struct (or enum) element —
		// a leak-only pointer slot like the others — so the struct field was rejected
		// and bailed to AST even though the `t.N.field` read already lowers. Admitted
		// when the element struct is leak-safe (visiting-threaded for recursion). = 7.
		{"structfield-tuple-struct", "struct A { v: i32 } struct Pair { both: (A, A) } function main(): i32 { var p = Pair { both: (A { v: 3 }, A { v: 4 }) }; return p.both.0.v + p.both.1.v; }"},
		// A struct field whose type is an Option of a TUPLE / a NESTED Option:
		// `H { t: Option[(i32, i32)] }` / `H { o: Option[Option[i32]] }`. The
		// struct-field leak-safe proof (opt_payload_ok_d) admitted a scalar or
		// struct Option payload but not a tuple or a nested Option — both leak-only
		// one-pointer payloads the local-position path already lowers. Adding those
		// two arms closes it. tuple: 3+4=7; nested: 7.
		{"structfield-opt-tuple", "struct H { t: Option[(i32, i32)] } function main(): i32 { var h = H { t: Some((3, 4)) }; match (h.t) { Some(pr) => { return pr.0 + pr.1; }, None => { return 0; } } return 0; }"},
		{"structfield-opt-opt", "struct H { o: Option[Option[i32]] } function main(): i32 { var h = H { o: Some(Some(7)) }; match (h.o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 1; } } }, None => { return 2; } } return 0; }"},
		// A DOUBLE-index read of a struct field that is an array-of-struct-arrays:
		// `h.g[i][j].x` over `H { g: P[][] }`. The field is eligible and `h.g[i]`
		// (single index) tracks the P[] element, but expr_struct_type resolved the
		// SECOND index (`h.g[i][j]`) only for a LOCAL arrarr, not a FIELD one — so
		// `.x` had no element type and bailed. The new ExprFieldAccess arm strips
		// both `[]` off the field type to the element struct. 1+2+3=6.
		{"structfield-arr2d-struct", "struct P { x: i32 } struct H { g: P[][] } function main(): i32 { var h = H { g: [[P { x: 1 }, P { x: 2 }], [P { x: 3 }]] }; var s = 0; var i = 0; while (i < h.g.len()) { var j = 0; while (j < h.g[i].len()) { s = s + h.g[i][j].x; j = j + 1; } i = i + 1; } return s; }"},
		// An enum variant whose PAYLOAD is an Option / Result, then matched:
		// `Maybe(Option[i32])` -> `match (o) { Some(v) => … }`. The nominal-enum
		// match-arm payload binding typed string/scalar/struct/enum/map/array
		// payloads but had no arm for an Option/Result payload, so the bound `o` was
		// untyped and the inner `match (o)` bailed. Adding mark_opt_type closes it
		// (o bound-but-not-matched already lowered). Option: 9; Result: 9.
		{"enum-payload-opt", "enum E { Maybe(Option[i32]), No } function f(e: E): i32 { match (e) { Maybe(o) => { match (o) { Some(v) => { return v; }, None => { return 1; } } }, No => { return 2; } } return 0; } function main(): i32 { return f(Maybe(Some(9))); }"},
		{"enum-payload-result", "enum E { Res(Result[i32, i32]), No } function f(e: E): i32 { match (e) { Res(r) => { match (r) { Ok(v) => { return v; }, Err(e2) => { return e2; } } }, No => { return 2; } } return 0; } function main(): i32 { return f(Res(Ok(9))); }"},
		// A struct-array (P[]) / enum-array (E[]) value as a TUPLE element. The
		// buffer is a heap pointer in the slot (leak mode — elements leak with the
		// leak-only tuple); the destructure binds mark_arr + the element struct/enum
		// name so `xs[i].field` / `match (xs[i])` resolve the element shape. struct:
		// 7+11+3=21; enum: A(7)->7 + B->100 + 3 = 110.
		{"structarr-tuple-elem", "struct P { x: i32 } function mk(): (P[], i32) { var a: P[] = []; a = a.append(P { x: 7 }); a = a.append(P { x: 11 }); return (a, 3); } function main(): i32 { var (ps, n) = mk(); return ps[0].x + ps[1].x + n; }"},
		{"enumarr-tuple-elem", "enum E { A(i32), B } function mk(): (E[], i32) { var a: E[] = []; a = a.append(A(7)); a = a.append(B); return (a, 3); } function main(): i32 { var (es, n) = mk(); var sum: i32 = n; var i: i32 = 0; while (i < es.len()) { match (es[i]) { A(x) => { sum = sum + x; }, B => { sum = sum + 100; } } i = i + 1; } return sum; }"},
		// A Map-typed enum-variant PAYLOAD (`JObject(Map[string, JsonValue])`) —
		// the std/json `json_get` shape. The match binds `m` from the variant
		// payload; marking the slot mark_map_type (the new is_map_type_name case in
		// the payload-binding cascade) lets `m.get(k)` recover Option[V] and
		// dispatch as a map op (previously `m` bound without its value type, so
		// `m.get` bailed). Builds {k: JNumber("42")}, gets it back, reads len = 2.
		{"map-enum-payload-get", "function jget(obj: JsonValue, key: string): Option[JsonValue] { match (obj) { JObject(m) => { match (m.get(key)) { Some(v) => { return Some(v); }, None => { return None; } } }, _ => { return None; } } return None; } function main(): i32 { var m: Map[string, JsonValue] = map_new(8); m = m.set(\"k\", JNumber(\"42\")); var o: JsonValue = JObject(m); match (jget(o, \"k\")) { Some(v) => { match (v) { JNumber(s) => { return s.len(); }, _ => { return 0; } } }, None => { return 99; } } return 0; }"},
		// A Map[K, V]-RECEIVER method — the shape core/map's contains_value /
		// get_or_insert / merge use. A Map receiver is now map-tracked (not
		// mis-marked as an enum), so built-in map ops on `self` (m.has / m.get_or
		// / m.len) dispatch as map ops, and a NON-builtin method on `self`
		// (m.goi / m.total here, or core/map's m.merge) dispatches to the
		// "Map.<method>" user-method label. goi("a")=10 + goi("z",5)=5 + total
		// (len 2 *100)=200 = 215.
		{"map-recv-method", "function (m: Map[string, i32]) goi(k: string, fallback: i32): i32 { if (m.has(k)) { return m.get_or(k, 0); } return fallback; } function (m: Map[string, i32]) total(): i32 { return m.len() * 100; } function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.set(\"a\", 10); m = m.set(\"b\", 20); return m.goi(\"a\", 0) + m.goi(\"z\", 5) + m.total(); }"},
		// A Map-receiver method iterating m.values() with a generic `==` — the
		// core/map contains_value shape. found(20)->+1, not-found(99)->no change.
		{"map-recv-contains", "function (m: Map[string, i32]) cv(target: i32): boolean { for v in m.values() { if (v == target) { return true; } } return false; } function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.set(\"a\", 10); m = m.set(\"b\", 20); var r: i32 = 0; if (m.cv(20)) { r = r + 1; } if (m.cv(99)) { r = r + 100; } return r; }"},
		// An ARRAY payload in an Option (`Some(xs)` where xs: string[]) — the
		// `m.get(k)` shape on a Map[K, V[]] (std/url's query-param parse). The
		// array is a leak-only pointer borrowed from the Option box: bound is_arr
		// + strarr so `xs.len()` / `xs.append(v)` (clone) resolve. hit: n=2,
		// more.len=3 -> 23; miss -> 7; total 30.
		{"opt-array-payload", "function pick(o: Option[string[]], fallback: i32): i32 { match (o) { Some(xs) => { var n = xs.len(); var more = xs.append(\"z\"); return n * 10 + more.len(); }, None => { return fallback; } } return 0; } function main(): i32 { var a: string[] = []; a = a.append(\"p\"); a = a.append(\"q\"); var hit = pick(Some(a), 0); var miss = pick(None, 7); return hit + miss; }"},
		// The checker-injected Url built-in (std/url), now injected into the IR
		// path too, so `Url { … }` construction + `{...u, field}` spreads + an
		// Option[Url] return lower (the url_parse shape). scheme("http")=4 +
		// host("example.com")=11 + port 80 = 95.
		{"builtin-url", "function up(s: string): Option[Url] { if (s.len() == 0) { return None; } var u: Url = Url { scheme: \"\", host: \"\", port: 0, path: \"\", query: \"\", fragment: \"\" }; u = Url { ...u, scheme: \"http\" }; u = Url { ...u, host: s, port: 80 }; return Some(u); } function main(): i32 { match (up(\"example.com\")) { Some(u) => { return u.scheme.len() + u.host.len() + u.port; }, None => { return 999; } } return 0; }"},
		{"with-loop", "function main(): i32 { var a = [0, 0, 0, 0]; var i = 0; while (i < 4) { a = a.with(i, i * i); i = i + 1; } return a[0] + a[1] + a[2] + a[3]; }"},
		{"append-build", "function main(): i32 { var a: i32[] = []; var i = 0; while (i < 5) { a = a.append(i * 2); i = i + 1; } return a[0] + a[4]; }"},
		// `.append(e)` as a general EXPRESSION (not the `out = out.append(e)`
		// reassign-self form): return position and var-init. Was BAIL call (only
		// the reassign-self statement lowered); now lowers via op_arr_push leaving
		// the grown array on the stack. StmtReturn excludes a moved receiver ident
		// from the exit dec-sweep so the consumed buffer isn't double-freed. This
		// is the shape std/string split/splitn/chunks end with (return out.append).
		{"append-return", "function build(a: i32[], v: i32): i32[] { return a.append(v); } function main(): i32 { var a: i32[] = []; a = a.append(10); var b = build(a, 20); return b[0] + b[1]; }"},
		{"append-varinit", "function main(): i32 { var a: i32[] = []; a = a.append(7); var b = a.append(35); return b[0] + b[1]; }"},
		{"append-return-loop", "function trail(xs: i32[]): i32[] { var out: i32[] = []; var i = 0; while (i < xs.len()) { if (xs[i] > 2) { out = out.append(xs[i]); } i = i + 1; } return out.append(99); } function main(): i32 { var a: i32[] = []; a = a.append(1); a = a.append(5); a = a.append(3); var r = trail(a); return r.len() * 100 + r[r.len() - 1]; }"},
		// NB: __alloc_u8 / string_from_bytes_unchecked programs are NOT differential cases —
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
		{"str-index-local", `function main(): i32 { var s = "hello"; return s[0] as i32; }`},
		{"str-index-loop", `function main(): i32 { var s = "abc"; var sum = 0; var i = 0; while (i < 3) { sum = sum + (s[i] as i32); i = i + 1; } return sum % 200; }`},
		{"str-index-param", `function first(s: string): i32 { return s[0] as i32; } function main(): i32 { return first("Z"); }`},
		{"str-slice-len", `function main(): i32 { var s = "hello"; var t = s[1:4]; return t.len(); }`},
		{"str-slice-idx0", `function main(): i32 { var s = "hello"; var t = s[1:4]; return t[0] as i32; }`},
		{"str-slice-chain", `function main(): i32 { return "hello"[1:4][2] as i32; }`},
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
		// u32[] struct fields ride the i32[] 4-byte element read; only the leak-
		// safety gate (is_leaksafe_array_field) had to admit them. Field
		// round-trip: construction, indexed read, extract-to-local, by-value param.
		{"struct-u32arr-field", `struct Vec { vals: u32[], n: i32 } function main(): i32 { var v = Vec { vals: [10, 20, 30], n: 3 }; var s = 0; var i = 0; while (i < v.n) { s = s + (v.vals[i] as i32); i = i + 1; } return s; }`},
		{"struct-u32arr-extract", `struct Vec { vals: u32[] } function main(): i32 { var v = Vec { vals: [7, 8, 9] }; var a = v.vals; return (a[0] as i32) + (a[2] as i32); }`},
		{"struct-u32arr-param", `struct Vec { vals: u32[], n: i32 } function sum(v: Vec): i32 { var s = 0; var i = 0; while (i < v.n) { s = s + (v.vals[i] as i32); i = i + 1; } return s; } function main(): i32 { var v = Vec { vals: [5, 10, 15], n: 3 }; return sum(v); }`},
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
		// A struct with a ≤32-bit non-i32 integer field (u32 / sub-word u8) is
		// leak-safe and lowers — these ride the same i32 slot, so only the
		// leaf-safe gate blocked them. Covers local / param / struct-returning /
		// mixed-field / functional-update shapes; u64 / f32 stay on AST (width
		// work). (i16/i8 sign-extend and u16 cases were retired (#4408); u8 is
		// the only sub-word type left.)
		{"struct-u32-field", `struct B { hi: u32, n: i32 } function main(): i32 { var b = B { hi: 4000000000 as u32, n: 7 }; var hi: u32 = b.hi >> 30; return (hi as i32) + b.n; }`},
		{"struct-u8-field", `struct B { c: u8, n: i32 } function main(): i32 { var b = B { c: 250 as u8, n: 5 }; return (b.c as i32) + b.n; }`},
		{"struct-u32-ret", `struct B { hi: u32, n: i32 } function mk(): B { return B { hi: 100 as u32, n: 4 }; } function main(): i32 { var b = mk(); return (b.hi as i32) + b.n; }`},
		{"struct-u32-param", `struct B { hi: u32, n: i32 } function sz(b: B): i32 { return b.n; } function main(): i32 { return sz(B { hi: 1 as u32, n: 9 }); }`},
		{"struct-mixed-int-fields", `struct B { a: u8, c: u32, d: i32 } function main(): i32 { var x = B { a: 1 as u8, c: 3 as u32, d: 4 }; return (x.a as i32) + (x.c as i32) + x.d; }`},
		{"struct-u32-update", `struct B { hi: u32, n: i32 } function main(): i32 { var b = B { hi: 5 as u32, n: 1 }; var c = B { ...b, n: 9 }; return (c.hi as i32) + c.n; }`},
		// A u64 struct field is a 64-bit integer field — routed through the i64 path
		// (struct_get_i64 / lower_i64 / struct_field_width 64). The full 64 bits must
		// survive (high half via `>> 32`); local / struct-returning / param shapes.
		{"struct-u64-field", `struct B { hi: u64, n: i32 } function main(): i32 { var b = B { hi: 5000000000 as u64, n: 3 }; var q: u64 = b.hi / (1000000000 as u64); return (q as i32) + b.n; }`},
		{"struct-u64-ret", `struct B { hi: u64, n: i32 } function mk(): B { return B { hi: 8000000000 as u64, n: 2 }; } function main(): i32 { var b = mk(); var q: u64 = b.hi / (1000000000 as u64); return (q as i32) + b.n; }`},
		{"struct-u64-param", `struct B { hi: u64, n: i32 } function f(b: B): i32 { var q: u64 = b.hi >> 32; return (q as i32) + b.n; } function main(): i32 { return f(B { hi: 4294967296 as u64, n: 5 }); }`},
		{"struct-in-loop", `struct P { x: i32, y: i32 } function main(): i32 { var s = 0; var i = 0; while (i < 4) { var p = P { x: i, y: i * 2 }; s = s + p.x + p.y; i = i + 1; } return s; }`},
		{"struct-update-one", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; var q = P { ...p, y: 9 }; return q.x + q.y; }`},
		{"struct-update-keeps-base", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 6 }; var q = P { ...p, x: 50 }; return p.x + q.x; }`},
		// Functional update with a NON-IDENT base (`P { ...<expr>, f: v }`): the
		// base is spilled into a scratch local once, so each copied field re-reads
		// the same evaluated value. Covers a struct-returning call, a struct field
		// read, and a struct-array element as the base.
		{"struct-update-call-base", `struct P { x: i32, y: i32 } function mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { var p = P { ...mk(), y: 9 }; return p.x * 10 + p.y; }`},
		{"struct-update-field-base", `struct Inner { a: i32, b: i32 } struct Outer { inner: Inner } function main(): i32 { var o = Outer { inner: Inner { a: 5, b: 6 } }; var n = Inner { ...o.inner, b: 20 }; return n.a * 10 + n.b; }`},
		{"struct-update-index-base", `struct P { x: i32, y: i32 } function main(): i32 { var a: P[] = [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; var q = P { ...a[1], y: 9 }; return q.x * 10 + q.y; }`},
		// Field mutation `p.x = v` (struct_set).
		{"field-mutate", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p.x = 40; return p.x + p.y; }`},
		{"field-mutate-loop", `struct C { n: i32 } function main(): i32 { var c = C { n: 0 }; var i = 0; while (i < 5) { c.n = c.n + i; i = i + 1; } return c.n; }`},
		{"field-mutate-alias", `struct P { x: i32 } function main(): i32 { var p = P { x: 1 }; var q = p; q.x = 9; return p.x; }`},
		// Tuples (tuple_make / tuple_get; no shape slot, numeric .N access) + 2-elem destructure.
		{"tuple-access", `function main(): i32 { var t = (3, 4); return t.0 + t.1; }`},
		{"tuple-three", `function main(): i32 { var t = (1, 2, 3); return t.0 * 100 + t.1 * 10 + t.2; }`},
		{"tuple-destructure", `function main(): i32 { var (a, b) = (40, 2); return a + b; }`},
		{"tuple-expr-elems", `function main(): i32 { var x = 5; var t = (x * 2, x + 1); return t.0 + t.1; }`},
		// A tuple-returning function with a `boolean` element. tuple_elems_lowerable
		// gated this on `"bool"`, but the type is spelled `boolean`, so a boolean
		// element wrongly bailed the whole function to AST. (Construction + `.N` /
		// destructure already treat a boolean as a scalar — only the return-type
		// gate was wrong.)
		{"tuple-bool-first", `function f(): (boolean, i32) { return (true, 7); } function main(): i32 { var t = f(); if (t.0) { return t.1; } return 0; }`},
		{"tuple-bool-first-false", `function f(): (boolean, i32) { return (false, 7); } function main(): i32 { var t = f(); if (t.0) { return t.1; } return 99; }`},
		{"tuple-bool-second", `function f(): (i32, boolean) { return (9, true); } function main(): i32 { var t = f(); if (t.1) { return t.0; } return 0; }`},
		{"tuple-bool-destructure", `function f(): (boolean, i32) { return (true, 42); } function main(): i32 { var (b, n) = f(); if (b) { return n; } return 0; }`},
		{"tuple-bool-both", `function f(): (boolean, boolean) { return (true, false); } function main(): i32 { var t = f(); var r = 0; if (t.0) { r = r + 1; } if (t.1) { r = r + 10; } return r; }`},
		// A tuple return with a ≤32-bit non-i32 integer element (u32 / sub-word
		// u8) — these ride the same i32 slot, so only the return-type gate
		// (tuple_elems_lowerable) blocked them. Covers the high-bit u32 case
		// and sub-word wrap. (i16/i8 sign-extend and u16 cases were retired
		// (#4408); u8 is the only sub-word type left.)
		{"tuple-u32", `function f(): (u32, i32) { return (4000000000 as u32, 7); } function main(): i32 { var t = f(); var hi: u32 = t.0 >> 30; return (hi as i32) + t.1; }`},
		{"tuple-u8", `function f(): (u8, i32) { return (250 as u8, 5); } function main(): i32 { var t = f(); return (t.0 as i32) + t.1; }`},
		{"tuple-u32-second", `function f(): (i32, u32) { return (3, 9 as u32); } function main(): i32 { var t = f(); return t.0 + (t.1 as i32); }`},
		// A u64 tuple element rides the i64 8-byte slot (tuple_get_w(64)); `.N`
		// access, destructure, and the 2nd-element position all preserve the full
		// 64 bits, and the element stays UNSIGNED for shifts (bit-63-set >> case).
		{"tuple-u64-access", `function f(): (u64, i32) { return (4294967296 as u64, 5); } function main(): i32 { var t = f(); var q: u64 = t.0 >> 32; return (q as i32) + t.1; }`},
		{"tuple-u64-destr", `function f(): (u64, i32) { return (5000000000 as u64, 3); } function main(): i32 { var (hi, n) = f(); var q: u64 = hi / (1000000000 as u64); return (q as i32) + n; }`},
		{"tuple-u64-second", `function f(): (i32, u64) { return (2, 8000000000 as u64); } function main(): i32 { var t = f(); var q: u64 = t.1 / (1000000000 as u64); return t.0 + (q as i32); }`},
		{"tuple-u64-unsigned", `function f(): (u64, i32) { return (18000000000000000000 as u64, 1); } function main(): i32 { var t = f(); var q: u64 = t.0 >> 60; return (q as i32) + t.1; }`},
		// A 4-byte scalar-array (i32[]/u32[]) tuple element is a leak-only pointer
		// in one slot like a string/struct element; destructure binds it as an
		// array so `arr[i]` reads back. Covers both element positions.
		{"tuple-i32arr-destr", `function f(): (i32[], i32) { return ([5, 10], 7); } function main(): i32 { var (arr, n) = f(); return arr[0] + arr[1] + n; }`},
		{"tuple-u32arr-destr", `function f(): (u32[], i32) { return ([5, 10], 7); } function main(): i32 { var (arr, n) = f(); return (arr[0] as i32) + (arr[1] as i32) + n; }`},
		{"tuple-i32arr-second", `function f(): (i32, i32[]) { return (3, [10, 20]); } function main(): i32 { var (n, arr) = f(); return n + arr[0] + arr[1]; }`},
		// f32 in composites rides the f64 8-byte slot (Fern represents f32 as f64
		// internally), so tuple elements + struct fields lower like f64: `.N`
		// access, destructure, 2nd-position, and float arithmetic on the element.
		{"tuple-f32-access", `function f(): (f32, i32) { return (4.5 as f32, 3); } function main(): i32 { var t = f(); return (t.0 as i32) + t.1; }`},
		{"tuple-f32-destr", `function f(): (f32, i32) { return (6.5 as f32, 2); } function main(): i32 { var (a, n) = f(); return (a as i32) + n; }`},
		{"tuple-f32-second", `function f(): (i32, f32) { return (1, 9.5 as f32); } function main(): i32 { var t = f(); return t.0 + (t.1 as i32); }`},
		{"tuple-f32-arith", `function f(): (f32, i32) { return (2.5 as f32, 1); } function main(): i32 { var t = f(); var d: f32 = t.0 * 2.0; return (d as i32) + t.1; }`},
		{"struct-f32-field", `struct B { v: f32, n: i32 } function main(): i32 { var b = B { v: 2.5 as f32, n: 3 }; return (b.v as i32) + b.n; }`},
		{"struct-f32-ret", `struct B { v: f32, n: i32 } function mk(): B { return B { v: 7.5 as f32, n: 1 }; } function main(): i32 { var b = mk(); return (b.v as i32) + b.n; }`},
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
		// An Option/Result payload that is a u32 — a full i32 slot like i32 (no
		// narrowing on read), so the box stores / reads at the default 32-bit
		// width and only is_leaksafe_payload (the field / tuple-element leak-
		// safety gate) had to admit it; the bound payload is marked u32 so its
		// arithmetic wraps. The sub-word (u8) payload stays on the AST
		// path (a separate slice).
		{"opt-u32-field-match", `struct S { o: Option[u32] } function main(): i32 { var s = S { o: Some(7) }; match (s.o) { Some(n) => { return n as i32; }, None => { return 1; } } return 0; }`},
		{"result-u32-field-match", `struct S { r: Result[u32, i32] } function main(): i32 { var s = S { r: Ok(9) }; match (s.r) { Ok(n) => { return n as i32; }, Err(e) => { return e; } } return 0; }`},
		{"opt-u32-payload-shift", `function main(): i32 { var o: Option[u32] = Some(4294967294 as u32); match (o) { Some(n) => { return (n >> 31) as i32; }, None => { return 0; } } return 0; }`},
		{"opt-u32-tuple-field", `struct S { t: (Option[u32], i32) } function main(): i32 { var s = S { t: (Some(7), 3) }; return s.t.1; }`},
		// A u64 Option/Result payload rides the i64 8-byte slot: construction tags
		// a u64 arg "i64" (8-byte store), and the match binding reads via
		// op_opt_payload_w(64) and marks the slot u64. The full 64 bits must
		// survive the box round-trip — `5000000000 >> 32 == 1` (a 32-bit-truncated
		// read would give 0). The value is < 2^63, so the shift is signedness-
		// agnostic: this pins the 8-byte WIDTH, independent of the separate
		// frontend gap that a match-bound u64 isn't typed unsigned for `>>`.
		{"opt-u64-field-match", `struct S { o: Option[u64] } function main(): i32 { var s = S { o: Some(42 as u64) }; match (s.o) { Some(n) => { return n as i32; }, None => { return 1; } } return 0; }`},
		{"result-u64-field-match", `struct S { r: Result[u64, i32] } function main(): i32 { var s = S { r: Ok(9 as u64) }; match (s.r) { Ok(n) => { return n as i32; }, Err(e) => { return e; } } return 0; }`},
		{"opt-u64-payload-wide", `function main(): i32 { var o: Option[u64] = Some(5000000000 as u64); match (o) { Some(n) => { return (n >> 32) as i32; }, None => { return 0; } } return 0; }`},
		{"opt-u64-tuple-field", `struct S { t: (Option[u64], i32) } function main(): i32 { var s = S { t: (Some(7 as u64), 3) }; return s.t.1; }`},
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
		// The std/array `position` / std/string `find` body shape: scan a string[]
		// for an equal element, returning `Some(index)` or `None` (a while-loop +
		// string equality + Option construction in one function). Guards that the
		// Option-returning search family lowers through the IR path.
		{"strarr-position-hit", `function pos(a: string[], s: string): Option[i32] { var i = 0; while (i < a.len()) { if (a[i] == s) { return Some(i); } i = i + 1; } return None; } function main(): i32 { match (pos(["a", "b", "c"], "b")) { Some(i) => { return i; }, None => { return 99; } } return 0; }`},
		{"strarr-position-miss", `function pos(a: string[], s: string): Option[i32] { var i = 0; while (i < a.len()) { if (a[i] == s) { return Some(i); } i = i + 1; } return None; } function main(): i32 { match (pos(["a", "b"], "z")) { Some(_) => { return 1; }, None => { return 7; } } return 0; }`},
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
		// UNION-type (`type Node = A | B`) variant payload binding (#3179). Each
		// variant is a pre-existing struct (no synthetic `__ev` field), so the
		// match arm binds the WHOLE scrutinee box pointer typed with the variant's
		// struct name — a later `x.value` then resolves. Previously the `__ev`
		// payload read bailed the whole module to AST; now it lowers through IR,
		// mirroring the legacy AST emitter's union-member split (asm.fern:3685).
		{"union-eval", `struct Num { value: i32 } struct Add { left: i32, right: i32 } type Node = Num | Add; function eval(n: Node): i32 { match (n) { Num(x) => { return x.value; }, Add(a) => { return a.left + a.right; } } return 0; } function main(): i32 { return eval(Num { value: 7 }) * 100 + eval(Add { left: 3, right: 9 }); }`},
		{"union-multifield", `struct Pt { x: i32, y: i32 } struct Pt3 { x: i32, y: i32, z: i32 } type V = Pt | Pt3; function sum(v: V): i32 { match (v) { Pt(p) => { return p.x + p.y; }, Pt3(q) => { return q.x + q.y + q.z; } } return 0; } function main(): i32 { return sum(Pt { x: 3, y: 4 }) * 100 + sum(Pt3 { x: 1, y: 2, z: 3 }); }`},
		{"union-field-in-expr", `struct VInt { v: i32 } struct VStr { s: string } type Val = VInt | VStr; function size(x: Val): i32 { match (x) { VInt(i) => { return i.v * 2; }, VStr(s) => { return s.s.len() + 1; } } return -1; } function main(): i32 { return size(VInt { v: 20 }) + size(VStr { s: "abc" }); }`},
		{"union-nested-match", `struct Lit { n: i32 } struct Bin { l: i32, r: i32 } type Expr2 = Lit | Bin; function ev(e: Expr2): i32 { match (e) { Lit(x) => { return x.n; }, Bin(b) => { var t = 0; match (b.l > b.r) { _ => { t = b.l + b.r; } } return t; } } return 0; } function main(): i32 { return ev(Lit { n: 5 }) + ev(Bin { l: 10, r: 20 }); }`},
		{"union-method-on-field", `struct Box1 { v: i32 } struct Box2 { v: i32 } type B = Box1 | Box2; function (a: Box1) g(): i32 { return a.v + 1; } function (b: Box2) g(): i32 { return b.v + 100; } function pick(x: B): i32 { match (x) { Box1(p) => { return p.g(); }, Box2(q) => { return q.g(); } } return 0; } function main(): i32 { return pick(Box1 { v: 5 }) + pick(Box2 { v: 5 }); }`},
		// A `_` binding on a union member skips the box bind (no field use) — must
		// still lower and dispatch on the variant tag alone.
		{"union-wildcard-bind", `struct On { } struct Off { } type Sw = On | Off; function f(s: Sw): i32 { match (s) { On(_) => { return 1; }, Off(_) => { return 0; } } return 9; } function main(): i32 { return f(On { }) * 10 + f(Off { }); }`},
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
		// Discarded fresh-ret-CALL local (#3457 follow-up): `var r = mk()` where mk
		// is fresh-struct-returning (Box is leak-safe, fields are properly rc-
		// counted by mk's struct-lit), r is READ (field copies) then goes dead
		// without escaping — so reclaimable_names_of now credits it and the scope-
		// end sweep deep-drops it (__struct_drop_Box). Must run correctly on BOTH
		// paths (the freed buffers are sole-owned, never a caller alias).
		{"fresh-ret-call-discarded", `struct Box { ops: i32[] } function mk(): Box { return Box { ops: [1, 2, 3] }; } function use_it(): i32 { var r: Box = mk(); var a = r.ops[0]; var b = r.ops[1]; var c = r.ops[2]; return a + b + c; } function main(): i32 { return use_it(); }`},
		// Same, but the discarded local's field is forwarded into a fresh builder
		// (the lower_func-style `s = s.append_all(r.ops)`) then r dies — the field
		// read is a borrow, so r stays reclaimable.
		{"fresh-ret-call-discarded-forward", `struct Box { ops: i32[] } function mk(): Box { return Box { ops: [5, 6, 7] }; } function sum(xs: i32[]): i32 { var s = 0; var i = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; } function use_it(): i32 { var r: Box = mk(); return sum(r.ops); } function main(): i32 { return use_it(); }`},
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
		// Type ascription to an Option / Result target (#2669): `None as
		// Option[i32]`. The parser now keeps the generic args in the cast op
		// name (`as_Option[i32]`), so a binding `var x = None as Option[i32]`
		// rebinds to `var x: Option[i32] = None` (payload type intact) and
		// lowers through the IR path instead of bailing to the AST backend on
		// the payload-less `var x: Option = None`. The Some/Ok/[] operands
		// already lowered (they carry their own payload type); these lock in
		// the bare-None / bare-Err cases plus the non-binding (return / nested)
		// positions and the array ascription that shares the suffix path.
		{"asc-none-opt-bind", `function main(): i32 { var x = None as Option[i32]; return match (x) { Some(v) => v, None => 7 }; }`},
		{"asc-none-opt-str", `function main(): i32 { var x = None as Option[string]; return match (x) { Some(v) => v.len(), None => 7 }; }`},
		{"asc-some-opt-bind", `function main(): i32 { var x = Some(5) as Option[i32]; return match (x) { Some(v) => v, None => 7 }; }`},
		{"asc-none-opt-ret", `function f(): Option[i32] { return None as Option[i32]; } function main(): i32 { return match (f()) { Some(v) => v, None => 7 }; }`},
		{"asc-none-opt-nested", `function main(): i32 { return match (None as Option[i32]) { Some(v) => v, None => 7 }; }`},
		{"asc-opt-arr-suffix", `function main(): i32 { var a = [3, 4] as i32[]; return a[0] + a[1]; }`},
		// A match-EXPRESSION whose scrutinee is an Option-typed tuple element (t.0):
		// try_opt_type now resolves a numeric (tuple-element) field via
		// expr_tuple_elem_tag, mirroring the main StmtMatch path (#3118).
		{"match-expr-tuple-elem0", `function main(): i32 { var t = (Some(5), 3); return match (t.0) { Some(v) => v, None => 0 } + t.1; }`},
		{"match-expr-tuple-elem1", `function main(): i32 { var t = (3, Some(8)); return match (t.1) { Some(v) => v, None => 0 } + t.0; }`},
		// A match-EXPRESSION whose scrutinee is an Option-array element (a[i]):
		// try_opt_type gained an ExprIndex case recovering the element type from the
		// array slot's Option[T][] opt-type, mirroring the main StmtMatch path (#3121).
		{"match-expr-arr-elem0", `function main(): i32 { var a = [Some(5)]; return match (a[0]) { Some(v) => v, None => 0 }; }`},
		{"match-expr-arr-elem-idx", `function main(): i32 { var a = [Some(3), Some(8)]; var i = 1; return match (a[i]) { Some(v) => v, None => 0 }; }`},
		{"match-expr-arr-field-elem", `struct B { xs: Option[i32][] } function main(): i32 { var b = B { xs: [Some(4), None] }; return match (b.xs[0]) { Some(v) => v, None => 0 }; }`},
		// An unannotated Option bound from an if-/match-EXPRESSION (which desugars to
		// an IIFE): the StmtVar opt-type inference now recovers o's Option type from
		// the first branch's Some(...) via iife_first_return_expr, so the later
		// match (o) lowers (#3124).
		{"ifexpr-opt-bind-some", `function main(): i32 { var x = 5; var o = if (x > 3) { Some(7) } else { None }; match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }`},
		{"ifexpr-opt-bind-none", `function main(): i32 { var x = 1; var o = if (x > 3) { Some(7) } else { None }; match (o) { Some(v) => { return v; }, None => { return 42; } } return 9; }`},
		{"matchexpr-opt-bind", `function main(): i32 { var e = 2; var o = match (e) { 1 => Some(10), _ => Some(20) }; match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }`},
		// A struct bound from an if-/match-EXPRESSION (IIFE): the StmtVar struct-type
		// inference now recovers p's struct type from the first branch's struct
		// literal, so p.field resolves (the struct sibling of #3124) (#3133).
		{"ifexpr-struct-bind", `struct P { x: i32 } function main(): i32 { var c = 5; var p = if (c > 3) { P { x: 7 } } else { P { x: 1 } }; return p.x; }`},
		{"ifexpr-struct-bind-else", `struct P { x: i32, y: i32 } function main(): i32 { var c = 1; var p = if (c > 3) { P { x: 7, y: 2 } } else { P { x: 1, y: 0 } }; return p.x + p.y; }`},
		{"matchexpr-struct-bind", `struct P { x: i32 } function main(): i32 { var c = 1; var p = match (c) { 1 => P { x: 9 }, _ => P { x: 0 } }; return p.x; }`},
		// A struct ARRAY bound from an if-/match-EXPRESSION (IIFE): the StmtVar
		// inference now records the element struct type and marks the slot is_arr, so
		// ps[i].field / ps.len() resolve (the struct-array sibling of #3133) (#3138).
		{"ifexpr-struct-arr-bind", `struct P { x: i32 } function main(): i32 { var c = 5; var ps = if (c > 3) { [P { x: 7 }] } else { [P { x: 1 }] }; return ps[0].x; }`},
		{"ifexpr-struct-arr-len", `struct P { x: i32 } function main(): i32 { var c = 5; var ps = if (c > 3) { [P { x: 7 }, P { x: 8 }] } else { [P { x: 1 }] }; return ps.len() + ps[1].x; }`},
		{"matchexpr-struct-arr-bind", `struct P { x: i32 } function main(): i32 { var k = 1; var ps = match (k) { 1 => [P { x: 9 }], _ => [P { x: 0 }] }; return ps[0].x; }`},
		// Binding an ARRAY-OF-STRUCT field into a local (`var ps: P[] = obj.params`):
		// aliases the field's buffer with a Perceus dup on the POINTER (element-
		// type-agnostic), balanced by the exit-sweep's shallow arr_dec — no deep-drop
		// is involved for the bind, since the source struct keeps owning the elements.
		// The slot is marked with the element struct type so `ps[i].field` / ps.len()
		// resolve. Mirrors the already-admitted scalar-array field bind.
		{"struct-arr-field-bind", `struct P { x: i32 } struct H { ps: P[] } function main(): i32 { var h = H { ps: [P { x: 7 }, P { x: 3 }] }; var ps: P[] = h.ps; return ps[0].x * 10 + ps[1].x; }`},
		{"struct-arr-field-bind-len", `struct P { x: i32 } struct H { ps: P[] } function main(): i32 { var h = H { ps: [P { x: 1 }, P { x: 2 }, P { x: 3 }] }; var ps: P[] = h.ps; return ps.len(); }`},
		{"struct-arr-field-bind-param", `struct P { d: boolean } struct H { ps: P[] } function any_d(h: H): boolean { var ps: P[] = h.ps; var i = 0; while (i < ps.len()) { if (ps[i].d) { return true; } i = i + 1; } return false; } function main(): i32 { var yes = H { ps: [P { d: false }, P { d: true }] }; var no = H { ps: [P { d: false }] }; var r = 0; if (any_d(yes)) { r = r + 10; } if (any_d(no)) { r = r + 1; } return r; }`},
		// Returning / assigning an array-of-struct field — the alias-creating
		// siblings of the bind above. Same buffer-pointer Perceus dup; the source
		// struct keeps owning the elements (no deep-drop). Return covers a borrowed-
		// param source and a reclaimable-local source; assign re-binds an existing
		// P[] local.
		{"struct-arr-field-return", `struct P { x: i32 } struct H { ps: P[] } function get(h: H): P[] { return h.ps; } function main(): i32 { var h = H { ps: [P { x: 4 }, P { x: 9 }] }; var got: P[] = get(h); return got[0].x * 10 + got[1].x; }`},
		{"struct-arr-field-return-local", `struct P { x: i32 } struct H { ps: P[] } function mk(): P[] { var h = H { ps: [P { x: 6 }, P { x: 2 }] }; return h.ps; } function main(): i32 { var ps = mk(); return ps[0].x * 10 + ps[1].x; }`},
		{"struct-arr-field-assign", `struct P { x: i32 } struct H { ps: P[] } function f(h: H): i32 { var ps: P[] = []; ps = h.ps; var s = 0; var i = 0; while (i < ps.len()) { s = s + ps[i].x; i = i + 1; } return s; } function main(): i32 { var h = H { ps: [P { x: 5 }, P { x: 7 }, P { x: 11 }] }; return f(h); }`},
		// A struct-array IDENT / PARAM as a struct-literal FIELD VALUE
		// (`S { es: xs }`) — the alias-creating sibling of the field-access form
		// (`S { es: s.es }`, already lowered) and the bind/return/assign positions.
		// Same buffer-pointer Perceus dup; admits the checker's new_scope* /
		// Scope-construction shape (`Scope { sigs: fs, ... }`) and lexer.fstring_tok
		// (`TokFString { parts: parts }`). Local-ident source, param source, and a
		// fresh-empty-then-populate source.
		{"struct-arr-into-lit-ident", `struct E { v: i32 } struct S { es: E[], tag: i32 } function main(): i32 { var xs: E[] = [E { v: 3 }, E { v: 5 }]; var s = S { es: xs, tag: 7 }; return s.es[0].v * 100 + s.es[1].v * 10 + s.tag; }`},
		{"struct-arr-into-lit-param", `struct E { v: i32 } struct S { es: E[], tag: i32 } function mk(xs: E[], t: i32): S { return S { es: xs, tag: t }; } function main(): i32 { var s = mk([E { v: 2 }, E { v: 8 }], 4); return s.es[0].v * 100 + s.es[1].v * 10 + s.tag; }`},
		{"struct-arr-into-lit-empty", `struct E { v: i32 } struct S { es: E[] } function empty(): S { var none: E[] = []; return S { es: none }; } function main(): i32 { var s = empty(); return s.es.len(); }`},
		// Array-of-ENUM field aliasing (bind / return / assign) — the enum twin of
		// the struct-array positions. Same buffer-pointer Perceus dup; the element
		// ENUM name is marked on the slot so a later `match (es[i])` recovers the
		// variant. `Expr[]` / `Stmt[]` field reads in the parser's AST walkers are
		// exactly this shape (e.g. iter_range_args' `return c.args`).
		{"enum-arr-field-bind-match", `enum E { A(i32), B } struct H { es: E[] } function sum(h: H): i32 { var es: E[] = h.es; var s = 0; var i = 0; while (i < es.len()) { match (es[i]) { A(n) => { s = s + n; }, B => { s = s + 100; } } i = i + 1; } return s; } function main(): i32 { var h = H { es: [A(5), B, A(9)] }; return sum(h); }`},
		{"enum-arr-field-return", `enum E { A(i32), B } struct H { es: E[] } function get(h: H): E[] { return h.es; } function main(): i32 { var h = H { es: [A(3), B] }; var es = get(h); return match (es[0]) { A(n) => n, B => 0 } + es.len(); }`},
		{"enum-arr-field-assign", `enum E { A(i32), B } struct H { es: E[] } function f(h: H): i32 { var es: E[] = []; es = h.es; var s = 0; var i = 0; while (i < es.len()) { match (es[i]) { A(n) => { s = s + n; }, B => {} } i = i + 1; } return s; } function main(): i32 { var h = H { es: [A(7), A(8), B] }; return f(h); }`},
		// `.append` / `.with` on a struct/enum-array FIELD receiver, producing a
		// fresh array (clone-then-grow / clone-then-set, sole-owned) — admits
		// checker.Scope.bind's `var ts: Type[] = s.types.append(t)`. The clone form
		// handles pointer elements at width 32 like the scalar case.
		{"enum-arr-field-append", `enum E { A(i32), B } struct S { es: E[] } function grow(s: S, x: E): E[] { return s.es.append(x); } function main(): i32 { var s = S { es: [A(3), B] }; var g = grow(s, A(7)); var sum = 0; var i = 0; while (i < g.len()) { match (g[i]) { A(n) => { sum = sum + n; }, B => { sum = sum + 100; } } i = i + 1; } return sum; }`},
		{"struct-arr-field-append", `struct P { x: i32 } struct S { ps: P[] } function grow(s: S): P[] { return s.ps.append(P { x: 9 }); } function main(): i32 { var s = S { ps: [P { x: 1 }, P { x: 2 }] }; var g = grow(s); return g.len() * 100 + g[2].x; }`},
		{"struct-arr-field-with", `struct P { x: i32 } struct S { ps: P[] } function set1(s: S): P[] { return s.ps.with(1, P { x: 8 }); } function main(): i32 { var s = S { ps: [P { x: 1 }, P { x: 2 }, P { x: 3 }] }; var g = set1(s); return g[0].x * 100 + g[1].x * 10 + g[2].x; }`},
		// A CALL returning an array-of-struct as a struct-literal field value
		// (`S { es: build(...) }`) — owned/moved value, no alias-inc; a struct-array
		// field is never deep-dropped so the missing inc only leaks, never over-frees.
		// Admits parser.module_with_builtins (`Module { structs: inject_builtin_enums
		// (mono.structs), funcs: mono.funcs, ... }`) — a call value beside field-access
		// values. Scalar-array call values stay restricted to .with/.append (deep-
		// dropped, so they need the fresh guarantee).
		{"struct-arr-call-into-lit", `struct E { v: i32 } struct S { es: E[], n: i32 } function build(seed: i32): E[] { return [E { v: seed }, E { v: seed + 1 }]; } function mk(seed: i32): S { return S { es: build(seed), n: seed + 5 }; } function main(): i32 { var s = mk(3); return s.es[0].v * 100 + s.es[1].v * 10 + s.n; }`},
		{"struct-arr-call-and-fieldaccess-into-lit", `struct E { v: i32 } struct M { funcs: E[], structs: E[], n: i32 } function more(xs: E[]): E[] { return xs.append(E { v: 9 }); } function rebuild(m: M): M { return M { funcs: m.funcs, structs: more(m.structs), n: m.n + 1 }; } function main(): i32 { var m = M { funcs: [E { v: 1 }], structs: [E { v: 2 }], n: 10 }; var r = rebuild(m); return r.funcs.len() * 1000 + r.structs.len() * 100 + r.n; }`},
		// The nested-array motivating shape (parser.module_has_default_params): an
		// array-of-struct field read out of an element of an outer array-of-struct,
		// bound in a loop, then indexed for a scalar field.
		{"struct-arr-field-bind-nested", `struct Param { has_default: boolean } struct Func { params: Param[] } struct Mod { funcs: Func[] } function has_dp(mod: Mod): boolean { var fi = 0; while (fi < mod.funcs.len()) { var ps: Param[] = mod.funcs[fi].params; var pi = 0; while (pi < ps.len()) { if (ps[pi].has_default) { return true; } pi = pi + 1; } fi = fi + 1; } return false; } function main(): i32 { var m = Mod { funcs: [Func { params: [Param { has_default: false }, Param { has_default: true }] }] }; if (has_dp(m)) { return 10; } return 0; }`},
		// for-in / .len() over an array bound from an if-/match-EXPRESSION: the StmtVar
		// is_arr inference now marks the slot is_arr for an IIFE-array result, so the
		// foreach lowers (indexing already worked without is_arr) (#3141).
		{"ifexpr-arr-foreach", `function main(): i32 { var c = 5; var a = if (c > 3) { [1, 2, 3] } else { [4] }; var s = 0; for x in a { s = s + x; } return s; }`},
		{"ifexpr-arr-len", `function main(): i32 { var c = 5; var a = if (c > 3) { [1, 2, 3] } else { [4] }; return a.len(); }`},
		{"matchexpr-arr-foreach", `function main(): i32 { var k = 1; var a = match (k) { 1 => [10, 20], _ => [1] }; var s = 0; for x in a { s = s + x; } return s; }`},
		// An Option array bound from an if-/match-EXPRESSION (IIFE): the StmtVar
		// opt-array inference now records the slot's Option[T][] from the first
		// branch's array literal, so match (a[i]) / for o in a recover the element
		// payload (the Option-array sibling of #3141) (#3146).
		{"ifexpr-optarr-index", `function main(): i32 { var c = 5; var a = if (c > 3) { [Some(7), None] } else { [Some(1)] }; return match (a[0]) { Some(v) => v, None => 0 }; }`},
		{"ifexpr-optarr-foreach", `function main(): i32 { var c = 5; var a = if (c > 3) { [Some(7), None, Some(3)] } else { [Some(1)] }; var s = 0; for o in a { match (o) { Some(v) => { s = s + v; }, None => {} } } return s; }`},
		{"matchexpr-optarr-index", `function main(): i32 { var k = 1; var a = match (k) { 1 => [Some(9)], _ => [Some(0)] }; return match (a[0]) { Some(v) => v, None => 0 }; }`},
		// A binding from a NESTED if-/match-expression (a branch is itself an
		// if-expression): iife_leaf_value unwraps the nested IIFE chain so the StmtVar
		// type inference sees the leaf struct/Some/array literal (#3156).
		{"nested-ifexpr-opt", `function main(): i32 { var c = 5; var o = if (c > 3) { if (c > 10) { Some(1) } else { Some(7) } } else { None }; return match (o) { Some(v) => v, None => 0 }; }`},
		{"nested-ifexpr-struct", `struct P { x: i32 } function main(): i32 { var c = 5; var p = if (c > 3) { if (c > 10) { P { x: 1 } } else { P { x: 7 } } } else { P { x: 0 } }; return p.x; }`},
		{"nested-ifexpr-arr", `function main(): i32 { var c = 5; var a = if (c > 3) { if (c > 10) { [1] } else { [7, 8] } } else { [0] }; var s = 0; for x in a { s = s + x; } return s; }`},
		// A match whose scrutinee is directly an if-/match-EXPRESSION (a 0-arg IIFE):
		// both the main StmtMatch scrutinee resolution and try_opt_type now recover
		// the Option type via iife_leaf_value + some_opt_type (#3161).
		{"match-scrut-ifexpr-some", `function main(): i32 { var c = 5; return match (if (c > 3) { Some(7) } else { None }) { Some(v) => v, None => 0 }; }`},
		{"match-scrut-ifexpr-none", `function main(): i32 { var c = 1; return match (if (c > 3) { Some(7) } else { None }) { Some(v) => v, None => 9 }; }`},
		{"stmt-match-scrut-ifexpr", `function main(): i32 { var c = 5; match (if (c > 3) { Some(7) } else { None }) { Some(v) => { return v; }, None => { return 0; } } return 9; }`},
		// An if-/match-expression binding whose branch returns an Option-typed LOCAL
		// (not a fresh Some): the StmtVar opt-IIFE inference falls back to the leaf
		// ident's tracked opt_type_of_slot (#3165).
		{"ifexpr-ret-optvar", `function f(c: i32): Option[i32] { if (c > 3) { return Some(7); } return None; } function main(): i32 { var o = f(5); var r = if (true) { o } else { None }; return match (r) { Some(v) => v, None => 0 }; }`},
		{"matchexpr-ret-optvar", `function main(): i32 { var o = Some(8); var k = 1; var r = match (k) { 1 => o, _ => o }; return match (r) { Some(v) => v, None => 0 }; }`},
		// A tuple literal with an if-/match-EXPRESSION element: the tuple lowering now
		// classifies each element by its leaf branch value via iife_leaf_value, so an
		// IIFE element is admitted with the right kind tag (#3172).
		{"tuple-ifexpr-elem0", `function main(): i32 { var c = 5; var t = (if (c > 3) { 7 } else { 1 }, 3); return t.0 + t.1; }`},
		{"tuple-ifexpr-elem1", `function main(): i32 { var c = 1; var t = (3, if (c > 3) { 7 } else { 1 }); return t.0 + t.1; }`},
		{"tuple-matchexpr-elem", `function main(): i32 { var k = 1; var t = (match (k) { 1 => 5, _ => 0 }, 3); return t.0 + t.1; }`},
		// A struct array field set from an if-/match-EXPRESSION whose every branch is
		// a fresh array literal (iife_returns_fresh_array): admitted as an owned value
		// (#3179). An aliased branch stays on the AST path (verified by probe).
		{"struct-fld-ifexpr-arr", `struct B { xs: i32[] } function main(): i32 { var c = 5; var b = B { xs: if (c > 3) { [1, 2, 3] } else { [4] } }; return b.xs.len(); }`},
		{"struct-fld-ifexpr-arr-else", `struct B { xs: i32[] } function main(): i32 { var c = 1; var b = B { xs: if (c > 3) { [1, 2, 3] } else { [4, 5] } }; return b.xs.len(); }`},
		{"struct-fld-matchexpr-arr", `struct B { xs: i32[] } function main(): i32 { var k = 1; var b = B { xs: match (k) { 1 => [7, 8, 9], _ => [0] } }; return b.xs.len() + b.xs[0]; }`},
		// An array literal whose element is an if-/match-EXPRESSION struct: the StmtVar
		// struct-array inference classifies the first element by its leaf branch via
		// iife_leaf_value, so a[i].field resolves (#3183).
		{"arr-ifexpr-struct-elem", `struct P { x: i32 } function main(): i32 { var c = 5; var a = [if (c > 3) { P { x: 7 } } else { P { x: 1 } }]; return a[0].x; }`},
		{"arr-ifexpr-struct-foreach", `struct P { x: i32 } function main(): i32 { var c = 5; var a = [if (c > 3) { P { x: 7 } } else { P { x: 1 } }, P { x: 2 }]; var s = 0; for p in a { s = s + p.x; } return s; }`},
		{"arr-matchexpr-struct-elem", `struct P { x: i32 } function main(): i32 { var k = 1; var a = [match (k) { 1 => P { x: 9 }, _ => P { x: 0 } }]; return a[0].x; }`},
		// Field access / method dispatch directly on an if-/match-EXPRESSION:
		// expr_struct_type now resolves an IIFE value's struct type via
		// iife_leaf_value, so `(if (c) { P{..} } else { .. }).field` lowers (#3186).
		{"ifexpr-field-direct", `struct P { x: i32 } function main(): i32 { var c = 5; return (if (c > 3) { P { x: 7 } } else { P { x: 1 } }).x; }`},
		{"ifexpr-method-direct", `struct P { x: i32 } function (p: P) g(): i32 { return p.x; } function main(): i32 { var c = 5; return (if (c > 3) { P { x: 7 } } else { P { x: 1 } }).g(); }`},
		{"matchexpr-field-direct", `struct P { x: i32 } function main(): i32 { var k = 1; return (match (k) { 1 => P { x: 9 }, _ => P { x: 0 } }).x; }`},
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
		// UNANNOTATED map-literal bindings (`var m = Map { … }` with no
		// `: Map[K,V]`): the binding must infer the key kind from the desugared
		// `map_new[_i32](n).insert(…)` chain (expr_map_kind) and store the full
		// `Map[K,V]` form, else `.get_or`/.has on an i32-key map emit key-kind 0
		// (string) and deref the i32 key as a string pointer (segfault). The AST
		// path (asm.fern) already infers this, so AST and IR must agree.
		{"map-unannot-i32-hit", `function main(): i32 { var m = Map { 1: 10, 2: 20 }; return m.get_or(2, 0); }`},
		{"map-unannot-i32-miss", `function main(): i32 { var m = Map { 1: 10 }; return m.get_or(9, 7); }`},
		{"map-unannot-i32-insert", `function main(): i32 { var m = Map { 1: 10 }; m = m.insert(2, 20); return m.get_or(2, 0); }`},
		{"map-unannot-i32-has", `function main(): i32 { var m = Map { 1: 10 }; var r = 0; if (m.has(1)) { r = r + 1; } if (m.has(9)) { r = r + 10; } return r; }`},
		{"map-unannot-i32-len", `function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; return m.len(); }`},
		{"map-unannot-i32-keys", `function main(): i32 { var m = Map { 3: 1, 4: 1, 5: 1 }; var s = 0; for k in m.keys() { s = s + k; } return s; }`},
		{"map-unannot-str-hit", `function main(): i32 { var m = Map { "a": 5, "bb": 6 }; return m.get_or("bb", 0); }`},
		{"map-unannot-str-miss", `function main(): i32 { var m = Map { "a": 5 }; return m.get_or("z", 8); }`},
		// String-VALUE maps (`Map[K, string]`): `m.get_or(k, d)` returns the
		// stored string, so the result must track as a string for `.len()` /
		// concat (else it reads the box's data-ptr slot as a length — garbage).
		// asm.fern infers the value type via infer_expr_type/map_val (annotated)
		// so AST is the oracle; the IR side now matches (expr_is_str + the
		// unannotated value-tag inference). Covers annotated + unannotated, i32-
		// and string-key, get_or hit, and .values() iteration.
		{"map-strval-annot-getor", `function main(): i32 { var m: Map[i32, string] = Map { 1: "hi" }; return m.get_or(1, "x").len(); }`},
		{"map-strval-annot-strkey", `function main(): i32 { var m: Map[string, string] = Map { "a": "bcd" }; return m.get_or("a", "z").len(); }`},
		{"map-strval-unannot-getor", `function main(): i32 { var m = Map { 1: "hi" }; return m.get_or(1, "x").len(); }`},
		{"map-strval-unannot-strkey", `function main(): i32 { var m = Map { "a": "bb", "c": "ddd" }; return m.get_or("c", "z").len(); }`},
		{"map-strval-values", `function main(): i32 { var m = Map { "a": "bb", "c": "ddd" }; var n = 0; for v in m.values() { n = n + v.len(); } return n; }`},
		{"map-strval-getor-miss", `function main(): i32 { var m = Map { 1: "hi" }; return m.get_or(9, "zzzz").len(); }`},
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
		// An i64-PRIM-receiver method returning i64 (`n.dbl()` on `(n: i64) dbl`):
		// the receiver has no struct type, so lower_i64's call-recovery used to
		// bail (couldn't form the "i64.<m>" i64-ret-fn key). method_recv_tyname now
		// recovers "i64"/"u64" from the receiver's value width, keeping the whole
		// thing on the IR path (mirrors std/i64's gcd/lcm flipping BAIL->ir).
		{"i64-method-recv", `function (n: i64) dbl(): i64 { return n * 2; } function main(): i32 { var a: i64 = 3000000000; var r: i64 = a.dbl(); if (r > 5000000000) { return 7; } return 0; }`},
		{"i64-method-recv-calls-method", `function (n: i64) absv(): i64 { if (n < 0) { return 0 - n; } return n; } function (n: i64) gcdv(m: i64): i64 { var a: i64 = n.absv(); var b: i64 = m.absv(); while (b != 0) { var t: i64 = b; b = a % b; a = t; } return a; } function main(): i32 { var x: i64 = 48; var y: i64 = 36; if (x.gcdv(y) == 12) { return 0; } return 1; }`},
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
		// WIDE `.to_string()` receivers (#5826). The AST emitter already served
		// these; the IR path fell through to the method paths, which resolve an
		// `i64.to_string` label that only exists with `import "std/i64"`, so the
		// whole MODULE bailed. f-strings desugar to `.to_string()`, so the
		// f-string row is the shape that made this common in ordinary code.
		// Boundary values are the point: INT64_MIN (whose negation wraps, hence
		// the unsigned digit arithmetic in rt_src_i64_to_string) and a u64 with
		// bit 63 set (which the SIGNED formatter renders negative).
		{"i64-to-string", `function main(): i32 { var n: i64 = 1234567890123; return n.to_string().len(); }`},
		{"i64-to-string-neg", `function main(): i32 { var n: i64 = 0 - 7; return n.to_string().len(); }`},
		{"i64-to-string-zero", `function main(): i32 { var n: i64 = 0; return n.to_string().len(); }`},
		{"i64-to-string-min", `function main(): i32 { var n: i64 = (0 as i64) - 9223372036854775807 - 1; return n.to_string().len(); }`},
		{"u64-to-string-high-bit", `function main(): i32 { var n: u64 = (0 as u64) - (1 as u64); return n.to_string().len(); }`},
		{"i64-fstring", `function main(): i32 { var n: i64 = 42; var s: string = f"v={n}"; return s.len(); }`},
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
		// Clock leaves migrated to Fern on the IR path (#2649). The VALUES are
		// non-deterministic, but deterministic PROPERTIES (a live clock reads
		// positive; realtime is past 2023) hold identically on the hand-asm AST
		// path and the Fern IR helper — the differential proves the IR helper
		// (via __syscall3 / __raw_scratch / __load_i64 + i64 math) matches.
		{"monotonic-ns-positive", `function main(): i32 { var a: i64 = monotonic_ns(); if (a > (0 as i64)) { return 1; } return 0; }`},
		{"now-unix-ms-past-2023", `function main(): i32 { var ms: i64 = now_unix_ms(); if (ms / (1000 as i64) > (1700000000 as i64)) { return 1; } return 0; }`},
		{"now-ns-past-2023", `function main(): i32 { var ns: i64 = now_ns(); if (ns / (1000000000 as i64) > (1700000000 as i64)) { return 1; } return 0; }`},
		{"monotonic-ns-nondecreasing", `function main(): i32 { var a: i64 = monotonic_ns(); var b: i64 = monotonic_ns(); if (b >= a) { return 1; } return 0; }`},
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
		// f-string interpolation (`f"...{expr}..."`): the parser desugars to a
		// `+`-chain of literal string parts and `(expr).to_string()` calls, so the
		// AST and IR self-host paths must agree byte-for-byte. Covers i32 / string
		// / expression / method-call / negation interpolants, adjacent interpolants,
		// an interpolant-then-literal, the `{{` brace escape, and empty / plain.
		{"fstring-i32", `function main(): i32 { var n = 7; var s = f"n={n}!"; return s.len(); }`},
		{"fstring-i32-char", `function main(): i32 { var n = 7; var s = f"n={n}!"; return s[2] as i32; }`},
		{"fstring-str", `function main(): i32 { var w = "xy"; var s = f"[{w}]"; return s.len(); }`},
		{"fstring-expr", `function main(): i32 { var a = 10; var s = f"v={a * 2}"; return s[2] as i32; }`},
		{"fstring-method", `function main(): i32 { var w = "hi"; return f"v={w.len()}".len(); }`},
		{"fstring-neg", `function main(): i32 { var a = 5; return f"v={0 - a}".len(); }`},
		{"fstring-multi", `function main(): i32 { var a = 1; var b = 2; return f"{a}{b}".len(); }`},
		{"fstring-interp-lit", `function main(): i32 { var n = 42; return f"{n} apples".len(); }`},
		{"fstring-empty", `function main(): i32 { return f"".len(); }`},
		{"fstring-plain", `function main(): i32 { return f"plain".len(); }`},
		{"fstring-esc-brace", `function main(): i32 { var s = f"a{{b"; return s[1] as i32; }`},
		// ASCII case transforms → fresh string (op_str_to_upper / _to_lower). The
		// AST path emits __fern_str_to_upper/_lower (str_search runtime); the IR
		// path emits its own emit_ir_str_case bodies — lengths/bytes must match.
		{"to-upper-len", `function main(): i32 { var s = "Hello"; return s.to_ascii_upper().len(); }`},
		{"to-upper-byte", `function main(): i32 { var s = "abc"; var u = s.to_ascii_upper(); return u[0]; }`},
		{"to-lower-byte", `function main(): i32 { var s = "ABC"; var l = s.to_ascii_lower(); return l[2]; }`},
		{"to-upper-mixed", `function main(): i32 { var u = "aB9z".to_ascii_upper(); return u[0] + u[1] + u[2] + u[3]; }`},
		{"to-lower-mixed", `function main(): i32 { var l = "Ab9Z".to_ascii_lower(); return l[0] + l[1] + l[2] + l[3]; }`},
		{"to-upper-empty", `function main(): i32 { return "".to_ascii_upper().len() + 5; }`},
		{"case-roundtrip", `function main(): i32 { var s = "Hello"; if (s.to_ascii_upper().to_ascii_lower() == "hello") { return 7; } return 0; }`},
		{"case-param", `function up(s: string): i32 { return s.to_ascii_upper()[0]; } function main(): i32 { return up("xyz"); }`},
		{"case-on-literal", `function main(): i32 { return "Mixed".to_ascii_lower().len(); }`},
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
		// Free-function spellings of the transform builtins (str_to_upper(s) /
		// str_to_lower / str_trim / str_repeat(s, n) / str_replace(s, a, b) /
		// str_contains(s, sub)) — the receiver is the first positional arg, the
		// rest are the method args. These route through the SAME ops as the
		// `.<field>()` method forms (lower_str_method), so AST and IR must agree.
		// The self-host compiler's own source uses these spellings, so lowering
		// them widens the IR subset for self-compilation. (str_split / the
		// predicates already had free-call cases above.)
		{"free-to-upper-len", `function main(): i32 { var t = str_to_upper("Hello"); return t.len(); }`},
		{"free-to-upper-byte", `function main(): i32 { var t = str_to_upper("abc"); return t[0]; }`},
		{"free-to-lower-byte", `function main(): i32 { var t = str_to_lower("ABC"); return t[2]; }`},
		{"free-trim-len", `function main(): i32 { var t = str_trim("  hi  "); return t.len(); }`},
		{"free-trim-byte", `function main(): i32 { var t = str_trim("  xy"); return t[0]; }`},
		{"free-repeat-len", `function main(): i32 { var t = str_repeat("ab", 3); return t.len(); }`},
		{"free-repeat-byte", `function main(): i32 { var t = str_repeat("xy", 4); return t[0] + t[7]; }`},
		{"free-replace-len", `function main(): i32 { var t = str_replace("a-b-c", "-", "_"); return t.len(); }`},
		{"free-replace-grow", `function main(): i32 { var t = str_replace("aaa", "a", "bb"); return t.len(); }`},
		{"free-contains-true", `function main(): i32 { if (str_contains("hello world", "o w")) { return 7; } return 0; }`},
		{"free-contains-false", `function main(): i32 { if (str_contains("hello", "xyz")) { return 7; } return 9; }`},
		{"free-nested", `function main(): i32 { var t = str_trim(str_to_upper("  ab  ")); return t.len(); }`},
		{"free-concat", `function main(): i32 { var t = str_to_upper("ab") + "Z"; return t.len(); }`},
		// str_to_i32(s): parse a string box to an i32 (the inverse of
		// i32_to_string). Allocation-free; the IR stack-ABI body must agree with
		// asm.fern's register-ABI __fern_str_to_i32. Covers plain, negative,
		// trailing-junk, empty (->0), and the i32_to_string round-trip.
		{"str2i32-basic", `function main(): i32 { return str_to_i32("42"); }`},
		{"str2i32-neg", `function main(): i32 { var n = str_to_i32("-5"); if (n < 0) { return 0 - n; } return n; }`},
		{"str2i32-trailing", `function main(): i32 { return str_to_i32("12x9"); }`},
		{"str2i32-empty", `function main(): i32 { return str_to_i32("") + 7; }`},
		{"str2i32-roundtrip", `function main(): i32 { return str_to_i32(i32_to_string(99)); }`},
		// chr(n): i32 byte -> 1-char string box. The IR stack-ABI body must agree
		// with asm.fern's register-ABI __fern_chr. Covers len, indexed byte, and
		// `+` concat of two chr results.
		{"chr-len", `function main(): i32 { return chr(65).len(); }`},
		{"chr-byte", `function main(): i32 { return chr(122)[0] as i32; }`},
		{"chr-concat", `function main(): i32 { var s = chr(72) + chr(105); return s.len() * 100 + s[0]; }`},
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
		// A struct-valued if-/match-EXPRESSION binding (lifted to a `__lam_N`
		// whose return type is inferred from its struct-literal body, so the
		// `__lam_N()` call site recovers the struct type for `.field` / method
		// dispatch). The legacy AST path also handles these, so they ride the
		// differential gate.
		{"struct-if-expr-field", `struct P { x: i32, y: i32 } function main(): i32 { var p = if (true) { P{x:1,y:2} } else { P{x:3,y:4} }; return p.x + p.y; }`},
		{"struct-match-expr-field", `struct P { x: i32, y: i32 } function main(): i32 { var p = match (1) { 1 => P{x:10,y:2}, _ => P{x:3,y:4} }; return p.x + p.y; }`},
		{"struct-if-expr-direct-field", `struct P { x: i32, y: i32 } function main(): i32 { return (if (true) { P{x:7,y:2} } else { P{x:3,y:4} }).x; }`},
		{"struct-if-expr-method", `struct P { x: i32, y: i32 } function (p: P) sum(): i32 { return p.x + p.y; } function main(): i32 { var p = if (true) { P{x:1,y:2} } else { P{x:3,y:4} }; return p.sum(); }`},
		// An enum-valued if-/match-EXPRESSION binding stays an inline IIFE (its
		// variant constructors read as captures, so lift_lambdas leaves it as
		// ExprLambda); expr_enum_type sees through the IIFE so the bound local
		// types as the enum and a method call on it dispatches to <Enum>.<method>.
		{"enum-if-expr-method", `enum Shape { Circle(i32), Square(i32) } function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } return 0; } function main(): i32 { var s = if (true) { Circle(2) } else { Square(3) }; return s.area(); }`},
		{"enum-match-expr-method", `enum Shape { Circle(i32), Square(i32) } function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } return 0; } function main(): i32 { var s = match (1) { 1 => Circle(2), _ => Square(3) }; return s.area(); }`},
		{"enum-unit-if-expr-method", `enum Color { Red, Green, Blue } function (c: Color) code(): i32 { match (c) { Red => { return 1; }, Green => { return 2; }, Blue => { return 3; } } return 0; } function main(): i32 { var c = if (false) { Red } else { Green }; return c.code(); }`},
		// A NESTED struct-valued if-/match-EXPRESSION binding: each inner branch
		// is itself lifted to a `__lam_M`, so fn_inferred_struct_ret recurses
		// through the `__lam` chain to the innermost struct literal.
		{"struct-nested-if-expr", `struct P { x: i32, y: i32 } function main(): i32 { var p = if (true) { if (false) { P{x:1,y:2} } else { P{x:5,y:6} } } else { P{x:3,y:4} }; return p.x + p.y; }`},
		{"struct-match-then-if-expr", `struct P { x: i32, y: i32 } function main(): i32 { var p = match (1) { 1 => if (true) { P{x:4,y:5} } else { P{x:0,y:0} }, _ => P{x:3,y:4} }; return p.x + p.y; }`},
		// A struct-returning USER function called in each if-/match-expression
		// branch: the lifted `__lam`'s leaf is a call to `mk`, so the struct type
		// is read from `mk`'s declared return type.
		{"struct-fncall-if-expr", `struct P { x: i32, y: i32 } function mk(v: i32): P { return P{x:v, y:v+1}; } function main(): i32 { var p = if (true) { mk(5) } else { mk(2) }; return p.x + p.y; }`},
		{"struct-fncall-match-expr", `struct P { x: i32, y: i32 } function mk(v: i32): P { return P{x:v, y:v+1}; } function main(): i32 { var p = match (2) { 2 => mk(10), _ => mk(0) }; return p.x + p.y; }`},
		// An if-/match-expression binding whose first branch CALLS an
		// Option/Result-returning function (`if (c) { mkO(7) } else { Some(0) }`):
		// the leaf is a call, so the bound local's opt-type is recovered from the
		// callee's registered return type, letting a later `match (o)` lower.
		{"opt-fncall-if-expr", `function mkO(v: i32): Option[i32] { return Some(v); } function main(): i32 { var o = if (true) { mkO(7) } else { Some(0) }; match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`},
		{"result-fncall-if-expr", `function div(a: i32, b: i32): Result[i32, i32] { if (b == 0) { return Err(1); } return Ok(a / b); } function main(): i32 { var r = if (true) { div(20, 4) } else { Err(9) }; match (r) { Ok(n) => { return n; }, Err(e) => { return e; } } return 0; }`},
		// `for x in <u64[]>`: the element rides the i64 8-byte read but is bound
		// u64, so body compares/shifts on it are unsigned. The IR path now lowers
		// this (previously bailed to AST); both paths must agree, and the large
		// element (> 2^63) makes a signed-vs-unsigned miscompile observable — a
		// signed `>` would mis-order it, picking the wrong max.
		{"u64-forin-sum", "function main(): i32 { var xs: u64[] = [10u64, 20u64, 5u64]; var s: u64 = 0u64; for x in xs { s = s + x; } return s as i32; }"},
		{"u64-forin-max-big", "function main(): i32 { var xs: u64[] = [5u64, 9223372036854775809u64, 7u64]; var best: u64 = 0u64; for x in xs { if (x > best) { best = x; } } if (best == 9223372036854775809u64) { return 0; } return 1; }"},
		// `arr[i]` index read on a u64[] producing a u64 value (param or local):
		// reads 8-byte (arr_index_is_i64 now accepts u64[]) and the use compares
		// it unsigned (expr_is_u64). The max over a u64[] PARAM whose middle
		// element is 2^63+1 — the shape cmp.max_of over a u64[] uses — would pick the
		// wrong element under a signed compare; both paths must return 0.
		{"u64-param-idx-sum", "function s(arr: u64[]): u64 { var t: u64 = 0u64; var i = 0; while (i < arr.len()) { t = t + arr[i]; i = i + 1; } return t; } function main(): i32 { return s([10u64, 20u64, 5u64]) as i32; }"},
		{"u64-param-idx-max-big", "function mx(arr: u64[]): u64 { var m: u64 = arr[0]; var i = 1; while (i < arr.len()) { if (arr[i] > m) { m = arr[i]; } i = i + 1; } return m; } function main(): i32 { if (mx([5u64, 9223372036854775809u64, 7u64]) == 9223372036854775809u64) { return 0; } return 1; }"},
		// `.append` / `.with` on an 8-byte-element array (i64[] / u64[]): grow via
		// the new arr_push_i64 (reuses __fern_arr_push on the register backends,
		// $__fern_arr_push_i64 on wasm) and in-place 8-byte store. Large values
		// (> 2^32, and > 2^63 for u64) catch a 4-byte truncation; the u64 unsigned
		// compare catches a signed-compare mishandling. Both must match AST.
		{"i64-append-build", "function main(): i32 { var a: i64[] = []; var i = 0; while (i < 4) { a = a.append((i as i64) * 5000000000); i = i + 1; } if (a[3] == 15000000000 as i64) { return 0; } return 1; }"},
		{"i64-sort-asc", "function main(): i32 { var a: i64[] = []; a = a.append(9 as i64); a = a.append(3 as i64); a = a.append(6 as i64); var k = 1; while (k < 3) { var key: i64 = a[k]; var j = k - 1; while (j >= 0 && a[j] > key) { a = a.with(j + 1, a[j]); j = j - 1; } a = a.with(j + 1, key); k = k + 1; } if (a[0] == 3 as i64 && a[2] == 9 as i64) { return 0; } return 1; }"},
		{"u64-append-with-big", "function main(): i32 { var xs: u64[] = []; xs = xs.append(9223372036854775809u64); xs = xs.append(3u64); xs = xs.with(1, 9223372036854775810u64); if (xs[0] != 9223372036854775809u64) { return 1; } if (xs[1] > xs[0]) { return 0; } return 2; }"},
		// Returning a u64[] (a pointer move) — a u64[]-returning sort shape.
		// The width-64 return guard must NOT mistake the u64[] array for a scalar
		// u64. Sorting elements spanning 2^63 proves the body's unsigned compares
		// order them correctly through the IR path; both paths must agree.
		{"u64-return-array", "function id(arr: u64[]): u64[] { return arr; } function main(): i32 { return id([7u64, 8u64])[1] as i32; }"},
		{"u64-sort-big", "function srt(arr: u64[]): u64[] { var n = arr.len(); var out: u64[] = []; var i = 0; while (i < n) { out = out.append(arr[i]); i = i + 1; } var k = 1; while (k < n) { var key: u64 = out[k]; var j = k - 1; while (j >= 0 && out[j] > key) { out = out.with(j + 1, out[j]); j = j - 1; } out = out.with(j + 1, key); k = k + 1; } return out; } function main(): i32 { var s = srt([9223372036854775810u64, 9223372036854775809u64, 3u64]); if (s[0] == 3u64 && s[1] == 9223372036854775809u64 && s[2] == 9223372036854775810u64) { return 0; } return 1; }"},
		// match on a STRING-receiver method's Option result (`match (s.m())`): the
		// scrutinee-type recovery now keys "string.<method>", so std/string's
		// parse_int_or (match (s.parse_int()) { … }) lowers. Both Some and None
		// arms exercised; must match the AST path.
		{"string-method-option-match", "function (s: string) firstlen(): Option[i32] { if (s.len() == 0) { return None; } return Some(s.len()); } function f(s: string): i32 { match (s.firstlen()) { Some(v) => { return v; }, None => { return 0 - 1; } } } function main(): i32 { return f(\"hello\") * 10 + (f(\"\") + 1); }"},
		// match Some(p) binding a leak-safe TUPLE payload (`(string, string)`) from
		// a method-call Option result — the std/string is_email_like shape
		// (match (s.split_once(\"@\")) { Some(p) => … p.0 … p.1 … }). The bound
		// slot is tagged with the tuple element types so p.0/p.1 read correctly;
		// both Some and None arms exercised, must match the AST path.
		{"option-tuple-payload-method-match", "function (s: string) halves(): Option[(string, string)] { if (s.len() < 2) { return None; } return Some((s[0:1], s[1:s.len()])); } function f(s: string): i32 { match (s.halves()) { Some(p) => { return p.0.len() * 100 + p.1.len(); }, None => { return 0 - 1; } } } function main(): i32 { if (f(\"hello\") == 104 && f(\"x\") == 0 - 1) { return 0; } return 1; }"},
		// RETURNING a tuple-element array — `(i32, i32)[]` — the std/array
		// enumerate/zip shape. The bare-tuple-return guard wrongly caught the
		// `(...)[]` ret type; excluding array types lets it take the array
		// move-on-return path. Build via append, return, read .0/.1 at the caller.
		{"tuple-array-build-return", "function enum2(xs: i32[]): (i32, i32)[] { var out: (i32, i32)[] = []; var i = 0; for x in xs { out = out.append((i, x * x)); i = i + 1; } return out; } function main(): i32 { var e = enum2([5, 6, 7]); if (e.len() == 3 && e[1].0 == 1 && e[1].1 == 36 && e[2].1 == 49) { return 0; } return 1; }"},
		// `defer` / `errdefer` — lift_lambdas now runs parser.lower_defers_module,
		// scheduling the deferred action at every scope exit, exactly as
		// module_with_builtins does for the AST backend.
		// A module whose only IR-ineligible construct was a defer now lowers via
		// the IR path. Covers basic / early-return / two-defer-order / errdefer.
		{"defer-basic", "function main(): i32 { var x = 0; defer { x = 99; } x = 1; return x; }"},
		{"defer-early-return", "function f(n: i32): i32 { var x = 5; defer { x = 0; } if (n > 0) { return n; } return x; } function main(): i32 { return f(7); }"},
		{"defer-order-two", "function main(): i32 { var a = [0, 0]; defer { a = a.with(0, 1); } defer { a = a.with(1, 2); } return a[0] + a[1]; }"},
		{"errdefer-ok-path", "function f(): Result[i32, string] { var x = 1; errdefer { x = 9; } return Ok(x); } function main(): i32 { match (f()) { Ok(v) => { return v; }, Err(_) => { return 0; } } }"},
	}

	// Each case must COMPILE through the IR path, assemble, link and run.
	// emitAndRun fatals if the driver refuses (there is no AST leg to fall back
	// to any more), if gcc rejects the asm, or if the binary cannot be run — so
	// this is an eligibility + assembly guard over ~930 programs, which is what
	// slice 5 needs kept green.
	//
	// It is NOT the behaviour-equivalence oracle it used to be, and that loss is
	// real: a miscompile that changes an exit code now passes here. See the header
	// for why there is nothing left to compare against.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emitAndRun(t, tc.src, true)
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
		// A string-CONCAT branch in a value-position if-/match-expression: the
		// lifted `__lam` carries a default i32 ret_type (the desugar doesn't infer
		// `string + string : string`), so before str_ret_fns_of's body-inference a
		// `.len()` on the result mis-dispatched to the array path — a silent
		// miscompile (returned 56, not 4). These assert the IR value directly (the
		// AST==IR differential is blind to a both-paths-wrong case).
		// A map-RETURNING call as a TUPLE-LITERAL element (`(mk(), 3)`): the
		// element tag now recovers the full Map[K, V] from the map_ret_fns
		// registry (#3317's missing arm in expr_map_type_tag), so a later
		// `t.0.get_or(…)` / `t.0.len()` dispatches as a map op. Before, the
		// element fell through to the scalar "i32" tag and the map method
		// silently returned the -1 unknown-method shim (10 read back as 2,
		// 4 as 2). Pinned to the absolute IR value (the AST==IR differential
		// is blind to a both-paths-wrong case).
		// Absolute digit counts for the wide stringifiers (#5826) — the
		// differential rows above prove IR matches AST, but are blind to a
		// both-paths-wrong case, and these are exactly the values where a
		// sign-handling slip shows up: INT64_MIN is 20 chars including the
		// minus, u64 max is 20 digits unsigned (11 if misread as signed -1).
		{"i64-to-string-min-len", `function main(): i32 { var n: i64 = (0 as i64) - 9223372036854775807 - 1; return n.to_string().len(); }`, 20},
		{"u64-to-string-max-len", `function main(): i32 { var n: u64 = (0 as u64) - (1 as u64); return n.to_string().len(); }`, 20},
		{"i64-to-string-13-digits", `function main(): i32 { var n: i64 = 1234567890123; return n.to_string().len(); }`, 13},
		{"map-ret-tuple-elem-getor", `function mk(): Map[i32, i32] { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 7); return m; } function main(): i32 { var t: (Map[i32, i32], i32) = (mk(), 3); return t.1 + t.0.get_or(1, 0); }`, 10},
		{"map-ret-tuple-elem-len", `function mk(): Map[i32, i32] { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 7); return m; } function main(): i32 { var t: (Map[i32, i32], i32) = (mk(), 3); return t.1 + t.0.len(); }`, 4},
		{"map-ret-tuple-elem-unannotated", `function mk(): Map[i32, i32] { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 7); return m; } function main(): i32 { var t = (mk(), 3); return t.1 + t.0.get_or(1, 0); }`, 10},
		// The METHOD-call sibling (`(f.mk(), 3)`): the registry keys methods
		// "<BaseType>.<method>", resolved via the receiver's struct type.
		{"map-ret-method-tuple-elem", `struct Fac { base: i32 } function (f: Fac) mk(): Map[i32, i32] { var m: Map[i32, i32] = map_new(4); m = m.insert(1, f.base); return m; } function main(): i32 { var f: Fac = Fac { base: 7 }; var t: (Map[i32, i32], i32) = (f.mk(), 3); return t.1 + t.0.get_or(1, 0); }`, 10},
		// The string[]-returning METHOD sibling (`(f.mks(), 3)`): before the
		// qualified strarr_ret_fns entry + expr_is_strarr method arm, the
		// element tag fell to scalar and `t.0[0].len()` dispatched to a
		// nonexistent `__fn_i32__len` (link failure).
		{"strarr-ret-method-tuple-elem", `struct Fac { base: i32 } function (f: Fac) mks(): string[] { var xs: string[] = []; xs = xs.append("ab"); return xs; } function main(): i32 { var f: Fac = Fac { base: 7 }; var t: (string[], i32) = (f.mks(), 3); return t.1 + t.0.len() + t.0[0].len(); }`, 6},
		{"str-concat-if-expr-direct", `function main(): i32 { return (if (true) { "ab" + "cd" } else { "x" }).len(); }`, 4},
		{"str-concat-if-expr-var", `function main(): i32 { var s = if (true) { "ab" + "cd" } else { "x" }; return s.len(); }`, 4},
		{"str-concat-if-expr-else", `function main(): i32 { var s = if (false) { "x" } else { "ab" + "cdef" }; return s.len(); }`, 6},
		{"str-concat-match-expr", `function main(): i32 { return (match (1) { 1 => "aa" + "bb", _ => "z" }).len(); }`, 4},
		// Value-position match-EXPRESSION binding a NON-i32 Option/Result payload and
		// BORROW-reading it to an i32 result (#2994-sibling IR-subset gap): the
		// iife_payload_bindable Option/Result branch now routes the payload through
		// iife_payload_field_bindable (the leak-safe borrow machinery the user-enum
		// branch already used) instead of hard-rejecting it. The payload is borrowed
		// from the box (leak-safe), and the i32 borrow read lands in the result temp.
		// Pinned to the absolute IR value (the AST==IR differential is blind to a
		// both-paths-wrong case for value-position match payloads).
		{"matchexpr-opt-str-len", `function main(): i32 { var o: Option[string] = Some("hi"); var y = match (o) { Some(s) => s.len(), None => 0 }; return y; }`, 2},
		{"matchexpr-opt-struct-field", `struct P { x: i32 } function main(): i32 { var o: Option[P] = Some(P { x: 9 }); var y = match (o) { Some(p) => p.x, None => 0 }; return y; }`, 9},
		{"matchexpr-result-err-str-len", `function main(): i32 { var r: Result[i32, string] = Err("bad"); var y = match (r) { Ok(n) => n, Err(s) => s.len() }; return y; }`, 3},
		{"matchexpr-opt-tuple-elem", `function main(): i32 { var o: Option[(i32, i32)] = Some((2, 3)); var y = match (o) { Some(t) => t.0 + t.1, None => 0 }; return y; }`, 5},
		{"matchexpr-result-ok-struct", `struct P { x: i32 } function main(): i32 { var r: Result[P, i32] = Ok(P { x: 8 }); var y = match (r) { Ok(p) => p.x + 4, Err(e) => e }; return y; }`, 12},
		// Value-position match-EXPRESSION binding a NON-i32 Option/Result payload and
		// returning it WHOLE (bare `Some(v) => v`) or as a composite — the result-temp
		// typing recovery functions (iife_arm_payload_result_kind /
		// iife_arm_composite_result_type) now resolve the Some/Ok/Err payload type from
		// the scrutinee's Option/Result spelling (iife_bound_payload_type), not just the
		// user-enum `__ev` field, so the bare string/struct/tuple payload return types
		// the temp instead of bailing to AST. Pinned to the absolute IR value (the
		// AST==IR differential is blind to a both-paths-wrong case for these payloads).
		{"matchexpr-opt-str-bare", `function main(): i32 { var o: Option[string] = Some("hi"); var s = match (o) { Some(v) => v, None => "" }; return s.len(); }`, 2},
		{"matchexpr-opt-struct-bare", `struct P { x: i32 } function main(): i32 { var o: Option[P] = Some(P { x: 7 }); var p = match (o) { Some(v) => v, None => P { x: 0 } }; return p.x; }`, 7},
		{"matchexpr-result-ok-str-bare", `function main(): i32 { var r: Result[string, i32] = Ok("abcd"); var s = match (r) { Ok(v) => v, Err(e) => "" }; return s.len(); }`, 4},
		{"matchexpr-result-ok-struct-bare", `struct P { x: i32 } function main(): i32 { var r: Result[P, i32] = Ok(P { x: 6 }); var p = match (r) { Ok(v) => v, Err(e) => P { x: 0 } }; return p.x; }`, 6},
		// NESTED struct/tuple-field borrow read of a match-EXPRESSION payload
		// (`Some(q) => q.p.x`): iife_payload_chain_type walks each struct-field /
		// tuple-element step from the payload type, so a multi-level i32 borrow read
		// classifies (the old single-level `name.field` check bailed). The store side
		// (the StmtMatch arm-body lowering of the nested field read) already lowered
		// in statement position; this admits it into the value-position IIFE temp.
		{"matchexpr-opt-nested-field", `struct P { x: i32 } struct Q { p: P } function main(): i32 { var o: Option[Q] = Some(Q { p: P { x: 5 } }); var y = match (o) { Some(q) => q.p.x, None => 0 }; return y; }`, 5},
		{"matchexpr-uenum-nested-field", `struct P { x: i32 } struct Q { p: P } enum E { Has(Q), Nil } function main(): i32 { var e: E = Has(Q { p: P { x: 6 } }); var y = match (e) { Has(q) => q.p.x, Nil => 0 }; return y; }`, 6},
		{"matchexpr-opt-nested-3deep", `struct A { v: i32 } struct B { a: A } struct C { b: B } function main(): i32 { var o: Option[C] = Some(C { b: B { a: A { v: 8 } } }); var y = match (o) { Some(c) => c.b.a.v, None => 0 }; return y; }`, 8},
		{"matchexpr-opt-nested-arith", `struct P { x: i32 } struct Q { p: P, k: i32 } function main(): i32 { var o: Option[Q] = Some(Q { p: P { x: 5 }, k: 6 }); var y = match (o) { Some(q) => q.p.x + q.k, None => 0 }; return y; }`, 11},
		// NARROWING CAST of an i64/f64 match-EXPRESSION payload to an i32 result
		// (`Some(n) => (n as i32)`): iife_arm_returns_narrowed_payload admits the
		// `name as i32/u32/u8` shape — the i64/f64 payload slot is marked at width
		// by the StmtMatch path so the cast reads the wide value and narrows, but the
		// result temp stays i32 (the old gate only admitted a BARE i64/f64 return).
		{"matchexpr-opt-i64-cast", `function main(): i32 { var o: Option[i64] = Some(5 as i64); var y = match (o) { Some(n) => (n as i32), None => 0 }; return y; }`, 5},
		{"matchexpr-opt-f64-cast", `function main(): i32 { var o: Option[f64] = Some(2.5); var y = match (o) { Some(n) => (n as i32), None => 0 }; return y; }`, 2},
		{"matchexpr-result-i64-cast", `function main(): i32 { var r: Result[i64, i32] = Ok(7 as i64); var y = match (r) { Ok(n) => (n as i32), Err(e) => e }; return y; }`, 7},
		{"matchexpr-uenum-i64-cast", `enum E { Big(i64), Nil } function main(): i32 { var e: E = Big(5 as i64); var y = match (e) { Big(n) => (n as i32), Nil => 0 }; return y; }`, 5},
		// Value-position match-EXPRESSION reading a bound i64/f64 payload via
		// ARITHMETIC (`Some(n) => n + 1`, `Some(p) => p.v + 1`, `Some(n) => n + 1.5`).
		// The result temp is classified to the payload's wide kind by the arithmetic
		// shape (iife_payload_arith_kind), and the StmtMatch arm-body store routes the
		// wide RHS through lower_i64 / the f64 path — statement-position already lowers
		// these, so only the value-position IIFE admission/classification was the gap.
		{"matchexpr-opt-i64-arith", `function main(): i64 { var o: Option[i64] = Some(5 as i64); return match (o) { Some(n) => n + 1, None => 0 as i64 }; }`, 6},
		{"matchexpr-result-i64-arith", `function main(): i64 { var r: Result[i64, i32] = Ok(5 as i64); return match (r) { Ok(n) => n + 1, Err(e) => 0 as i64 }; }`, 6},
		{"matchexpr-uenum-i64-arith", `enum E { V(i64), W } function main(): i64 { var e: E = V(5 as i64); return match (e) { V(n) => n + 1, W => 0 as i64 }; }`, 6},
		{"matchexpr-opt-f64-arith", `function main(): i32 { var o: Option[f64] = Some(4.5); return (match (o) { Some(n) => n + 1.5, None => 0.0 } as i32); }`, 6},
		{"matchexpr-opt-struct-i64-arith", `struct P { v: i64 } function main(): i64 { var o: Option[P] = Some(P{v: 5 as i64}); return match (o) { Some(p) => p.v + 1, None => 0 as i64 }; }`, 6},
		// METHOD CALL on a struct/enum match-EXPRESSION payload returning i64/f64
		// (`Some(p) => p.get()`, get(): i64): iife_method_ret_kind recovers the wide
		// result kind (typing the temp i64/f64) and iife_payload_wide_bindable admits
		// the call — the old gate only admitted bare/field/arith reads, so a wide-
		// returning payload method bailed. The i32-returning method case already rode
		// the i32 borrow path; this is its wide sibling.
		{"matchexpr-opt-method-i64", `struct P { v: i64 } function (p: P) get(): i64 { return p.v; } function main(): i64 { var o: Option[P] = Some(P { v: 7 as i64 }); return match (o) { Some(p) => p.get(), None => 0 as i64 }; }`, 7},
		{"matchexpr-uenum-method-i64", `struct P { v: i64 } enum E { Has(P), Nil } function (p: P) g(): i64 { return p.v + 2 as i64; } function main(): i64 { var e: E = Has(P { v: 7 as i64 }); return match (e) { Has(p) => p.g(), Nil => 0 as i64 }; }`, 9},
		// f-string interpolation, pinned to the absolute IR-path value (the
		// AST==IR differential above is blind to a both-paths-wrong desugar). The
		// returned byte proves the interpolant text actually landed: `f"n={7}!"`
		// → "n=7!" (s[2]='7'=55); `f"v={10*2}"` → "v=20" (s[2]='2'=50); the `{{`
		// escape → "a{b" (s[1]='{'=123).
		{"fstring-i32-val", `function main(): i32 { var n = 7; var s = f"n={n}!"; return s[2] as i32; }`, 55},
		{"fstring-expr-val", `function main(): i32 { var a = 10; var s = f"v={a * 2}"; return s[2] as i32; }`, 50},
		{"fstring-esc-brace-val", `function main(): i32 { var s = f"a{{b"; return s[1] as i32; }`, 123},
		{"fstring-len-val", `function main(): i32 { var n = 42; return f"{n} apples".len(); }`, 9},
		// boolean-element tuple returns, pinned to the absolute IR-path value (the
		// AST==IR differential is blind to a both-paths-wrong result). The boolean
		// element round-trips through the tuple box and drives the `if`.
		{"tuple-bool-val", `function f(): (boolean, i32) { return (true, 7); } function main(): i32 { var t = f(); if (t.0) { return t.1; } return 0; }`, 7},
		{"tuple-bool-both-val", `function f(): (boolean, boolean) { return (true, false); } function main(): i32 { var t = f(); var r = 0; if (t.0) { r = r + 1; } if (t.1) { r = r + 10; } return r; }`, 1},
		// ≤32-bit non-i32 integer tuple elements, pinned to the absolute IR value:
		// the u32 high bits must survive the round-trip. (The signed-i8-negative
		// sibling case was retired along with i8 (#4408).)
		{"tuple-u32-val", `function f(): (u32, i32) { return (4000000000 as u32, 7); } function main(): i32 { var t = f(); var hi: u32 = t.0 >> 30; return (hi as i32) + t.1; }`, 10},
		// ≤32-bit non-i32 integer STRUCT fields, pinned to the absolute IR value:
		// the u32 high bits survive the field round-trip. (The signed-i8-negative
		// sibling case was retired along with i8 (#4408).)
		{"struct-u32-field-val", `struct B { hi: u32, n: i32 } function main(): i32 { var b = B { hi: 4000000000 as u32, n: 7 }; var hi: u32 = b.hi >> 30; return (hi as i32) + b.n; }`, 10},
		// A u64 struct field, pinned to the absolute IR value: 2^32 >> 32 == 1 proves
		// the high 32 bits survive the field round-trip (a truncating read gives 5).
		{"struct-u64-param-val", `struct B { hi: u64, n: i32 } function f(b: B): i32 { var q: u64 = b.hi >> 32; return (q as i32) + b.n; } function main(): i32 { return f(B { hi: 4294967296 as u64, n: 5 }); }`, 6},
		// u64 tuple element, pinned to the absolute IR value. The unsigned case
		// (18e18 has bit 63 set; `>> 60` unsigned == 15, a signed shift differs)
		// proves both the 64-bit width and the unsigned tracking survive the slot.
		{"tuple-u64-access-val", `function f(): (u64, i32) { return (4294967296 as u64, 5); } function main(): i32 { var t = f(); var q: u64 = t.0 >> 32; return (q as i32) + t.1; }`, 6},
		{"tuple-u64-unsigned-val", `function f(): (u64, i32) { return (18000000000000000000 as u64, 1); } function main(): i32 { var t = f(); var q: u64 = t.0 >> 60; return (q as i32) + t.1; }`, 16},
		// A string-ARRAY-valued if-/match-expression: the lifted `__lam` carries a
		// default i32 ret_type, so the binding was mis-treated as a scalar and the
		// 8-byte string elements were read at i32 width — a silent miscompile
		// (`xs[i].len()` returned 1, not the element length). array_ret_fns +
		// strarr_ret_fns_of now infer the array element type from the __lam body.
		// Asserted against the IR value directly (the AST==IR gate was blind).
		{"strarr-if-expr-direct-elem", `function main(): i32 { return (if (true) { ["a", "bb"] } else { ["ccc"] })[1].len(); }`, 2},
		{"strarr-if-expr-var-elems", `function main(): i32 { var xs = if (true) { ["a", "bb"] } else { ["ccc"] }; return xs[0].len() + xs[1].len(); }`, 3},
		{"strarr-if-expr-forin", `function main(): i32 { var xs = if (true) { ["a", "bb", "ccc"] } else { ["z"] }; var t = 0; for s in xs { t = t + s.len(); } return t; }`, 6},
		{"strarr-match-expr-elems", `function main(): i32 { var xs = match (1) { 1 => ["hi", "yo"], _ => ["x"] }; return xs[0].len() + xs[1].len() + xs.len(); }`, 6},
		// f64-ARRAY-valued if-/match-expression: same lifted-__lam scalar miscompile
		// as the string-array case (8-byte f64 elements read at i32 width). #3224.
		{"f64arr-if-expr-elem", `function main(): i32 { var xs = if (true) { [1.5, 2.5] } else { [3.5] }; return xs.len() * 10 + (xs[1] as i32); }`, 22},
		{"f64arr-if-expr-forin", `function main(): i32 { var xs = if (true) { [1.5, 2.5, 3.0] } else { [9.0] }; var t = 0.0; for x in xs { t = t + x; } return t as i32; }`, 7},
		{"f64arr-match-expr-elem", `function main(): i32 { return (match (1) { 1 => [2.5, 4.5], _ => [0.0] })[1] as i32; }`, 4},
		// A lifted if-/match-expression whose branch CALLS an opt-returning fn with
		// a `None`/call other branch is lambda-lifted (None is a keyword) — its
		// `__lam` opt return type is inferred from the body so a later `match (o)`
		// recovers the payload (#3236 sibling for the lifted shape).
		{"opt-fncall-if-none-else", `function mkO(v: i32): Option[i32] { return Some(v); } function main(): i32 { var o = if (true) { mkO(7) } else { None }; match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 7},
		{"opt-fncall-if-call-else", `function mkO(v: i32): Option[i32] { return Some(v); } function main(): i32 { var o = if (true) { mkO(7) } else { mkO(2) }; match (o) { Some(n) => { return n; }, None => { return 0; } } return 0; }`, 7},
		{"result-fncall-if-err-else", `function div(a: i32, b: i32): Result[i32, i32] { if (b == 0) { return Err(1); } return Ok(a / b); } function main(): i32 { var r = if (true) { div(20, 4) } else { Err(9) }; match (r) { Ok(n) => { return n; }, Err(e) => { return e; } } return 0; }`, 5},
		// A u32 Option/Result payload, IR value pinned. The u32 case reads a
		// high-bit value back through the box; the `>> 31` is LOGICAL
		// (4294967294 >> 31 = 1), so a wrong i32-arithmetic shift (-> -1, exit
		// 255) is caught — proof the bound payload is marked u32, not i32.
		{"opt-u32-payload-shift-val", `function main(): i32 { var o: Option[u32] = Some(4294967294 as u32); match (o) { Some(n) => { return (n >> 31) as i32; }, None => { return 0; } } return 0; }`, 1},
		{"result-u32-payload-val", `struct S { r: Result[u32, i32] } function main(): i32 { var s = S { r: Ok(42) }; match (s.r) { Ok(n) => { return n as i32; }, Err(e) => { return e; } } return 0; }`, 42},
		// A u64 Option payload, IR value pinned for 8-byte WIDTH: 5000000000
		// (> 2^32) `>> 32 == 1`, but a 32-bit-truncated payload read would give 0.
		// Value < 2^63 so the shift is signedness-agnostic — the pin isolates the
		// box width from the separate frontend u64-typing gap.
		{"opt-u64-payload-wide-val", `function main(): i32 { var o: Option[u64] = Some(5000000000 as u64); match (o) { Some(n) => { return (n >> 32) as i32; }, None => { return 0; } } return 0; }`, 1},
		{"result-u64-payload-val", `struct S { r: Result[u64, i32] } function main(): i32 { var s = S { r: Ok(42 as u64) }; match (s.r) { Ok(n) => { return n as i32; }, Err(e) => { return e; } } return 0; }`, 42},
		// u32[] struct-field round-trip, IR value pinned: the three elements read
		// back through the field array and sum.
		{"struct-u32arr-field-val", `struct Vec { vals: u32[], n: i32 } function main(): i32 { var v = Vec { vals: [10, 20, 30], n: 3 }; var s = 0; var i = 0; while (i < v.n) { s = s + (v.vals[i] as i32); i = i + 1; } return s; }`, 60},
		// A scalar-array tuple element round-trips through the destructure: the
		// array pointer is read back and its elements summed (5+10+7).
		{"tuple-i32arr-destr-val", `function f(): (i32[], i32) { return ([5, 10], 7); } function main(): i32 { var (arr, n) = f(); return arr[0] + arr[1] + n; }`, 22},
		// A struct-ARRAY-valued if-/match-expression (literal, or a P[]-returning
		// call in the branch): the lifted __lam's element struct type is inferred
		// so `ps[i].field` / `for p in ps { p.method() }` resolve. P[] sibling of
		// the string[]/f64[] array fixes (#3224).
		{"structarr-if-expr-elem", `struct P { x: i32, y: i32 } function main(): i32 { var ps = if (true) { [P{x:1,y:2}, P{x:3,y:4}] } else { [P{x:0,y:0}] }; return ps[1].x + ps[1].y; }`, 7},
		{"structarr-if-expr-forin-method", `struct P { x: i32, y: i32 } function (p: P) s(): i32 { return p.x + p.y; } function main(): i32 { var ps = if (true) { [P{x:1,y:2}, P{x:3,y:4}] } else { [P{x:0,y:0}] }; var t = 0; for p in ps { t = t + p.s(); } return t; }`, 10},
		{"structarr-match-expr-elem", `struct P { x: i32, y: i32 } function main(): i32 { var ps = match (1) { 1 => [P{x:5,y:6}], _ => [P{x:0,y:0}] }; return ps[0].x * 10 + ps[0].y; }`, 56},
		{"structarr-fncall-if-expr", `struct P { x: i32, y: i32 } function mk(): P[] { return [P{x:5,y:6}]; } function main(): i32 { var ps = if (true) { mk() } else { mk() }; return ps[0].x + ps[0].y; }`, 11},
		// A Map-typed STRUCT FIELD receiver (`c.m.get_or(k, d)`): map-method
		// dispatch resolves the map type from the field declaration, not just a
		// local slot, so reads through a struct field lower (the field read pushes
		// the map pointer). #map-struct-field.
		{"map-field-get_or", `struct Cache { m: Map[i32, i32], hits: i32 } function main(): i32 { var c = Cache{m: Map { 5: 50, 7: 70 }, hits: 1}; return c.m.get_or(5, 0) + c.m.get_or(7, 0) + c.hits; }`, 121},
		{"map-field-method", `struct Cfg { table: Map[string, i32] } function (c: Cfg) lookup(k: string): i32 { return c.table.get_or(k, 0); } function main(): i32 { var c = Cfg{table: Map { "a": 3, "b": 4 }}; return c.lookup("a") + c.lookup("b"); }`, 7},
		{"map-field-has-len", `struct Cache { m: Map[string, i32] } function main(): i32 { var c = Cache{m: Map { "a": 1, "b": 2 }}; var t = 0; if (c.m.has("a")) { t = t + c.m.len(); } return t; }`, 2},
		// A Map[K,V] PARAMETER recovers its map type, so map methods on the param
		// (`m.get_or(k, d)`) dispatch as map ops (the local-annotation path already
		// did this; params lacked it). #map-param.
		{"map-param-get_or", `function total(m: Map[i32, i32]): i32 { return m.get_or(1, 0) + m.get_or(2, 0); } function main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20 }; return total(m); }`, 30},
		{"map-param-string-key", `function look(m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); } function main(): i32 { var m: Map[string, i32] = Map { "x": 7 }; return look(m, "x"); }`, 7},
		// Iterating a Map-typed struct FIELD's keys()/values() (`for k in
		// c.m.keys()`): the foreach resolves the map type from the field decl,
		// like the map-method dispatch. #map-struct-field-iter.
		{"map-field-keys-forin", `struct Cfg { m: Map[i32, i32] } function main(): i32 { var c = Cfg{m: Map { 1: 10, 2: 20, 3: 30 }}; var t = 0; for k in c.m.keys() { t = t + c.m.get_or(k, 0); } return t; }`, 60},
		{"map-field-values-forin", `struct Cfg { m: Map[string, i32] } function main(): i32 { var c = Cfg{m: Map { "a": 3, "b": 4 }}; var t = 0; for v in c.m.values() { t = t + v; } return t; }`, 7},
		// An UNANNOTATED binding from a map-returning function (`var m = build()`):
		// the `map_ret_fns` registry recovers the slot's map type so `m.get_or(...)`
		// dispatches without a `: Map[K,V]` annotation. #3317.
		{"map-ret-fn-binding", `function build(): Map[i32, i32] { return Map { 1: 5, 2: 6 }; } function main(): i32 { var m = build(); return m.get_or(1, 0) + m.get_or(2, 0); }`, 11},
		{"map-ret-method-binding", `struct Reg { base: i32 } function (r: Reg) table(): Map[i32, i32] { return Map { 1: r.base, 2: r.base + 1 }; } function main(): i32 { var reg = Reg{base: 10}; var m = reg.table(); return m.get_or(1, 0) + m.get_or(2, 0); }`, 21},
		// A Map TUPLE element (`(Map { … }, x)`): the map-literal element is admitted
		// to tuple construction (a leak-only pointer slot) with a `Map[K,V]` tag, so
		// `t.0.get_or(…)` dispatches as a map op, a rebind `var m = t.0` recovers the
		// map type, and a string-VALUE element's get_or tracks as a string. The
		// self-host AST path also mishandled this (returned 4), so these pin the
		// absolute IR value. #3317.
		{"map-tuple-elem-get_or", `function main(): i32 { var t = (Map { 1: 10 }, 5); return t.0.get_or(1, 0) + t.1; }`, 15},
		{"map-tuple-elem-rebind", `function main(): i32 { var t = (Map { 1: 10 }, 5); var m = t.0; return m.get_or(1, 0) + t.1; }`, 15},
		{"map-tuple-elem-string-val", `function main(): i32 { var t = (Map { 1: "abcd" }, 5); return t.0.get_or(1, "z").len() + t.1; }`, 9},
		// An ARRAY of maps (`var ms = [Map { … }, …]`): the array slot carries the
		// ELEMENT map type (the map sibling of the struct-array element-type
		// overload), so `ms[i].get_or(…)` dispatches as a map op, a rebind
		// `var m = ms[i]` recovers the map type, an annotated `Map[K,V][]` binding
		// works, and a string-VALUE element's get_or tracks as a string. The
		// self-host AST path also mishandled this (link error on `i32.get_or`), so
		// these pin the absolute IR value. #3317.
		{"map-array-elem-get_or", `function main(): i32 { var ms = [Map { 1: 10 }, Map { 1: 20 }]; return ms[0].get_or(1, 0) + ms[1].get_or(1, 0); }`, 30},
		{"map-array-elem-rebind", `function main(): i32 { var ms = [Map { 1: 10 }, Map { 1: 20 }]; var m = ms[1]; return m.get_or(1, 0) + ms[0].get_or(1, 0); }`, 30},
		{"map-array-elem-annotated", `function main(): i32 { var ms: Map[i32, i32][] = [Map { 1: 10 }]; return ms[0].get_or(1, 0); }`, 10},
		{"map-array-elem-string-val", `function main(): i32 { var ms = [Map { 1: "abcd" }]; return ms[0].get_or(1, "z").len(); }`, 4},
		// A struct-ARRAY tuple element (`([P { .. }], x)`): the element's recorded
		// `P[]` tuple tag lets `t.0[i].field` / `t.0[i].method()` recover the
		// element struct type (the array sibling of the struct-field-array case).
		// The struct-array element constructs as a leak-only pointer slot. The
		// self-host AST path also mishandled the indexed field read, so these pin
		// the absolute IR value. #3353.
		{"tuple-structarr-elem-field", `struct P { x: i32 } function main(): i32 { var t = ([P{x:5}], 3); return t.0[0].x + t.1; }`, 8},
		{"tuple-structarr-elem-multi", `struct P { x: i32, y: i32 } function main(): i32 { var t = ([P{x:5,y:6}, P{x:7,y:8}], 100); return t.0[0].x + t.0[1].y + t.1; }`, 113},
		{"tuple-structarr-elem-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function main(): i32 { var t = ([P{x:5}], 3); return t.0[0].dbl() + t.1; }`, 13},
		// A string[] tuple element (`(["a","b"], x)`): the element's recorded
		// `string[]` tuple tag lets `t.0[i]` read as a string (`.len()`) and a
		// rebind `var xs = t.0` recover the string[] type. The element is a heap
		// pointer in one slot; the self-host AST path mishandled it (and refused it
		// at construction), so these pin the absolute IR value. #3353.
		{"tuple-strarr-elem-len", `function main(): i32 { var t = (["ab","cd"], 3); return t.0[1].len() + t.1; }`, 5},
		{"tuple-strarr-elem-two", `function main(): i32 { var t = (["ab","cd"], 3); return t.0[0].len() + t.0[1].len() + t.1; }`, 7},
		{"tuple-strarr-elem-rebind", `function main(): i32 { var t = (["ab","cd"], 3); var xs = t.0; return xs[1].len() + t.1; }`, 5},
		// An f64[] tuple element (`([1.5, 2.5], x)`): the element's recorded `f64[]`
		// tuple tag lets `t.0[i]` read an 8-byte f64 (arr_get width 64) and a rebind
		// `var xs = t.0` recover the f64[] type. The element is a heap pointer in one
		// slot; the self-host AST path mishandled it (and refused it at
		// construction), so these pin the absolute IR value. #3353.
		{"tuple-f64arr-elem-index", `function main(): i32 { var t = ([1.5, 2.5], 3); return (t.0[1] as i32) + t.1; }`, 5},
		{"tuple-f64arr-elem-sum", `function main(): i32 { var t = ([1.5, 2.5, 4.0], 10); var s = 0.0; var i = 0; while (i < 3) { s = s + t.0[i]; i = i + 1; } return (s as i32) + t.1; }`, 18},
		{"tuple-f64arr-elem-rebind", `function main(): i32 { var t = ([1.5, 2.5], 3); var xs = t.0; return (xs[1] as i32) + t.1; }`, 5},
		// An i64[]/u64[] tuple element (`([x as i64], y)`): the element's recorded
		// `i64[]` tuple tag lets `t.0[i]` read an 8-byte i64 (arr_get_i64) and a
		// rebind `var xs = t.0` recover the i64[] type. The literal is identified by
		// its unambiguous 64-bit first element (a bare integer literal stays i32).
		// The element is a heap pointer in one slot; the self-host AST path bailed it
		// at construction (lower_expr can't lower an `as i64` element), so these pin
		// the absolute IR value. #3353.
		{"tuple-i64arr-elem-index", `function main(): i32 { var t = ([10 as i64, 20 as i64], 3); return (t.0[1] as i32) + t.1; }`, 23},
		{"tuple-i64arr-elem-two", `function main(): i32 { var t = ([10 as i64, 20 as i64], 3); return (t.0[0] as i32) + (t.0[1] as i32) + t.1; }`, 33},
		{"tuple-i64arr-elem-rebind", `function main(): i32 { var t = ([10 as i64, 20 as i64], 3); var xs = t.0; return (xs[1] as i32) + t.1; }`, 23},
		{"tuple-i64arr-elem-while", `function main(): i32 { var t = ([10 as i64, 20 as i64], 3); var s: i64 = 0 as i64; var i = 0; while (i < 2) { s = s + t.0[i]; i = i + 1; } return (s as i32) + t.1; }`, 33},
		{"tuple-u64arr-elem-index", `function main(): i32 { var t = ([10 as u64, 20 as u64], 3); return (t.0[1] as i32) + t.1; }`, 23},
		// An UNANNOTATED i64 array literal binding (`var xs = [10 as i64, …]`): the
		// first element is i64-wide, so the slot is inferred i64[] and lowers the
		// same as the annotated `var xs: i64[] = …` (arr_make_i64 + 8-byte element
		// reads) instead of bailing to AST. #3353.
		{"i64arr-unannot-index", `function main(): i32 { var xs = [10 as i64, 20 as i64]; var q: i64 = xs[0] + xs[1]; return q as i32; }`, 30},
		{"i64arr-unannot-while", `function main(): i32 { var xs = [1 as i64, 2 as i64, 3 as i64]; var s: i64 = 0 as i64; var i = 0; while (i < 3) { s = s + xs[i]; i = i + 1; } return s as i32; }`, 6},
		{"i64arr-unannot-forin", `function main(): i32 { var xs = [10 as i64, 20 as i64]; var s: i64 = 0 as i64; for x in xs { s = s + x; } return s as i32; }`, 30},
		// Two random_i32 draws differ (a stuck/zero generator returns 0/1).
		{"random-i32-varies", `function main(): i32 { var a: i32 = random_i32(); var b: i32 = random_i32(); if (a == 0) { return 0; } if (a == b) { return 1; } return 7; }`, 7},
		// A random byte is in 0..255.
		{"random-bytes-byte-range", `function main(): i32 { var s: string = random_bytes(4); var x: i32 = s[0] as i32; if (x >= 0) { if (x <= 255) { return 1; } } return 0; }`, 1},
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
		{"fs-roundtrip", `function main(): i32 { var p: string = "/tmp/fern_fsrt_x86.txt"; match (write_file(p, "hello world")) { Ok(_) => {}, Err(_) => { return 1; } } match (read_file(p)) { Ok(c) => { if (c != "hello world") { return 2; } }, Err(_) => { return 3; } } match (remove_file(p)) { Ok(_) => {}, Err(_) => { return 4; } } match (read_file(p)) { Ok(_) => { return 5; }, Err(_) => {} } return 41; }`, 41},
		{"random-bytes-chunked", `function main(): i32 { var b: string = random_bytes(600); if (b.len() != 600) { return 1; } var v: i32 = 0; var i: i32 = 256; while (i < 600) { v = v | (b[i] as i32); i = i + 1; } if (v == 0) { return 2; } var w: i32 = 0; var j: i32 = 0; while (j < 256) { w = w | (b[j] as i32); j = j + 1; } if (w == 0) { return 3; } return 42; }`, 42},
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
		// Multi-payload variant arm in a value-position match-EXPRESSION
		// (`return match (e) { Pair(a, b) => a + b }`): lower_iife_match now admits
		// an arm with extra_bindings when every payload is i32 (#3193). The legacy
		// AST emitter mishandles this (segfaults), so these ride the IR-only gate.
		{"match-expr-multi-bind", `enum E { Pair(i32, i32) } function main(): i32 { var e = E.Pair(3, 4); return match (e) { Pair(a, b) => a + b }; }`, 7},
		{"match-expr-multi-2var", `enum E { Pair(i32, i32), Single(i32) } function main(): i32 { var e = E.Single(9); return match (e) { Pair(a, b) => a + b, Single(x) => x }; }`, 9},
		{"match-expr-multi-wildcard", `enum E { Pair(i32, i32) } function main(): i32 { var e = E.Pair(3, 4); return match (e) { Pair(_, b) => b }; }`, 4},
		// CAPTURING inline lambdas in ARRAY-element position (#2994). lift_inline_
		// closures hoists each to a unique `<fd>$cloN` and replaces the lambda with
		// a `__mkclo$<fd>$cloN(caps…)` env-box marker; the array's `is_closurearr`
		// slot routes `fs[i](args)` through the existing env-first closure-call path
		// (push box, args, box[0] funcval, call_indirect arity+1). The legacy AST
		// x86-64 emitter bails on these (a first-class closure box), so they ride the
		// IR-only gate against the absolute value. (A capturing lambda in a struct
		// FIELD is scoped out of this slice — see lift_inline_closures_expr — and
		// stays on AST.) Single capturing lambda; fs[0](7) = 7 + 10 = 17.
		{"clo-cap-arr-elem", `function main(): i32 { var n = 10; var fs = [function(x: i32): i32 { return x + n; }]; return fs[0](7); }`, 17},
		// Capture used twice in the body (x*n + n with n=3, x=5): 15 + 3 = 18.
		{"clo-cap-arr-body-twice", `function main(): i32 { var n = 3; var fs = [function(x: i32): i32 { return x * n + n; }]; return fs[0](5); }`, 18},
		// Two captures in one lambda (a + b with a=4, b=100, x=7): 7 + 4 + 100 = 111.
		{"clo-cap-arr-two-caps", `function main(): i32 { var a = 4; var b = 100; var fs = [function(x: i32): i32 { return x + a + b; }]; return fs[0](7); }`, 111},
		// Multiple distinct capturing lambdas in ONE array — each gets its own
		// `$cloN`. fs[0](5)=5+1=6, fs[1](5)=5*10=50; 6 + 50 = 56.
		{"clo-cap-arr-multi", `function main(): i32 { var a = 1; var b = 10; var fs = [function(x: i32): i32 { return x + a; }, function(x: i32): i32 { return x * b; }]; return fs[0](5) + fs[1](5); }`, 56},
		// A capturing array element bound to a local first, then called (`var g =
		// fs[0]; g(args)`) — the closure-array element binds a closure LOCAL whose
		// call also unpacks the box. g(5) = 5 + n = 5 + 100 = 105.
		{"clo-cap-arr-bind-call", `function main(): i32 { var n = 100; var fs = [function(x: i32): i32 { return x + n; }]; var g = fs[0]; return g(5); }`, 105},
		// Two distinct capturing lambdas summed via a `for f in fs` loop — each
		// gets its own `$cloN` and reads its own env box. With a=1, b=10:
		// fs[0](10) = 10 + a = 11, fs[1](10) = 10 * b = 100; 11 + 100 = 111.
		{"clo-cap-arr-multi-forin", `function main(): i32 { var a = 1; var b = 10; var fs = [function(x: i32): i32 { return x + a; }, function(x: i32): i32 { return x * b; }]; var s = 0; for f in fs { s = s + f(10); } return s; }`, 111},
		// A CAPTURING inline lambda in a STRUCT FIELD value (#3445). The lift pass
		// now wraps EVERY fn-typed field into an env box: a capturing lambda →
		// [funcval($cloN), caps…], a no-capture lambda / bare fn-name → a [$wrapN]
		// env-taking trampoline (slot 0 only); `b.f(args)` dispatches uniformly
		// env-first. Single capturing field: b.f(7) = 7 + n = 7 + 10 = 17.
		{"clo-cap-struct-field", `struct Box { f: (i32) => i32 } function main(): i32 { var n = 10; var b = Box { f: function(x: i32): i32 { return x + n; } }; return b.f(7); }`, 17},
		// Two distinct capturing fields, each its own `$cloN` env box. With n=5,
		// m=3: o.add(10)=10+5=15, o.mul(10)=10*3=30; 15 + 30 = 45.
		{"clo-cap-struct-field-2cap", `struct Ops { add: (i32) => i32, mul: (i32) => i32 } function main(): i32 { var n = 5; var m = 3; var o = Ops { add: function(x: i32): i32 { return x + n; }, mul: function(x: i32): i32 { return x * m; } }; return o.add(10) + o.mul(10); }`, 45},
		// A capturing field PLUS a no-capture lambda field in the same struct: the
		// capturing field is a [$cloN, caps…] box, the no-capture field a [$wrapN]
		// trampoline box; both call env-first. n=100: cap(7)=107, plain(7)=14; 121.
		{"clo-cap-struct-field-mixed-nocap", `struct Ops { cap: (i32) => i32, plain: (i32) => i32 } function main(): i32 { var n = 100; var o = Ops { cap: function(x: i32): i32 { return x + n; }, plain: function(x: i32): i32 { return x * 2; } }; return o.cap(7) + o.plain(7); }`, 121},
		// A capturing field PLUS a bare fn-name field: the fn-name field is wrapped
		// in a [$wrapN] trampoline that calls the named fn. n=1: cap(7)=8,
		// named(7)=trip(7)=21; 8 + 21 = 29.
		{"clo-cap-struct-field-mixed-named", `struct Ops { cap: (i32) => i32, named: (i32) => i32 } function trip(x: i32): i32 { return x * 3; } function main(): i32 { var n = 1; var o = Ops { cap: function(x: i32): i32 { return x + n; }, named: trip }; return o.cap(7) + o.named(7); }`, 29},
		// A capturing field PLUS a separate non-fn (i32) field: the i32 field is
		// stored normally, the fn field as the env box. n=10: f(7)=17, base=100; 117.
		{"clo-cap-struct-field-with-nonfn", `struct Box { f: (i32) => i32, base: i32 } function main(): i32 { var n = 10; var b = Box { f: function(x: i32): i32 { return x + n; }, base: 100 }; return b.f(7) + b.base; }`, 117},
		// CAPTURING inline lambda passed as a CALL ARGUMENT (slice #3445 follow-up).
		// The lift pass wraps the fn-typed argument into an env box [funcval, caps…]
		// and the callee's fn-typed param `f` is a closure local (lower_func), so
		// `f(x)` dispatches env-first. n=10: apply(λx.x+n, 5) = 5 + 10 = 15.
		{"clo-cap-fn-arg", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var n = 10; return apply(function(x: i32): i32 { return x + n; }, 5); }`, 15},
		// Capturing lambda argument with TWO captures: box [funcval, a, b]. a=8,
		// b=100: apply(λx.x+a+b, 0) = 0 + 8 + 100 = 108.
		{"clo-cap-fn-arg-two-caps", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var a = 8; var b = 100; return apply(function(x: i32): i32 { return x + a + b; }, 0); }`, 108},
		// A capturing lambda arg AND a no-capture lambda arg into a TWO-fn-param
		// call: the capturing one is a [funcval, caps…] box, the no-capture one a
		// [$wrapN] trampoline box; both dispatch env-first. n=10:
		// combine(λx.x+n, λx.x*2, 5) = (5+10) + (5*2) = 15 + 10 = 25.
		{"clo-cap-fn-arg-plus-nocap", `function combine(f: (i32) => i32, g: (i32) => i32, x: i32): i32 { return f(x) + g(x); } function main(): i32 { var n = 10; return combine(function(x: i32): i32 { return x + n; }, function(x: i32): i32 { return x * 2; }, 5); }`, 25},
		// Capturing closures collected in an array, then each APPLIED through a
		// fn-arg helper inside a `for f in fs` loop: the loop binds `f` a closure
		// local and `apply(f, 10)` passes it unwrapped (already a box) to apply's
		// fn-typed param. a=1, b=10: apply(fs[0],10)=10+a=11, apply(fs[1],10)=100;
		// 11 + 100 = 111.
		{"clo-cap-fn-arg-forin", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var a = 1; var b = 10; var fs = [function(x: i32): i32 { return x + a; }, function(x: i32): i32 { return x * b; }]; var s = 0; for f in fs { s = s + apply(f, 10); } return s; }`, 111},
		// NO-CAPTURE lambda argument (regression guard for the env-box arg path):
		// wrapped in a [$wrapN] env-ignoring trampoline. apply(λx.x+1, 5) = 6.
		{"clo-nocap-fn-arg", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { return apply(function(x: i32): i32 { return x + 1; }, 5); }`, 6},
		// BARE fn-name argument (regression guard): wrapped in a [$wrapN] trampoline
		// that forwards to the named fn. apply(inc, 5) = inc(5) = 6.
		{"clo-named-fn-arg", `function inc(x: i32): i32 { return x + 1; } function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { return apply(inc, 5); }`, 6},
		// A CLOSURE returned from a call, passed as an argument (regression guard):
		// `mk(10)` is already an env box, so it flows UNWRAPPED to apply's fn-typed
		// param. apply(mk(10), 5) = 5 + 10 = 15.
		{"clo-from-call-fn-arg", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function mk(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; } function main(): i32 { return apply(mk(10), 5); }`, 15},
		// A fn-VALUE LOCAL `var g = dbl` is env-boxed at its binding into a [$wrapN]
		// trampoline box and marked a closure local, so passing it to apply's
		// fn-typed param flows the box unwrapped (env-first). apply(g, 21) = 42.
		{"clo-fnval-local-arg", `function dbl(n: i32): i32 { return n * 2; } function apply(f: (i32) => i32, n: i32): i32 { return f(n); } function main(): i32 { var g = dbl; return apply(g, 21); }`, 42},
		{"method-fn-param-named", `struct Obj { k: i32 } function inc(x: i32): i32 { return x + 1; } function (o: Obj) ap(f: (i32) => i32, x: i32): i32 { return f(x) + o.k; } function main(): i32 { var o = Obj{k:100}; return o.ap(inc, 5); }`, 106},
		// MATCH binds a fn-typed Option payload and CALLS it (slice #3445 match-fn).
		// The constructor side wraps the no-capture lambda payload of `Some(...)`
		// into a [$wrapN] env box; the match arm marks `f` a closure local so `f()`
		// dispatches env-first. Option[() => i32], Some(λ.9) -> 9.
		{"match-opt-fn-call", `function mk(): Option[() => i32] { return Some(function(): i32 { return 9; }); } function main(): i32 { match (mk()) { Some(f) => { return f(); }, None => { return 0; } } }`, 9},
		// MATCH binds a fn-typed Result Ok payload that CAPTURES and calls it. The
		// constructor wraps the capturing lambda into a [funcval, n] box; the Ok arm
		// marks `g` a closure local so `g(5)` dispatches env-first against the box.
		// Result[(i32) => i32, i32], mk2(10) = Ok(λx.x+10); g(5) = 15.
		{"match-result-fn-call-captured", `function mk2(n: i32): Result[(i32) => i32, i32] { return Ok(function(x: i32): i32 { return x + n; }); } function main(): i32 { match (mk2(10)) { Ok(g) => { return g(5); }, Err(e) => { return e; } } }`, 15},
		// MATCH binds a no-capture fn-typed Option payload and calls it, while the
		// matching function ALSO has an outer capture-free local in scope (regression
		// guard that the closure-local mark doesn't leak onto unrelated locals).
		// Option[() => i32], Some(λ.7); f() + base = 7 + 100 = 107.
		{"match-opt-fn-call-outer-local", `function mk(): Option[() => i32] { return Some(function(): i32 { return 7; }); } function main(): i32 { var base = 100; match (mk()) { Some(f) => { return f() + base; }, None => { return 0; } } }`, 107},
		// A closure stored as a MAP VALUE, retrieved and called (slice #3445
		// map-values): `m.set(1, <lambda>)` wraps the lambda into an env box (the
		// lift method-callee arm — `.set` lowers to the builtin op_map_set whose
		// value param is the generic `V`, the wrap trigger), the map stores the box
		// pointer, `m.get(1)` returns it, and the `Some(f) => f()` match-binding
		// (slice #3445 match-fn) dispatches it env-first. Was `route=ir` but
		// SEGFAULTED when compiled (the closure was stored unboxed). The capturing
		// variant carries an env `[funcval, n]`; the no-capture a `[$wrapN]` box.
		// (Converting a USER method's declared fn-param to the env-box ABI is a
		// separate deferred slice — it destabilises the byte-identical fixpoint.)
		{"map-value-closure", `import "core/map"; function main(): i32 { var m: Map[i32, () => i32] = map_new(4); m = m.set(1, function(): i32 { return 42; }); match (m.get(1)) { Some(f) => { return f(); }, None => { return 0; } } }`, 42},
		{"map-value-closure-captured", `import "core/map"; function main(): i32 { var n = 10; var m: Map[i32, () => i32] = map_new(4); m = m.set(1, function(): i32 { return n + 7; }); match (m.get(1)) { Some(f) => { return f(); }, None => { return 0; } } }`, 17},
		// A match-EXPRESSION arm that binds a NON-SCALAR payload (struct / enum /
		// string) and passes it as an ARGUMENT to a free-function call (#3498). The
		// statement-form match already lowered this; the value-position gate now
		// admits an i32-returning free-fn call whose args are borrow reads of the
		// payload, so `V(p) => g(p)` rides the i32 result temp. The recursive-list
		// `sum` is the headline shape (`Cons(h, t) => h + sum(t)` passes the enum
		// payload `t` to the recursive call). Native/AST-correct; pinned to the IR
		// value (the gate decides IR-vs-AST, so the value confirms the IR path).
		{"match-expr-struct-payload-call", `struct S { v: i32 } enum E { A(S), N } function g(s: S): i32 { return s.v; } function f(e: E): i32 { return match (e) { A(s) => g(s), N => 0 }; } function main(): i32 { return f(A(S { v: 5 })); }`, 5},
		{"match-expr-string-payload-call", `enum E { A(string), N } function g(s: string): i32 { return s.len(); } function f(e: E): i32 { return match (e) { A(s) => g(s), N => 0 }; } function main(): i32 { return f(A("hi")); }`, 2},
		{"match-expr-enum-payload-call", `enum L { C(i32, L), N } function hd(l: L): i32 { return match (l) { C(h, t) => h, N => 0 }; } function snd(l: L): i32 { return match (l) { C(h, t) => hd(t), N => 0 }; } function main(): i32 { return snd(C(7, C(9, N))); }`, 9},
		{"match-expr-recursive-sum", `enum L { C(i32, L), N } function sum(l: L): i32 { return match (l) { C(h, t) => h + sum(t), N => 0 }; } function main(): i32 { return sum(C(1, C(2, C(3, N)))); }`, 6},
		{"match-expr-payload-call-mixed-args", `struct S { v: i32 } enum E { A(S), N } function g(s: S, k: i32): i32 { return s.v + k; } function f(e: E): i32 { return match (e) { A(s) => g(s, 3), N => 0 }; } function main(): i32 { return f(A(S { v: 5 })); }`, 8},
		// The METHOD sibling (#3498 follow-up): a match-EXPRESSION arm calling a
		// METHOD on ANOTHER receiver (`w.take(p)`, not the payload) with the bound
		// payload as a borrow argument, returning i32. The value-position gate
		// (iife_call_is_i32_borrow) infers the method's receiver type and proves its
		// return i32 by exclusion — the same admission the free-function case uses.
		{"match-expr-method-other-recv-payload-arg", `struct S { v: i32 } struct W { } enum E { A(S), N } function (w: W) take(s: S): i32 { return s.v; } function f(e: E): i32 { var w = W {}; return match (e) { A(s) => w.take(s), N => 0 }; } function main(): i32 { return f(A(S { v: 5 })); }`, 5},
		{"match-expr-method-other-recv-scalar-arg", `struct W { } enum E { A(i32), N } function (w: W) dbl(x: i32): i32 { return x * 2; } function f(e: E): i32 { var w = W {}; return match (e) { A(x) => w.dbl(x), N => 0 }; } function main(): i32 { return f(A(7)); }`, 14},
		// A borrow-DERIVED value in a match-EXPRESSION arm (#3498 follow-up): a
		// call that BORROWS the payload and returns a fresh value is itself
		// borrow-safe, so `.len()` over a payload string-method result
		// (`s.trim().len()`) and a NESTED-call argument (`sum(id(t))`, `id` returns
		// the recursive enum) ride the i32 result temp instead of bailing.
		{"match-expr-payload-strmethod-len", `function f(o: Option[string]): i32 { return match (o) { Some(s) => s.trim().len(), None => 0 }; } function main(): i32 { return f(Some("hi ")); }`, 2},
		{"match-expr-nested-call-arg", `enum L { C(i32, L), N } function id(l: L): L { return l; } function sum(l: L): i32 { return match (l) { C(h, t) => h + sum(id(t)), N => 0 }; } function main(): i32 { return sum(C(1, C(2, C(3, N)))); }`, 6},
	}
	// Erased-generic 64-bit shapes (#4451 goal-1 widening): a 64-bit (i64/u64)
	// argument at a bare-typevar param lowers via lower_i64 (fn_param_sigs flag
	// '5'), and an erased return mirroring that argument reads back full-width
	// (the str_ret_fns argref resolution in infer_expr_width / lower_i64).
	// Previously these bailed the module to the AST path; the exit codes pin
	// the 8-byte round-trip (a truncation returns the 38 arm).
	irOnly = append(irOnly, []struct {
		name string
		src  string
		want int
	}{
		{"erased-i64-arg-only", `function first[T](x: T, n: i32): i32 { return n; } function main(): i32 { var r: i32 = first[i64](4200000000 as i64, 7); return r; }`, 7},
		{"erased-i64-roundtrip", `function ident[T](x: T): T { return x; } function main(): i32 { var big: i64 = ident[i64](4200000000 as i64); if (big == 4200000000 as i64) { return 42; } return 38; }`, 42},
		{"erased-i64-tuple-elem", `function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { var t: (i32, i64) = pair[i32, i64](38, 4200000000 as i64); var big: i64 = t.1; if (big == 4200000000 as i64) { return 42; } return 38; }`, 42},
		{"erased-i64-both-elems", `function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { var t: (i64, i64) = pair[i64, i64](5000000000 as i64, 6000000000 as i64); if (t.0 + t.1 == 11000000000 as i64) { return 42; } return 38; }`, 42},
		{"erased-u64-roundtrip", `function ident[T](x: T): T { return x; } function main(): i32 { var u: u64 = ident[u64](18000000000000000000 as u64); if (u == 18000000000000000000 as u64) { return 42; } return 38; }`, 42},
	}...)
	for _, tc := range irOnly {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRun(t, tc.src, true); got != tc.want {
				t.Errorf("IR path %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
