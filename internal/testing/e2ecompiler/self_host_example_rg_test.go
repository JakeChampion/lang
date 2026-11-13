package e2ecompiler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// rgCase is one command line, with its standard input, and the directory
// below the fixtures where it runs. With noStdin, the standard input is
// /dev/null. With anyOrder, the lines of the output can come in any order,
// as they do from ripgrep with more than one thread.
type rgCase struct {
	args     []string
	stdin    string
	dir      string
	noStdin  bool
	anyOrder bool
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
		"ab.txt":    "bbabb\nba\nfoobar xfoo\n",
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
		// Leftmost-first: the first alternative and the loop that ends on an
		// empty repeat win, where leftmost-longest gives a longer match.
		"-o foo|foobar w.txt",
		"-o -e foo -e foobar ab.txt",
		"-o -e foobar -e foo ab.txt",
		"--color always x*|foo w.txt",
		"--color always -n (?:foo|foob)a? ab.txt",
		"-o (?:b?(?:|a))* ab.txt",
		"-o b(|a)+ ab.txt",
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
		rgCase{args: rgArgs("foo"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-n foo -"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-j1 foo - a.txt"), stdin: stdinOf("w.txt")},
		rgCase{args: rgArgs("-H foo"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-c foo"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-l foo -"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("foo"), stdin: stdinOf("bin.dat")},
		rgCase{args: rgArgs("-o -n o+"), stdin: stdinOf("crlf.txt")},
		rgCase{args: rgArgs("foo"), stdin: ""},
	)
	return append(cases, rgWalkCases()...)
}

type rgResult struct {
	stdout, stderr string
	code           int
}

// sorted is the result with the lines of the stdout in order.
func (r rgResult) sorted() rgResult {
	lines := strings.SplitAfter(r.stdout, "\n")
	sort.Strings(lines)
	return rgResult{strings.Join(lines, ""), r.stderr, r.code}
}

// rgRun runs one case in `dir`, or in the directory of the case below it.
// The home directory is empty, so that no global gitignore applies.
func rgRun(t *testing.T, cmd *exec.Cmd, dir string, c rgCase) rgResult {
	t.Helper()
	cmd.Dir = filepath.Join(dir, c.dir)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(dir, "home")}
	if !c.noStdin {
		cmd.Stdin = strings.NewReader(c.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", cmd.Path, &stderr)
	}
	return rgResult{stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()}
}

// rgTree writes the tree that the walk cases search. `tree` is a git
// repository with each kind of ignore file. `plain` has a .gitignore and
// no .git, so that ripgrep does not use the .gitignore.
func rgTree(t *testing.T, dir string) {
	t.Helper()
	var big strings.Builder
	for i := 0; big.Len() < 70000; i++ {
		fmt.Fprintf(&big, "line %d of the text\n", i)
		if i%500 == 0 {
			big.WriteString("foo before the NUL byte\n")
		}
	}
	big.WriteString("abc\x00def foo\nfoo after the NUL byte\n")
	files := map[string]string{
		"home/.keep":             "",
		"tree/.git/info/exclude": "excluded.txt\n",
		"tree/.gitignore":        "*.log\n/build/\n!keep.log\nsub/deep/\n# a comment\n\\#hash.txt\n",
		"tree/.ignore":           "dot_ignored.txt\n",
		"tree/.rgignore":         "!dot_ignored.txt\nrg_ignored.txt\n",
		"tree/a.txt":             "foo here\nbar\n",
		"tree/b.log":             "foo log\n",
		"tree/keep.log":          "foo keep\n",
		"tree/#hash.txt":         "foo hash\n",
		"tree/build/out.txt":     "foo build\n",
		"tree/src/build/x.txt":   "foo src build\n",
		"tree/src/main.rs":       "fn foo() {}\n",
		"tree/src/lib.c":         "int foo;\n",
		"tree/src/Makefile":      "foo: bar\n",
		"tree/sub/.gitignore":    "*.md\n",
		"tree/sub/readme.md":     "foo md\n",
		"tree/sub/y.txt":         "foo y\nfoo y again\n",
		"tree/sub/deep/z.txt":    "foo deep\n",
		"tree/.hidden.txt":       "foo hidden\n",
		"tree/.hdir/h.txt":       "foo hdir\n",
		"tree/dot_ignored.txt":   "foo dot\n",
		"tree/rg_ignored.txt":    "foo rg\n",
		"tree/excluded.txt":      "foo excluded\n",
		"tree/bin.dat":           "foo\x00bar\n",
		"tree/big.txt":           big.String(),
		"tree/extra.ignore":      "a.txt\n",
		"plain/.gitignore":       "*.txt\n",
		"plain/p.txt":            "foo plain\n",
		"plain/q.md":             "foo q\n",
		"plain/nested/.ignore":   "r.md\n",
		"plain/nested/r.md":      "foo r\n",
		"plain/nested/s.md":      "foo s\n",
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "tree", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(dir, "tree", "linkdir")); err != nil {
		t.Fatal(err)
	}
}

// rgWalkCases search the trees of rgTree. ripgrep writes the files of a
// walk in the order of the walk only with one thread, so each case gives
// -j1 or --sort. A case with no path runs with no standard input, as
// ripgrep then searches the current directory.
func rgWalkCases() []rgCase {
	in := func(dir, line string) rgCase { return rgCase{args: rgArgs(line), dir: dir, noStdin: true} }
	return []rgCase{
		in("tree", "-j1 foo"),
		in("tree", "--sort path foo"),
		in("tree", "--sort path -n foo ."),
		in("tree/src", "--sort path foo"),
		in("tree/sub", "--sort path foo ../src"),
		in("", "--sort path foo tree"),
		in("", "--sort path foo tree/"),
		in("", "--sortr path foo tree"),
		in("", "--sort path foo tree plain"),
		in("", "--sort path foo tree/a.txt tree/b.log tree/.hidden.txt"),
		in("", "--sort path --hidden foo tree"),
		in("", "--sort path -. foo tree"),
		in("", "--sort path --no-ignore foo tree"),
		in("", "--sort path --no-ignore --ignore foo tree"),
		in("", "--sort path -u foo tree"),
		in("", "--sort path -uu foo tree"),
		in("", "--sort path -uuu foo tree"),
		in("", "--sort path -uuuu foo tree"),
		in("", "--sort path --no-ignore-vcs foo tree"),
		in("", "--sort path --no-ignore-dot foo tree"),
		in("", "--sort path --no-ignore-exclude foo tree"),
		in("", "--sort path --no-ignore-parent foo"),
		in("plain", "--sort path foo"),
		in("plain", "--sort path --no-require-git foo"),
		in("", "--sort path --ignore-file tree/extra.ignore foo tree"),
		in("", "--sort path --ignore-file nofile foo tree"),
		in("", "--sort path --max-depth 1 foo tree"),
		in("", "--sort path --maxdepth=0 foo tree"),
		in("", "--sort path -L foo tree"),
		in("", "--sort path -g *.txt foo tree"),
		in("", "--sort path -g !*.txt foo tree"),
		in("", "--sort path -g *.txt -g !sub/** foo tree"),
		in("", "--sort path -g *.log foo tree"),
		in("", "--sort path --iglob *.TXT foo tree"),
		in("", "--sort path --glob-case-insensitive -g *.TXT foo tree"),
		in("", "--sort path -g [ foo tree"),
		in("", "--sort path -t rust foo tree"),
		in("", "--sort path -t c -t make foo tree"),
		in("", "--sort path -T txt foo tree"),
		in("", "--sort path --type-add log:*.log -t log --no-ignore foo tree"),
		in("", "--sort path --type-add bad foo tree"),
		in("", "--sort path -t bogus foo tree"),
		in("", "--sort path --type-clear rust -t rust foo tree"),
		in("", "--type-add zz:*.zz --type-list"),
		in("", "--sort path --files tree"),
		in("", "--sort path --files --null tree"),
		in("", "--sort path --files -t rust tree"),
		in("", "--sortr path --files tree plain"),
		in("", "--sort path -l foo tree"),
		in("", "--sort path -l --null foo tree"),
		in("", "--sort path --files-without-match here tree"),
		in("", "--sort path -c foo tree"),
		in("", "--sort path -c --null foo tree"),
		in("", "--sort path -co foo tree"),
		in("", "--sort path -q foo tree"),
		in("", "--sort path -q zzz tree"),
		in("", "--sort path -m1 foo tree"),
		in("", "--sort path -m1 -A1 foo tree/sub"),
		in("", "--sort path -m0 foo tree"),
		in("", "--sort path -x foo.here tree"),
		in("", "--sort path -n --null foo tree/sub"),
		in("", "--sort path --heading foo tree"),
		in("", "--sort path --heading -n -A1 foo tree/sub tree/a.txt"),
		in("", "--sort path --no-heading foo tree/sub"),
		in("", "--sort path --path-separator \\ foo tree/sub"),
		in("", "--sort path -C1 foo tree/sub tree/a.txt"),
		in("", "--sort path --mmap foo tree"),
		in("", "--sort path --no-mmap foo tree"),
		in("", "--sort path foo tree/big.txt"),
		in("", "--sort path --no-mmap foo tree/big.txt"),
		in("", "--sort path -c foo tree/big.txt"),
		in("", "--sort path --no-mmap -c foo tree/big.txt"),
		in("", "--sort path -g big.txt foo tree"),
		in("", "--sort path -g big.txt -c foo tree"),
		in("", "--sort path -g big.txt -l foo tree"),
		in("", "--sort path -g big.txt --binary foo tree"),
		in("", "--sort path -g big.txt -a -c foo tree"),
		in("", "--sort path -g big.txt -B1 foo tree"),
		in("", "--sort path foo nosuchdir tree/sub"),
		in("tree", "-j1 -g nomatch foo"),
		in("", "foo tree/sub"),
		{args: rgArgs("-j2 -c foo tree"), noStdin: true, anyOrder: true},
		{args: rgArgs("-j3 -n foo tree"), noStdin: true, anyOrder: true},
		{args: rgArgs("-j4 -l foo tree plain"), noStdin: true, anyOrder: true},
		{args: rgArgs("-j2 --files tree"), noStdin: true, anyOrder: true},
	}
}

// rgOracle fails the test when /usr/bin/rg is not ripgrep 14.1.0.
func rgOracle(t *testing.T) {
	t.Helper()
	version, err := exec.Command(ripgrep, "--version").Output()
	if err != nil {
		t.Fatalf("ripgrep 14.1.0 is required at %s: %v", ripgrep, err)
	}
	if !strings.HasPrefix(string(version), "ripgrep 14.1.0") {
		t.Fatalf("the oracle is ripgrep 14.1.0, but %s is %q", ripgrep, strings.SplitN(string(version), "\n", 2)[0])
	}
}

// rgPort builds the port for `target` and gives the command that runs it.
func rgPort(t *testing.T, cli *selfHostCLI, target string) func(args ...string) *exec.Cmd {
	t.Helper()
	src, err := filepath.Abs("../../../examples/rg/rg.fern")
	if err != nil {
		t.Fatal(err)
	}
	if target == "x86-64-linux" {
		bin := cli.x86Binary(t, src)
		return func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
	}
	_, qemu := arm64Tooling(t)
	bin := cli.arm64Binary(t, src)
	return func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
}

// TestSelfHostExampleRgAgainstRipgrep runs the port and ripgrep 14.1.0 with
// the same command lines on the same files. The stdout, the stderr and the
// exit status must be the same.
func TestSelfHostExampleRgAgainstRipgrep(t *testing.T) {
	rgOracle(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	rgFixtures(t, dir)
	rgTree(t, dir)
	cases := rgCases(dir)
	if len(cases) < 60 {
		t.Fatalf("%d cases, fewer than 60", len(cases))
	}
	want := make([]rgResult, len(cases))
	for i, c := range cases {
		want[i] = rgRun(t, exec.Command(ripgrep, c.args...), dir, c)
		if c.anyOrder {
			want[i] = want[i].sorted()
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := rgPort(t, cli, target)
			for i, c := range cases {
				got := rgRun(t, port(c.args...), dir, c)
				if c.anyOrder {
					got = got.sorted()
				}
				if got != want[i] {
					t.Errorf("rg %q in %q, stdin %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
						c.args, c.dir, c.stdin, got.code, got.stdout, got.stderr, want[i].code, want[i].stdout, want[i].stderr)
				}
			}
			t.Logf("%d command lines give the output of ripgrep", len(cases))
		})
	}
}

// rgVendored is a case of ripgrep's integration tests, as
// examples/rg/testdata/ripgrep-tests.json holds it.
type rgVendored struct {
	Name  string        `json:"name"`
	Args  []string      `json:"args"`
	Cwd   string        `json:"cwd"`
	Stdin *rgVendorFile `json:"stdin"`
	Setup []rgVendorOp  `json:"setup"`
}

// rgVendorFile is the content of a file: text, base64 bytes, a constant of
// the tests, a file of testdata, or a count of NUL bytes.
type rgVendorFile struct {
	Text   *string `json:"text"`
	Base64 string  `json:"base64"`
	Const  string  `json:"const"`
	Data   string  `json:"data"`
	Size   int64   `json:"size"`
}

// rgVendorOp is one step of a case: make a file or a directory, remove a
// file, or make a symbolic link to a path in the directory of the case.
type rgVendorOp struct {
	Op     string `json:"op"`
	Path   string `json:"path"`
	Target string `json:"target"`
	rgVendorFile
}

type rgVendorSuite struct {
	Consts   map[string]string `json:"consts"`
	Cases    []rgVendored      `json:"cases"`
	testdata string
}

func (s *rgVendorSuite) bytes(t *testing.T, f rgVendorFile) []byte {
	t.Helper()
	switch {
	case f.Text != nil:
		return []byte(*f.Text)
	case f.Base64 != "":
		b, err := base64.StdEncoding.DecodeString(f.Base64)
		if err != nil {
			t.Fatal(err)
		}
		return b
	case f.Const != "":
		return []byte(s.Consts[f.Const])
	case f.Data != "":
		b, err := os.ReadFile(filepath.Join(s.testdata, f.Data))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return make([]byte, f.Size)
}

func (s *rgVendorSuite) setup(t *testing.T, dir string, c rgVendored) {
	t.Helper()
	for _, op := range c.Setup {
		path := filepath.Join(dir, op.Path)
		var err error
		switch op.Op {
		case "file":
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				err = os.WriteFile(path, s.bytes(t, op.rgVendorFile), 0o644)
			}
		case "dir":
			err = os.MkdirAll(path, 0o755)
		case "remove":
			err = os.Remove(path)
		case "symlink":
			err = os.Symlink(filepath.Join(dir, op.Target), path)
		default:
			t.Fatalf("%s: unknown step %q", c.Name, op.Op)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
	}
}

// run runs one case in a new directory. Each run gets --path-separator /
// first, as ripgrep's harness gives it, and -j1, so that ripgrep writes the
// files in the order of its walk. The home directory is empty, so that no
// global gitignore applies, and the standard input is /dev/null unless the
// case gives one.
func (s *rgVendorSuite) run(t *testing.T, prog func(args ...string) *exec.Cmd, c rgVendored) rgResult {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	s.setup(t, dir, c)
	cmd := prog(append([]string{"--path-separator", "/", "-j1"}, c.Args...)...)
	cmd.Dir = filepath.Join(dir, c.Cwd)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(s.bytes(t, *c.Stdin))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", cmd.Path, &stderr)
	}
	// The temporary directory in a message differs from run to run.
	return rgResult{stdout.String(), strings.ReplaceAll(stderr.String(), dir, "DIR"), cmd.ProcessState.ExitCode()}
}

// TestSelfHostExampleRgVendoredCases runs the cases of the integration
// tests of ripgrep 14.1.1 with the port and with ripgrep 14.1.0. The
// stdout, the stderr and the exit status must be the same for exactly the
// cases that testdata/ripgrep-pass.txt names. A named case that fails is a
// regression, and a case that passes must be named.
func TestSelfHostExampleRgVendoredCases(t *testing.T) {
	rgOracle(t)
	testdata, err := filepath.Abs("../../../examples/rg/testdata")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(testdata, "ripgrep-tests.json"))
	if err != nil {
		t.Fatal(err)
	}
	suite := rgVendorSuite{testdata: testdata}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	pinned := rgPinned(t, filepath.Join(testdata, "ripgrep-pass.txt"))
	oracle := func(args ...string) *exec.Cmd { return exec.Command(ripgrep, args...) }
	want := make([]rgResult, len(suite.Cases))
	for i, c := range suite.Cases {
		want[i] = suite.run(t, oracle, c)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := rgPort(t, cli, target)
			pass := 0
			for i, c := range suite.Cases {
				got := suite.run(t, port, c)
				same := got == want[i]
				if same {
					pass++
				}
				if same && !pinned[c.Name] {
					t.Errorf("%s passes, but testdata/ripgrep-pass.txt does not name it", c.Name)
				}
				if !same && pinned[c.Name] {
					t.Errorf("%s regressed: rg %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
						c.Name, c.Args, got.code, got.stdout, got.stderr, want[i].code, want[i].stdout, want[i].stderr)
				}
			}
			t.Logf("%d of %d vendored cases give the output of ripgrep", pass, len(suite.Cases))
		})
	}
}

// rgPinned reads the names of the cases that must pass.
func rgPinned(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			pinned[line] = true
		}
	}
	return pinned
}
