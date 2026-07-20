package e2eselfhost

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSALiftIRLower is the production-shaped end-to-end gate for the
// stack-IR -> SSA lift: it lowers a Fern SOURCE the way the real compiler does
// (AST -> ir.Op[] via irlower.lower_func_for, the asm_ir backend's input), then
// LIFTS that real IR to SSA, optimises, and emits via ssa_x86 / ssa_arm64. The
// emitted binary's exit code is checked against the native interpreter's result
// for the same source (the differential oracle). Where TestSelfHostSSALiftEmit
// drives hand-built ir.Op[], this drives ACTUAL irlower output — so it proves
// the lift consumes the real production IR, not synthetic ops, all the way to
// running native code on both backends.
//
// Coverage is the lift's current subset: integer control flow (straight-line,
// loops, if-merge, break, cross-function calls, recursion), string literals
// + length (const_str / str_len, which lower RC-free), i32 arrays
// (arr_make / arr_get / arr_set / arr_len), scalar-field structs
// (struct_make / struct_get, incl. nested), tuples (tuple_make /
// tuple_get, incl. nested), f64 scalars (const_f64 + fadd / fmul /
// fgt / … + fneg), i32<->f64 casts (i32_to_f64 / f64_to_i32), string
// concat / equality (str_concat / str_eq), the string builder
// (strbuf_reset / _append / _take), the process / output ops
// (print_str / eprint_str / exit), string indexing (str_index), and
// Option / Result (opt_make / opt_none / opt_tag / opt_payload), args()
// (the argv string[]), closures (const_func -> funcaddr + call_indirect,
// the lambda hoisted via lift_lambdas), user enum matching
// (struct_make tag + variant_is over the module's variant structs), and
// array append / slice (arr_push / arr_push_owned / arr_slice / str_slice
// via the injected __ssa_arr_push / __ssa_arr_slice helpers),
// string_from_bytes (str_from_bytes -> __ssa_arr_slice full-array copy), and
// i64 (const_i64 + width-aware i64 arithmetic / comparison + int_extend /
// int_wrap casts), with irlower's RC-helper calls stripped. Out-of-subset
// programs make the driver exit non-zero; only in-subset programs are
// listed here.
func TestSelfHostSSALiftIRLower(t *testing.T) {
	// x86 tooling is required test-wide (the driver is an x86-64 binary);
	// arm64 tooling only by the arm64 subtests, so it is acquired inside them
	// — a top-level arm64Tooling skip silently took the x86_64 legs with it
	// on CI's x86 shards, which carry no aarch64 cross toolchain (#5452).
	x86gcc, x86runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "parser.fern", "astwalk.fern",
		"ir.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern",
		"irlower.fern", "ssa_lift.fern", "ssa_lift_irlower_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_lift_irlower_run.fern", "ssa_lift_irlower_run")

	// emit feeds the source to the driver on stdin and returns the emitted asm.
	emit := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(x86runner[1:], bin), args...)...)
		}
		cmd.Stdin = strings.NewReader(src)
		out, err := cmd.Output()
		if err != nil {
			// cmd.Output captures stderr into ExitError.Stderr — surface it plus
			// the driver binary's hash, so a lift/irlower bail reports its reason
			// and the exact driver build is comparable across environments
			// (#5452), not a bare exit 4.
			var stderr []byte
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = ee.Stderr
			}
			hash := "?"
			if b, rerr := os.ReadFile(bin); rerr == nil {
				hash = fmt.Sprintf("%x", sha256.Sum256(b))
			}
			t.Fatalf("emit driver failed for %q: %v\nstderr: %s\ndriver sha256: %s", src, err, stderr, hash)
		}
		return out
	}
	run := func(t *testing.T, asm []byte, gcc string, pie bool, mk func(string, ...string) *exec.Cmd, tag string) int {
		t.Helper()
		asmPath := filepath.Join(dir, "il_"+tag+".s")
		binPath := filepath.Join(dir, "il_"+tag)
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		gccArgs := []string{"-static", "-nostdlib"}
		if pie {
			gccArgs = append(gccArgs, "-no-pie")
		}
		gccArgs = append(gccArgs, asmPath, "-o", binPath)
		if out, err := exec.Command(gcc, gccArgs...).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed: %v\n%s\n--- asm ---\n%s", err, out, asm)
		}
		cmd := mk(binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally")
		}
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		{"arith", `function main(): i32 { return (3 + 4) * 2 - 1; }`},
		{"loopsum", `function main(): i32 { var i = 1; var acc = 0; while (i <= 5) { acc = acc + i; i = i + 1; } return acc; }`},
		{"branch", `function main(): i32 { var x = 0; if (7 > 3) { x = 42; } return x; }`},
		{"breakloop", `function main(): i32 { var i = 0; while (i < 100) { if (i == 42) { break; } i = i + 1; } return i; }`},
		{"callsum", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }`},
		{"factrec", `function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }`},
		{"strlen", `function main(): i32 { var s: string = "hello"; return s.len(); }`},
		{"strlen2", `function main(): i32 { return ("abcd").len() + ("xy").len(); }`},
		{"strpick", `function main(): i32 { var s: string = "hi"; var t: string = "world"; if (s.len() < t.len()) { return t.len(); } return s.len(); }`},
		// Harder shapes over real irlower output: nested loops, mutual recursion
		// across two functions, bitwise / shift operators, nested if, and an
		// early return out of a loop.
		{"nestloop", `function main(): i32 { var t = 0; var i = 0; while (i < 4) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }`},
		{"mutualrec", `function isodd(n: i32): boolean { if (n == 0) { return false; } return iseven(n - 1); } function iseven(n: i32): boolean { if (n == 0) { return true; } return isodd(n - 1); } function main(): i32 { if (iseven(10)) { return 1; } return 0; }`},
		{"bitwise", `function main(): i32 { var a = 12; var b = 10; return (a & b) + (a | b) + (a ^ b); }`},
		{"shift", `function main(): i32 { return (1 << 5) + (64 >> 2); }`},
		{"nestif", `function main(): i32 { var x = 7; if (x > 5) { if (x > 10) { return 1; } return 2; } return 3; }`},
		{"earlyret", `function main(): i32 { var i = 0; while (i < 100) { if (i * i > 50) { return i; } i = i + 1; } return 99; }`},
		// i32 arrays over real irlower output (slice 2): literal + index, length,
		// element update via `.with`, a loop summing elements, and a borrowed
		// array param across a call. These lower with arr_make / arr_get /
		// arr_set / arr_len plus the RC-helper calls the lift strips.
		{"arrlit", `function main(): i32 { var a = [10, 20, 30]; return a[1]; }`},
		{"arrlen", `function main(): i32 { var a = [1, 2, 3, 4]; return a.len(); }`},
		{"arrwith", `function main(): i32 { var a = [1, 2, 3]; a = a.with(1, 99); return a[0] + a[1] + a[2]; }`},
		{"arrsum", `function main(): i32 { var a = [5, 10, 15]; var s = 0; var i = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`},
		{"arrpass", `function sum3(a: i32[]): i32 { return a[0] + a[1] + a[2]; } function main(): i32 { var xs = [7, 8, 9]; return sum3(xs); }`},
		// Scalar-field structs over real irlower output (slice 3): literal +
		// field read, a borrowed struct param across a call, a boolean field
		// driving a branch, a spread functional-update, and a nested struct
		// field (a pointer field, stored/read i32-wide in the low SSA heap).
		{"structlit", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 10, y: 32 }; return p.x + p.y; }`},
		{"structfn", `struct P { x: i32, y: i32 } function sx(p: P): i32 { return p.x; } function main(): i32 { var p = P { x: 5, y: 9 }; return sx(p) + p.y; }`},
		{"boolfield", `struct F { a: boolean, n: i32 } function main(): i32 { var f = F { a: true, n: 7 }; if (f.a) { return f.n; } return 0; }`},
		{"structupd", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p = P { ...p, x: 40 }; return p.x + p.y; }`},
		{"structnest", `struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { var o = Outer { inner: Inner { v: 30 }, k: 12 }; return o.inner.v + o.k; }`},
		// Tuples over real irlower output (slice 4): a pair + element reads, a
		// tuple returned from a function, a nested tuple (a pointer element),
		// and a boolean-element tuple driving a branch.
		{"tuplepair", `function main(): i32 { var t = (10, 32); return t.0 + t.1; }`},
		{"tuplefn", `function mk(): (i32, i32) { return (5, 9); } function main(): i32 { var t = mk(); return t.0 + t.1; }`},
		{"tuplenest", `function main(): i32 { var t = (1, (2, 3)); return t.0 + t.1.0 + t.1.1; }`},
		{"tuplebool", `function main(): i32 { var t = (true, 7); if (t.0) { return t.1; } return 0; }`},
		// f64 scalars over real irlower output (slice 5): arithmetic +
		// comparison, a bare compare, equality, and unary negation. The
		// function returns via an i32 comparison result, so no float cast /
		// float-returning call is needed (those are later slices).
		{"f64arith", `function main(): i32 { var x: f64 = 1.5; var y: f64 = 2.0; var z = x * y + 0.5; if (z > 3.0) { return 7; } return 0; }`},
		{"f64cmp", `function main(): i32 { var a: f64 = 3.14; var b: f64 = 2.71; if (a > b) { return 1; } return 0; }`},
		{"f64eq", `function main(): i32 { var x: f64 = 2.0; var y: f64 = 2.0; if (x == y) { return 4; } return 0; }`},
		{"f64neg", `function main(): i32 { var x: f64 = 5.0; var y = -x; if (y < 0.0) { return 9; } return 0; }`},
		// i32<->f64 casts over real irlower output (slice 6): float->int truncate,
		// int->float, and a round-trip through both.
		{"f2i", `function main(): i32 { var x: f64 = 3.7; return x as i32; }`},
		{"i2f", `function main(): i32 { var n: i32 = 5; var x: f64 = n as f64; if (x > 4.5) { return 8; } return 0; }`},
		{"castroundtrip", `function main(): i32 { var n: i32 = 10; var x: f64 = (n as f64) * 1.5; return x as i32; }`},
		// String ops over real irlower output (slice 7): concatenation (fresh
		// heap string) + length, content equality, inequality (str_eq + not),
		// and a concat feeding an equality.
		{"strconcat", `function main(): i32 { var a: string = "hel"; var b: string = "lo"; var c = a + b; return c.len(); }`},
		{"streq", `function main(): i32 { var a: string = "abc"; var b: string = "abc"; if (a == b) { return 1; } return 0; }`},
		{"strne", `function main(): i32 { var a: string = "abc"; var b: string = "xyz"; if (a != b) { return 2; } return 0; }`},
		{"concateq", `function main(): i32 { var a: string = "foo"; var b = a + "bar"; if (b == "foobar") { return 7; } return 0; }`},
		// String builder over real irlower output (slice 8): reset + appends +
		// take, checked by the built string's length and its content.
		{"strbuf", `function main(): i32 { strbuf_reset(); strbuf_append("ab"); strbuf_append("cde"); var s = strbuf_take(); return s.len(); }`},
		{"strbufeq", `function main(): i32 { strbuf_reset(); strbuf_append("x"); strbuf_append("yz"); var s = strbuf_take(); if (s == "xyz") { return 7; } return 0; }`},
		// Process / output ops over real irlower output (slice 9): exit (fully
		// exit-code-observable), a conditional exit, and print / write / eprint
		// (side effects; the following return's exit code checks stack balance).
		{"exit", `function main(): i32 { exit(42); return 0; }`},
		{"exitcond", `function main(): i32 { var x = 5; if (x > 3) { exit(9); } return 1; }`},
		{"print", `function main(): i32 { print("hello"); return 5; }`},
		{"write", `function main(): i32 { write("hi"); return 3; }`},
		{"eprint", `function main(): i32 { eprint("err"); return 7; }`},
		// String indexing over real irlower output (slice 10): a single byte
		// read, a loop summing bytes, and indexing a string literal.
		{"strindex", `function main(): i32 { var s: string = "ABC"; return s[1]; }`},
		{"strsum", `function main(): i32 { var s: string = "AB"; var sum = 0; var i = 0; while (i < s.len()) { sum = sum + s[i]; i = i + 1; } return sum; }`},
		{"strlit0", `function main(): i32 { return ("XY")[0]; }`},
		// Option / Result over real irlower output (slice 11): a Some payload
		// bind, a None arm, a function returning Option matched at the call site,
		// and a Result (Ok/Err) match.
		{"optsome", `function main(): i32 { var o: Option[i32] = Some(42); match (o) { Some(v) => { return v; }, None => { return 0; } } }`},
		{"optnone", `function main(): i32 { var o: Option[i32] = None; match (o) { Some(v) => { return v; }, None => { return 7; } } }`},
		{"optchain", `function f(n: i32): Option[i32] { if (n > 0) { return Some(n * 2); } return None; } function main(): i32 { match (f(5)) { Some(v) => { return v; }, None => { return 0; } } }`},
		{"result", `function g(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n + 1); } return Err(9); } function main(): i32 { match (g(10)) { Ok(v) => { return v; }, Err(e) => { return e; } } }`},
		// args() over real irlower output (slice 12): the argv string[]. Guarded
		// as `>= 1` so it holds regardless of how the harness invokes the binary
		// (the differential diffs the emitted binary vs the interpreter).
		{"argslen", `function main(): i32 { if (args().len() >= 1) { return 5; } return 0; }`},
		// Closures over real irlower output (slice 13): a capturing lambda, a
		// non-capturing function value passed + called indirectly, and a lambda
		// capturing two locals. The lifted `<fn>$clo` lambda is emitted as its
		// own function; const_func -> funcaddr, the box is an arr_make, and the
		// captures read via arr_get.
		{"clcapture", `function main(): i32 { var x = 10; var f = (y: i32) => x + y; return f(5); }`},
		{"clnoncap", `function inc(x: i32): i32 { return x + 1; } function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(inc, 41); }`},
		{"clmulti", `function main(): i32 { var a = 3; var b = 4; var f = (y: i32) => a + b + y; return f(5); }`},
		// User enum matching over real irlower output (slice 14): a payload-bearing
		// two-variant enum, a bare (payloadless) three-variant enum, an enum
		// threaded through a variable before the match, and a variant carrying a
		// negated payload. Enum construction lowers to a tag-prefixed struct box
		// (slot 0 = the variant's struct id); `match` lowers to `variant_is`, which
		// the lift renders as load_elem(box, 0) == that same id — so an enum
		// discriminant round-trips through the low SSA heap. struct_id_of over the
		// module's (parser-desugared) variant structs is the single mapping both
		// sides share, so the tag written and the tag tested agree by construction.
		{"enumpair", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } function main(): i32 { return area(Circle(2)) + area(Square(3)); }`},
		{"enumbare", `enum Color { Red, Green, Blue } function code(c: Color): i32 { match (c) { Red => { return 1; }, Green => { return 2; }, Blue => { return 3; } } } function main(): i32 { return code(Red) * 100 + code(Green) * 10 + code(Blue); }`},
		{"enumvar", `enum Expr { Lit(i32), Neg(i32) } function eval(e: Expr): i32 { match (e) { Lit(v) => { return v; }, Neg(v) => { return 0 - v; } } } function main(): i32 { var e: Expr = Lit(7); var a = eval(e); return a + eval(Neg(5)); }`},
		{"enumthree", `enum Dir { N, E, S, W } function turn(d: Dir): i32 { match (d) { N => { return 10; }, E => { return 20; }, S => { return 30; }, W => { return 40; } } } function main(): i32 { return turn(S) + turn(W); }`},
		// Array append + slice over real irlower output (slice 15): `.append`
		// (arr_push), the sole-owner self-append `a = a.append(v)` in a loop
		// (arr_push_owned), an array slice `a[lo:hi]` (arr_slice), and a string
		// slice `s[lo:hi]` (str_slice). Each lowers to a call to a pure-Fern runtime
		// helper — __ssa_arr_push / __ssa_arr_slice, the same helpers build_func's
		// `.append` / `a[lo:hi]` lowerings call — which the driver injects
		// (build_func over ssa.ssa_helpers_src) when the lifted program calls it.
		// arr_push_owned collapses to the copying push on the leak-only bump heap
		// (no in-place realloc), and str_slice copies a fresh substring rather than
		// the native zero-copy immortal view. These were the largest lift-bail
		// buckets in the coverage scan; the scan of parser.fern rises 58% -> 99%.
		{"arrpush", `function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.append(4); return a[3] + a.len(); }`},
		{"arrpushloop", `function main(): i32 { var a: i32[] = []; var i = 0; while (i < 5) { a = a.append(i * 2); i = i + 1; } return a.len() * 10 + a[4]; }`},
		{"arrslice", `function main(): i32 { var a: i32[] = [10, 20, 30, 40, 50]; var b = a[1:4]; return b.len() * 100 + b[0] + b[2]; }`},
		{"strslice", `function main(): i32 { var s: string = "hello world"; var t = s[0:5]; return t.len() + s[6:11].len(); }`},
		// string_from_bytes over real irlower output (slice 16): a byte array ->
		// string via str_from_bytes, which lifts to __ssa_arr_slice(bs, 0,
		// bs.len()) — a full-array copy that IS the string (shared layout),
		// reusing the slice-15 helper. Checked by length and content equality.
		{"strfrombytes", `function main(): i32 { var bs: u8[] = [72, 73]; var s: string = string_from_bytes(bs); return s.len(); }`},
		{"strfrombyteseq", `function main(): i32 { var bs: u8[] = [65, 66, 67]; var s: string = string_from_bytes(bs); if (s == "ABC") { return 7; } return 0; }`},
		// i64 over real irlower output (slice 17): a const_i64 comparison, i64
		// addition and multiplication whose results OVERFLOW 32 bits (proving the
		// arithmetic is genuinely 64-bit, not truncated), an i32->i64 widen
		// (int_extend / sext) feeding an overflowing multiply, and an i64->i32
		// truncate (int_wrap). Each is diffed against the interpreter; the
		// comparison / truncation makes the 64-bit value observable in the i32
		// exit code.
		{"i64cmp", `function main(): i32 { if (3000000000 > 2000000000) { return 7; } return 0; }`},
		{"i64add", `function main(): i32 { var x: i64 = 3000000000; var y: i64 = x + x; if (y > 5000000000) { return 9; } return 0; }`},
		{"i64mul", `function main(): i32 { var a: i64 = 100000; var b: i64 = 100000; var p: i64 = a * b; if (p == 10000000000) { return 4; } return 0; }`},
		{"i64extend", `function main(): i32 { var n: i32 = 2000000; var x: i64 = n as i64; var y: i64 = x * x; if (y > 3000000000000) { return 6; } return 0; }`},
		{"i64wrap", `function main(): i32 { var x: i64 = 4294967338; var n: i32 = x as i32; return n; }`},
	}
	for _, tc := range cases {
		tc := tc
		ref := runInterpExit(t, tc.src) // independent oracle: the interpreter
		t.Run("x86_64/"+tc.name, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			mk := func(b string, a ...string) *exec.Cmd { return exec.Command(b, a...) }
			if got := run(t, emit(t, tc.src), x86gcc, true, mk, "x86-"+tc.name); got != ref {
				t.Errorf("x86-64 irlower->lift %s = %d, interp = %d", tc.name, got, ref)
			}
		})
		t.Run("arm64/"+tc.name, func(t *testing.T) {
			armgcc, qemu := arm64Tooling(t)
			mk := func(b string, a ...string) *exec.Cmd { return runArm64Bin(qemu, b, a...) }
			if got := run(t, emit(t, tc.src, "-target", "arm64"), armgcc, false, mk, "arm-"+tc.name); got != ref {
				t.Errorf("arm64 irlower->lift %s = %d, interp = %d", tc.name, got, ref)
			}
		})
	}
}
