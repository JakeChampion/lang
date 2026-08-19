package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostReadDirIR pins `read_dir(path)` lowering on the self-host x86-64 IR
// path. read_dir lists a directory's base-name children (openat+getdents64,
// skipping . / ..) and returns Result[string[], IoError]; it had a full AST
// runtime (__fn___fern_read_dir) but no IR lowering, so any user (std/test's
// assert_eq_dir_listing) bailed the module (#3457). It now lowers to
// op_read_dir -> the Fern __fn___fern_read_dir runtime (boxing a
// string[] via __fern_arr_box). The program makes a temp dir, writes two files,
// read_dirs it, asserts the count is 2, removes the tree, and exits 0 — exercising
// temp_dir / write_file / read_dir / remove_dir_all all on the IR path. The test
// also pins that the IR runtime was reached (call __fn___fern_read_dir in the asm).
func TestSelfHostReadDirIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// temp_dir -> write 2 files -> read_dir -> assert 2 entries -> remove. Exit 0
	// only if every step succeeds and the listing has exactly 2 names.
	const src = `function main(): i32 {
    match (temp_dir("fern-readdir-ir")) {
        Ok(d) => {
            match (write_file(d + "/a.txt", "x")) { Err(_) => { return 1; }, Ok(_) => {}, }
            match (write_file(d + "/b.txt", "y")) { Err(_) => { return 2; }, Ok(_) => {}, }
            match (read_dir(d)) {
                Ok(names) => {
                    var n: i32 = names.len();
                    match (remove_dir_all(d)) { Err(_) => { return 3; }, Ok(_) => {}, }
                    if (n != 2) { return 4; }
                    return 0;
                },
                Err(_) => { return 5; },
            }
        },
        Err(_) => { return 6; },
    }
}`

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
	// The Fern-compiled symbol (#2649): read_dir is asmcore.rt_src_read_dir
	// now, so op_read_dir calls the stack-ABI __fn___fern_read_dir. The bare
	// "__fern_read_dir" this used to look for is a SUBSTRING of that, so the
	// assertion kept passing across the migration without ever proving which
	// symbol was reached — name the whole thing.
	if !strings.Contains(string(asm), "call __fn___fern_read_dir") {
		t.Fatal("read_dir did not reach the Fern IR runtime (no call __fn___fern_read_dir in asm)")
	}
	progBin := buildBin(t, gcc, dir, "readdir_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("read_dir IR program exited %d, want 0 (temp_dir+write+read_dir(==2)+remove)", code)
	}
}

// TestSelfHostReadDirIRWasm is the wasm mirror: read_dir now lowers through the
// wasm IR path too (it was a wasm_eligible exclusion). The wasm op_read_dir
// handler calls a fresh runtime ($__fern_read_dir: preview1 path_open + an
// fd_readdir cookie loop, skipping . / .., building a string[] via
// $__fern_arr_push, wrapped Result[string[], IoError]). Unlike the x86 test this
// exercises read_dir alone (temp_dir / write_file are not used — the Go side
// stages the directory): it lists a directory of three known entries (asserting
// the count and total name length, order-independent since WASI fd_readdir order
// is filesystem-defined) and a missing directory (asserting Err), under wasmtime
// with the temp dir as preopen, exiting 0 only if both resolve. Also pins that
// the IR path was taken (`call $__fern_read_dir` in the WAT).
func TestSelfHostReadDirIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_dir wasm IR e2e")
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

	// Stage a directory "rd_dir" with three entries: a.txt, b.txt, and a subdir
	// "sub" — names total 5+5+3 = 13 bytes (the order-independent check). The
	// "." / ".." entries must be filtered by the runtime.
	rd := filepath.Join(dir, "rd_dir")
	if err := os.Mkdir(rd, 0o755); err != nil {
		t.Fatalf("mkdir rd_dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rd, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rd, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rd, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	const src = `function main(): i32 {
    match (read_dir("rd_missing")) {
        Ok(_) => { return 5; },
        Err(_) => {
            match (read_dir("rd_dir")) {
                Ok(names) => {
                    if (names.len() != 3) { return 1; }
                    var total: i32 = 0;
                    var i: i32 = 0;
                    while (i < names.len()) {
                        var nm: string = names[i];
                        if (nm.len() == 0) { return 2; }
                        total = total + nm.len();
                        i = i + 1;
                    }
                    if (total != 13) { return 3; }
                    return 0;
                },
                Err(_) => { return 4; },
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
	if !bytes.Contains(wat, []byte("call $__fern_read_dir")) {
		t.Fatal("read_dir did not reach the wasm IR runtime path (no call $__fern_read_dir in WAT)")
	}
	watFile := filepath.Join(dir, "rd_prog.wat")
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
		t.Errorf("read_dir wasm IR program exited %d, want 0 (3 entries totalling 13 bytes / missing -> Err)\n--- WAT ---\n%s", code, wat)
	}
}
