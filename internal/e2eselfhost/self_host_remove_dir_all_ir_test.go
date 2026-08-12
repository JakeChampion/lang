package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRemoveDirAllIR pins `remove_dir_all(path)` lowering on the self-host
// x86-64 IR path (#2649). remove_dir_all is now a Fern runtime function
// (rt_src_remove_dir_all): a best-effort recursive rm -rf that probes the target
// with openat to classify it, delegates directory enumeration to the (Fern)
// read_dir, recurses into each child, then rmdirs. This test stages a nested tree
// on disk (two subdir levels), has the compiled program remove_dir_all it
// (asserting None), then remove_dir_all a missing path (asserting None — rm -rf
// ignores a missing target), and exits 0 only if both resolve. The Go side then
// confirms the whole tree is gone (the recursion really removed it) and that the
// IR path was taken (`call __fn___fern_remove_dir_all` in the asm).
func TestSelfHostRemoveDirAllIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Stage a nested tree: rda/{f1.txt, f2.txt, sub/{g.txt, sub2/{h.txt}}}.
	// remove_dir_all must recurse through both subdir levels.
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
	if _, err := os.Stat(rda); !os.IsNotExist(err) {
		t.Errorf("rda still present after remove_dir_all (stat err = %v)", err)
	}
}

// TestSelfHostRemoveDirAllIRWasm pins `remove_dir_all(path)` lowering on the wasm
// IR path. remove_dir_all is a recursive rm -rf returning Result[(), IoError] (Ok(()) on
// success / Some(IoError) on failure); it was a wasm_eligible exclusion (no wasm
// IR runtime), so any module using it fell back to the legacy AST emitter. It now
// lowers to op_remove_dir_all -> a fresh recursive wasm runtime
// ($__fern_remove_dir_all: preview1 path_open(O_DIRECTORY) to classify dir vs
// file, an fd_readdir cookie loop to enumerate children, path_unlink_file for
// files / recurse + path_remove_directory for subdirs). It composes read_dir's
// fd_readdir loop with remove_file's path_unlink_file, adding recursion. The
// program removes a populated nested tree (asserting None), then a missing path
// (asserting None — rm -rf ignores a missing target), exiting 0 only if both
// resolve; the test also confirms the tree is actually gone on disk and that the
// IR path was taken (`call $__fern_remove_dir_all` in the WAT).
func TestSelfHostRemoveDirAllIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host remove_dir_all wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// Stage a nested tree under the preopen:
	//   rda_dir/{f1.txt, f2.txt, sub/{g.txt, sub2/{h.txt}}}
	// remove_dir_all must recurse through both subdir levels (files unlinked,
	// dirs rmdir'd after emptying) and leave nothing behind.
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
	// The whole tree must be gone (the recursion really removed it).
	if _, err := os.Stat(rda); !os.IsNotExist(err) {
		t.Errorf("rda_dir still present after remove_dir_all (stat err = %v)", err)
	}
}
