package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The self-host interpreter's host-facing builtins answer what the Go
// interpreter's do: a program's argv (its own path first), its stdin through
// a Reader and through read_line, and a cell it reads and writes. Each
// program is run by both CLIs' -interp with the same stdin and arguments, and
// the exit code and stdout must agree (#10644).
func TestSelfHostInterpHostBuiltins(t *testing.T) {
	cli := buildSelfHostCLI(t)
	native := buildFernCLIBin(t)
	cases := []struct {
		name, src, stdin string
		args             []string
	}{
		{
			name: "args",
			src: `function main(): i32 {
    var a: string[] = args();
    var i: i32 = 1;
    while (i < a.len()) {
        print(a[i]);
        i = i + 1;
    }
    return a.len();
}
`,
			args: []string{"alpha", "beta"},
		},
		{
			name: "stdin-reader",
			src: `import "std/io";
import "std/string";

function main(): i32 {
    match (io.read_input("-")) {
        Ok(text) => {
            var lines: string[] = text.lines();
            var i: i32 = 0;
            while (i < lines.len()) {
                print("line: " + lines[i]);
                i = i + 1;
            }
            return lines.len();
        },
        Err(_) => { return 9; }
    }
}
`,
			stdin: "one\ntwo\nthree\n",
		},
		{
			name: "read-line",
			src: `function main(): i32 {
    match (read_line()) {
        Some(l) => { print("got " + l); },
        None => { return 4; }
    }
    match (read_line()) {
        Some(l) => { print("got " + l); return 0; },
        None => { return 5; }
    }
}
`,
			stdin: "first\n",
		},
		{
			name: "cells",
			src: `function bump(c: Cell[i32]): void {
    c.set(c.get() + 2);
}

function main(): i32 {
    var n: Cell[i32] = cell_new(5);
    bump(n);
    bump(n);
    var s: Cell[string] = cell_new("a");
    s.set(s.get() + "b");
    var f: Cell[f64] = cell_new(1.5);
    f.set(f.get() * 2.0);
    var b: Cell[boolean] = cell_new(false);
    b.set(!b.get());
    print(s.get());
    if (s.get() == "ab" && f.get() == 3.0 && b.get()) { return n.get(); }
    return 1;
}
`,
		},
		{
			name: "test-runner",
			src: `import "std/test";

function test_addition(): test.TestOutcome {
    return test.assert_eq(2 + 2, 4);
}

function test_strings(): test.TestOutcome {
    return test.assert_eq("foo" + "bar", "foobar");
}

function main(): i32 {
    var r: test.TestRunner = test.test_new("hello");
    r = r.it("addition", test_addition);
    r = r.it("strings", test_strings);
    return r.finish();
}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			selfArgs := append([]string{"-interp", path, cli.stdlib}, c.args...)
			selfOut, selfErr, selfCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, selfArgs...), c.stdin)
			nativeOut, nativeErr, nativeCode := runInterpCLI(t, exec.Command(native, append([]string{"-interp", path}, c.args...)...), c.stdin)
			if selfCode != nativeCode || selfOut != nativeOut {
				t.Errorf("self-host -interp: exit %d, stdout %q (stderr %q)\nnative -interp: exit %d, stdout %q (stderr %q)",
					selfCode, selfOut, selfErr, nativeCode, nativeOut, nativeErr)
			}
			if selfCode == 254 {
				t.Errorf("the self-host interpreter could not evaluate the program:\n%s", selfErr)
			}
		})
	}
}

func runInterpCLI(t *testing.T, cmd *exec.Cmd, stdin string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdin = bytes.NewReader([]byte(stdin))
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run %v: %v", cmd.Args, err)
		}
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}
