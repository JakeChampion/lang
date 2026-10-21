package e2ecompiler

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -fmt` and `fern -lint` take what `-check` and `-interp` take: `-` for
// stdin, and a directory for every source beneath it (#12680).
func TestSelfHostFmtLintPaths(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	unformatted := "function main(): i32 {\nreturn 0;\n}\n"
	writeTree(t, dir, map[string]string{
		"tree/a.fern":      unformatted,
		"tree/sub/b.fern":  "function f(): i32 {\nreturn 1;\n}\n",
		"tree/c.fern.md":   "# Doc\n\n```fern\n<<*>>=\nfunction main(): i32 {\nreturn 0;\n}\n```\n",
		"tree/notes.txt":   "not a source\n",
		"empty/readme.txt": "nothing to format\n",
	})
	run := func(stdin string, args ...string) (string, string, int) {
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("fmt-stdin", func(t *testing.T) {
		out, errs, code := run(unformatted, "-fmt", "-")
		if code != 0 || out != "function main(): i32 {\n  return 0;\n}\n" {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
		}
	})
	t.Run("fmt-diff-stdin", func(t *testing.T) {
		out, _, code := run(unformatted, "-fmt", "-d", "-")
		if code != 1 || !strings.HasPrefix(out, "--- <stdin>\n+++ <stdin>\n") {
			t.Errorf("exit %d, stdout %q; want a diff of <stdin> and exit 1", code, out)
		}
	})
	t.Run("fmt-write-stdin", func(t *testing.T) {
		_, errs, code := run(unformatted, "-fmt", "-w", "-")
		if code != 2 || !strings.Contains(errs, "cannot write back stdin") {
			t.Errorf("exit %d, stderr %q; want a usage error", code, errs)
		}
	})
	t.Run("fmt-directory", func(t *testing.T) {
		out, errs, code := run("", "-fmt", "-d", "tree")
		var heads []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "--- ") {
				heads = append(heads, strings.TrimPrefix(l, "--- "))
			}
		}
		want := []string{"tree/a.fern", "tree/c.fern.md", "tree/sub/b.fern"}
		if code != 1 || strings.Join(heads, " ") != strings.Join(want, " ") {
			t.Errorf("exit %d, diffs of %v, stderr %q; want %v", code, heads, errs, want)
		}
	})
	t.Run("fmt-write-directory", func(t *testing.T) {
		if _, errs, code := run("", "-fmt", "-w", "tree"); code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errs)
		}
		if got := string(mustRead(t, filepath.Join(dir, "tree", "sub", "b.fern"))); got != "function f(): i32 {\n  return 1;\n}\n" {
			t.Errorf("tree/sub/b.fern = %q after -w", got)
		}
		if got := string(mustRead(t, filepath.Join(dir, "tree", "notes.txt"))); got != "not a source\n" {
			t.Errorf("-fmt -w rewrote a non-source: %q", got)
		}
	})
	t.Run("fmt-empty-directory", func(t *testing.T) {
		_, errs, code := run("", "-fmt", "empty")
		if code != 2 || !strings.Contains(errs, "no .fern or .fern.md files under empty") {
			t.Errorf("exit %d, stderr %q", code, errs)
		}
	})
	t.Run("fmt-missing-file", func(t *testing.T) {
		_, errs, code := run("", "-fmt", "missing.fern")
		if code != 1 || !strings.HasPrefix(errs, "fern: cannot read file missing.fern: ") {
			t.Errorf("exit %d, stderr %q", code, errs)
		}
	})
	t.Run("lint-stdin", func(t *testing.T) {
		src := "function f(a: i32): i32 {\n  if (a == 1) {\n    return 1;\n  }\n  return 0;\n}\n"
		out, errs, code := run(src, "-lint", "-lint-opt", "cyclomatic-complexity.max=1", "-")
		if code != 0 || !strings.HasPrefix(out, "<stdin>:1:1: warning[cyclomatic-complexity]") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
		}
	})
}
