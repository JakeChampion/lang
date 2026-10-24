package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
