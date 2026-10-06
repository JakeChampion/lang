package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// errnoSource provokes one errno per line and prints what `errno.of` makes of
// it, beside Other's message where there is one. The runtime is handed each
// errno in its host's numbering — Linux's, or preview 1's on both wasm legs —
// and every leg must print Linux's (#11296).
const errnoSource = `import "std/errno";
import "std/i32";

function report(label: string, r: Result[string, IoError]): void {
    match (r) {
        Ok(_) => { print(label + " succeeded"); },
        Err(e) => {
            match (e) {
                Other(_, m, _) => { print(label + " " + errno.of(e).to_string() + " " + m); },
                _ => { print(label + " " + errno.of(e).to_string()); },
            }
        },
    }
}

function unit(label: string, r: Result[void, IoError]): void {
    match (r) {
        Ok(_) => { report(label, Ok("")); },
        Err(e) => { report(label, Err(e)); },
    }
}

function main(): i32 {
    match (create_dir_all("d/sub")) { Err(_) => { return 1; }, Ok(_) => {} }
    match (write_file("f.txt", "x")) { Err(_) => { return 2; }, Ok(_) => {} }
    report("enoent", read_file("missing.txt"));
    report("enotdir", read_file("f.txt/x"));
    unit("eisdir", write_file("d", "x"));
    report("einval", temp_dir("a/b"));
    unit("enotempty", remove_dir("d"));
    print("unknown " + errno.of(Other("", "Unknown error 4095", 0)).to_string());
    if (errno.of(Unsupported) != errno.EOPNOTSUPP || errno.EOPNOTSUPP != errno.ENOTSUP) { return 3; }
    if (errno.of(PermissionDenied("p")) != errno.EACCES || errno.of(AlreadyExists("p")) != errno.EEXIST) { return 4; }
    if (errno.of(Interrupted) != errno.EINTR || errno.of(InvalidUtf8("p")) != errno.EILSEQ) { return 5; }
    return 0;
}
`

const errnoWant = `enoent 2
enotdir 20 Not a directory
eisdir 21 Is a directory
einval 22 Invalid argument
enotempty 39 Directory not empty
unknown 0
`

// errnoXattrSource is errnoSource for the self-host interpreter, whose only
// host calls that fail with an IoError are the xattr family: it reads an
// attribute through a missing file, through a file used as a directory, and
// one the file does not carry. The test makes f.txt.
const errnoXattrSource = `import "std/errno";
import "std/i32";

function report(label: string, r: Result[string, IoError]): void {
    match (r) {
        Ok(_) => { print(label + " succeeded"); },
        Err(e) => {
            match (e) {
                Other(_, m, _) => { print(label + " " + errno.of(e).to_string() + " " + m); },
                _ => { print(label + " " + errno.of(e).to_string()); },
            }
        },
    }
}

function main(): i32 {
    report("enoent", getxattr("missing.txt", "user.absent"));
    report("enotdir", getxattr("f.txt/x", "user.absent"));
    report("enodata", getxattr("f.txt", "user.absent"));
    return 0;
}
`

const errnoXattrWant = `enoent 2
enotdir 20 Not a directory
enodata 61 No data available
`

func runErrnoProgram(t *testing.T, cmd *exec.Cmd) (string, string) {
	t.Helper()
	return runErrnoProgramWant(t, cmd, errnoWant)
}

func runErrnoProgramWant(t *testing.T, cmd *exec.Cmd, want string) (string, string) {
	t.Helper()
	if cmd.Dir == "" {
		cmd.Dir = t.TempDir()
	}
	var ob, eb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &ob, &eb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally\n%s", cmd.Path, eb.String())
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (see errnoSource)\nstdout:\n%s\nstderr:\n%s", code, ob.String(), eb.String())
	}
	if got := ob.String(); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
	return ob.String(), eb.String()
}

// TestSelfHostErrnoOf runs errnoSource under the interpreter and through the
// self-host compiler on x86-64, arm64 and wasm (a core module over preview 1
// and a component over preview 2, whose error-code path translates twice),
// each compiled leg with the leak census on: Other's box grew a field, and
// every one the runtime built must still be given back. errnoXattrSource runs
// under both interpreters: the self-host one hands the program the errno of
// the IoError its own host call failed with.
func TestSelfHostErrnoOf(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(errnoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("interp", func(t *testing.T) {
		runErrnoProgram(t, exec.Command(buildLangBinForInterp(t), "-interp", src))
	})
	xsrc := filepath.Join(t.TempDir(), "xattr.fern")
	if err := os.WriteFile(xsrc, []byte(errnoXattrSource), 0o644); err != nil {
		t.Fatal(err)
	}
	inFileDir := func(cmd *exec.Cmd) *exec.Cmd {
		cmd.Dir = t.TempDir()
		if err := os.WriteFile(filepath.Join(cmd.Dir, "f.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	t.Run("interp-xattr", func(t *testing.T) {
		runErrnoProgramWant(t, inFileDir(exec.Command(buildLangBinForInterp(t), "-interp", xsrc)), errnoXattrWant)
	})
	cli := buildSelfHostCLI(t)
	t.Run("selfhost-interp-xattr", func(t *testing.T) {
		runErrnoProgramWant(t, inFileDir(runX86_64Bin(cli.runner, cli.bin, "-interp", xsrc, cli.stdlib)), errnoXattrWant)
	})
	t.Run("x86-64-linux", func(t *testing.T) {
		_, stderr := runErrnoProgram(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1")))
		assertBalancedCensus(t, stderr)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		_, stderr := runErrnoProgram(t, runArm64Bin(qemu, cli.arm64Binary(t, src, "FERN_LEAKCHECK=1")))
		assertBalancedCensus(t, stderr)
	})
	t.Run("wasm32-wasi-preview1", func(t *testing.T) {
		wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1")
		_, stderr := runErrnoProgram(t, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", wat))
		assertBalancedCensus(t, stderr)
	})
	t.Run("wasm32-wasi-preview2", func(t *testing.T) {
		comp := cli.wasmComponent(t, src, "FERN_LEAKCHECK=1")
		_, stderr := runErrnoProgram(t, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", comp))
		assertBalancedCensus(t, stderr)
	})
}
