package e2eselfhost

import "testing"

// f32LiteralSrc is f32 arithmetic whose literals were read as f64 (#10757): an
// unsuffixed literal tree at an f32 destination or beside an f32 operand, and a
// suffixed `f32` literal the checker typed f64, which surfaced in array
// literals, generic calls and the value an `if` or `match` yields. Each line
// prints what the interpreter prints.
const f32LiteralSrc = `import "std/float";
import "std/i32";
function id[T](x: T): T { return x; }
function pick[T](c: boolean, a: T, b: T): T { return if (c) { a } else { b }; }
function half(x: f32): f32 { return x / 2.0; }
function tag(n: i32): f32 { return match (n) { 0 => 0.25f32, _ => id(1.5f32) }; }
function main(): i32 {
    let neg: f32 = 0.0 - 1.0;
    if (f32_from_bits(f32_bits(neg)) != neg) { return 1; }
    if (f32_bits(0.1) != 1036831949) { return 3; }
    let p: i32 = 3;
    let q: i32 = 4;
    let from_vars: f32 = (p - q) as f32;
    if (from_vars != 0.0 - 1.0) { return 2; }
    let a: f32 = 1.5;
    let xs: f32[] = [a, 2.5f32, id(0.5f32), pick(true, 4.0f32, 8.0f32)];
    let sum: f32 = 0.0;
    for x in xs { sum = sum + x; }
    let v: f32 = if (sum > 8.0f32) { sum / 2.0 } else { sum };
    print(neg.to_string() + " " + sum.to_string() + " " + v.to_string() + " " + half(3.0).to_string() + " " + tag(0).to_string() + " " + tag(1).to_string());
    return 0;
}
`

func f32LiteralWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, f32LiteralSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostF32LiteralIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := f32LiteralWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", f32LiteralSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q (a non-zero exit names the failing step)", code, out, want)
	}
}

func TestSelfHostF32LiteralIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := f32LiteralWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", f32LiteralSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostF32LiteralWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := f32LiteralWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", f32LiteralSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
