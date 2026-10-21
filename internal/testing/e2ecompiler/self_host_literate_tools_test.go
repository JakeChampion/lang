package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -lint` and `fern -doc` accept a literate `.fern.md`, as every other
// mode that takes a `.fern` does (#12679): each runs over the modules the
// document tangles to, and a lint finding is reported at its document line. A
// directory walk passes over a chunk library, which has no module, and over a
// document beside a module of the same stem, which the loader does not take.
func TestSelfHostLiterateLintAndDoc(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	doc := "# Shapes\n\nProse about the module.\n\n```fern\n<<*>>=\n// area is the product of the sides.\npub function area(w: i32, h: i32): i32 {\n  if (w == 0) {\n    return 0;\n  }\n  return w * h;\n}\n```\n"
	writeTree(t, dir, map[string]string{
		"lit/shapes.fern.md":  doc,
		"lit/plain.fern":      "pub function one(): i32 {\n  return 1;\n}\n",
		"lit/plain.fern.md":   "# Plain\n\n```fern\n<<*>>=\npub function two(): i32 {\n  return 2;\n}\n```\n",
		"lit/library.fern.md": "# Chunks\n\n```fern\n<<clamp>>=\nif (x < 0) {\n  x = 0;\n}\n```\n",
		"broken/good.fern":    "pub function good(): i32 {\n  return 1;\n}\n",
		"broken/bad.fern.md":  "# Bad\n\n```fern\n<<*>>=\n<<missing>>\n```\n",
	})
	run := func(args ...string) (string, string, int) {
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Dir = dir
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("lint-document", func(t *testing.T) {
		out, errs, code := run("-lint", "-lint-opt", "cyclomatic-complexity.max=1", "lit/shapes.fern.md")
		// `pub function area` is the document's line 8; `function`, column 5.
		want := "lit/shapes.fern.md:8:5: warning[cyclomatic-complexity]: function `area` has a cyclomatic complexity of 2"
		if code != 0 || !strings.HasPrefix(out, want) || !strings.Contains(out, "\n    pub function area(w: i32, h: i32): i32 {\n") {
			t.Errorf("exit %d, stdout %q, stderr %q; want it to open %q and show the document line", code, out, errs, want)
		}
	})
	t.Run("lint-directory", func(t *testing.T) {
		out, errs, code := run("-lint", "-lint-opt", "cyclomatic-complexity.max=1", "lit")
		if code != 0 || errs != "" || !strings.Contains(out, "lit/shapes.fern.md:8:5:") {
			t.Errorf("exit %d, stdout %q, stderr %q; want the .fern.md linted and the chunk library passed over", code, out, errs)
		}
	})
	t.Run("lint-chunk-library-named", func(t *testing.T) {
		if _, errs, code := run("-lint", "lit/library.fern.md"); code != 1 || !strings.Contains(errs, "no root chunk") {
			t.Errorf("exit %d, stderr %q; want the named chunk library refused", code, errs)
		}
	})
	t.Run("doc-document", func(t *testing.T) {
		out, errs, code := run("-doc", "lit/shapes.fern.md")
		if code != 0 || !strings.Contains(out, "title: lit/shapes\n") || !strings.Contains(out, "## `area`\n\n```fern\npub function area(w: i32, h: i32): i32\n```\n\narea is the product of the sides.\n") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
		}
	})
	t.Run("doc-directory", func(t *testing.T) {
		if _, errs, code := run("-doc", "-o", "pages", "lit"); code != 0 || errs != "" {
			t.Fatalf("exit %d, stderr %q", code, errs)
		}
		for _, page := range []string{"shapes.md", "plain.md"} {
			if _, err := os.Stat(filepath.Join(dir, "pages", page)); err != nil {
				t.Errorf("no page %s: %v", page, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "pages", "library.md")); err == nil {
			t.Errorf("the chunk library got a page")
		}
		if plain := string(mustRead(t, filepath.Join(dir, "pages", "plain.md"))); !strings.Contains(plain, "`one`") || strings.Contains(plain, "`two`") {
			t.Errorf("plain.md is not plain.fern's page:\n%s", plain)
		}
	})
	t.Run("doc-directory-untangled", func(t *testing.T) {
		_, errs, code := run("-doc", "-o", "pages-broken", "broken")
		if code != 1 || !strings.Contains(errs, "bad.fern.md") {
			t.Errorf("exit %d, stderr %q; want the document reported and exit 1", code, errs)
		}
		if _, err := os.Stat(filepath.Join(dir, "pages-broken", "good.md")); err != nil {
			t.Errorf("the walk stopped before good.fern's page: %v", err)
		}
	})
}
