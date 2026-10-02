package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// forHeaderBindingSrc reaches a `for` header's bindings from the two places the
// typed lowering did not bind them (#10759): a nested value pattern in a map
// loop, and a `defer` in the loop body, whose replay runs after the body has
// closed. Each defer run reads its own iteration's binding. The counted values
// (strings, a string-valued map) put the census leg to work.
const forHeaderBindingSrc = `import "core/map";

function nested_map_value(): i32 {
    let t: Map[i32, (i32, string)] = map_new(4);
    t = t.insert(7, (10, "abc"));
    t = t.insert(8, (20, "de"));
    let sum: i32 = 0;
    for (k, (lo, s)) in t { sum = sum + k + lo + s.len(); }
    return sum;
}

function defer_map_value(a: Cell[i32]): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2 };
    for (k, v) in m {
        defer a.set(a.get() + v + k.len());
    }
    return a.get();
}

function defer_element(a: Cell[i32]): i32 {
    let xs: string[] = ["x", "yy", "zzz"];
    for x in xs {
        defer a.set(a.get() + x.len());
    }
    return a.get();
}

function defer_pair(a: Cell[i32]): i32 {
    let xs: (i32, string)[] = [(1, "p"), (3, "qq")];
    for (n, s) in xs {
        defer a.set(a.get() + n * s.len());
    }
    return a.get();
}

function main(): i32 {
    if (nested_map_value() != 50) { return 1; }
    let a: Cell[i32] = cell_new(0);
    if (defer_map_value(a) != 5 || a.get() != 5) { return 2; }
    let b: Cell[i32] = cell_new(0);
    if (defer_element(b) != 6 || b.get() != 6) { return 3; }
    let c: Cell[i32] = cell_new(0);
    if (defer_pair(c) != 7 || c.get() != 7) { return 4; }
    return 42;
}
`

func TestSelfHostForHeaderBindingIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if got := interpExit(t, buildLangBinForInterp(t), forHeaderBindingSrc); got != 42 {
		t.Fatalf("interpreter = %d, want 42", got)
	}
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", forHeaderBindingSrc)); code != 42 {
		t.Fatalf("exit %d, want 42 (the code names the failing function)", code)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", forHeaderBindingSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 42 {
		t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostForHeaderBindingIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", forHeaderBindingSrc)); code != 42 {
		t.Fatalf("arm64: exit %d, want 42 (the code names the failing function)", code)
	}
}

func TestSelfHostForHeaderBindingWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", forHeaderBindingSrc)); code != 42 {
		t.Fatalf("wasm: exit %d, want 42 (the code names the failing function)", code)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", forHeaderBindingSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 42 {
		t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
