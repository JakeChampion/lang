package e2ecompiler

import "testing"

// An if-expression at a fn-typed argument whose arms mix a local fn value
// with a lambda, or pass locals through a generic call (#10927, shape 3).
// The lift admitted an identifier arm only when it named a module function,
// so a local holding a fn value, already an env box, made the whole
// if-expression decline, and the lambda arm kept the bare `__lam_N` address
// the first pass gave it.
const iifeLocalFnValueSrc = `import "std/i32";
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    let k: i32 = 100;
    let c: boolean = true;
    let v0: (i32) => i32 = (x: i32) => x + 650;
    let v1: (i32) => i32 = (x: i32) => x + k;
    let mixed: i32 = apply(if (c) { v0 } else { (x: i32) => 690 }, 1);
    let other: i32 = apply(if (!c) { v0 } else { (x: i32) => x * 3 }, 2);
    let picked: i32 = apply(if (c) { pick(false, v0, v1) } else { (x: i32) => 0 }, 3);
    let both: i32 = apply(if (c) { pick(true, v0, (x: i32) => x) } else { v1 }, 4);
    let nested: i32 = apply(if (c) { if (!c) { v1 } else { pick(false, v0, v1) } } else { v0 }, 5);
    print(mixed.to_string() + " " + other.to_string() + " " + picked.to_string() + " " + both.to_string() + " " + nested.to_string());
    return 0;
}
`

func iifeLocalFnValueWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, iifeLocalFnValueSrc)
	if code != 0 || want != "651 6 103 654 105\n" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostIifeLocalFnValueX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := iifeLocalFnValueWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", iifeLocalFnValueSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostIifeLocalFnValueArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := iifeLocalFnValueWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", iifeLocalFnValueSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostIifeLocalFnValueWasm(t *testing.T) {
	cli := newStrictCLI(t)
	want := iifeLocalFnValueWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", iifeLocalFnValueSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
