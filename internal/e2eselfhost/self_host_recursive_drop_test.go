package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// __sem_drop_<T> released an enum's self-typed payload by calling itself, so
// the last reference to a 300k-node list overflowed the stack (#9046). A
// variant's last payload of the enum's own type is now the next turn of a
// loop in the helper. The shared tail checks the loop stops at a node another
// list still holds, and the tree that a non-tail child still recurses.
const recursiveDropSrc = `enum L { Nil, Cons(i32, L) }
enum T { Leaf, Node(T, i32, T) }
@noinline
function build(n: i32): L {
    var l: L = Nil;
    var i: i32 = 0;
    while (i < n) { l = Cons(i, l); i = i + 1; }
    return l;
}
@noinline
function head(l: L): i32 {
    match (l) { Cons(h, t) => { return h; }, Nil => { return 0 - 1; } }
}
@noinline
function mid(t: T): i32 {
    match (t) { Node(a, v, b) => { return v; }, Leaf => { return 0 - 1; } }
}
function main(): i32 {
    var shared: L = build(1000);
    var a: L = Cons(7, shared);
    var b: L = Cons(9, shared);
    var long: L = build(300000);
    var tr: T = Node(Node(Leaf, 1, Leaf), 2, Node(Leaf, 3, Node(Leaf, 4, Leaf)));
    return head(a) + head(b) + head(shared) % 101 + head(long) % 7 + mid(tr);
}
`

// 7 + 9 + 999 % 101 + 299999 % 7 + 2.
const recursiveDropWant = 7 + 9 + 999%101 + 299999%7 + 2

func writeRecursiveDropSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recursive_drop.fern")
	if err := os.WriteFile(path, []byte(recursiveDropSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostRecursiveDropX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeRecursiveDropSrc(t)
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(strings.TrimSuffix(mode, "=1"), func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
			if exit != recursiveDropWant || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, recursiveDropWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostRecursiveDropArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	asm, err := os.ReadFile(cli.emit(t, writeRecursiveDropSrc(t), "arm64-linux", "FERN_LEAKCHECK=1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "recursive_drop", string(asm)))
	var eb strings.Builder
	cmd.Stderr = &eb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != recursiveDropWant {
		t.Fatalf("exit = %d, want %d\n%s", code, recursiveDropWant, eb.String())
	}
	assertBalancedCensus(t, eb.String())
}

func TestSelfHostRecursiveDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm recursive drop")
	}
	cli := buildSelfHostCLI(t)
	stderr, exit := runWasmCensus(t, cli.emit(t, writeRecursiveDropSrc(t), "wasm32-wasi", "FERN_LEAKCHECK=1"))
	if exit != recursiveDropWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, recursiveDropWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
