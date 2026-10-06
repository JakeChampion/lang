package e2ecompiler

import "testing"

// dynImportedTraitSrc coerces into a dyn set over a trait another module
// declares: core/cmp's Display, spelled `cmp.Display` and through an alias,
// over core/cmp's impls for scalars and the program's own impl for Q. The
// checker refused every such coercion, and an impl was matched to the trait by
// its simple name alone (#10816).
const dynImportedTraitSrc = `import "core/cmp";
import "core/cmp" as c;
import "std/i32";
struct Q { x: i32 }
impl cmp.Display for Q { function to_string(self: Self): string { return "Q" + self.x.to_string(); } }
function render(xs: dyn cmp.Display[]): string {
    let out: string = "";
    for x in xs { out = out + x.to_string() + ";"; }
    return out;
}
function main(): i32 {
    let xs: dyn cmp.Display[] = [42, "hi", true, Q { x: 7 }];
    let d: dyn c.Display = Q { x: 12 };
    print(render(xs) + " " + d.to_string());
    return 0;
}
`

func dynImportedTraitWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, dynImportedTraitSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostDynImportedTraitIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := dynImportedTraitWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", dynImportedTraitSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostDynImportedTraitIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := dynImportedTraitWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", dynImportedTraitSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostDynImportedTraitWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := dynImportedTraitWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", dynImportedTraitSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

// dynF32ImplementorSrc dispatches over a trait f32 implements without ever
// boxing an f32. The wasm dispatch still emits the f32 arm, and read its
// receiver as an i32 where the method takes the f64 this backend holds an f32
// in, so the module failed validation.
const dynF32ImplementorSrc = `import "std/i32";
import "std/float";
trait Show { function show(self: Self): string; }
impl Show for i32 { function show(self: Self): string { return self.to_string(); } }
impl Show for f32 { function show(self: Self): string { return self.to_string(); } }
function main(): i32 {
    let xs: dyn Show[] = [41, 42];
    let out: string = "";
    for d in xs { out = out + d.show() + ";"; }
    print(out);
    return 0;
}
`

func TestSelfHostDynF32ImplementorWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want, code := runInterp(t, dynF32ImplementorSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", dynF32ImplementorSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
