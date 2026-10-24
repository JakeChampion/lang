package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var pubUseSelfHostProgram = map[string]string{
	"helpers.fern": "" +
		"pub struct Point { x: i32, y: i32 }\n" +
		"pub enum Shape { Sq(Point), Nothing }\n" +
		"pub function add5(n: i32): i32 { return n + 5; }\n" +
		"pub function make_point(x: i32, y: i32): Point { return Point { x: x, y: y }; }\n" +
		"pub function area(p: Point): i32 { return p.x * p.y; }\n",
	"facade.fern": "pub use \"./helpers\".{Point, Shape, add5, make_point, area};\n",
	"main.fern": "" +
		"import \"./facade\";\n" +
		"function main(): i32 {\n" +
		"    let p: facade.Point = facade.make_point(6, 7);\n" +
		"    let s: facade.Shape = Sq(p);\n" +
		"    match (s) {\n" +
		"        Sq(q) => { return facade.add5(facade.area(q)); },\n" +
		"        _ => { return 0; }\n" +
		"    }\n" +
		"}\n",
}

func writeSelfHostCLIProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	return dir
}

func TestSelfHostPubUseModloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "driver")

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

func TestWasmSelfHostPubUseReexport(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host pub-use wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
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
		t.Fatalf("fern -target wasm32-wasi on pub-use program: %v (%d bytes)", err, len(wat))
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
