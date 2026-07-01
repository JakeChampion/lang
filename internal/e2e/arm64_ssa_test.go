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

// TestArm64SSACoverageGapErrors confirms a program needing a runtime helper the
// arm64-ssa path doesn't emit yet (here std/i32's to_string, which reaches the
// byte allocator __alloc_u8) fails with a clean compile/link error rather than a
// miscompile — the experimental-backend contract that lets the epic widen
// coverage incrementally.
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

	srcPath := filepath.Join(dir, "tostring.fern")
	src := `import "std/i32";
function main(): i32 { print((42).to_string()); return 0; }`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "tostring.bin")
	emit := exec.Command(bin, "-target", "arm64-ssa", "-o", out, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	err := emit.Run()
	if err == nil {
		t.Fatalf("expected a coverage-gap error for f64 arithmetic, got success")
	}
	if !bytes.Contains(eb.Bytes(), []byte("arm64-ssa")) {
		t.Errorf("error not attributed to arm64-ssa:\n%s", eb.String())
	}
}
