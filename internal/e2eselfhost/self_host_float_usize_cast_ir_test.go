package e2eselfhost

import "testing"

// `f64 as usize` and `usize as f64` (#10874). The typed lowering refused the
// pair (`cast contract: f64 as usize`), and the SSA backends had no pointer-
// width conversion. The cast saturates at the pointer width, as native's
// does: at u64 on the register backends and at u32 on wasm, which is also
// the interpreter's width.
const floatUsizeCastSrc = `import "std/i64";
function main(): i32 {
    var g: f64 = 2.5;
    var v: usize = g as usize;
    var neg: f64 = -3.0;
    var n: usize = neg as usize;
    var wide: f64 = 4000000000.0;
    var w: usize = wide as usize;
    var back: f64 = (3000000000 as usize) as f64;
    var half: f32 = (7 as usize) as f32;
    print((v as i64).to_string() + " " + (n as i64).to_string() + " " + (w as i64).to_string() + " " + (back as i64).to_string() + " " + (half as i64).to_string());
    return 0;
}
`

// floatUsizeOverflowSrc is TestX86_64FloatToUsize's program with the
// saturated answer printed beside it.
const floatUsizeOverflowSrc = `import "std/i64";
function main(): i32 {
    var f: f64 = 5000000000.0;
    var u: usize = f as usize;
    var big: f64 = 1e30;
    var m: usize = big as usize;
    print((u as i64).to_string() + " " + (m as i64).to_string());
    if (u == 5000000000 as usize) { return 7; }
    return 1;
}
`

func floatUsizeWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, floatUsizeCastSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostFloatUsizeCastIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := floatUsizeWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", floatUsizeCastSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", floatUsizeOverflowSrc)); code != 7 || out != "5000000000 -1\n" {
		t.Fatalf("overflow: exit %d, stdout %q; want 7, %q", code, out, "5000000000 -1\n")
	}
}

func TestSelfHostFloatUsizeCastIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := floatUsizeWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", floatUsizeCastSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", floatUsizeOverflowSrc)); code != 7 || out != "5000000000 -1\n" {
		t.Fatalf("arm64 overflow: exit %d, stdout %q; want 7, %q", code, out, "5000000000 -1\n")
	}
}

func TestSelfHostFloatUsizeCastWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := floatUsizeWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", floatUsizeCastSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
	// A 32-bit address saturates at u32::MAX, where the interpreter does too.
	overflow, icode := runInterp(t, floatUsizeOverflowSrc)
	if icode != 1 {
		t.Fatalf("interpreter overflow: exit %d, want 1", icode)
	}
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", floatUsizeOverflowSrc)); code != 1 || out != overflow {
		t.Fatalf("wasm overflow: exit %d, stdout %q; want 1, %q", code, out, overflow)
	}
}
