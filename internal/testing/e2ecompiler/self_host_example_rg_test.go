package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ripgrep is the oracle of the rg example. The test fails without it.
const ripgrep = "/usr/bin/rg"

func TestSelfHostExampleRgUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/rg/rg_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

type rgCase struct {
	args  []string
	stdin string
}

func rgArgs(line string) []string { return strings.Fields(line) }

// rgFixtures writes the files that the cases search.
func rgFixtures(t *testing.T, dir string) {
	t.Helper()
	var many strings.Builder
	for i := 1; i <= 300; i++ {
		if i%13 == 0 || i%29 == 0 {
			fmt.Fprintf(&many, "line %d foo %d\n", i, i*7)
		} else {
			fmt.Fprintf(&many, "line %d bar\n", i)
		}
	}
	long := strings.Repeat("abcdefghij", 20000) + " needle " + strings.Repeat("xyz", 3000) + "\nshort needle\n"
	files := map[string]string{
		"a.txt":     "alpha beta\ngamma foo\nfoo bar foo\nnothing here\nFoo again\nlast foo",
		"w.txt":     "foobar foo\nxfoo foo_x\n-foo- (foo)\nfoo\nfoofoo\n",
		"upper.txt": "Foo\nFOO\nfoo\nfOo bar\nnone\n",
		"crlf.txt":  "one\r\ntwo foo\r\nthree\r\n",
		"empty.txt": "",
		"nonl.txt":  "foo",
		"blank.txt": "\n\nfoo\n\n",
		"uni.txt":   "café au lait\nnaïve foo\nψυχή καὶ σῶμα\nplain\n",
		"many.txt":  many.String(),
		"long.txt":  long,
		"bin.dat":   "abc\x00def foo\nmore foo\n",
		"bin2.dat":  "foo 1\nbar\nabc\x00def\nfoo 2\n",
		"bin3.dat":  "abc\x00def\n",
		"bin4.dat":  "a\x00foo\nzz\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rgCases are the command lines of the differential test. A case with
// several files passes -j1: with more threads, ripgrep writes the files in
// the order that their searches end.
func rgCases(dir string) []rgCase {
	stdinOf := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			panic(err)
		}
		return string(data)
	}
	lines := []string{
		// Selection, names and line numbers.
		"foo a.txt",
		"-j1 foo a.txt w.txt",
		"-n foo a.txt",
		"-N foo a.txt",
		"-n -N foo a.txt",
		"-N -n foo a.txt",
		"-H foo a.txt",
		"-j1 -I foo a.txt w.txt",
		"-j1 --no-filename -n foo a.txt crlf.txt",
		"--with-filename --line-number foo a.txt",
		"-j1 -H -I foo a.txt",
		"-j1 -I -H foo a.txt",
		"-i FOO upper.txt",
		"--ignore-case -n foo upper.txt",
		"-i -s foo upper.txt",
		"-v foo a.txt",
		"-vn --invert-match foo a.txt",
		"-w foo w.txt",
		"-wn -e -foo w.txt",
		"--word-regexp -i FOO w.txt",
		"-w o a.txt",
		"-F (foo) w.txt",
		"-F -e . a.txt",
		"-Fi FOO.BAR a.txt",
		"--fixed-strings -e foo -e (foo) w.txt",
		// Counts and file names.
		"-c foo a.txt",
		"-j1 -c foo a.txt crlf.txt empty.txt w.txt",
		"-c zzz a.txt",
		"-cv foo a.txt",
		"-co foo w.txt",
		"-c -o -v foo a.txt",
		"--count -i foo upper.txt",
		"-l foo a.txt",
		"-j1 -l foo a.txt crlf.txt empty.txt bin.dat",
		"-lv foo w.txt",
		"-c -l foo a.txt",
		"-l -c foo a.txt",
		"--files-with-matches zzz a.txt",
		// Only the matches.
		"-o foo a.txt",
		"-on o+ a.txt",
		"-o x* w.txt",
		"-o -n -H fo* w.txt",
		"-ov foo a.txt",
		"-ow foo w.txt",
		"--only-matching -i f.o upper.txt",
		// Context.
		"-A1 foo a.txt",
		"-B1 -n foo a.txt",
		"-C1 -n foo a.txt",
		"-n -C3 foo many.txt",
		"-C 2 -A 0 foo many.txt",
		"-A2 -B1 -n 1[05] many.txt",
		"--after-context 1 --before-context=2 foo many.txt",
		"--context=1 -c foo many.txt",
		"-j1 -A1 foo a.txt empty.txt crlf.txt",
		"-j1 -A1 foo a.txt bin.dat crlf.txt",
		"-C0 foo a.txt",
		"-A1 -l foo a.txt",
		"-B1 -o -n foo a.txt",
		"-C1 -v foo a.txt",
		"-nA1 foo a.txt",
		"-A 99999999999 foo a.txt",
		// Color.
		"--color always foo a.txt",
		"--color=always -n -H foo a.txt",
		"-j1 --color always -n -C1 foo a.txt crlf.txt",
		"--color always -o -n foo a.txt",
		"-j1 --color always -c foo a.txt crlf.txt",
		"--color always -c foo a.txt",
		"--color always -l foo a.txt",
		"--color always -v foo a.txt",
		"--color always -C1 -v -n foo a.txt",
		"--color=ansi -w foo w.txt",
		"--color never foo a.txt",
		"--color auto foo a.txt",
		"--color always -H foo bin.dat",
		"--color always o* w.txt",
		// Patterns.
		"-e foo -e bar a.txt",
		"-e foo a.txt -e gamma",
		"--regexp=foo --regexp alpha a.txt",
		"-efoo a.txt",
		"-- -foo w.txt",
		"f[aeiou]o upper.txt",
		"^foo w.txt",
		"foo$ w.txt",
		`\bfoo\b w.txt`,
		`-c \d+ many.txt`,
		"-o (ab)+c? long.txt",
		"-c o{2,} a.txt",
		"two.foo$ crlf.txt",
		"café uni.txt",
		"-n naïve uni.txt",
		"-c ψυχή uni.txt",
		// Edges.
		"foo empty.txt",
		"-v foo empty.txt",
		"-c foo empty.txt",
		"foo nonl.txt",
		"-n -v foo blank.txt",
		"-n needle long.txt",
		"-c needle long.txt",
		"-o needle long.txt",
		// Binary files.
		"foo bin.dat",
		"-c foo bin.dat",
		"-n foo bin2.dat",
		"-o foo bin2.dat",
		"-H -A1 bar bin2.dat",
		"zzz bin3.dat",
		"-v zzz bin3.dat",
		"-v foo bin4.dat",
		"-c -v foo bin4.dat",
		"-l foo bin.dat",
		// A missing file.
		"foo nofile",
		"-j1 foo nofile",
		"-j1 foo nofile a.txt",
		"-j1 -c foo nofile",
		"-j1 -l -n foo a.txt nofile",
		// Errors on the command line.
		"",
		"-n",
		"-Z foo a.txt",
		"--bogus foo a.txt",
		"--bogus=1 foo a.txt",
		"--count=1 foo a.txt",
		"-A",
		"foo --color",
		"-A x foo a.txt",
		"--after-context x foo a.txt",
		"-A -1 foo a.txt",
		"-C1foo a.txt",
		"-A 99999999999999999999 foo a.txt",
		"--color bogus foo a.txt",
		"-j x foo a.txt",
	}
	var cases []rgCase
	for _, l := range lines {
		cases = append(cases, rgCase{args: rgArgs(l)})
	}
	// The standard input.
	cases = append(cases,
		rgCase{rgArgs("foo"), stdinOf("a.txt")},
		rgCase{rgArgs("-n foo -"), stdinOf("a.txt")},
		rgCase{rgArgs("-j1 foo - a.txt"), stdinOf("w.txt")},
		rgCase{rgArgs("-H foo"), stdinOf("a.txt")},
		rgCase{rgArgs("-c foo"), stdinOf("a.txt")},
		rgCase{rgArgs("-l foo -"), stdinOf("a.txt")},
		rgCase{rgArgs("foo"), stdinOf("bin.dat")},
		rgCase{rgArgs("-o -n o+"), stdinOf("crlf.txt")},
		rgCase{rgArgs("foo"), ""},
	)
	return cases
}

type rgResult struct {
	stdout, stderr string
	code           int
}

func rgRun(t *testing.T, cmd *exec.Cmd, dir, stdin string) rgResult {
	t.Helper()
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", cmd.Path, &stderr)
	}
	return rgResult{stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()}
}

// TestSelfHostExampleRgAgainstRipgrep runs the port and ripgrep 14.1.0 with
// the same command lines on the same files. The stdout, the stderr and the
// exit status must be the same.
func TestSelfHostExampleRgAgainstRipgrep(t *testing.T) {
	version, err := exec.Command(ripgrep, "--version").Output()
	if err != nil {
		t.Fatalf("ripgrep 14.1.0 is required at %s: %v", ripgrep, err)
	}
	if !strings.HasPrefix(string(version), "ripgrep 14.1.0") {
		t.Fatalf("the oracle is ripgrep 14.1.0, but %s is %q", ripgrep, strings.SplitN(string(version), "\n", 2)[0])
	}
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/rg/rg.fern")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rgFixtures(t, dir)
	cases := rgCases(dir)
	if len(cases) < 60 {
		t.Fatalf("%d cases, fewer than 60", len(cases))
	}
	want := make([]rgResult, len(cases))
	for i, c := range cases {
		want[i] = rgRun(t, exec.Command(ripgrep, c.args...), dir, c.stdin)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var port func(args ...string) *exec.Cmd
			if target == "x86-64-linux" {
				bin := cli.x86Binary(t, src)
				port = func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
			} else {
				_, qemu := arm64Tooling(t)
				bin := cli.arm64Binary(t, src)
				port = func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
			}
			for i, c := range cases {
				got := rgRun(t, port(c.args...), dir, c.stdin)
				if got != want[i] {
					t.Errorf("rg %q, stdin %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
						c.args, c.stdin, got.code, got.stdout, got.stderr, want[i].code, want[i].stdout, want[i].stderr)
				}
			}
			t.Logf("%d command lines give the output of ripgrep", len(cases))
		})
	}
}
