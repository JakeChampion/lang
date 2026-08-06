// E2E tests for the experimental SSA-direct arm64 backend
// (`-target arm64-ssa`). Builds the fern CLI from this checkout,
// compiles small Fern programs with the new target, and runs the
// resulting static AArch64 ELF, asserting the process exit code
// (main's return value, low byte).
//
// The binary runs NATIVELY on an arm64 Linux host and under
// qemu-aarch64 elsewhere; only a host with neither SKIPs. Requiring
// qemu unconditionally is what kept this file out of CI entirely:
// the test-e2e-arm64 lane runs `^TestArm64` on ubuntu-24.04-arm,
// which needs no emulator and therefore has none installed, and no
// other lane matches the prefix (test-e2e-other skips it). So these
// cases asserted unmet behaviour unnoticed — they caught neither the
// missing float bit-reinterprets (#5725) nor the i64[] element
// corruption (#5729), both of which they should have failed on.
package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestArm64SSACliRoundtrip drives the whole parse → check → ir →
// ssa.LiftFromIR → arm64ssa.EmitAsmModule → linkNative pipeline through
// the CLI and runs each binary, exercising the SSA register allocator's
// real output on cross-function calls, recursion, control flow, memory,
// and strings.
func TestArm64SSACliRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	// "" = run it directly (already on arm64); otherwise the qemu path.
	qemu := arm64QemuOrEmpty(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "call",
			src: `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(40, 2); }`,
			want: 42,
		},
		{
			name: "loop_and_recursion",
			src: `function fib(n: i32): i32 {
  if (n < 2) { return n; }
  return fib(n - 1) + fib(n - 2);
}
function main(): i32 {
  var i: i32 = 0;
  var s: i32 = 0;
  while (i < 10) { s = s + i; i = i + 1; }
  return s + fib(8);
}`,
			want: 66, // 45 + fib(8)=21
		},
		{
			name: "div_rem_shift",
			src: `function main(): i32 {
  var a: i32 = 47;
  var b: i32 = 5;
  return (a / b) * 10 + (a % b) + (1 << 3);
}`,
			want: 100, // 9*10 + 2 + 8
		},
		{
			name: "string_len",
			src: `function main(): i32 {
  var s: string = "Hello";
  return s.len();
}`,
			want: 5,
		},
		{
			// f64 arithmetic through a cross-function call — exercises the FP
			// sequences and the call-result width propagation (an f64 return must
			// not be masked back to i32).
			name: "float_call",
			src: `function scale(x: f64): f64 { return x * 2.0; }
function main(): i32 { return scale(3.5) as i32; }`,
			want: 7,
		},
		{
			// The f32 sibling of float_call, and NOT redundant with it: a float
			// lives in a general register as its f64 bit pattern, so an f32
			// return whose width is 32 used to be sign-extended from bit 31 at
			// the call boundary (ssa.AnnotateCallWidths only looked at
			// ReturnWidth, never ReturnFloat). That kept the low mantissa half
			// and dropped the sign + exponent, so every f32 crossing a call
			// arrived as a denormal that reads back as 0 — this returned 0
			// instead of 96. Covers a param, a return, and arithmetic on both.
			name: "float32_call",
			src: `function addf(a: f32, b: f32): f32 { return a + b; }
function main(): i32 { return addf(90.0f32, 6.55f32) as i32; }`,
			want: 96,
		},
		{
			// `?` inside an array literal. The try desugar carries an early
			// `return`, so the container construction splits across blocks —
			// and the dead arm used to abandon a value on the lift's operand
			// stack, shadowing the element address pushed before the split.
			// The store then took the abandoned value as its address, which
			// lifted to a use its def does not dominate and SIGSEGV'd here
			// while every other backend ran the program (#5903).
			//
			// `return v` is load-bearing in all three of these. A version
			// returning something DERIVED from the container
			// (`return Some(arr[0])`) compiles and runs fine even with the fix
			// reverted — the later reads consume the abandoned value harmlessly
			// — so it pins nothing. Which means the array's contents can't also
			// be asserted through the exit code here: observing them is exactly
			// what stops the bug reproducing. The assertion is that the shape
			// compiles and runs at all; unfixed it is a hard `ssa.Verify` error
			// and the harness fails on the compile.
			name: "try_in_array_literal",
			src: `function f(): Option[i32] {
    var v: Option[i32] = Some(184i32);
    var arr: i32[] = [(v?)];
    return v;
}
function main(): i32 { return match (f()) { Some(x) => x - 142, None => 1 }; }`,
			want: 42,
		},
		{
			// The tuple-literal form of the same shape.
			name: "try_in_tuple_literal",
			src: `function f(): Option[i32] {
    var v: Option[i32] = Some(184i32);
    var t: (i32, i32) = ((v?), 7i32);
    return v;
}
function main(): i32 { return match (f()) { Some(x) => x - 142, None => 1 }; }`,
			want: 42,
		},
		{
			// Both elements read afterwards, so the array really is built and
			// indexed rather than dead-coded away — and this still reproduces,
			// unlike the variant that RETURNS the sum.
			name: "try_in_array_literal_read_back",
			src: `function f(): Option[i32] {
    var v: Option[i32] = Some(184i32);
    var arr: i32[] = [(v?), 7i32];
    var s: i32 = arr[0] + arr[1];
    return v;
}
function main(): i32 { return match (f()) { Some(x) => x - 142, None => 1 }; }`,
			want: 42,
		},
		{
			// The None path: the try must still take its early exit. Does not
			// reproduce the bug (nothing is abandoned when the arm is the one
			// that runs), but pins that the fix didn't break the exit itself.
			name: "try_in_array_literal_none",
			src: `function f(): Option[i32] {
    var v: Option[i32] = None;
    var arr: i32[] = [(v?)];
    return v;
}
function main(): i32 { return match (f()) { Some(x) => 99, None => 7 }; }`,
			want: 7,
		},
		{
			// float32_call's INDIRECT sibling. A nested function is never
			// called by name — the enclosing function builds a closure cell and
			// calls through it — so the callee-name lookup that widens a direct
			// call's result cannot see it, and the f32 came back masked to 32
			// bits: the low mantissa half with the sign and exponent gone, a
			// denormal that reads back as 0. The width now comes from the
			// signature the IR call op carries.
			name: "float32_nested_fn_return",
			src: `function outer(): f32 {
    function inner(): f32 { return 42.5f32; }
    return inner();
}
function main(): i32 { return outer() as i32; }`,
			want: 42,
		},
		{
			// The same indirect call reached the other way: a function VALUE in
			// a parameter, where there is no closure in scope to trace back to
			// at all. Reading the call site's signature covers both shapes,
			// which is why it beats resolving the closure target.
			name: "float32_fn_value_param",
			src: `function apply(f: () => f32): f32 { return f(); }
function main(): i32 {
    function g(): f32 { return 42.5f32; }
    return apply(g) as i32;
}`,
			want: 42,
		},
		{
			// The i64 half of the same annotation: a wide integer through an
			// indirect call is truncated by the identical mask. 2^32 + 42 is
			// chosen so a truncation to the low word survives as 42 while the
			// high bit is what actually differs — the `/ 1000i64` keeps the
			// result inside a byte only when the high half made it through.
			name: "i64_fn_value_param",
			src: `function apply(f: () => i64): i64 { return f(); }
function main(): i32 {
    function g(): i64 { return 4294967296i64 + 42i64; }
    return (apply(g) / 1000000000i64) as i32;
}`,
			want: 4,
		},
		{
			// f32 arithmetic must round to f32 after EVERY operation, including
			// when the whole chain is constant and folds at compile time. The
			// SSA path lost the f32 width at the lift, so both constant folders
			// (SCCP and Fold) evaluated the chain at f64 precision and produced
			// -360517687 — a value f32 cannot represent, its ulp at that
			// magnitude being 32 — where the interpreter and both native
			// backends produce -360517664. Masked to a byte the two differ as
			// 201 vs 224, so the exit code discriminates.
			name: "float32_precision",
			src: `function main(): i32 {
  var a: f32 = 73.46f32;
  var b: f32 = 12.68f32;
  var c: f32 = 34.42f32;
  var d: f32 = 99.49f32;
  var e: f32 = 27.06f32;
  var g: f32 = 27.89f32;
  var h: f32 = 91.90f32;
  return ((((a - b) * c) * (d * (e * (g - h)))) as i32) & 255;
}`,
			want: 224,
		},
		{
			// Float-to-int conversion saturates (docs/FLOAT-SEMANTICS.md): NaN
			// gives 0, out of range clamps to the destination's min/max. AArch64
			// fcvtz{s,u} saturate to the DESTINATION REGISTER's width, so
			// converting into `x` and narrowing with maskFix wrapped instead of
			// clamping — `(91.23f32 * 1e9) as i32` gave 1035689984 where every
			// other backend gives INT32_MAX. Sums three cases into one byte:
			// overflow (+1), NaN (+2), underflow (+4) = 7.
			name: "float_to_int_saturates",
			src: `function scale(x: f32): f32 { return x * 1000000000.0f32; }
function main(): i32 {
  var a: f32 = 91.23f32;
  var z: f32 = 0.0f32;
  var n: i32 = 0;
  if ((scale(a) as i32) == 2147483647) { n = n + 1; }
  if (((z / z) as i32) == 0) { n = n + 2; }
  if (((z - scale(a)) as i32) == ((0 - 2147483647) - 1)) { n = n + 4; }
  return n;
}`,
			want: 7,
		},
		{
			// Ordered comparisons against NaN are all false; only `!=` is true.
			// The renderer emitted the UNSIGNED AArch64 condition codes, which
			// agree with the IEEE ones on ordered operands but read true on
			// unordered (fcmp marks NaN with C=1, so `hi`/`hs` fired). Sums the
			// six predicates' 0/1 so a single exit code pins all of them: only
			// `!=` contributes, hence 1.
			name: "float_nan_compare",
			src: `function main(): i32 {
  var z: f32 = 0.0f32;
  var nan: f32 = z / z;
  var n: i32 = 0;
  if (nan == 1.0f32) { n = n + 1; }
  if (nan != 1.0f32) { n = n + 1; }
  if (nan < 1.0f32) { n = n + 1; }
  if (nan <= 1.0f32) { n = n + 1; }
  if (nan > 1.0f32) { n = n + 1; }
  if (nan >= 1.0f32) { n = n + 1; }
  return n;
}`,
			want: 1,
		},
		{
			// A closure capturing [4-byte scalar, pointer] — the shape that used
			// to SIGSEGV (#5767). The IR emits a `__closure_drop_<name>` thunk
			// that reads its pointer captures at env-block offsets, but this
			// path skipped ir.ElideClosurePair (which arm64, x86-64 and wasmbin
			// all run), so OpMakeClosure survived and the thunk was handed the
			// closure cell instead of the env. With the
			// pointer capture at env offset 4 the thunk loaded 8 bytes across
			// both cell fields — 0x410028 became 0x0041002800000000 — and
			// rc.dec'd that. `f` is never called: building the closure is
			// enough. Other capture orders did not crash only because their
			// offset happened to land on the env pointer or on fn_idx (below
			// the heap, so the rc guard swallowed it) — decrementing the wrong
			// object rather than faulting.
			name: "closure_scalar_then_pointer_capture",
			src: `function main(): i32 {
  var v1: i32 = 1;
  var o: Option[i32] = Some(7);
  function f(): i32 { return v1 + (match (o) { Some(e) => 1, None => 2 }); }
  return 32;
}`,
			want: 32,
		},
		{
			// Option Some path via the pair-return convention (CallPair + TRetPair
			// + match): half(84) = Some(42).
			name: "option_some",
			src: `function half(n: i32): Option[i32] {
  if (n % 2 == 0) { return Some(n / 2); }
  return None;
}
function main(): i32 { return match (half(84)) { Some(v) => v, None => 0 }; }`,
			want: 42,
		},
		{
			// Option None path: half(7) = None -> 99.
			name: "option_none",
			src: `function half(n: i32): Option[i32] {
  if (n % 2 == 0) { return Some(n / 2); }
  return None;
}
function main(): i32 { return match (half(7)) { Some(v) => v, None => 99 }; }`,
			want: 99,
		},
		{
			// A capturing closure invoked directly (MakeClosure + CallIndirect):
			// addBase captures base=100; addBase(23) = 123.
			name: "closure_capture",
			src: `function main(): i32 {
  var base: i32 = 100;
  var addBase = (x: i32) => x + base;
  return addBase(23);
}`,
			want: 123,
		},
		{
			// Higher-order: a multi-capture closure passed to another function that
			// dispatches it indirectly. apply(g,100) with g capturing a=10,b=5 = 115.
			name: "higher_order_closure",
			src: `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
  var a: i32 = 10;
  var b: i32 = 5;
  var g = (x: i32) => x + a + b;
  return apply(g, 100);
}`,
			want: 115,
		},
		{
			// A closure ARRAY mixing a capturing and a zero-capture lambda — the
			// shape the fernsmith differential oracle found segfaulting (#6144).
			// Neither closure is called, so this is construction + drop only: at
			// scope exit the IR's generic __drop_arr_closure walks the array and
			// dispatches each element's drop sub-pair at (element + 2*ptrW). The
			// SSA cell was 2 slots wide, so that read past the cell into the next
			// heap block and called the LAMBDA as the drop routine, with the env
			// in the wrong register — SIGSEGV the moment it touched a capture.
			name: "closure_array_construct_and_drop",
			src: `function main(): i32 {
  var v0: i32 = 1;
  var v2: ((i32) => i32)[] = [((a: i32) => (v0 & a)), ((b: i32) => b)];
  return 0;
}`,
			want: 0,
		},
		{
			// The same array, but dispatched before it drops: element 0 reads its
			// capture through the cell's env slot, element 1 has no env at all. So
			// this fails on a wrong cell layout in either direction — a bad call
			// slot corrupts the result, a bad drop slot faults at scope exit.
			name: "closure_array_dispatch_and_drop",
			src: `function main(): i32 {
  var base: i32 = 40;
  var fs: ((i32) => i32)[] = [((a: i32) => a + base), ((b: i32) => b)];
  return fs[0](1) + fs[1](1);
}`,
			want: 42,
		},
		{
			// A bare (payloadless) enum matched by variant — OpEnumSentinel: the
			// value is a pointer to a shared static tag cell; the match reads it.
			name: "bare_enum",
			src: `enum Color { Red, Green, Blue }
function main(): i32 {
  var c: Color = Color.Green;
  return match (c) { Red => 1, Green => 2, Blue => 3 };
}`,
			want: 2,
		},
		{
			// A constant too large for a single movz/bitmask — exercises the
			// assembler's movz/movk synthesis. 1000000 % 256 = 64.
			name: "large_const",
			src:  `function main(): i32 { return 1000000 % 256; }`,
			want: 64,
		},
		{
			// Array append growth (__fern_arr_push_grow): a fresh array grown in a
			// loop, then indexed. a[7] = 7*7 = 49.
			name: "array_append",
			src: `function main(): i32 {
  var a: i32[] = [];
  var i: i32 = 0;
  while (i < 10) { a = a.append(i * i); i = i + 1; }
  return a[7];
}`,
			want: 49,
		},
		{
			// Append then iterate: sum of [1..5] appended one at a time = 15.
			name: "array_append_sum",
			src: `function main(): i32 {
  var a: i32[] = [];
  var i: i32 = 0;
  while (i < 5) { a = a.append(i + 1); i = i + 1; }
  var s: i32 = 0;
  for x in a { s = s + x; }
  return s;
}`,
			want: 15,
		},
		{
			// An 8-byte-stride array (i64) — exercises __arr_idx_8. a[1] = 200.
			name: "i64_array_index",
			src: `function main(): i32 {
  var a: i64[] = [100, 200, 300];
  return (a[1]) as i32;
}`,
			want: 200,
		},
		{
			// A stdlib float method — dead-function elimination drops the unused
			// transcendentals std/float also defines (cos/sin/…), so only __abs_f64
			// is pulled in. abs(-3.5) as i32 = 3.
			name: "stdlib_float_abs",
			src: `import "std/float";
function main(): i32 { var x: f64 = 0.0 - 3.5; return (x.abs()) as i32; }`,
			want: 3,
		},
		{
			// Likewise sqrt — DFE keeps only __sqrt_f64. sqrt(16) = 4.
			name: "stdlib_float_sqrt",
			src: `import "std/float";
function main(): i32 { var x: f64 = 16.0; return (x.sqrt()) as i32; }`,
			want: 4,
		},
		{
			// exp — fdlibm's __ieee754_exp (the __exp_f64 helper + the shared .rodata
			// coefficient table): k = round(x/ln2), a two-chunk ln2 subtraction, the
			// P1–P5 rational correction. exp(1) ≈ e; within 0.001 → 1.
			name: "stdlib_float_exp",
			src: `import "std/float";
function main(): i32 {
  var e = (1.0).exp();
  return if ((e - 2.718281828459045).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// log — fdlibm's __ieee754_log (__log_f64): mantissa normalisation to
			// [√2/2, √2), s = f/(2+f), the Lg1–Lg7 series in s². log(e) ≈ 1;
			// within-tolerance → 1.
			name: "stdlib_float_log",
			src: `import "std/float";
function main(): i32 {
  var l = (2.718281828459045).log();
  return if ((l - 1.0).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// pow — an integral y (10) takes __pow_f64's exact repeated-squaring path,
			// which is a leaf and never reaches __log_f64 / __exp_f64. pow(2, 10) is
			// therefore exactly 1024, not merely within 0.5 of it.
			name: "stdlib_float_pow",
			src: `import "std/float";
function main(): i32 {
  var p = (2.0).pow(10.0);
  return if (p == 1024.0) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// The domain edges, which the polynomials this replaced got wrong on
			// every backend: exp overflowed into the SIGN bit (exp(1000) was
			// -6.1e-183, not +Inf), log(0) was -709.09 rather than -Inf, and log of
			// a negative was 0 rather than NaN. Each predicate contributes a bit, so
			// one exit code names which edge broke.
			name: "stdlib_float_domain",
			src: `import "std/float";
function main(): i32 {
  var r: i32 = 0;
  if ((1000.0).exp() > 1.0e308) { r = r + 1; }
  if ((0.0 - 1000.0).exp() == 0.0) { r = r + 2; }
  if ((0.0).log() < (0.0 - 1.0e308)) { r = r + 4; }
  var n = (0.0 - 1.0).log();
  if (n != n) { r = r + 8; }
  return r;
}`,
			want: 15,
		},
		{
			// sin — the shared reduction (k = round(x·2/π), a three-chunk π/2
			// subtraction) plus fdlibm's __kernel_sin. sin(π/2) ≈ 1; within-tolerance
			// → 1.
			name: "stdlib_float_sin",
			src: `import "std/float";
function main(): i32 {
  var s = (1.5707963267948966).sin();
  return if ((s - 1.0).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// cos — same reduction, __kernel_cos, cos-quadrant selection (__cos_f64).
			// cos(π) ≈ -1; within-tolerance → 1.
			name: "stdlib_float_cos",
			src: `import "std/float";
function main(): i32 {
  var c = (3.141592653589793).cos();
  return if ((c + 1.0).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// random_i32 — a single getrandom(2) read into a stack slot. The value is
			// nondeterministic, so the test only asserts it lowers and runs (r == r).
			name: "random_i32_call",
			src:  `function main(): i32 { var r = random_i32(); return if (r == r) { 7 } else { 0 }; }`,
			want: 7,
		},
		{
			// random_bytes(n) — a fresh single-word rc string of n CSPRNG bytes; the
			// length is deterministic (n) even though the contents aren't. len = 16.
			name: "random_bytes_len",
			src:  `function main(): i32 { var b: string = random_bytes(16); return b.len(); }`,
			want: 16,
		},
		{
			// random_bytes(0) edge — a zero-length string (getrandom is a no-op); the
			// 8-byte header + trailing NUL are still written. len = 0, +5 = 5.
			name: "random_bytes_zero",
			src:  `function main(): i32 { var b: string = random_bytes(0); return b.len() + 5; }`,
			want: 5,
		},
		{
			// tcp_listen(0) binds an ephemeral port on 0.0.0.0 — a real
			// socket(2)/bind(2)/listen(2) round-trip against the host, whether the
			// binary runs natively or under qemu-user (which passes the syscalls
			// through). A non-negative fd means success; then tcp_close it.
			name: "tcp_listen_close",
			src: `function main(): i32 {
  var fd = tcp_listen(0);
  if (fd < 0) { return 99; }
  var c = tcp_close(fd);
  return if (fd >= 0) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// tcp_pollable is the identity on native (a socket's readiness token IS its
			// fd), so tcp_pollable(42) == 42.
			name: "tcp_pollable_id",
			src:  `function main(): i32 { return tcp_pollable(42); }`,
			want: 42,
		},
		{
			// tcp_recv on a bad fd → read(2) returns -EBADF, which the helper clamps to
			// a zero-length string. Exercises the recv alloc + read + len-clamp path.
			name: "tcp_recv_badfd",
			src:  `function main(): i32 { var s = tcp_recv(999, 10); return s.len(); }`,
			want: 0,
		},
		{
			// tcp_send / tcp_accept on a bad fd return -errno (negative). Confirms the
			// write(2) / accept(2) syscall paths and the negative-errno passthrough.
			name: "tcp_send_accept_badfd",
			src: `function main(): i32 {
  var s = tcp_send(999, "hi");
  var a = tcp_accept(999);
  var sn: i32 = if (s < 0) { 1 } else { 0 };
  var an: i32 = if (a < 0) { 1 } else { 0 };
  return sn + an;
}`,
			want: 2,
		},
		{
			// poll on an empty fd set short-circuits to -1 (nothing to wait on).
			// Exercises the nfds == 0 guard.
			name: "poll_empty",
			src:  `function main(): i32 { var fds: i32[] = []; var r = poll(fds, 0); return if (r == -1) { 1 } else { 0 }; }`,
			want: 1,
		},
		{
			// poll over a real pollfd set: a live listener fd that has no pending
			// connection is not POLLIN-ready, so a 0 ms poll returns -1. Exercises the
			// pollfd[] marshal, the ppoll(2) syscall, and the revents scan.
			name: "poll_listener_not_ready",
			src: `function main(): i32 {
  var fd = tcp_listen(0);
  var fds: i32[] = [fd];
  var r = poll(fds, 0);
  var c = tcp_close(fd);
  return if (r == -1) { 3 } else { 0 };
}`,
			want: 3,
		},
		{
			// wasm_timer_pollable is a native stub returning -1 (no pollable to make;
			// the deadline is poll(2)'s timeout arg). Lets std/async's with_deadline
			// stay portable across native + wasm.
			name: "wasm_timer_pollable_stub",
			src:  `function main(): i32 { var t = wasm_timer_pollable(1000000); return if (t == -1) { 1 } else { 0 }; }`,
			want: 1,
		},
		{
			// wasm_pollable_drop is a native no-op returning 0 (a pollable is just an
			// fd, closed via tcp_close).
			name: "wasm_pollable_drop_stub",
			src:  `function main(): i32 { return wasm_pollable_drop(5) + 4; }`,
			want: 4,
		},
		{
			// wasm_poll is -1 on native (no real pollables; readiness rides poll(2)),
			// ignoring its array arg. On wasm it's the real wasi:io/poll.poll.
			name: "wasm_poll_stub",
			src:  `function main(): i32 { var ps: i32[] = [3, 7]; var i = wasm_poll(ps); return if (i == -1) { 5 } else { 0 }; }`,
			want: 5,
		},
		{
			// Writer write-path round-trip: open_writer creates the file and returns a
			// Writer handle (fd at handle+8, immortal-rc so it's never freed); w.write
			// streams the bytes; w.close closes the fd; read_file reads it back (len 5).
			// Exercises the handle box, the Result[Writer, IoError] Ok box, and the
			// Option[IoError] None boxes.
			name: "writer_roundtrip",
			src: `function main(): i32 {
  var wr = match (open_writer("/tmp/fern_ssa_e2e_wpath.txt")) {
    Ok(w) => match (w.write("hello")) {
      Some(e) => 30,
      None => match (w.close()) { Some(e) => 40, None => 0 }
    },
    Err(e) => 50
  };
  if (wr != 0) { return wr; }
  return match (read_file("/tmp/fern_ssa_e2e_wpath.txt")) { Ok(s) => s.len(), Err(e) => 60 };
}`,
			want: 5,
		},
		{
			// open_writer failure: a path under a nonexistent directory yields ENOENT,
			// mapped through __fern_io_error to Err(NotFound). Exercises the open_writer
			// error path + the Result Err box.
			name: "open_writer_err",
			src: `function main(): i32 {
  return match (open_writer("/no_such_dir_ssa_ow/f.txt")) {
    Ok(w) => 0,
    Err(e) => match (e) { NotFound(p) => 10, _ => 19 }
  };
}`,
			want: 10,
		},
		{
			// Reader read-path round-trip: write a file, open_reader it, read_chunk(4)
			// returns Some("abcd") (len 4), then r.close(). Exercises the Reader handle,
			// Result[Reader, IoError] Ok box, and the Option[string] Some box.
			name: "reader_roundtrip",
			src: `function main(): i32 {
  var w = write_file("/tmp/fern_ssa_e2e_rdr.txt", "abcdefg");
  return match (open_reader("/tmp/fern_ssa_e2e_rdr.txt")) {
    Ok(r) => match (r.read_chunk(4)) {
      Some(s) => match (r.close()) { Some(e) => 40, None => s.len() },
      None => 30
    },
    Err(e) => 50
  };
}`,
			want: 4,
		},
		{
			// read_chunk at EOF (an empty file) yields None. Exercises the read <= 0
			// -> None branch of the Option[string] box.
			name: "reader_read_chunk_eof",
			src: `function main(): i32 {
  var w = write_file("/tmp/fern_ssa_e2e_rdeof.txt", "");
  return match (open_reader("/tmp/fern_ssa_e2e_rdeof.txt")) {
    Ok(r) => match (r.read_chunk(8)) { Some(s) => 1, None => 7 },
    Err(e) => 50
  };
}`,
			want: 7,
		},
		{
			// open_reader failure: a nonexistent path yields Err(NotFound).
			name: "open_reader_err",
			src: `function main(): i32 {
  return match (open_reader("/no_such_ssa_rd_dir/x.txt")) {
    Ok(r) => 0,
    Err(e) => match (e) { NotFound(p) => 10, _ => 19 }
  };
}`,
			want: 10,
		},
		{
			// stdout() returns a Writer handle wrapping fd 1; a small write succeeds
			// (None). The handle construction (fixed-fd, immortal rc) is the same as
			// open_writer's, just without a syscall. Output goes to the harness's null
			// stdout, so it's invisible.
			name: "stdout_handle",
			src:  `function main(): i32 { return match (stdout().write("x")) { Some(e) => 0, None => 5 }; }`,
			want: 5,
		},
		{
			// stderr() returns a Writer handle wrapping fd 2.
			name: "stderr_handle",
			src:  `function main(): i32 { return match (stderr().write("y")) { Some(e) => 0, None => 6 }; }`,
			want: 6,
		},
		{
			// open_appender opens O_WRONLY|O_CREAT|O_APPEND: two open→write→close
			// cycles accumulate rather than truncate. write_file("") first gives a
			// deterministic empty starting point, so the final length is 4 ("ABCD").
			name: "read_line_two_lines",
			src: `function main(): i32 {
  var t = write_file("/tmp/fern_ssa_e2e_rl.txt", "abc\nde\n");
  return match (open_reader("/tmp/fern_ssa_e2e_rl.txt")) {
    Ok(r) => match (r.read_line()) {
      Some(l1) => match (r.read_line()) { Some(l2) => l1.len() + l2.len(), None => 80 },
      None => 70
    },
    Err(e) => 50
  };
}`,
			// "abc\n" (len 4, newline kept) + "de\n" (len 3) = 7. Exercises the .bss
			// line buffer, the byte-at-a-time read loop, and the Option[string] Some box.
			want: 7,
		},
		{
			// read_line at EOF (after the file's only line) yields None. Exercises the
			// first-read-returns-0 -> None branch.
			name: "read_line_eof_none",
			src: `function main(): i32 {
  var t = write_file("/tmp/fern_ssa_e2e_rl2.txt", "x\n");
  return match (open_reader("/tmp/fern_ssa_e2e_rl2.txt")) {
    Ok(r) => match (r.read_line()) {
      Some(l1) => match (r.read_line()) { Some(l2) => 1, None => 9 },
      None => 70
    },
    Err(e) => 50
  };
}`,
			want: 9,
		},
		{
			name: "open_appender_accumulates",
			src: `function main(): i32 {
  var t = write_file("/tmp/fern_ssa_e2e_app.txt", "");
  var w1 = match (open_appender("/tmp/fern_ssa_e2e_app.txt")) {
    Ok(w) => match (w.write("AB")) { Some(e) => 9, None => match (w.close()) { Some(e2) => 8, None => 0 } },
    Err(e) => 7
  };
  var w2 = match (open_appender("/tmp/fern_ssa_e2e_app.txt")) {
    Ok(w) => match (w.write("CD")) { Some(e) => 9, None => match (w.close()) { Some(e2) => 8, None => 0 } },
    Err(e) => 7
  };
  if (w1 + w2 != 0) { return 90; }
  return match (read_file("/tmp/fern_ssa_e2e_app.txt")) { Ok(s) => s.len(), Err(e) => 60 };
}`,
			want: 4,
		},
		{
			// Integer to_string — the full digit-formatting chain: __alloc_u8
			// (byte buffer), __fern_arr_cow_inplace (arr[i] = digit), and
			// string_from_bytes_unchecked (u8[] -> string). len("123456") = 6.
			name: "int_to_string_len",
			src: `import "std/i32";
function main(): i32 { return (123456).to_string().len(); }`,
			want: 6,
		},
		{
			// print(s): the two-write puts helper — string bytes then a newline
			// to fd 1. Runs the syscalls and returns to main, which exits 7.
			name: "print_string",
			src:  `function main(): i32 { print("hi from arm64-ssa"); return 7; }`,
			want: 7,
		},
		{
			// String char index s[i] via __str_idx (bounds-checked byte address
			// + a byte load). "abc"[1] = 'b' = 98.
			name: "string_index",
			src: `function main(): i32 {
  var s: string = "abc";
  return s[1] as i32;
}`,
			want: 98,
		},
		{
			// A char-index loop: sum every byte of "hello" mod 256 =
			// (104+101+108+108+111) % 256 = 532 % 256 = 20. Exercises __str_idx
			// under a loop with a per-iteration bounds check.
			name: "string_index_loop",
			src: `function main(): i32 {
  var s: string = "hello";
  var sum: i32 = 0;
  var i: i32 = 0;
  while (i < s.len()) { sum = sum + (s[i] as i32); i = i + 1; }
  return sum % 256;
}`,
			want: 20,
		},
		{
			// String slice s[a:b] via __str_slice — allocates a fresh substring.
			// len("hello world"[6:11]) = len("world") = 5.
			name: "string_slice_len",
			src: `function main(): i32 {
  var s: string = "hello world";
  return s[6:11].len();
}`,
			want: 5,
		},
		{
			// Slice content check: the first byte of s[6:11] ("world") is 'w' = 119,
			// confirming the copied bytes (not just the length) are correct.
			name: "string_slice_content",
			src: `function main(): i32 {
  var s: string = "hello world";
  var w: str = s[6:11];
  return w[0] as i32;
}`,
			want: 119,
		},
		{
			// Out-of-range slice traps with exit 134 (high > src_len), matching the
			// native backend's bounds trap rather than a silent miscompile.
			name: "string_slice_oob_trap",
			src:  `function main(): i32 { var s: string = "abc"; return s[1:9].len(); }`,
			want: 134,
		},
		{
			// u32 logical right shift of a call result whose bit 31 is set. mk()
			// returns 0x90000000; the call-result is stored sign-extended, so a
			// 64-bit `lsr` would drag the high-bit 1s into the result. Correct u32:
			// (0x90000000 >> 3) = 0x12000000, then >> 24 = 0x12 = 18. (The bug that
			// miscompiled SHA-256 gave 0xF2 = 242.) HARDCODED want, so it catches a
			// regression even if the model oracle regressed in lockstep.
			name: "u32_shr_call_result",
			src: `function mk(): u32 { var a: u32 = 2415919104; return a; }
function main(): i32 { var v: u32 = mk(); return ((v >> 3) >> 24) as i32; }`,
			want: 18,
		},
		{
			// End-to-end SHA-256: the hex digest of "abc" is the fixed vector
			// "ba7816bf...", so its first character is 'b' = 98. This exercises the
			// whole u32 arithmetic surface (rotr via shifts, wrapping adds, big-
			// endian packing, __str_slice) and is the strongest guard on the u32 `>>`
			// width fix — before it, this digest came out wrong.
			name: "sha256_first_char",
			src: `import "std/crypto";
function main(): i32 {
  var h: string = crypto.sha256_hex("abc");
  return h[0] as i32;
}`,
			want: 98, // 'b'
		},
		{
			// string[] from split, whose scope-exit drop uses __fern_drop_arr_str.
			// "a,b,c".split(",") has 3 parts -> len 3.
			name: "string_split_len",
			src: `import "std/string";
function main(): i32 { var p = "a,b,c".split(","); return p.len(); }`,
			want: 3,
		},
		{
			// Split then iterate the parts, summing their lengths — exercises the
			// string[] element walk and per-element access alongside the drop.
			// len("alpha")+len("beta")+len("gamma") = 5+4+5 = 14.
			name: "string_split_iterate",
			src: `import "std/string";
function main(): i32 {
  var parts = "alpha,beta,gamma".split(",");
  var total: i32 = 0;
  for p in parts { total = total + p.len(); }
  return total;
}`,
			want: 14,
		},
		{
			// f64 -> i64 conversion that needs the high 32 bits. mult is built in a
			// loop (so it can't constant-fold to a compile-time i64), forcing the
			// runtime fcvtzs: (0.14 * 10^15) as i64 = 140000000000000, / 10^9 =
			// 140000, exit 140000&0xFF = 224. A 32-bit-narrowed conversion gave 1.
			name: "f64_to_i64_width",
			src: `function main(): i32 {
  var frac: f64 = 0.14;
  var mult: f64 = 1.0;
  var i: i32 = 0;
  while (i < 15) { mult = mult * 10.0; i = i + 1; }
  var fracInt: i64 = (frac * mult) as i64;
  return (fracInt / 1000000000) as i32;
}`,
			want: 224,
		},
		{
			// End-to-end float formatting: 3.14.to_string() = "3.14" (len 4). Before
			// the f64->i64 width fix the fractional part came out as a 15-digit garbage
			// tail ("3.000001246019584", len 17) because (frac * 10^15) as i64 was
			// truncated to 32 bits.
			name: "f64_to_string_frac",
			src: `import "std/float";
function main(): i32 { var x: f64 = 3.14; return x.to_string().len(); }`,
			want: 4,
		},
		{
			// The args() builtin: with no extra CLI arguments the process sees just
			// argv[0] (the binary path), so args().len() = 1. Exercises _start's
			// argc/argv capture and the container/string construction in the helper.
			name: "args_len",
			src:  `function main(): i32 { return args().len(); }`,
			want: 1,
		},
		{
			// args() content: argv[0] is the absolute binary path the harness runs
			// (out is under an absolute t.TempDir()), so its first byte is '/' = 47.
			// Confirms the per-arg string bytes are copied, not just the count.
			name: "args_first_char",
			src:  `function main(): i32 { var a = args(); return a[0][0] as i32; }`,
			want: 47,
		},
		{
			// write(s): print's no-newline sibling (a single write(2) to stdout).
			// Runs the syscall and returns to main -> exit 7. (Content is verified by
			// hand; the roundtrip harness only observes the exit code.)
			name: "write_string",
			src:  `function main(): i32 { write("hi from arm64-ssa"); return 7; }`,
			want: 7,
		},
		{
			// eprint(s): the stderr sibling (bytes + newline to fd 2), then returns.
			name: "eprint_string",
			src:  `function main(): i32 { eprint("err"); return 5; }`,
			want: 5,
		},
		{
			// putchar(c): write the low byte of the argument to stdout. 'A' then
			// newline, then return 9.
			name: "putchar_byte",
			src:  `function main(): i32 { putchar(65); putchar(10); return 9; }`,
			want: 9,
		},
		{
			// exit(code): terminate immediately with the status. The `return 99` is
			// never reached, so a correct exit yields 3 (not 99).
			name: "exit_code",
			src:  `function main(): i32 { exit(3); return 99; }`,
			want: 3,
		},
		{
			// A conditional exit from inside a loop: bail with the counter at 4.
			name: "exit_in_loop",
			src: `function main(): i32 {
  var i: i32 = 0;
  while (i < 10) { if (i == 4) { exit(i); } i = i + 1; }
  return 88;
}`,
			want: 4,
		},
		{
			// The global string builder: reset, append three fragments, take. The
			// result is "Hello, Fern!" (len 12). Exercises strbuf_reset / _append /
			// _take and the shared .bss buffer.
			name: "strbuf_build",
			src: `function main(): i32 {
  strbuf_reset();
  strbuf_append("Hello, ");
  strbuf_append("Fern");
  strbuf_append("!");
  return strbuf_take().len();
}`,
			want: 12,
		},
		{
			// Reuse after take: the counter resets, so a second build is independent.
			// "ababababab" (10) then "xyz" (3): 10*100 + 3 = 1003, exit 1003&0xFF = 235.
			name: "strbuf_reuse",
			src: `function main(): i32 {
  strbuf_reset();
  var i: i32 = 0;
  while (i < 5) { strbuf_append("ab"); i = i + 1; }
  var s = strbuf_take();
  strbuf_reset();
  strbuf_append("xyz");
  var t = strbuf_take();
  return s.len() * 100 + t.len();
}`,
			want: 235,
		},
		{
			// env() Some path: FERN_E2E_VAR=hi is injected into the run environment,
			// so env("FERN_E2E_VAR") = Some("hi") and v.len() = 2. Exercises _start's
			// envp capture and the Option-box the match reads at [box+0]/[box+8].
			name: "env_present",
			src:  `function main(): i32 { return match (env("FERN_E2E_VAR")) { Some(v) => v.len(), None => 0 }; }`,
			want: 2,
		},
		{
			// env() None path: a variable that is not set yields None -> 42.
			name: "env_absent",
			src:  `function main(): i32 { return match (env("FERN_UNSET_ZZZ_9137")) { Some(v) => 1, None => 42 }; }`,
			want: 42,
		},
		{
			// write_file success: writing to a temp path succeeds, so the result is
			// None -> 0. Exercises the path NUL-termination, openat/write/close, and
			// the Option[IoError] None box.
			name: "write_file_ok",
			src:  `function main(): i32 { return match (write_file("/tmp/fern_ssa_e2e_wf.txt", "hi")) { Err(e) => 1, Ok(_) => 0 }; }`,
			want: 0,
		},
		{
			// write_file failure: a path under a nonexistent directory yields ENOENT,
			// so the result is Some(NotFound). Destructuring the IoError confirms the
			// errno -> tag mapping and the box layout the match reads.
			name: "write_file_err",
			src: `function main(): i32 {
  return match (write_file("/no_such_dir_ssa_9137/f.txt", "x")) {
    Err(e) => match (e) { NotFound(p) => 10, _ => 19 },
    Ok(_) => 0
  };
}`,
			want: 10,
		},
		{
			// read_file round-trip: write "abcde" then read it back. Ok(s).len() = 5.
			// Exercises openat(O_RDONLY) / fstat sizing / the read loop / the
			// Result[string, IoError] Ok box, and pairs with write_file.
			name: "read_file_roundtrip",
			src: `function main(): i32 {
  var w = write_file("/tmp/fern_ssa_e2e_rf.txt", "abcde");
  return match (read_file("/tmp/fern_ssa_e2e_rf.txt")) { Ok(s) => s.len(), Err(e) => 0 };
}`,
			want: 5,
		},
		{
			// read_file failure: a nonexistent file yields Err(NotFound). Destructuring
			// confirms the errno -> IoError mapping on the read path.
			name: "read_file_err",
			src: `function main(): i32 {
  return match (read_file("/no_such_file_ssa_9137")) {
    Ok(s) => 1,
    Err(e) => match (e) { NotFound(p) => 10, _ => 19 }
  };
}`,
			want: 10,
		},
		{
			// remove_file success: write a file, remove it (None), then confirm it's
			// gone by re-reading (Err(NotFound)). Exercises the unlinkat syscall and
			// the None (tag 1) path of the Option[IoError] box.
			name: "remove_file_ok",
			src: `function main(): i32 {
  var w = write_file("/tmp/fern_ssa_e2e_rmf.txt", "gone");
  var r = match (remove_file("/tmp/fern_ssa_e2e_rmf.txt")) { Err(e) => 1, Ok(_) => 5 };
  var g = match (read_file("/tmp/fern_ssa_e2e_rmf.txt")) { Ok(s) => 0, Err(e) => 2 };
  return r + g;
}`,
			want: 7,
		},
		{
			// remove_file failure: removing a nonexistent file yields Some(NotFound)
			// (os.Remove-style: a missing target is an error). Exercises the errno ->
			// IoError mapping on the unlink path.
			name: "remove_file_err",
			src: `function main(): i32 {
  return match (remove_file("/no_such_file_ssa_rmf_9137")) {
    Ok(_) => 1,
    Err(e) => match (e) { NotFound(p) => 10, _ => 19 }
  };
}`,
			want: 10,
		},
		{
			// temp_dir success: create "/tmp/<prefix>-XXXXXXXX" and return the path.
			// The arm64-ssa suffix is a fixed 8 hex digits, so the length is
			// deterministic: 5 ("/tmp/") + 8 (prefix) + 1 ("-") + 8 (hex) = 22.
			// Exercises getrandom / the hex-format loop / mkdirat / the string Ok box.
			name: "temp_dir_ok",
			src:  `function main(): i32 { return match (temp_dir("fern_ssa")) { Ok(p) => p.len(), Err(e) => 0 }; }`,
			want: 22,
		},
		{
			// temp_dir usable: the returned directory is real and writable — build a
			// path inside it, write_file "hello", then read it back (len 5). Proves
			// the created directory actually exists on disk.
			name: "temp_dir_usable",
			src: `function main(): i32 {
  return match (temp_dir("fern_ssa")) {
    Ok(p) => match (write_file(p + "/g.txt", "hello")) {
      Err(e) => 3,
      Ok(_) => match (read_file(p + "/g.txt")) { Ok(s) => s.len(), Err(e) => 1 }
    },
    Err(e) => 2
  };
}`,
			want: 5,
		},
		{
			// temp_dir failure: a prefix that puts the target under a nonexistent
			// parent yields ENOENT, so mkdirat fails and the errno maps through
			// __fern_io_error to Err(IoError). Exercises the temp_dir error path.
			name: "temp_dir_err",
			src:  `function main(): i32 { return match (temp_dir("no_such_ssa/x")) { Ok(p) => 0, Err(e) => 1 }; }`,
			want: 1,
		},
		{
			// read_dir success: create a temp dir, write two files into it, then list
			// it — "." and ".." are excluded, so the count is 2. Exercises openat
			// O_DIRECTORY / the getdents64 two-pass count+fill / lseek rewind / the
			// string[] container Ok box.
			name: "read_dir_count",
			src: `function main(): i32 {
  return match (temp_dir("fern_rd")) {
    Ok(d) => {
      var a = write_file(d + "/a.txt", "x");
      var b = write_file(d + "/b.txt", "y");
      match (read_dir(d)) { Ok(es) => es.len(), Err(e) => 100 }
    },
    Err(e) => 200
  };
}`,
			want: 2,
		},
		{
			// read_dir element: a single-entry directory, so es[0] is deterministic.
			// The base name "hello.txt" has length 9 — proves the per-entry string is
			// constructed correctly and is indexable out of the container.
			name: "read_dir_elem",
			src: `function main(): i32 {
  return match (temp_dir("fern_rd")) {
    Ok(d) => {
      var a = write_file(d + "/hello.txt", "z");
      match (read_dir(d)) { Ok(es) => es[0].len(), Err(e) => 100 }
    },
    Err(e) => 200
  };
}`,
			want: 9,
		},
		{
			// read_dir failure: a nonexistent directory yields ENOENT → Err(NotFound).
			name: "read_dir_err",
			src: `function main(): i32 {
  return match (read_dir("/no_such_dir_ssa_rd_9137")) {
    Ok(es) => 0,
    Err(e) => match (e) { NotFound(p) => 10, _ => 19 }
  };
}`,
			want: 10,
		},
		{
			// remove_dir_all (recursive rm -rf): create a temp dir with two files,
			// remove_dir_all it (None), then read_dir confirms it's gone (Err). Each
			// child file drives a recursion that hits ENOTDIR and unlinks; the emptied
			// directory is then rmdir'd.
			name: "remove_dir_all_dir",
			src: `function main(): i32 {
  return match (temp_dir("fern_rda")) {
    Ok(d) => {
      var a = write_file(d + "/a.txt", "x");
      var b = write_file(d + "/b.txt", "y");
      var r = match (remove_dir_all(d)) { Err(e) => 40, Ok(_) => 0 };
      var g = match (read_dir(d)) { Ok(es) => 50, Err(e) => 0 };
      r + g + 5
    },
    Err(e) => 60
  };
}`,
			want: 5,
		},
		{
			// remove_dir_all on a missing path is a silent success (None, matching
			// os.RemoveAll); on a plain file it unlinks (ENOTDIR path) and the file is
			// gone afterward.
			name: "remove_dir_all_missing_and_file",
			src: `function main(): i32 {
  var m = match (remove_dir_all("/no_such_ssa_rda_dir")) { Err(e) => 1, Ok(_) => 0 };
  var t = write_file("/tmp/fern_ssa_e2e_rda_file.txt", "z");
  var f = match (remove_dir_all("/tmp/fern_ssa_e2e_rda_file.txt")) { Err(e) => 2, Ok(_) => 0 };
  var g = match (read_file("/tmp/fern_ssa_e2e_rda_file.txt")) { Ok(s) => 4, Err(e) => 0 };
  return m + f + g + 7;
}`,
			want: 7,
		},
		{
			// `dyn Trait` dispatch (OpConstVtable + OpBoxDyn + OpCallDyn): two
			// concrete structs coerced to `dyn Producer`, each boxed into a
			// {data, vtable} cell, dispatched through the vtable's slot-0 pointer.
			// sum(IntBox{40}) + sum(Pair{1,1}) = 40 + 2 = 42. Exercises the vtable
			// `.rodata` cell, the inline box alloc, and the indirect slot call.
			name: "dyn_trait_dispatch",
			src: `trait Producer { function get(self: Self): i32; }
struct IntBox { v: i32 }
impl Producer for IntBox { function get(self: Self): i32 { return self.v; } }
struct Pair { a: i32, b: i32 }
impl Producer for Pair { function get(self: Self): i32 { return self.a + self.b; } }
function sum(p: dyn Producer): i32 { return p.get(); }
function main(): i32 {
  var x: dyn Producer = IntBox { v: 40 };
  var y: dyn Producer = Pair { a: 1, b: 1 };
  return sum(x) + sum(y);
}`,
			want: 42,
		},
		{
			// A multi-method trait dispatched via `dyn` — two vtable slots, one of
			// them taking an extra argument. describe(Square{5}) = area 25 +
			// scaled(3) 15 = 40; describe(Rect{2,4}) = 8 + (2+4)*3=18 → 26. 40+26=66.
			// Exercises OpCallDyn's slot math (slot 1) and its receiver-first arg ABI.
			name: "dyn_trait_multi_method",
			src: `trait Shape {
  function area(self: Self): i32;
  function scaled(self: Self, k: i32): i32;
}
struct Square { side: i32 }
impl Shape for Square {
  function area(self: Self): i32 { return self.side * self.side; }
  function scaled(self: Self, k: i32): i32 { return self.side * k; }
}
struct Rect { w: i32, h: i32 }
impl Shape for Rect {
  function area(self: Self): i32 { return self.w * self.h; }
  function scaled(self: Self, k: i32): i32 { return (self.w + self.h) * k; }
}
function describe(s: dyn Shape): i32 { return s.area() + s.scaled(3); }
function main(): i32 {
  var a: dyn Shape = Square { side: 5 };
  var b: dyn Shape = Rect { w: 2, h: 4 };
  return describe(a) + describe(b);
}`,
			want: 66,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, c.name+".bin")
			emit := exec.Command(bin, "-target", "arm64-ssa", "-o", out, srcPath)
			var eb bytes.Buffer
			emit.Stderr = &eb
			if err := emit.Run(); err != nil {
				t.Fatalf("fern -target arm64-ssa: %v\nstderr:\n%s", err, eb.String())
			}
			run := runArm64Bin(qemu, out)
			// The child inherits this environment either way (qemu-user forwards
			// it to the guest), so a known variable makes the env() Some-path
			// deterministic. Harmless to the cases that don't read it.
			run.Env = append(os.Environ(), "FERN_E2E_VAR=hi")
			err := run.Run()
			got := 0
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					got = ee.ExitCode()
				} else {
					t.Fatalf("run %s: %v", out, err)
				}
			}
			if got != c.want {
				t.Errorf("%s: exit=%d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestArm64SSACoverageGapErrors confirms a program needing a runtime builtin the
// arm64-ssa path doesn't emit yet (here the `subprocess` builtin, which reaches
// the still-unported `subprocess` helper) fails with a clean compile/link error
// rather than a miscompile — the experimental-backend contract that lets the epic
// widen coverage incrementally.
func TestArm64SSACoverageGapErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}

	srcPath := filepath.Join(dir, "sub.fern")
	src := `function main(): i32 { var p = subprocess("echo", [], ""); return p.exit_code; }`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "sub.bin")
	emit := exec.Command(bin, "-target", "arm64-ssa", "-o", out, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	err := emit.Run()
	if err == nil {
		t.Fatalf("expected a coverage-gap error for the subprocess() builtin, got success")
	}
	if !bytes.Contains(eb.Bytes(), []byte("arm64-ssa")) {
		t.Errorf("error not attributed to arm64-ssa:\n%s", eb.String())
	}
}
