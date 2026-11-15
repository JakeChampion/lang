package e2e

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Descriptor 3 is a regular file at offset 5, descriptor 4 is a pipe, and
// descriptor 5 is a socket. Each Reader must start where its descriptor
// stands. The exit code names the step that fails.
const readerFromFdSource = `function drain(fd: i32, want: string, code: i32): i32 {
    match (reader_from_fd(fd)) {
        Ok(r) => {
            let got = "";
            let more = true;
            while (more) {
                match (r.read_chunk(64)) {
                    Ok(s) => {
                        if (s.len() == 0) {
                            more = false;
                        } else {
                            got = got + s;
                        }
                    },
                    Err(_) => {
                        return code + 1;
                    }
                }
            }
            r.close();
            if (got != want) {
                return code + 2;
            }
            return 0;
        },
        Err(_) => {
            return code + 3;
        }
    }
}

function bad_descriptor(fd: i32, code: i32): i32 {
    match (reader_from_fd(fd)) {
        Ok(r) => {
            r.close();
            return code;
        },
        Err(e) => {
            match (e) {
                Other(_, _, n) => {
                    if (n != 9) {
                        return code + 1;
                    }
                },
                _ => {
                    return code + 2;
                }
            }
        }
    }
    return 0;
}

function main(): i32 {
    let rc = drain(3, "keep\n", 10);
    if (rc != 0) {
        return rc;
    }
    rc = drain(4, "pipe\n", 20);
    if (rc != 0) {
        return rc;
    }
    rc = drain(5, "socket\n", 30);
    if (rc != 0) {
        return rc;
    }
    rc = bad_descriptor(1000, 40);
    if (rc != 0) {
        return rc;
    }
    return bad_descriptor(-1, 50);
}
`

// readerFromFdRun gives cmd descriptors 3 to 5 and runs it. A reader that
// opens the file again leaves the offset of the file at 5.
func readerFromFdRun(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "regular")
	const content = "skip\nkeep\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	if _, err := pw.WriteString("pipe\n"); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syscall.Write(pair[0], []byte("socket\n")); err != nil {
		t.Fatal(err)
	}
	syscall.Close(pair[0])
	sock := os.NewFile(uintptr(pair[1]), "socket")
	defer sock.Close()
	cmd.ExtraFiles = []*os.File{file, pr, sock}
	out, err := cmd.CombinedOutput()
	if code := exitCodeOf(err); code != 0 {
		t.Fatalf("exit = %d, want 0 (the code names the failing step)\n%s", code, out)
	}
	if off, err := file.Seek(0, io.SeekCurrent); err != nil || off != int64(len(content)) {
		t.Errorf("offset of the file after the run = %d (%v), want %d: the reader did not read the descriptor itself", off, err, len(content))
	}
}

func TestX86_64ReaderFromFd(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, readerFromFdSource, nil)
	readerFromFdRun(t, runX86_64Bin(runner, bin))
}

func TestArm64ReaderFromFd(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, readerFromFdSource, nil)
	readerFromFdRun(t, runArm64Bin(qemu, bin))
}

// The Go interpreter wraps the descriptor with os.NewFile.
func TestInterpReaderFromFd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(readerFromFdSource), 0o644); err != nil {
		t.Fatal(err)
	}
	readerFromFdRun(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
}

// arm64-darwin has a descriptor table: the program builds there.
func TestArm64DarwinReaderFromFdBuilds(t *testing.T) {
	e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, readerFromFdSource, nil)
}

// A WASI component and a freestanding image have no descriptor table that a
// parent fills. The gate refuses the builtin under proc.
func TestReaderFromFdRefused(t *testing.T) {
	const src = `function main(): i32 {
    match (reader_from_fd(3)) {
        Ok(r) => {
            r.close();
            return 0;
        },
        Err(_) => {
            return 1;
        }
    }
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{e2eharness.TargetWasm32Wasi, "wasm32-wasi-http", "arm64-freestanding", "x86-64-freestanding"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 || vs[0].Builtin != "reader_from_fd" || vs[0].Capability != "proc" {
			t.Errorf("platforms.Enforce on %s: %v, want reader_from_fd refused on proc", target, vs)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android", "x86-64-android"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("platforms.Enforce on %s: %v, want none", target, vs)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, path, filepath.Join(dir, "prog.wasm")).CombinedOutput()
	if err == nil {
		t.Errorf("the compiler built reader_from_fd for wasm32-wasi, which has no descriptor numbers")
	}
	if !strings.Contains(string(out), "required by `reader_from_fd`; targets providing it") {
		t.Errorf("the wasm32-wasi refusal does not name the builtin:\n%s", out)
	}
}
