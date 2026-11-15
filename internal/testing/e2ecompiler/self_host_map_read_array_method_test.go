package e2ecompiler

import "testing"

// A local takes its type from the read of a map whose values are arrays of
// a struct. A method of std/array on that local needs the instance for the
// struct, so the monomorphiser must know the type of `get_or` and `get`.
const mapReadArrayMethodSrc = `import "std/array";
import "core/map";
struct D { name: string, kind: i32 }
function main(): i32 {
    let by: Map[string, D[]] = Map { };
    by = by.insert("h", [D { name: "x", kind: 1 }, D { name: "y", kind: 2 }]);
    let list = by.get_or("h", []);
    let wanted = "y";
    let current = (d: D): boolean => d.kind == 2 && d.name == wanted;
    let code = list.filter((d: D): boolean => d.kind == 1).len();
    if (list.any(current)) {
        code += 10;
    }
    if let Some(got) = by.get("h") {
        code += 50 * got.map((d: D): string => d.name).len();
    }
    return code;
}
`

func TestSelfHostMapReadArrayMethodX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", mapReadArrayMethodSrc))
	if code != 111 {
		t.Fatalf("x86-64: exit %d, want 111", code)
	}
}

func TestSelfHostMapReadArrayMethodArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", mapReadArrayMethodSrc))
	if code != 111 {
		t.Fatalf("arm64: exit %d, want 111", code)
	}
}

func TestSelfHostMapReadArrayMethodWasm(t *testing.T) {
	cli := newStrictCLI(t)
	code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", mapReadArrayMethodSrc))
	if code != 111 {
		t.Fatalf("wasm: exit %d, want 111", code)
	}
}
