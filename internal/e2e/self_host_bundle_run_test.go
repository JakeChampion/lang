package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// bundle_run.fern is the multi-module self-host driver: it reads a
// marked bundle of modules from stdin, flattens + merges them via
// flatten.bundle, and emits x86-64 asm for the merged Module.
//
// This test exercises a THREE-module bundle with a transitive,
// cross-module reference graph:
//
//	a:    add(x, y)            = x + y
//	b:    import a; add_one(x) = a.add(x, 1)
//	main: import a, b; main()  = b.add_one(a.add(2, 3))   // = 6
//
// It compiles bundle_run.fern, pipes the bundle in to get the merged
// program's asm, then assembles + runs that and asserts it exits 6.
// Proves the self-host pipeline resolves a multi-import graph
// (including an import that itself references another import) down to
// one working binary.
func TestSelfHostBundleRunX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run.fern"))
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

	bundle := "" +
		"///MODULE a\n" +
		"pub function add(x: i32, y: i32): i32 { return x + y; }\n" +
		"function main(): i32 { return 0; }\n" +
		"///MODULE b\n" +
		"import \"./a\";\n" +
		"pub function add_one(x: i32): i32 { return a.add(x, 1); }\n" +
		"function main(): i32 { return 0; }\n" +
		"///MODULE main\n" +
		"import \"./a\";\n" +
		"import \"./b\";\n" +
		"function main(): i32 { return b.add_one(a.add(2, 3)); }\n"

	var dcmd *exec.Cmd
	if len(runner) == 0 {
		dcmd = exec.Command(driverBin)
	} else {
		dcmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	dcmd.Stdin = bytes.NewReader([]byte(bundle))
	mergedAsm, err := dcmd.Output()
	if err != nil {
		t.Fatalf("run driver: %v", err)
	}
	if len(mergedAsm) == 0 {
		t.Fatal("driver emitted 0 bytes for the merged bundle")
	}

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
	if code := mcmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("merged 3-module program exited %d, want 6 (b.add_one(a.add(2,3)))", code)
	}
}
