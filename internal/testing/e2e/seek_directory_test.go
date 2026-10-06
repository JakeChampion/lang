//go:build linux

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/tables/strerror"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The paths are relative because wasm resolves every path against its preopen.
const seekDirProg = `import "std/i32";
import "std/i64";

function seek(label: string, r: Reader, off: i64, whence: i32): void {
    match (r.seek(off, whence)) {
        Ok(n) => { print(label + " " + n.to_string()); },
        Err(e) => {
            match (e) {
                Other(_, m, n) => { print(label + " " + m + " " + n.to_string()); },
                _ => { print(label + " another variant"); }
            }
        }
    }
}

function read(label: string, r: Reader): void {
    match (r.read_chunk(16)) {
        Ok(_) => { print(label + " read ok"); },
        Err(e) => {
            match (e) {
                Other(_, m, _) => { print(label + " read " + m); },
                _ => { print(label + " read another variant"); }
            }
        }
    }
}

function main(): i32 {
    match (open_reader("d")) {
        Ok(r) => {
            seek("set 0", r, 0, 0);
            read("at 0", r);
            seek("set 5", r, 5, 0);
            seek("cur 0", r, 0, 1);
            seek("cur 3", r, 3, 1);
            seek("cur -2", r, -2, 1);
            read("at 6", r);
            seek("set -1", r, -1, 0);
            seek("cur -100", r, -100, 1);
            seek("cur 0 again", r, 0, 1);
            seek("end 0", r, 0, 2);
            seek("back to 0", r, 0, 0);
            read("back at 0", r);
            match (r.close()) { Some(_) => { print("close failed"); }, None => { print("closed"); } }
        },
        Err(_) => { print("open failed"); }
    }
    match (open_reader("d")) {
        Ok(r) => {
            seek("reopened cur 0", r, 0, 1);
            r.close();
        },
        Err(_) => { print("reopen failed"); }
    }
    return 0;
}
`

// seekDirWant is what every target prints, with SEEK_END's answer in place of
// %s: a native lseek's is the filesystem's own, so the interpreter and the
// register runtimes print what the host says, and wasm, whose host refuses to
// seek a directory, counts from the directory's size.
const seekDirWant = `set 0 0
at 0 read Is a directory
set 5 5
cur 0 5
cur 3 8
cur -2 6
at 6 read Is a directory
set -1 Invalid argument 22
cur -100 Invalid argument 22
cur 0 again 6
end 0 %s
back to 0 0
back at 0 read Is a directory
closed
reopened cur 0 0
`

// hostSeekEnd is the host lseek's answer to SEEK_END on dir, as the program
// prints it.
func hostSeekEnd(t *testing.T, dir string) string {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pos, err := f.Seek(0, io.SeekEnd)
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("%s %d", strerror.Text(strerror.Linux, int(errno)), int(errno))
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(pos)
}

// A Reader opened on a directory seeks on every target, moving an offset no
// read uses: each read is EISDIR wherever the offset stands (#11713). The
// wasm hosts refuse to seek a directory, so its runtime keeps the offset.
func TestSeekDirectoryReaderEveryTarget(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "d")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(seekDirProg), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(d)
	if err != nil {
		t.Fatal(err)
	}
	native := fmt.Sprintf(seekDirWant, hostSeekEnd(t, d))
	wasm := fmt.Sprintf(seekDirWant, fmt.Sprint(st.Size()))
	leakcheck := []string{"FERN_LEAKCHECK=1"}

	targets := []struct {
		name   string
		want   string
		census bool
		argv   func(t *testing.T) []string
	}{
		{"interpreter", native, false, func(t *testing.T) []string {
			return []string{e2eharness.BuildLangBinForInterp(t), "-interp", srcPath}
		}},
		{"x86_64", native, true, func(t *testing.T) []string {
			runner := e2eharness.X86_64Runner(t)
			bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, srcPath, leakcheck)
			return e2eharness.RunX86_64Bin(runner, bin).Args
		}},
		{"arm64", native, true, func(t *testing.T) []string {
			qemu := e2eharness.Arm64Runner(t)
			bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetArm64Linux, srcPath, leakcheck)
			return e2eharness.RunArm64Bin(qemu, bin).Args
		}},
		{"wasm-preview1", wasm, true, func(t *testing.T) []string {
			core := e2eharness.CompileSelfHostFile(t, e2eharness.TargetWasm32Wasi, srcPath, leakcheck)
			return []string{e2eharness.Wasmtime(t), "run", "--dir=.", core}
		}},
		{"wasm-component", wasm, true, func(t *testing.T) []string {
			component := filepath.Join(t.TempDir(), "main.wasm")
			cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, srcPath, component)
			cmd.Env = e2eharness.SelfHostChildEnv(leakcheck...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile component: %v\n%s", err, out)
			}
			return []string{e2eharness.Wasmtime(t), "run", "--dir=.", component}
		}},
	}
	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			argv := tc.argv(t)
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Dir = dir
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != tc.want {
				t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", got, tc.want, stderr.String())
			}
			if tc.census {
				e2eharness.CheckLeakcheckBalanced(t, stderr.String())
			}
		})
	}
}
