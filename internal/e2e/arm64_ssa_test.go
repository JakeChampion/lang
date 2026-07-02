// E2E tests for the experimental SSA-direct arm64 backend
// (`-target arm64-ssa`). Builds the fern CLI from this checkout,
// compiles small Fern programs with the new target, and runs the
// resulting static AArch64 ELF under qemu-aarch64, asserting the
// process exit code (main's return value, low byte).
//
// SKIPs when qemu-aarch64 isn't on PATH so the suite stays green on
// machines without the emulator.
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
// the CLI and runs each binary under qemu, exercising the SSA register
// allocator's real output on cross-function calls, recursion, control
// flow, memory, and strings.
func TestArm64SSACliRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	qemu := ""
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if qemu == "" {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-ssa e2e")
	}

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
			// exp — a range-reduced degree-7 Taylor polynomial (the __exp_f64 helper +
			// the shared .rodata coefficient table). exp(1) ≈ e; the tolerance check
			// (a few ulp of the polynomial approx) returns 1 when within 0.001.
			name: "stdlib_float_exp",
			src: `import "std/float";
function main(): i32 {
  var e = (1.0).exp();
  return if ((e - 2.718281828459045).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// log — mantissa normalisation + an odd-power series for ln(m) (__log_f64).
			// log(e) ≈ 1; within-tolerance → 1.
			name: "stdlib_float_log",
			src: `import "std/float";
function main(): i32 {
  var l = (2.718281828459045).log();
  return if ((l - 1.0).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// pow — exp(y·ln x), exercising __pow_f64's chained calls into __log_f64 /
			// __exp_f64 through their x0-bits ABI. pow(2, 10) ≈ 1024; within 0.5 → 1.
			name: "stdlib_float_pow",
			src: `import "std/float";
function main(): i32 {
  var p = (2.0).pow(10.0);
  return if ((p - 1024.0).abs() < 0.5) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// sin — quadrant-reduced (k=round(x/(π/2))) odd-power series via the shared
			// reduction (__sin_f64). sin(π/2) ≈ 1; within-tolerance → 1.
			name: "stdlib_float_sin",
			src: `import "std/float";
function main(): i32 {
  var s = (1.5707963267948966).sin();
  return if ((s - 1.0).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// cos — same reduction, cos-quadrant selection (__cos_f64). cos(π) ≈ -1;
			// within-tolerance → 1.
			name: "stdlib_float_cos",
			src: `import "std/float";
function main(): i32 {
  var c = (3.141592653589793).cos();
  return if ((c + 1.0).abs() < 0.001) { 1 } else { 0 };
}`,
			want: 1,
		},
		{
			// Integer to_string — the full digit-formatting chain: __alloc_u8
			// (byte buffer), __fern_arr_cow_inplace (arr[i] = digit), and
			// string_from_bytes (u8[] -> string). len("123456") = 6.
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
  var w: string = s[6:11];
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
			src:  `function main(): i32 { return match (write_file("/tmp/fern_ssa_e2e_wf.txt", "hi")) { Some(e) => 1, None => 0 }; }`,
			want: 0,
		},
		{
			// write_file failure: a path under a nonexistent directory yields ENOENT,
			// so the result is Some(NotFound). Destructuring the IoError confirms the
			// errno -> tag mapping and the box layout the match reads.
			name: "write_file_err",
			src: `function main(): i32 {
  return match (write_file("/no_such_dir_ssa_9137/f.txt", "x")) {
    Some(e) => match (e) { NotFound(p) => 10, _ => 19 },
    None => 0
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
  var r = match (remove_file("/tmp/fern_ssa_e2e_rmf.txt")) { Some(e) => 1, None => 5 };
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
    None => 1,
    Some(e) => match (e) { NotFound(p) => 10, _ => 19 }
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
      Some(e) => 3,
      None => match (read_file(p + "/g.txt")) { Ok(s) => s.len(), Err(e) => 1 }
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
			run := exec.Command(qemu, out)
			// qemu-user forwards the host environment to the guest, so a known
			// variable makes the env() Some-path deterministic. Harmless to the
			// cases that don't read it.
			run.Env = append(os.Environ(), "FERN_E2E_VAR=hi")
			err := run.Run()
			got := 0
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					got = ee.ExitCode()
				} else {
					t.Fatalf("run under qemu: %v", err)
				}
			}
			if got != c.want {
				t.Errorf("%s: exit=%d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestArm64SSACoverageGapErrors confirms a program needing a runtime builtin the
// arm64-ssa path doesn't emit yet (here the `tcp_listen` socket builtin, which
// reaches the still-unported `tcp_listen` helper) fails with a clean compile/link
// error rather than a miscompile — the experimental-backend contract that lets
// the epic widen coverage incrementally.
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

	srcPath := filepath.Join(dir, "tcp.fern")
	src := `function main(): i32 { return tcp_listen(8080); }`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "tcp.bin")
	emit := exec.Command(bin, "-target", "arm64-ssa", "-o", out, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	err := emit.Run()
	if err == nil {
		t.Fatalf("expected a coverage-gap error for the tcp_listen() builtin, got success")
	}
	if !bytes.Contains(eb.Bytes(), []byte("arm64-ssa")) {
		t.Errorf("error not attributed to arm64-ssa:\n%s", eb.String())
	}
}
