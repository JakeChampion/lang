package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The open helpers call openat(2), which returns the LOWEST free descriptor,
// so a program exec'd with a standard stream closed — `prog >&-` — used to be
// handed 0, 1 or 2 for the file it opened, and stdout() then silently aliased
// that file: the write reported success and the bytes landed in the file.
// Every open helper now moves such a descriptor up (fcntl F_DUPFD 3) and
// closes the original, as glibc's fopen does (#8823).
//
// Two programs, one per direction. Neither can run under the interpreter:
// the Go runtime reopens a closed standard descriptor on /dev/null at
// startup, so os.OpenFile never sees 0/1/2 free there.

// fdSaferStdoutSrc opens a file with fd 1 closed. stdout().write must FAIL
// (exit 2 if it succeeded, which means it went to the file) and the file
// must hold only what was written to it.
const fdSaferStdoutSrc = `function main(): i32 {
    match (open_writer("alias.out")) {
        Ok(w) => {
            var refused: boolean = false;
            match (stdout().write("TO-STDOUT\n")) {
                Some(e) => { refused = true; },
                None => { }
            }
            match (w.write("TO-FILE\n")) {
                Some(e) => { return 4; },
                None => { }
            }
            w.close();
            if (!refused) { return 2; }
            return 0;
        },
        Err(e) => { return 3; }
    }
}
`

// fdSaferStdinSrc opens a file with fd 0 closed. read_line() on stdin must
// answer None (exit 2 if it read the FILE's first line), and the reader
// must still hand back that line.
const fdSaferStdinSrc = `function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => {
            match (read_line()) {
                Some(line) => { return 2; },
                None => { }
            }
            match (r.read_line()) {
                Some(line) => {
                    if (line != "FROM-FILE\n") { return 3; }
                    return 0;
                },
                None => { return 4; }
            }
        },
        Err(e) => { return 5; }
    }
}
`

// closedStdio is a typed nil *os.File: os/exec passes it to os.StartProcess
// as a nil entry, which leaves that descriptor CLOSED in the child rather
// than on /dev/null.
var closedStdio = (*os.File)(nil)

func TestOpenedHandleNeverLandsOnAStandardDescriptor(t *testing.T) {
	bin := buildFernCLI(t)
	qemu, haveArm64 := arm64Runner()
	for _, tc := range []struct {
		name   string
		target []string
		arm64  bool
	}{
		{"x86-64", []string{"-target", "x86-64-linux"}, false},
		{"arm64", []string{"-target", "arm64-linux"}, true},
		{"arm64-ssa", []string{"-target", "arm64-linux", "-backend", "ssa"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.arm64 && !haveArm64 {
				t.Skip("no way to run an arm64 binary here")
			}
			runner := ""
			if tc.arm64 {
				runner = qemu
			}
			dir := t.TempDir()
			build := func(name, src string) string {
				srcPath := filepath.Join(dir, name+".fern")
				if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				out := filepath.Join(dir, name)
				args := append(append([]string{}, tc.target...), "-o", out, srcPath)
				if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
					t.Fatalf("compile %s: %v\n%s", name, err, o)
				}
				return out
			}
			run := func(prog string, stdin, stdout *os.File) int {
				var cmd *exec.Cmd
				if runner == "" {
					cmd = exec.Command(prog)
				} else {
					cmd = exec.Command(runner, prog)
				}
				cmd.Dir = dir
				cmd.Stdin, cmd.Stdout = stdin, stdout
				_ = cmd.Run()
				return cmd.ProcessState.ExitCode()
			}

			prog := build("stdout_closed", fdSaferStdoutSrc)
			if code := run(prog, nil, closedStdio); code != 0 {
				t.Errorf("with fd 1 closed: exit %d, want 0 (2 = stdout().write went to the opened file)", code)
			}
			if got, err := os.ReadFile(filepath.Join(dir, "alias.out")); err != nil || string(got) != "TO-FILE\n" {
				t.Errorf("the opened file holds %q (err %v), want only what was written to it", got, err)
			}

			// The arm64 SSA backend has no read_line runtime helper yet, so
			// the stdin direction is x86-64 and the shipping arm64 backend.
			if tc.name == "arm64-ssa" {
				return
			}
			if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("FROM-FILE\nsecond\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			prog = build("stdin_closed", fdSaferStdinSrc)
			if code := run(prog, closedStdio, nil); code != 0 {
				t.Errorf("with fd 0 closed: exit %d, want 0 (2 = read_line() read the opened file)", code)
			}
		})
	}
}
