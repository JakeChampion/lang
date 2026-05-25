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
	if err := constfold.Fold(prog); err != nil {
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
// ~1024), so `var ys = xs` never bumped xs's refcount and `ys.set(...)`
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
    ys = ys.set(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`},
		{"array_index_assign", `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;
    ys[1] = 999;
    if (xs[1] != 20) { return 1; }
    if (ys[1] != 999) { return 2; }
    return 0;
}`},
		{"map_set", `function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.set("a", 1);
    var n = m;
    n = n.set("a", 999);
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
