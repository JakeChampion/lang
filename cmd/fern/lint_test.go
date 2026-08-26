package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noisy scores 3; quiet scores 1.
const noisySrc = `function noisy(n: i32): i32 {
    if (n > 0) { return 1; }
    if (n < 0) { return 2; }
    return 0;
}
`

const quietSrc = "function quiet(): i32 { return 0; }\n"

func writeFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runLintTest(t *testing.T, paths, sets, opts []string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := runLint(paths, sets, opts, &out)
	return code, out.String()
}

func TestLintExitStatus(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "a.fern", noisySrc)

	// A warning reports but leaves the exit status alone; only `deny`
	// fails the run. That difference is the whole point of severities.
	code, out := runLintTest(t, []string{f}, nil, []string{"cyclomatic-complexity.max=2"})
	if code != 0 {
		t.Errorf("warn exit = %d, want 0", code)
	}
	if !strings.Contains(out, "warning[cyclomatic-complexity]") || !strings.Contains(out, "1 warning generated") {
		t.Errorf("output missing the warning:\n%s", out)
	}

	code, out = runLintTest(t, []string{f}, []string{"cyclomatic-complexity=deny"}, []string{"cyclomatic-complexity.max=2"})
	if code != 1 {
		t.Errorf("deny exit = %d, want 1", code)
	}
	if !strings.Contains(out, "error[cyclomatic-complexity]") {
		t.Errorf("output missing the error:\n%s", out)
	}

	// Nothing to report is silent, the way a clean `-check` is.
	code, out = runLintTest(t, []string{f}, nil, nil)
	if code != 0 || out != "" {
		t.Errorf("clean run: exit %d, output %q", code, out)
	}
}

func TestLintWalksDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.fern", noisySrc)
	writeFile(t, dir, "nested/b.fern", noisySrc)
	writeFile(t, dir, "nested/notfern.txt", "ignored")

	code, out := runLintTest(t, []string{dir}, nil, []string{"cyclomatic-complexity.max=2"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "2 warnings generated") {
		t.Errorf("want a finding from each nested .fern source:\n%s", out)
	}
}

// A path named twice must be linted once, or a `fern -lint . a.fern` run
// double-reports every finding in a.fern.
func TestLintDeduplicatesTargets(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "a.fern", noisySrc)
	_, out := runLintTest(t, []string{dir, f, f}, nil, []string{"cyclomatic-complexity.max=2"})
	if !strings.Contains(out, "1 warning generated") {
		t.Errorf("overlapping targets were linted more than once:\n%s", out)
	}
}

func TestLintUsageErrors(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "a.fern", quietSrc)
	md := writeFile(t, dir, "doc.fern.md", "# hi\n")
	txt := writeFile(t, dir, "notes.txt", "hi\n")

	cases := []struct {
		name  string
		paths []string
		sets  []string
		opts  []string
	}{
		{"missing path", []string{filepath.Join(dir, "nope.fern")}, nil, nil},
		{"literate document", []string{md}, nil, nil},
		{"not a fern source", []string{txt}, nil, nil},
		{"empty directory", []string{t.TempDir()}, nil, nil},
		{"malformed -lint-set", []string{good}, []string{"cyclomatic-complexity"}, nil},
		{"unknown rule", []string{good}, []string{"nope=deny"}, nil},
		{"malformed -lint-opt", []string{good}, nil, []string{"cyclomatic-complexity.max"}},
		{"unknown option", []string{good}, nil, []string{"cyclomatic-complexity.limit=3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runLintTest(t, tc.paths, tc.sets, tc.opts); code != 2 {
				t.Errorf("exit = %d, want 2 (usage error)", code)
			}
		})
	}
}

// A file that does not parse has no shape to lint, but must not stop the
// run: linting a tree should report every file it can read.
func TestLintContinuesPastUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a_broken.fern", "function {{{\n")
	writeFile(t, dir, "b_good.fern", noisySrc)

	code, out := runLintTest(t, []string{dir}, nil, []string{"cyclomatic-complexity.max=2"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "1 warning generated") {
		t.Errorf("the parseable file was not linted:\n%s", out)
	}
}

func TestLintReadsManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "fern.toml", "[package]\nname = \"demo\"\n\n[lint]\ncyclomatic-complexity = \"deny\"\n\n[lint.options]\ncyclomatic-complexity.max = 2\n")
	f := writeFile(t, dir, "a.fern", noisySrc)

	code, out := runLintTest(t, []string{f}, nil, nil)
	if code != 1 {
		t.Fatalf("exit = %d, want the manifest's deny to fail the run\n%s", code, out)
	}
	if !strings.Contains(out, "error[cyclomatic-complexity]") || !strings.Contains(out, "limit of 2") {
		t.Errorf("manifest severity and option did not both apply:\n%s", out)
	}

	// A flag beats the manifest — otherwise a checked-in setting could
	// not be overridden for one run.
	if code, out := runLintTest(t, []string{f}, []string{"cyclomatic-complexity=allow"}, nil); code != 0 || out != "" {
		t.Errorf("-lint-set did not override the manifest: exit %d, output %q", code, out)
	}
}

func TestLintRejectsBadManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "fern.toml", "[package]\nname = \"demo\"\n\n[lint]\nnot-a-rule = \"deny\"\n")
	f := writeFile(t, dir, "a.fern", quietSrc)
	code, _ := runLintTest(t, []string{f}, nil, nil)
	if code != 2 {
		t.Errorf("exit = %d, want 2 — a manifest naming no known rule configures nothing", code)
	}
}

func TestListLintRules(t *testing.T) {
	var out bytes.Buffer
	listLintRules(&out)
	got := out.String()
	for _, want := range []string{"cyclomatic-complexity", "[warn]", "option cyclomatic-complexity.max ="} {
		if !strings.Contains(got, want) {
			t.Errorf("`-lint-rules` output missing %q:\n%s", want, got)
		}
	}
}
