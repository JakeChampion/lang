package e2ecompiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// markdownSpecPass is the set of the examples of the CommonMark 0.31.2 spec
// whose HTML the port gives exactly. The test fails when an example in the set
// fails, and when an example outside the set passes, so the set moves with
// each change to the port.
const markdownSpecPass = "1-14,16,18-19,21,24,28-31,36,42-55,57-65,67-79,83-120,122-137,139-144,146-147,149-151,153-154,156-166,169-175,178-186,189-191,197,199,207-213,219-326,347-348,351-354,358-363,365-368,371-372,374-375,379-380,383-388,391-392,397-398,400-401,420-421,434-436,439,448,451,488,490,493,497,508,511,513,545-548,551-552,563,590,592,602,606-612,618-622,624,632-637,644-652"

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

func runMarkdown(t *testing.T, bin, input string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin)
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
		got, stderr, code := runMarkdown(t, bin, ex.Markdown)
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

// The command line gives the HTML of cmark for documents whose text needs no
// inline parse beyond escapes and line breaks. cmark 0.30.2, the version in
// the package archives, has the HTML block rules of spec 0.30, so no case
// uses the tags `search` or `source`, or `<!` and a lower-case letter.
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
	}
	for _, c := range cases {
		cmd := exec.Command(ref, "--unsafe")
		cmd.Stdin = strings.NewReader(c)
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("cmark on %q: %v", c, err)
		}
		got, stderr, code := runMarkdown(t, bin, c)
		if code != 0 || got != string(want) {
			t.Errorf("md on %q: exit %d\ngot  %q\nwant %q\n%s", c, code, got, want, stderr)
		}
	}
}
