package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// pubUseSelfHostProgram is a 3-module re-export program, the self-host
// mirror of the native internal/e2e/pub_use_test.go fixtures:
//
//   - helpers defines the real symbols (a struct, two functions),
//   - facade re-exports them via `pub use "./helpers".{…}`,
//   - main imports facade and uses the re-exported names through the
//     facade: a re-exported TYPE in a `var` annotation (facade.Point)
//     and re-exported VALUES in qualified calls (facade.make_point /
//     facade.area / facade.add5).
//
// area(6,7) = 42, add5(42) = 47, so a correct compile exits 47. The
// decisive property: `facade.add5` etc. must resolve to the original
// `helpers__add5` (the symbol the bundled program defines), NOT
// `facade__add5` (which has no decl) — the self-host `pub use` redirect
// built in flatten.bundle. See examples/self_host/flatten.fern and
// docs/PRELUDE-TO-MODULES.md.
var pubUseSelfHostProgram = map[string]string{
	"helpers.fern": "" +
		"pub struct Point { x: i32, y: i32 }\n" +
		"pub function add5(n: i32): i32 { return n + 5; }\n" +
		"pub function make_point(x: i32, y: i32): Point { return Point { x: x, y: y }; }\n" +
		"pub function area(p: Point): i32 { return p.x * p.y; }\n",
	"facade.fern": "pub use \"./helpers\".{Point, add5, make_point, area};\n",
	"main.fern": "" +
		"import \"./facade\";\n" +
		"function main(): i32 {\n" +
		"    var p: facade.Point = facade.make_point(6, 7);\n" +
		"    return facade.add5(facade.area(p));\n" +
		"}\n",
}

// writeSelfHostCLIProject lays out every source the unified self-host CLI
// driver (fern.fern) needs, derived from the driver's own imports rather than
// listed here. The list this replaces went stale the moment fern.fern gained
// an import, which is the failure CopySelfHostDriver exists to make
// impossible.
func writeSelfHostCLIProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	return dir
}

// TestSelfHostPubUseModloadX86_64 compiles the 3-module re-export program
// through the import-driven self-host IR driver (asm_modload_run.fern):
// real on-disk module loading (the loader follows `import` AND the
// `pub use` re-export target into the graph), flatten.bundle's re-export
// redirect, then the IR codegen path to x86-64. Asserts the program exits
// 47 — the self-host parity gate for issue #3136 part 2 on x86-64.
func TestSelfHostPubUseModloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	progDir := t.TempDir()
	for name, src := range pubUseSelfHostProgram {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(progDir, "main.fern")
	progAsm := runDriverFile(t, runner, driverBin, entry)
	if len(progAsm) == 0 {
		t.Fatal("driver emitted 0 bytes for the pub-use program")
	}
	progBin := buildBin(t, gcc, progDir, "prog", string(progAsm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 47 {
		t.Errorf("pub-use program exited %d, want 47", code)
	}
}

// TestWasmSelfHostPubUseReexport compiles the same 3-module re-export
// program through the unified self-host CLI driver (fern.fern) targeting
// wasm: real on-disk module loading + flatten.bundle's `pub use` redirect,
// then wasm codegen. The emitted WAT runs under wasmtime and must exit 47 —
// the self-host parity gate for issue #3136 part 2 on wasm. Named `TestWasm*`
// so it lands in the wasmtime-provisioned wasm e2e workflow rather than the
// (wasmtime-less) self-host shards, where it would only ever SKIP.
func TestWasmSelfHostPubUseReexport(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host pub-use wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// fern.fern takes a host filesystem path as argv; a qemu runner
		// wouldn't see the same path. Native-only (CI runs wasm on x86).
		t.Skip("self-host pub-use wasm e2e runs only natively (argv path)")
	}
	dir := writeSelfHostCLIProject(t)
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	progDir := t.TempDir()
	for name, src := range pubUseSelfHostProgram {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(progDir, "main.fern")

	wat, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", entry).Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("fern -target wasm on pub-use program: %v (%d bytes)", err, len(wat))
	}
	watPath := filepath.Join(progDir, "prog.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 47 {
		t.Errorf("pub-use wasm program exited %d, want 47", code)
	}
}
