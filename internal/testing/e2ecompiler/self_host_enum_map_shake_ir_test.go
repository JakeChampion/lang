package e2ecompiler

import "testing"

var enumMapShakeSrcs = []struct{ name, src string }{
	{"direct", `import "std/i32";
enum J { S(string), O(Map[string, J]) }
function main(): i32 {
    let j: J = S("hi");
    match (j) { S(s) => { print(s.len().to_string()); }, _ => { print("other"); } }
    return 0;
}
`},
	{"nested", `import "std/i32";
import "core/map";
enum Inner { V0, V1(boolean, Map[i32, i32]) }
enum Outer { W(Inner), N(Option[i32]) }
function main(): i32 {
    let o: Outer = N(Some(3));
    match (o) { N(Some(n)) => { print(n.to_string()); }, _ => { print("other"); } }
    return 0;
}
`},
}

func eachEnumMapShake(t *testing.T, run func(t *testing.T, src, want string)) {
	for _, c := range enumMapShakeSrcs {
		t.Run(c.name, func(t *testing.T) {
			want, code := runInterp(t, c.src)
			if code != 0 || want == "" {
				t.Fatalf("interpreter: exit %d, stdout %q", code, want)
			}
			run(t, c.src, want)
		})
	}
}

func TestSelfHostEnumMapShakeIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	eachEnumMapShake(t, func(t *testing.T, src, want string) {
		if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); code != 0 || out != want {
			t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
		}
	})
}

func TestSelfHostEnumMapShakeIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	eachEnumMapShake(t, func(t *testing.T, src, want string) {
		if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src)); code != 0 || out != want {
			t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
		}
	})
}

func TestSelfHostEnumMapShakeWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	eachEnumMapShake(t, func(t *testing.T, src, want string) {
		if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", src)); code != 0 || out != want {
			t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
		}
	})
}
