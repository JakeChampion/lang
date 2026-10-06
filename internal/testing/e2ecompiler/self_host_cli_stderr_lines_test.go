package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The driver's stderr reports are one line each. eprint appends the newline
// itself, so a message that also ends in "\n" leaves a blank line after it, and
// one split across several eprint calls comes out over several lines. stderr
// is compared whole, trailing newline included, so either shape fails.
func TestSelfHostCLIStderrIsOneLine(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	entry := write("ok.fern", "function main(): i32 {\n  return 0;\n}\n")
	doc := write("doc.fern.md", "# doc\n\n```fern file=a.fern\nfunction main(): i32 { return 0; }\n```\n")
	// A regular file standing where a directory is expected: a write under it
	// fails with ENOTDIR even for root, which a read-only directory does not.
	plain := write("plain", "x\n")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"compile-output-unwritable", []string{"-o", filepath.Join(plain, "x.out"), entry, h.stdlib},
			"fern: cannot write output " + filepath.Join(plain, "x.out") + ": Not a directory"},
		{"compile-output-dir-missing", []string{"-o", filepath.Join(dir, "nodir", "x.out"), entry, h.stdlib},
			"fern: cannot write output " + filepath.Join(dir, "nodir", "x.out") + ": not found"},
		{"fmt-output-unwritable", []string{"-fmt", "-o", filepath.Join(plain, "x.fern"), entry},
			"fern: cannot write output " + filepath.Join(plain, "x.fern") + ": Not a directory"},
		{"tangle-output-dir-uncreatable", []string{"-tangle", "-o", filepath.Join(plain, "out"), doc},
			"fern: cannot create output directory " + filepath.Join(plain, "out") + ": Not a directory"},
		{"unknown-flag", []string{"-bogus", entry},
			"fern: unknown flag: -bogus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(h.cli, c.args...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				t.Fatalf("exit 0, want a failure")
			}
			if got := stderr.String(); got != c.want+"\n" {
				t.Errorf("stderr:\n%q\nwant:\n%q", got, c.want+"\n")
			}
		})
	}

	// The usage block is several lines, one eprint each; none of them is blank.
	t.Run("usage-has-no-blank-lines", func(t *testing.T) {
		cmd := exec.Command(h.cli)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			t.Fatalf("exit 0, want a usage failure")
		}
		got := stderr.String()
		if !strings.HasPrefix(got, "usage: fern ") || !strings.HasSuffix(got, ")\n") {
			t.Fatalf("stderr is not the usage block:\n%s", got)
		}
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		if len(lines) < 2 {
			t.Fatalf("usage is %d line(s), want the multi-line block:\n%s", len(lines), got)
		}
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				t.Errorf("usage line %d is blank:\n%q", i+1, got)
			}
		}
	})
}
