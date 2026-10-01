package e2eselfhost

import "testing"

// An if-expression as a call argument, with an arm whose value is a generic
// passthrough call carrying lambda arguments (#10927). The inline-closure
// lift declined the whole if-expression because a call-valued arm was not a
// boxable arm, so a capture-free lambda stayed a bare `__lam_N` address and a
// capturing one stayed an `ExprLambda`, and the typed lowering refused main.
// The array element keeps a passthrough call that is handed a nested
// if-expression on the array path: that call is not boxable as an arm.
const iifePassthroughArmSrc = `import "std/i32";
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var k: i32 = 40;
    var c: boolean = true;
    var free: i32 = apply(if (c) { pick(false, (x: i32) => x + 2, (x: i32) => x * 3) } else { (x: i32) => x }, 5);
    var captured: i32 = apply(if (c) { pick(true, (x: i32) => x + k, (x: i32) => x) } else { (x: i32) => k }, 2);
    var nested: i32 = apply(if (!c) { (x: i32) => 0 } else { (if (c) { pick(false, (x: i32) => x - 1, (x: i32) => x + 10) } else { (x: i32) => x }) }, 7);
    var piped: i32 = (if (c) { pick(true, (x: i32) => x + k, (x: i32) => x) } else { (x: i32) => x }) |> apply(1);
    var arr: ((i32) => i32)[] = [if (c) { pick(true, (x: i32) => x * 2, if (c) { (x: i32) => 500 } else { (x: i32) => x }) } else { (x: i32) => x + k }];
    print(free.to_string() + " " + captured.to_string() + " " + nested.to_string() + " " + piped.to_string() + " " + arr[0](6).to_string());
    return 0;
}
`

func iifePassthroughArmWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, iifePassthroughArmSrc)
	if code != 0 || want != "15 42 17 41 12\n" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostIifePassthroughArmX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := iifePassthroughArmWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", iifePassthroughArmSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostIifePassthroughArmArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := iifePassthroughArmWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", iifePassthroughArmSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostIifePassthroughArmWasm(t *testing.T) {
	cli := newStrictCLI(t)
	want := iifePassthroughArmWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", iifePassthroughArmSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
