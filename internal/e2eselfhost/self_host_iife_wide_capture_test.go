package e2eselfhost

import "testing"

// A lambda capturing a wide scalar, wrapped in a generic passthrough call
// inside an if-expression arm (#10927, shape 2). The wide-capture pass made
// the lambda's i64 cell inside the if-expression's body, but the expression
// walk reported no change, so the function-level gate discarded the rewrite
// and the closure boxing met the bare i64 its env box cannot carry.
const iifeWideCaptureSrc = `import "std/i32";
import "core/map";
function id[T](x: T): T { return x; }
function gen_f0(): (i32) => i32 { return (x: i32) => x + 1; }
function main(): i32 {
    var k: i64 = 7i64 << 33i64;
    var c: boolean = false;
    var f: (i32) => i32 = if (c) { id(gen_f0()) } else { id((x: i32) => ((k >> 32i64) as i32) + x) };
    var g: (i32) => i32 = if (!c) { id((x: i32) => x) } else { id((x: i32) => ((k >> 32i64) as i32)) };
    var m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    var acc: i32 = 0;
    for (key, value) in m {
        var w: i64 = (key as i64) * 1000000000i64;
        var h: (i32) => i32 = if (c) { id(gen_f0()) } else { id((x: i32) => ((w / 1000000000i64) as i32) + x) };
        acc = acc + h(value);
    }
    print(f(1).to_string() + " " + g(5).to_string() + " " + acc.to_string());
    return 0;
}
`

func iifeWideCaptureWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, iifeWideCaptureSrc)
	if code != 0 || want != "15 5 10\n" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostIifeWideCaptureX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := iifeWideCaptureWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", iifeWideCaptureSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostIifeWideCaptureArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := iifeWideCaptureWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", iifeWideCaptureSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostIifeWideCaptureWasm(t *testing.T) {
	cli := newStrictCLI(t)
	want := iifeWideCaptureWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", iifeWideCaptureSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
