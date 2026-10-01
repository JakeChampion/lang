package e2eselfhost

import "testing"

// builtinEnumMapShakeSrc names a builtin enum only through a variant that
// holds no map, in a body. `JObject` holds a `Map`, and `JsonValue`'s drop
// walks it, so the tree shaker has to keep core/map's routed helpers; it
// dropped them, and the x86-64 assembler refused the module for calling
// `__map_drop_boxes_impl` (#10772).
const builtinEnumMapShakeSrc = `import "std/json";
import "std/i32";
function main(): i32 {
    var j: JsonValue = JString("hi");
    match (j) { JString(s) => { print(s.len().to_string()); }, _ => { print("other"); } }
    return 0;
}
`

func builtinEnumMapShakeWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, builtinEnumMapShakeSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostBuiltinEnumMapShakeIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := builtinEnumMapShakeWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", builtinEnumMapShakeSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostBuiltinEnumMapShakeIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := builtinEnumMapShakeWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", builtinEnumMapShakeSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostBuiltinEnumMapShakeWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := builtinEnumMapShakeWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", builtinEnumMapShakeSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
