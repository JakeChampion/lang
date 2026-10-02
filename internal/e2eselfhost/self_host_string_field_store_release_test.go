package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A string local stored into a struct literal at its last use, inside a loop,
// leaked a box per iteration on the AST lowering (#10192). The store retains
// there, because only a top-level move elides the retain, but the release gate
// asked the loop-inclusive move analysis and read the store as a move, so the
// local's own release never came. Beside it: the same store with a later read,
// a string[] field, and the top-level store a callee makes.
const stringFieldStoreSrc = `import "std/i32";
struct Box { s: string }
struct Bag { s: string[] }
function top(i: i32): i32 {
    let t: string = "t" + i.to_string();
    let x: Box = Box { s: t };
    return x.s.len();
}
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let a: string = "a" + i.to_string();
        let x: Box = Box { s: a };
        n = n + x.s.len();
        let b: string = "b" + i.to_string();
        let y: Box = Box { s: b };
        n = n + y.s.len() + b.len();
        let c: string[] = ["c" + i.to_string()];
        let z: Bag = Bag { s: c };
        n = n + z.s.len();
        n = n + top(i);
        i = i + 1;
    }
    return n % 251;
}
`

func writeStringFieldStoreSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "string_field_store.fern")
	if err := os.WriteFile(path, []byte(stringFieldStoreSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Per round: a is 2 or 3 bytes, b twice that, c one element, top like a:
// 10 x (2 + 4 + 1 + 2) + 90 x (3 + 6 + 1 + 3) = 1260.
const stringFieldStoreWant = 1260 % 251

func TestSelfHostStringFieldStoreReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStringFieldStoreSrc(t)
	bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != stringFieldStoreWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, stringFieldStoreWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostStringFieldStoreReleaseSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStringFieldStoreSrc(t)
	bin := cli.x86Binary(t, src, "FERN_SANITIZE=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != stringFieldStoreWant || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, stringFieldStoreWant, stderr)
	}
}

func TestSelfHostStringFieldStoreReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm string field store release")
	}
	cli := buildSelfHostCLI(t)
	src := writeStringFieldStoreSrc(t)
	wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1")
	stderr, exit := runWasmCensus(t, wat)
	if exit != stringFieldStoreWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, stringFieldStoreWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
