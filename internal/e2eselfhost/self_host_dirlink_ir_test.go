package e2eselfhost

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The single-step directory and link primitives (#8883) through the SELF-HOST
// IR path: create_dir / remove_dir / create_link / create_symlink /
// read_link / rename, plus `umask` on the native leg.
//
// The self-host emits these as generated Fern runtime bodies
// (`asmcore.rt_src_create_dir` and its six siblings), with `sysno`,
// `at_fdcwd` and `at_removedir` carrying the whole per-target difference. Every
// one of those is a constant that produces a plausible syscall when it is
// wrong — AT_REMOVEDIR is 0x200 on Linux and 0x80 on Darwin, and without it
// unlinkat asks to remove a directory as a file — so the probe checks the
// resulting TREE and not only the return value.
//
// `internal/e2eselfhost` is PRIMARY for a self-host lowering change
// (docs/TEST-GATES.md): the fixpoint is self-referential and cannot see a
// stable miscompile of a construct it does not itself use, and nothing in the
// compiler calls these seven.

// driverStderr is what a failed driver wrote on stderr — the diagnostic a
// bare exit status hides. exec.Cmd.Output stashes it on the ExitError.
func driverStderr(err error) []byte {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.Stderr
	}
	return nil
}

// selfHostDirLinkSource is the probe, parameterised by the directory its paths
// resolve against — "" for the wasm leg, which runs under its preopen.
func selfHostDirLinkSource(prefix string, withUmask bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	src := fmt.Sprintf(`function main(): i32 {
    match (create_dir(%[1]q, 493)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (create_dir(%[1]q, 493)) { Ok(_) => { return 2; }, Err(_) => {} }
    match (create_dir(%[2]q, 493)) { Ok(_) => { return 3; }, Err(_) => {} }

    match (write_file(%[3]q, "hello\n")) { Ok(_) => {}, Err(_) => { return 4; } }
    match (create_link(%[3]q, %[4]q)) { Ok(_) => {}, Err(_) => { return 5; } }
    match (read_file(%[4]q)) {
        Ok(s) => { if (s != "hello\n") { return 6; } },
        Err(_) => { return 7; }
    }
    match (create_link(%[3]q, %[4]q)) { Ok(_) => { return 8; }, Err(_) => {} }

    match (create_symlink("dangling-target", %[5]q)) { Ok(_) => {}, Err(_) => { return 9; } }
    match (read_link(%[5]q)) {
        Ok(target) => { if (target != "dangling-target") { return 10; } },
        Err(_) => { return 11; }
    }
    match (read_link(%[3]q)) { Ok(_) => { return 12; }, Err(_) => {} }

    match (write_file(%[6]q, "x")) { Ok(_) => {}, Err(_) => { return 13; } }
    match (remove_dir(%[1]q)) { Ok(_) => { return 14; }, Err(_) => {} }
    match (remove_file(%[6]q)) { Ok(_) => {}, Err(_) => { return 15; } }
    match (remove_dir(%[1]q)) { Ok(_) => {}, Err(_) => { return 16; } }
    match (remove_dir(%[1]q)) { Ok(_) => { return 17; }, Err(_) => {} }
`, p("d"), p("d/nope/deep"), p("f.txt"), p("hard.txt"), p("link"), p("d/inner.txt"))
	// rename replaces an existing destination in one step, which a
	// create_link plus remove_file pair cannot: that pair is EEXIST here.
	src += fmt.Sprintf(`    match (write_file(%[1]q, "moved\n")) { Ok(_) => {}, Err(_) => { return 18; } }
    match (write_file(%[2]q, "stale\n")) { Ok(_) => {}, Err(_) => { return 19; } }
    match (rename(%[1]q, %[2]q)) { Ok(_) => {}, Err(_) => { return 20; } }
    match (read_file(%[1]q)) { Ok(_) => { return 21; }, Err(_) => {} }
    match (read_file(%[2]q)) {
        Ok(s) => { if (s != "moved\n") { return 22; } },
        Err(_) => { return 23; }
    }
    match (rename(%[1]q, %[2]q)) { Ok(_) => { return 24; }, Err(_) => {} }
`, p("g.txt"), p("occupied.txt"))
	if withUmask {
		src += `    var prev: i32 = umask(18);
    if (umask(prev) != 18) { return 25; }
`
	}
	src += `    return 0;
}
`
	return src
}

// selfHostDirLinkTree asserts what the probe left on disk — the half a return
// value cannot prove.
func selfHostDirLinkTree(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, "d")); !os.IsNotExist(err) {
		t.Errorf("d/ still exists after remove_dir (lstat err = %v)", err)
	}
	target, err := os.Readlink(filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "dangling-target" {
		t.Errorf("link -> %q, want %q", target, "dangling-target")
	}
	got, err := os.ReadFile(filepath.Join(dir, "hard.txt"))
	if err != nil {
		t.Fatalf("read hard.txt: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("hard.txt = %q, want %q", got, "hello\n")
	}
	if _, err := os.Lstat(filepath.Join(dir, "g.txt")); !os.IsNotExist(err) {
		t.Errorf("g.txt still exists after rename (lstat err = %v)", err)
	}
	got, err = os.ReadFile(filepath.Join(dir, "occupied.txt"))
	if err != nil {
		t.Fatalf("read occupied.txt: %v", err)
	}
	if string(got) != "moved\n" {
		t.Errorf("occupied.txt = %q, want %q — rename did not replace it", got, "moved\n")
	}
}

func TestSelfHostDirLinkIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("dirlink test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	src := selfHostDirLinkSource(work, true)

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, driverStderr(err))
	}
	for _, sym := range []string{
		"__fern_create_dir", "__fern_remove_dir", "__fern_create_link",
		"__fern_create_symlink", "__fern_read_link", "__fern_umask",
		"__fern_rename",
	} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("%s did not reach the IR runtime path (absent from the asm)", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "dirlink_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("dirlink program exited %d, want 0 — the code names the step (see selfHostDirLinkSource)", code)
	}
	selfHostDirLinkTree(t, work)
}

// The wasm leg: six of the seven are real WASI calls
// (path_create_directory / path_remove_directory / path_link /
// path_symlink / path_readlink / path_rename), so they are exercised rather
// than classified out. `umask` is absent — WASI has no file-mode creation mask and
// E066 refuses the builtin on that target.
func TestSelfHostDirLinkWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dirlink wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := selfHostDirLinkSource("", false)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, driverStderr(err))
	}
	work := t.TempDir()
	watFile := filepath.Join(work, "dirlink_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = work
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("dirlink wasm program exited %d, want 0 — the code names the step\n--- WAT ---\n%s", code, wat)
	}
	selfHostDirLinkTree(t, work)
}
