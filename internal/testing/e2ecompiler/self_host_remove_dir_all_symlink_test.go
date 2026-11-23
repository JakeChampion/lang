package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// remove_dir_all removes a link and does not follow it (#16840). The tree
// holds a link to a directory outside it, a link to a file outside it, and a
// link loop. A path below a file is an error, not a success.
const removeDirAllSymlinkSrc = `function main(): i32 {
    match (remove_dir_all("tree")) {
        Err(_) => { return 1; },
        Ok(_) => {},
    }
    match (remove_dir_all("plain.txt/below")) {
        Err(_) => {},
        Ok(_) => { return 2; },
    }
    return 0;
}`

func writeRemoveDirAllSymlinkTree(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{"tree/sub", "outside"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"tree/a.txt", "tree/sub/b.txt", "outside/keep.txt", "plain.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"tree/dirlink":  "../outside",
		"tree/filelink": "../outside/keep.txt",
		"tree/sub/loop": "loop",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
}

func checkRemoveDirAllSymlinkTree(t *testing.T, dir string, run *exec.Cmd) {
	t.Helper()
	run.Dir = dir
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("the program did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit %d, want 0 (1: the tree was not removed, 2: a path below a file gave Ok)\n%s", code, out)
	}
	if _, err := os.Lstat(filepath.Join(dir, "tree")); !os.IsNotExist(err) {
		t.Errorf("tree is still there (lstat err = %v)", err)
	}
	for _, f := range []string{"outside/keep.txt", "plain.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s is gone: remove_dir_all followed a link out of the tree (%v)", f, err)
		}
	}
}

func TestSelfHostRemoveDirAllDoesNotFollowLinks(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(removeDirAllSymlinkSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		dir := t.TempDir()
		writeRemoveDirAllSymlinkTree(t, dir)
		checkRemoveDirAllSymlinkTree(t, dir, runX86_64Bin(cli.runner, cli.x86Binary(t, src)))
	})
	t.Run("arm64-linux", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		bin := buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))
		dir := t.TempDir()
		writeRemoveDirAllSymlinkTree(t, dir)
		checkRemoveDirAllSymlinkTree(t, dir, runArm64Bin(qemu, bin))
	})
	t.Run("wasm32-wasi-core-module", func(t *testing.T) {
		wasm := filepath.Join(t.TempDir(), "main.wasm")
		if msg, err := cli.compileCmd("wasm32-wasi", wasm, src, "-emit", "core-module").CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		dir := t.TempDir()
		writeRemoveDirAllSymlinkTree(t, dir)
		checkRemoveDirAllSymlinkTree(t, dir, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.::/", wasm))
	})
	t.Run("wasm32-wasi-component", func(t *testing.T) {
		wasm := cli.wasmComponent(t, src)
		dir := t.TempDir()
		writeRemoveDirAllSymlinkTree(t, dir)
		checkRemoveDirAllSymlinkTree(t, dir, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.::/", wasm))
	})
}

func TestSelfHostRemoveDirAllDoesNotFollowLinksDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skipf("%s/%s cannot run an arm64 Mach-O", runtime.GOOS, runtime.GOARCH)
	}
	fern := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(removeDirAllSymlinkSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if msg, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, msg)
	}
	dir := t.TempDir()
	writeRemoveDirAllSymlinkTree(t, dir)
	checkRemoveDirAllSymlinkTree(t, dir, exec.Command(bin))
}
