package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/wasm/component"
)

// runNativeWasmCli compiles src through the native preview-2 pipeline
// (wasmbin core with Preview2WASI + SynthCliRun → component.Compose's
// cli/run wrapper, no wasm-tools, no preview-1 adapter) and runs the
// component under wasmtime, returning the process exit code. main()'s
// i32 return lowers through wasi:cli/run as result<_, _>: 0 → exit 0,
// non-zero → exit 1. The programs here are import-free (pure
// computation), so the import-free cli/run builder composes them
// directly.
//
// This exercises the real native memory layout (heap at ~1024), where
// the rc helpers' low-address guard lives — the path the preview-1
// adapter used to mask.
func runNativeWasmCli(t *testing.T, src string) int {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	comp := component.BuildWasiCliRunComponent(core, "_lang_run")
	compPath := filepath.Join(dir, "prog.component.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", compPath)
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

// TestWASMNativeAliasedArraySetCoW guards the fix for the native-path
// copy-on-write bug: __fern_rc_inc's low-address guard (0x10000) used
// to skip every increment on the native heap layout (objects at
// ~1024), so `var ys = xs` never bumped xs's refcount and `ys.with(...)`
// took the rc==1 mutate-in-place fast path, corrupting xs. The
// preview-1 adapter's higher heap base masked this; the native
// `-target wasm` path (and now the e2e suite) exercises it directly.
func TestWASMNativeAliasedArraySetCoW(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"array_set", `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`},
		{"array_index_assign", `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys = ys.with(1, 999);
    if (xs[1] != 20) { return 1; }
    if (ys[1] != 999) { return 2; }
    return 0;
}`},
		{"map_set", `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    var n = m;
    n = n.insert("a", 999);
    if (m.get_or("a", -1) != 1) { return 1; }
    if (n.get_or("a", -1) != 999) { return 2; }
    return 0;
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runNativeWasmCli(t, c.src); got != 0 {
				t.Errorf("native -target wasm exit %d, want 0 (aliased mutation must copy)", got)
			}
		})
	}
}

// TestWASMNativeRcDecReclaim guards the dec-side companion to the CoW
// inc fix: the dec/free/reclamation helpers' low-address guard also used
// 0x10000, so on the native heap (~1024) every dec was skipped — drops
// never balanced their inc and the freelist never filled. The
// preview-1 adapter (heap above 64 KiB) fired dec normally, masking the
// asymmetry; these cases run the native cli/run path where inc fires but
// dec, pre-fix, did not. "drop_dec_fires" is the distinguishing case:
// pre-fix __rc_get(inner) reads 2 after the drop (inc fired, dec did
// not) and the program returns 1; post-fix it reads 1 and returns 0.
func TestWASMNativeRcDecReclaim(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		// Drop must dec the nested element back to its pre-call rc.
		{"drop_dec_fires", `function consume(inner: u8[]): i32 {
    var outer: u8[][] = [inner];
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1);  // 0 iff drop balanced the inc
}`},
		// Nesting fresh + aliased elements and dropping the outer
		// array yields correct values and no over-release.
		{"drop_no_over_release", `function build(): i32 {
    var inner: i32[] = [1, 2, 3];
    var a: i32[][] = [inner];        // aliased element (inc'd)
    var b: i32[][] = [[4, 5], [6]];  // fresh elements (not inc'd)
    return a[0][1] + b[1][0];        // 2 + 6
}
function main(): i32 {
    return (build() - 8) + __rc_underflow_count();
}`},
		// Overwrite-set frees the prior value; no over-release.
		{"dec_on_overwrite", `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    var i: i32 = 0;
    while (i < 64) { m = m.insert(7, [i, i + 1, i + 2]); i = i + 1; }
    var v: i32[] = m.get_or(7, []);
    return (v[2] - 65) + __rc_underflow_count();
}`},
		// Freelist reuse: heavy alloc churn stays correct and bounded
		// (a corrupted freelist would trap or mis-read here).
		{"churn_freelist_reuse", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var xs: i32[] = [i, i + 1, i + 2, i + 3];
        acc = acc + xs[3] - xs[0];   // always 3
        i = i + 1;
    }
    return (acc - 1500) + __rc_underflow_count();  // 500 * 3
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runNativeWasmCli(t, c.src); got != 0 {
				t.Errorf("native -target wasm exit %d, want 0 (dec must fire on the native heap)", got)
			}
		})
	}
}
