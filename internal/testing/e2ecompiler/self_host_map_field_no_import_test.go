package e2ecompiler

import (
	"strings"
	"testing"
)

const mapFieldNoImportSrc = `enum E { A(i32), B(Map[string, i32]) }
struct Holder { n: i32, counts: Map[i32, i32] }
function pick(h: Holder): i32 { return h.n; }
function main(): i32 {
    let e: E = A(1);
    match (e) {
        A(x) => { return x; },
        _ => { return 0; }
    }
}
`

func mapFieldNoImportCheck(t *testing.T, target, text string, code int) {
	t.Helper()
	if code != 1 {
		t.Fatalf("%s: exit %d, want 1", target, code)
	}
	if strings.Contains(text, "__map_drop") || strings.Contains(text, "__map_") {
		t.Fatalf("%s: the emitted program names a core/map helper, which nothing imported", target)
	}
}

func TestSelfHostMapFieldNoImportX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	asm := cli.emit(t, "x86-64-linux", mapFieldNoImportSrc)
	code, _ := cli.runX86(t, asm)
	mapFieldNoImportCheck(t, "x86-64", asm, code)
}

func TestSelfHostMapFieldNoImportArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	asm := cli.emit(t, "arm64-linux", mapFieldNoImportSrc)
	code, _ := runArm64(t, gcc, qemu, asm)
	mapFieldNoImportCheck(t, "arm64", asm, code)
}

func TestSelfHostMapFieldNoImportWasm(t *testing.T) {
	cli := newStrictCLI(t)
	wat := cli.emit(t, "wasm32-wasi", mapFieldNoImportSrc)
	code, _ := runWasm(t, wat)
	mapFieldNoImportCheck(t, "wasm", wat, code)
}
