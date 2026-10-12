package e2ecompiler

import "testing"

// enumMapShakeSrc names an enum only through a variant that holds no map, in a
// body. `O` holds a `Map`, and `J`'s drop walks it, so the tree shaker has to
// keep core/map's routed helpers; it dropped them, and the x86-64 assembler
// refused the module for calling `__map_drop_boxes_impl` (#10772).
const enumMapShakeSrc = `import "std/i32";
enum J { S(string), O(Map[string, J]) }
function main(): i32 {
    let j: J = S("hi");
    match (j) { S(s) => { print(s.len().to_string()); }, _ => { print("other"); } }
    return 0;
}
`

func enumMapShakeWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, enumMapShakeSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostEnumMapShakeIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := enumMapShakeWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", enumMapShakeSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostEnumMapShakeIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := enumMapShakeWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", enumMapShakeSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostEnumMapShakeWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := enumMapShakeWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", enumMapShakeSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
