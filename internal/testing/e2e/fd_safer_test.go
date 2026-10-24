package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const fdSaferStdoutSrc = `function main(): i32 {
    match (open_writer("alias.out")) {
        Ok(w) => {
            let refused: boolean = false;
            match (stdout().write("TO-STDOUT\n")) {
                Err(e) => { refused = true; },
                Ok(_) => { }
            }
            match (w.write("TO-FILE\n")) {
                Err(e) => { return 4; },
                Ok(_) => { }
            }
            w.close();
            if (!refused) { return 2; }
            return 0;
        },
        Err(e) => { return 3; }
    }
}
`

const fdSaferStdinSrc = `import "std/io";

function main(): i32 {
    match (open_reader("data.txt")) {
        Ok(r) => {
            match (io.read_line(stdin())) {
                Ok(Some(line)) => { return 2; },
                _ => { }
            }
            match (io.read_line(r)) {
                Ok(Some(line)) => {
                    if (line != "FROM-FILE\n") { return 3; }
                    return 0;
                },
                _ => { return 4; }
            }
        },
        Err(e) => { return 5; }
    }
}
`

var closedStdio = (*os.File)(nil)

func TestOpenedHandleNeverLandsOnAStandardDescriptor(t *testing.T) {
	bin := buildFernCLI(t)
	x86Qemu, haveX86 := x86Runner()
	arm64Qemu, haveArm64 := arm64Runner()
	for _, tc := range []struct {
		name   string
		target []string
		arm64  bool
	}{
		{"x86-64", []string{"-target", "x86-64-linux"}, false},
		{"arm64", []string{"-target", "arm64-linux"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, have := x86Qemu, haveX86
			if tc.arm64 {
				runner, have = arm64Qemu, haveArm64
			}
			if !have {
				t.Skipf("no way to run a %s binary here", tc.name)
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

			if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("FROM-FILE\nsecond\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			prog = build("stdin_closed", fdSaferStdinSrc)
			if code := run(prog, closedStdio, nil); code != 0 {
				t.Errorf("with fd 0 closed: exit %d, want 0 (2 = stdin read the opened file)", code)
			}
		})
	}
}
