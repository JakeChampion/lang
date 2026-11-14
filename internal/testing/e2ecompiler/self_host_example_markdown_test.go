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
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// markdownSpecPass is the set of the examples of the CommonMark 0.31.2 spec
// whose HTML the port gives exactly. The test fails when an example in the set
// fails, and when an example outside the set passes, so the set moves with
// each change to the port.
const markdownSpecPass = "1-652"

func TestSelfHostExampleMarkdownUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/markdown/markdown_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// buildMarkdown compiles the markdown command line for this host.
func buildMarkdown(t *testing.T) string {
	t.Helper()
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native self-host target")
	}
	src, err := filepath.Abs("../../../examples/markdown/md.fern")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	bin := filepath.Join(t.TempDir(), "md")
	if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build md: %v\n%s", err, out)
	}
	return bin
}

type markdownExample struct {
	Markdown string `json:"markdown"`
	HTML     string `json:"html"`
	Example  int    `json:"example"`
	Section  string `json:"section"`
}

// markdownSet reads a list of example numbers and ranges such as "1-3,5".
func markdownSet(t *testing.T, list string) map[int]bool {
	t.Helper()
	set := map[int]bool{}
	for _, part := range strings.Split(list, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			t.Fatalf("markdownSpecPass: %q is not a number", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				t.Fatalf("markdownSpecPass: %q is not a range", part)
			}
		}
		for n := a; n <= b; n++ {
			set[n] = true
		}
	}
	return set
}

func runMarkdown(t *testing.T, bin, input string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", bin, stderr.String())
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// The spec runner of CommonMark normalizes the HTML before it compares it.
// This test compares the exact text, because the port gives the HTML of cmark.
// As the spec runner of cmark does, it gives md the option --unsafe.
func TestSelfHostExampleMarkdownSpec(t *testing.T) {
	data, err := os.ReadFile("../../../examples/markdown/tests/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples []markdownExample
	if err := json.Unmarshal(data, &examples); err != nil {
		t.Fatal(err)
	}
	if len(examples) != 652 {
		t.Fatalf("spec.json has %d examples, want the 652 of CommonMark 0.31.2", len(examples))
	}
	want := markdownSet(t, markdownSpecPass)
	bin := buildMarkdown(t)
	passed := map[string]int{}
	total := map[string]int{}
	var sections []string
	var lost, gained []int
	for _, ex := range examples {
		if total[ex.Section] == 0 {
			sections = append(sections, ex.Section)
		}
		total[ex.Section]++
		got, stderr, code := runMarkdown(t, bin, ex.Markdown, "--unsafe")
		ok := code == 0 && got == ex.HTML
		if ok {
			passed[ex.Section]++
		}
		switch {
		case ok && !want[ex.Example]:
			gained = append(gained, ex.Example)
		case !ok && want[ex.Example]:
			lost = append(lost, ex.Example)
			t.Errorf("example %d (%s): exit %d\ninput %q\ngot   %q\nwant  %q\n%s", ex.Example, ex.Section, code, ex.Markdown, got, ex.HTML, stderr)
		}
	}
	var report strings.Builder
	sum := 0
	for _, s := range sections {
		fmt.Fprintf(&report, "\n%4d/%4d %s", passed[s], total[s], s)
		sum += passed[s]
	}
	t.Logf("%d of %d examples pass:%s", sum, len(examples), report.String())
	sort.Ints(gained)
	if len(gained) > 0 {
		t.Errorf("examples %v pass now and are not in markdownSpecPass; add them", gained)
	}
	if len(lost) > 0 {
		t.Errorf("examples %v are in markdownSpecPass and fail", lost)
	}
}

// The command line gives the HTML of cmark. cmark 0.30.2 is the version in
// the package archives. Where it does not follow spec 0.31.2, the port
// follows the spec (see examples/markdown/README.md). Thus no case uses the
// tags `search` or `source`, `<!` and a lower-case letter, or a comment that
// spec 0.31.2 changed. No case has a symbol such as `£` next to a delimiter
// run, or a `_` closer after a `_` closer that found no opener. No case has
// a definition whose title is not the end of its line.
func TestSelfHostExampleMarkdownAgainstCmark(t *testing.T) {
	ref, err := exec.LookPath("cmark")
	if err != nil {
		t.Fatalf("cmark 0.30.2 is required on PATH: %v", err)
	}
	version, err := exec.Command(ref, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "cmark 0.30.2 ") {
		t.Fatalf("the oracle is cmark 0.30.2, but %s gives %q (%v)", ref, strings.SplitN(string(version), "\n", 2)[0], err)
	}
	bin := buildMarkdown(t)
	cases := []string{
		"# h1\n## h2 ##\n###### h6\n####### p\n#nope\n",
		"Setext\n===\n\nAnd\nmore\n---\n\n- - -\n___\n",
		"    code\n\n\n      more\n\t tab\n\n",
		"~~~ py x\nkeep  \n~~~~\n``` a\\*b\n```\n````\nopen",
		"> a\n> > b\nlazy\n>\n> - c\n>   - d\n\n> e\n",
		"- a\n- b\n\n- c\n\n1. x\n2. y\n3) z\n\n10. ten\n",
		"- a\n  - b\n    - c\n\n      d\n- e\n\n\n  f\n",
		"-\n  foo\n-\n\n  bar\n* \n+ x\n",
		"1. a\n\n   b\n\n       code\n2. c\n",
		"<div>\n*x*\n\ny\n<!-- a\n\nb -->\n<?p ?>\n<!X>\n<![CDATA[\nz\n]]>\n",
		"<pre>\n\n</pre>\n<style>\n\n</style>\n<textarea>\nt\n\n</textarea>\n",
		"<a b=\"c\">\n</a>\n\n<del>\n\n</del>\n",
		"[a]: /u 'b'\n[b]:\n  <c d>\n  \"e\"\n\nx\n\n[c]: /u x\n",
		"a  \nb\\\nc\t\nd\\\\\n  e   ",
		"\tfoo\tbar\n -\tx\n\n\t\ty\n>\t\tz\n",
		"a\r\nb\r\n\r\n    c\r\n- d\re\n",
		"\ufeff# bom\n",
		"a\x00b\n",
		"*a* **b** _c_ __d__ ***e*** *f **g** h* *(a)* **[b](c)** 5*6*7 a_b_c\n",
		"*foo**bar**baz* ***foo** bar* foo***bar***baz **foo*bar** *a\nb*\n",
		"`code` `` a`b `` ` x ` `\n` ```a`` a\\*b \\[c\\] \\q &amp; &copy; &#35; &#x41; &#0; &bogus; &copy\n",
		"[a](/u \"t\") [b](<c d> 'e') [c][d] [d] [D][] [e]() ![i](/j \"k\") [f](/g(h)i \"&quot;\")\n\n[d]: /url (title)\n",
		"<http://a.b/c?d=e&f> <x@y.z> <A+b:c> <a href=\"x\"> </b> <?p ?> <![CDATA[x]]> <!X y> <a@b> <3>\n",
		"a  \nb\\\nc\n  d\\\n e\n\n[link *em `code`*](/u?a=b&c=ö \"t&amp;\")\n",
		"![a *b* [c](d) `e` <f>](g) [[nested]](/u) [a [b](c) d](e) [x ![y](z) w](v)\n",
		"> *a\n> b* `c\n> d`\n\n- [e\n  f](g)\n\n1. **h\n   i**\n",
		"[foo][bar] [bar] [baz][]\n\n[BAR]: /b 'one'\n[baz]: <x y> \"two\"\n[foo]: /f\n",
		"a <b> &lt;c&gt; \"d\" 'e' f > g & h\n# *i* `j` [k](l)\nm\n===\n",
	}
	for _, c := range cases {
		cmd := exec.Command(ref, "--unsafe")
		cmd.Stdin = strings.NewReader(c)
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("cmark on %q: %v", c, err)
		}
		got, stderr, code := runMarkdown(t, bin, c, "--unsafe")
		if code != 0 || got != string(want) {
			t.Errorf("md on %q: exit %d\ngot  %q\nwant %q\n%s", c, code, got, want, stderr)
		}
	}
	// md writes the HTML of a long document in parts of 64 KiB. It reads a
	// file argument with no copy when the file is valid UTF-8. A code fence
	// that does not close holds the rest of a document, so that case is not
	// in the long document.
	var parts []string
	for _, c := range cases {
		if !strings.HasSuffix(c, "open") {
			parts = append(parts, c)
		}
	}
	long := strings.Repeat(strings.Join(parts, "\n\n")+"\n\n", 100)
	cmd := exec.Command(ref, "--unsafe")
	cmd.Stdin = strings.NewReader(long)
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("cmark on the long document: %v", err)
	}
	if len(want) < 4*65536 {
		t.Fatalf("the HTML of the long document has %d bytes, want 4 parts of 64 KiB or more", len(want))
	}
	if got, stderr, code := runMarkdown(t, bin, long, "--unsafe"); code != 0 || got != string(want) {
		t.Errorf("md on the long document: exit %d, %d bytes, want %d\n%s", code, len(got), len(want), stderr)
	}
	file := filepath.Join(t.TempDir(), "long.md")
	if err := os.WriteFile(file, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := exec.Command(bin, "--unsafe", file).Output(); err != nil || string(got) != string(want) {
		t.Errorf("md on the long document as a file: %v, %d bytes, want %d", err, len(got), len(want))
	}
	// A file that is not valid UTF-8 gives the same HTML as stdin.
	bad := "a\xffb *c\xe2\x82* \xf0\x9f\n"
	if err := os.WriteFile(file, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	fromStdin, _, _ := runMarkdown(t, bin, bad)
	if got, err := exec.Command(bin, file).Output(); err != nil || string(got) != fromStdin || !strings.Contains(fromStdin, "�") {
		t.Errorf("md on a file that is not UTF-8: %v\ngot   %q\nstdin %q", err, got, fromStdin)
	}
}

// markdownRun runs a build of md for one target: its arguments, its stdin
// and the directory where it runs. It gives stdout, stderr and the exit
// status.
type markdownRun func(args []string, stdin []byte, dir string) (string, string, int)

// markdownTargets builds md for x86-64, arm64 and wasm32-wasi.
func markdownTargets(t *testing.T) map[string]markdownRun {
	t.Helper()
	src, err := filepath.Abs("../../../examples/markdown/md.fern")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	runs := map[string]markdownRun{}
	x86 := filepath.Join(t.TempDir(), "md")
	if out, err := cli.compileCmd("x86-64-linux", x86, src).CombinedOutput(); err != nil {
		t.Fatalf("build md for x86-64-linux: %v\n%s", err, out)
	}
	runs["x86-64-linux"] = func(args []string, stdin []byte, dir string) (string, string, int) {
		return markdownExec(t, runX86_64Bin(cli.runner, x86, args...), stdin, dir)
	}
	_, qemu := arm64Tooling(t)
	arm := cli.arm64Binary(t, src)
	runs["arm64-linux"] = func(args []string, stdin []byte, dir string) (string, string, int) {
		return markdownExec(t, runArm64Bin(qemu, arm, args...), stdin, dir)
	}
	wasmtime := e2eharness.Wasmtime(t)
	wasm := cli.wasmComponent(t, src)
	runs["wasm32-wasi"] = func(args []string, stdin []byte, dir string) (string, string, int) {
		return markdownExec(t, exec.Command(wasmtime, append([]string{"run", "--dir", ".", wasm}, args...)...), stdin, dir)
	}
	return runs
}

func markdownExec(t *testing.T, cmd *exec.Cmd, stdin []byte, dir string) (string, string, int) {
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Errorf("%s did not start: %s", cmd.Path, stderr.String())
		return "", "", -1
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// markdownBytes reads a field of tests/cmark.json: a string, or the base64
// of bytes that are not UTF-8.
func markdownBytes(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s)
	}
	var b struct {
		Base64 string `json:"base64"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("tests/cmark.json: %s is not a string or base64: %v", raw, err)
	}
	out, err := base64.StdEncoding.DecodeString(b.Base64)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type markdownCase struct {
	Args   []string        `json:"args"`
	Stdin  json.RawMessage `json:"stdin"`
	Stdout json.RawMessage `json:"stdout"`
	Stderr json.RawMessage `json:"stderr"`
	Exit   int             `json:"exit"`
}

// The usage text of md is the usage text of cmark with the name md. Its
// version text names md, and is not in the cases.
func markdownUsage(stdout string) string {
	return strings.Replace(stdout, "Usage:   cmark [FILE*]", "Usage:   md [FILE*]", 1)
}

// TestSelfHostExampleMarkdownCmarkCases runs md on each target on the cases
// of tests/cmark.json, which scripts/gen-markdown-cmark-cases.py records
// from cmark 0.30.2: each option and format, the errors of the command
// line, files that are joined, missing or a directory, and invalid UTF-8.
// Stdout, stderr and the exit status must be those of cmark. When cmark is
// on the PATH, the recorded output must still be its output.
func TestSelfHostExampleMarkdownCmarkCases(t *testing.T) {
	data, err := os.ReadFile("../../../examples/markdown/tests/cmark.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Files map[string]json.RawMessage `json:"files"`
		Cases []markdownCase             `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, raw := range corpus.Files {
		if err := os.WriteFile(filepath.Join(dir, name), markdownBytes(t, raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ref, err := exec.LookPath("cmark"); err == nil {
		for _, c := range corpus.Cases {
			stdout, stderr, code := markdownExec(t, exec.Command(ref, c.Args...), markdownBytes(t, c.Stdin), dir)
			if stdout != string(markdownBytes(t, c.Stdout)) || stderr != string(markdownBytes(t, c.Stderr)) || code != c.Exit {
				t.Errorf("cmark %q does not give the recorded output; run scripts/gen-markdown-cmark-cases.py", c.Args)
			}
		}
	} else {
		t.Logf("cmark is not on the PATH, so only the recorded output is checked")
	}
	for target, run := range markdownTargets(t) {
		t.Run(target, func(t *testing.T) {
			for _, c := range corpus.Cases {
				stdout, stderr, code := run(c.Args, markdownBytes(t, c.Stdin), dir)
				want := markdownUsage(string(markdownBytes(t, c.Stdout)))
				wantErr := string(markdownBytes(t, c.Stderr))
				if stdout != want || stderr != wantErr || code != c.Exit {
					t.Errorf("md %q: exit %d, want %d\nstdout %q\nwant   %q\nstderr %q\nwant   %q", c.Args, code, c.Exit, stdout, want, stderr, wantErr)
				}
			}
		})
	}
}

// markdownCmarkOnly is the set of the spec examples where the port follows
// spec 0.31.2 and cmark 0.30.2 does not (see examples/markdown/README.md).
// Their output differs in each format.
var markdownCmarkOnly = map[int]bool{354: true, 625: true, 626: true}

// TestSelfHostExampleMarkdownFormats gives each example of the spec to md on
// each target and to cmark, for each format and some sets of options, and
// compares the output. Only the examples of markdownCmarkOnly differ.
func TestSelfHostExampleMarkdownFormats(t *testing.T) {
	ref, err := exec.LookPath("cmark")
	if err != nil {
		t.Fatalf("cmark 0.30.2 is required on PATH: %v", err)
	}
	data, err := os.ReadFile("../../../examples/markdown/tests/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples []markdownExample
	if err := json.Unmarshal(data, &examples); err != nil {
		t.Fatal(err)
	}
	sets := [][]string{
		{"-t", "html", "--sourcepos", "--smart"},
		{"-t", "xml", "--sourcepos"},
		{"-t", "xml", "--smart", "--unsafe"},
		{"-t", "man"},
		{"-t", "man", "--width", "20", "--smart"},
		{"-t", "latex"},
		{"-t", "latex", "--width", "20", "--smart"},
		{"-t", "commonmark"},
		{"-t", "commonmark", "--width", "20", "--smart"},
		{"-t", "commonmark", "--hardbreaks"},
		{"-t", "commonmark", "--nobreaks", "--width", "30"},
	}
	type job struct{ set, example int }
	var jobs []job
	for s := range sets {
		for e := range examples {
			jobs = append(jobs, job{s, e})
		}
	}
	dir := t.TempDir()
	parallel := func(run markdownRun) []string {
		out := make([]string, len(jobs))
		next := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range next {
					j := jobs[i]
					stdout, stderr, code := run(sets[j.set], []byte(examples[j.example].Markdown), dir)
					out[i] = fmt.Sprintf("exit %d\nstderr %q\n%s", code, stderr, stdout)
				}
			}()
		}
		for i := range jobs {
			next <- i
		}
		close(next)
		wg.Wait()
		return out
	}
	want := parallel(func(args []string, stdin []byte, dir string) (string, string, int) {
		return markdownExec(t, exec.Command(ref, args...), stdin, dir)
	})
	for target, run := range markdownTargets(t) {
		t.Run(target, func(t *testing.T) {
			got := parallel(run)
			differ := map[string][]int{}
			for i, j := range jobs {
				n := examples[j.example].Example
				if (got[i] != want[i]) == markdownCmarkOnly[n] {
					continue
				}
				key := strings.Join(sets[j.set], " ")
				differ[key] = append(differ[key], n)
				if len(differ[key]) <= 2 {
					t.Errorf("md %s on example %d, input %q:\ngot  %q\nwant %q", key, n, examples[j.example].Markdown, got[i], want[i])
				}
			}
			for key, list := range differ {
				t.Errorf("md %s: examples %v are not as markdownCmarkOnly says", key, list)
			}
		})
	}
}
