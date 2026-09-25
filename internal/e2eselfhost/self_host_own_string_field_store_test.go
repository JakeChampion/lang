package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An `own` string parameter stored into a struct literal leaked a box per call
// on the AST lowering (#10268). The store retains the parameter, since a
// construction moves only a local, but the release gate read the store as an
// escape and withheld the parameter's exit release. The literal is returned,
// bound and returned, and bound to a local that is read and dropped; the last
// function stores nothing.
const ownStringFieldStoreSrc = `import "std/i32";
struct Box { s: string }
function store(own s: string): Box { return Box { s: s }; }
function via(own s: string): Box { var b: Box = Box { s: s }; return b; }
function peek(own s: string): i32 { var b: Box = Box { s: s }; return b.s.len(); }
function plain(own s: string): i32 { return s.len(); }
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var b: Box = store("a" + i.to_string());
        n = n + b.s.len();
        var c: Box = via("b" + i.to_string());
        n = n + c.s.len();
        n = n + peek("c" + i.to_string());
        n = n + plain("d" + i.to_string());
        i = i + 1;
    }
    return n % 101;
}
`

// 4 x (10 x 2 + 90 x 3) = 1160.
const ownStringFieldStoreWant = 1160 % 101

var ownStringFieldStoreLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
}

func writeOwnStringFieldStoreSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "own_string_field_store.fern")
	if err := os.WriteFile(path, []byte(ownStringFieldStoreSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostOwnStringFieldStoreX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeOwnStringFieldStoreSrc(t)
	for _, lw := range ownStringFieldStoreLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != ownStringFieldStoreWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, ownStringFieldStoreWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostOwnStringFieldStoreSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeOwnStringFieldStoreSrc(t)
	for _, lw := range ownStringFieldStoreLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != ownStringFieldStoreWant || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, ownStringFieldStoreWant, stderr)
			}
		})
	}
}

func TestSelfHostOwnStringFieldStoreWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm own string field store")
	}
	cli := buildSelfHostCLI(t)
	src := writeOwnStringFieldStoreSrc(t)
	for _, lw := range ownStringFieldStoreLowerings {
		t.Run(lw.name, func(t *testing.T) {
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWasmCensus(t, wat)
			if exit != ownStringFieldStoreWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, ownStringFieldStoreWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
