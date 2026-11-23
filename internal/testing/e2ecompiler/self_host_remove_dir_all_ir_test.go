package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// rdaLinkTree adds links that remove_dir_all must remove and not follow: a
// link to a directory outside the tree, and a link that points at itself.
// It returns the file in the outside directory, which must survive.
func rdaLinkTree(t *testing.T, root, tree string) string {
	t.Helper()
	keep := filepath.Join(root, "keep")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", keep, err)
	}
	precious := filepath.Join(keep, "file.txt")
	if err := os.WriteFile(precious, []byte("precious"), 0o644); err != nil {
		t.Fatalf("write %s: %v", precious, err)
	}
	rel, err := filepath.Rel(tree, keep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(tree, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink("loop", filepath.Join(tree, "sub", "loop")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return precious
}

func checkRdaLinkTree(t *testing.T, tree, precious string) {
	t.Helper()
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("%s still present after remove_dir_all (lstat err = %v)", tree, err)
	}
	if b, err := os.ReadFile(precious); err != nil || string(b) != "precious" {
		t.Errorf("remove_dir_all followed a link: %s = %q, %v", precious, b, err)
	}
}

func TestSelfHostRemoveDirAllIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	rda := filepath.Join(dir, "rda")
	for _, d := range []string{filepath.Join(rda, "sub", "sub2")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range []string{
		filepath.Join(rda, "f1.txt"), filepath.Join(rda, "f2.txt"),
		filepath.Join(rda, "sub", "g.txt"), filepath.Join(rda, "sub", "sub2", "h.txt"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	precious := rdaLinkTree(t, dir, rda)

	src := fmt.Sprintf(`function main(): i32 {
    match (remove_dir_all(%q)) {
        Err(_) => { return 1; },
        Ok(_) => {
            match (remove_dir_all(%q)) {
                Err(_) => { return 2; },
                Ok(_) => { return 0; },
            }
        },
    }
}`, rda, rda+"-missing")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("call __fn___fern_remove_dir_all")) {
		t.Fatal("remove_dir_all did not reach the Fern IR runtime (no call __fn___fern_remove_dir_all in asm)")
	}
	progBin := buildBin(t, gcc, dir, "rda_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("remove_dir_all IR program exited %d, want 0 (nested tree removed -> None, missing -> None)", code)
	}
	checkRdaLinkTree(t, rda, precious)
}

func TestSelfHostRemoveDirAllArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	rda := filepath.Join(dir, "rda")
	if err := os.MkdirAll(filepath.Join(rda, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rda, "sub", "g.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	precious := rdaLinkTree(t, dir, rda)

	src := fmt.Sprintf(`function main(): i32 {
    match (remove_dir_all(%q)) {
        Err(_) => { return 1; },
        Ok(_) => { return 0; },
    }
}`, rda)
	driverArgs := []string{"-target", "arm64-linux"}
	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, driverArgs...)
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), driverArgs...)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fern_remove_dir_all")) {
		t.Fatal("remove_dir_all did not reach the arm64 IR runtime (no __fern_remove_dir_all in asm)")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "rda_arm64", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("arm64 program did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("remove_dir_all arm64 IR program exited %d, want 0", code)
	}
	checkRdaLinkTree(t, rda, precious)
}

func TestSelfHostRemoveDirAllIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host remove_dir_all wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	rda := filepath.Join(dir, "rda_dir")
	mustMk := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	mustWr := func(p string) {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustMk(filepath.Join(rda, "sub", "sub2"))
	mustWr(filepath.Join(rda, "f1.txt"))
	mustWr(filepath.Join(rda, "f2.txt"))
	mustWr(filepath.Join(rda, "sub", "g.txt"))
	mustWr(filepath.Join(rda, "sub", "sub2", "h.txt"))
	precious := rdaLinkTree(t, dir, rda)

	const src = `function main(): i32 {
    match (remove_dir_all("rda_dir")) {
        Err(_) => { return 1; },
        Ok(_) => {
            match (remove_dir_all("rda_missing")) {
                Err(_) => { return 2; },
                Ok(_) => { return 0; },
            }
        },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_remove_dir_all")) {
		t.Fatal("remove_dir_all did not reach the wasm IR runtime path (no call $__fern_remove_dir_all in WAT)")
	}
	watFile := filepath.Join(dir, "rda_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("remove_dir_all wasm IR program exited %d, want 0 (tree removed -> None, missing -> None)\n--- WAT ---\n%s", code, wat)
	}
	checkRdaLinkTree(t, rda, precious)
}
