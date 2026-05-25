package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// End-to-end proof of the self-host module-flattening pipeline
// (flatten.fern bricks 1–3). bundle_demo.fern takes a TWO-module
// program — an imported module `a` exporting `add`, and an entry that
// calls `a.add(2, 3)` — flattens + mangles + merges it into one
// Module via flatten.bundle, then lowers it to x86-64 asm with
// asm.emit_module and prints it.
//
// This test compiles bundle_demo.fern on the host, runs it to obtain
// the asm for the MERGED program, assembles + runs that, and asserts
// the merged binary exits 5 (== a.add(2, 3)). That's the end-to-end
// demonstration that multi-module Lang source — the shape the
// compiler itself has — lowers to a single working binary through the
// self-host pipeline.
func TestSelfHostBundleDemoX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_demo.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build the demo driver.
	prog, _, err := modload.Load(filepath.Join(dir, "bundle_demo.fern"))
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
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	// Run the driver to get the merged program's asm.
	var dcmd *exec.Cmd
	if len(runner) == 0 {
		dcmd = exec.Command(driverBin)
	} else {
		dcmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	mergedAsm, err := dcmd.Output()
	if err != nil {
		t.Fatalf("run driver: %v", err)
	}
	if len(mergedAsm) == 0 {
		t.Fatal("driver emitted 0 bytes of asm for the merged program")
	}

	// Assemble + run the merged program; it must exit 5.
	mergedAsmPath := filepath.Join(dir, "merged.s")
	mergedBin := filepath.Join(dir, "merged")
	if err := os.WriteFile(mergedAsmPath, mergedAsm, 0o644); err != nil {
		t.Fatalf("write merged asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", mergedAsmPath, "-o", mergedBin).CombinedOutput(); err != nil {
		t.Fatalf("merged gcc: %v\n%s\n--- asm ---\n%s", err, out, mergedAsm)
	}
	var mcmd *exec.Cmd
	if len(runner) == 0 {
		mcmd = exec.Command(mergedBin)
	} else {
		mcmd = exec.Command(runner[0], append(runner[1:], mergedBin)...)
	}
	_, _ = mcmd.CombinedOutput()
	if code := mcmd.ProcessState.ExitCode(); code != 5 {
		t.Errorf("merged 2-module program exited %d, want 5 (a.add(2,3))", code)
	}
}
