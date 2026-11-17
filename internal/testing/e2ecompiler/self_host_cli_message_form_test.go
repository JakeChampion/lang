package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A message of the driver has one of four forms: `fern: ...` (a mode names
// its flag, `fern: -MODE: ...`), a position in a file, a usage line, or an
// indented line that continues the line before it.
var cliMessageForm = regexp.MustCompile(`^(fern: |usage: |  |[^ :]+:\d+:\d+: )`)

func TestSelfHostCLIMessageForm(t *testing.T) {
	fern := e2eharness.ShippedFern(t)
	cache := filepath.Join(t.TempDir(), "cache")
	empty := t.TempDir()
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "fern.toml"), []byte("[package]\nname = \"app\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keyed := t.TempDir()
	if err := os.WriteFile(filepath.Join(keyed, "fern.toml"), []byte("[package]\nname = \"app\"\nindex = \"index.toml\"\n\n[dependencies]\nhelper = \"1.0.0\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"resolve-no-manifest", []string{"-resolve", empty}, 1, "fern: -resolve: no fern.toml governs "},
		{"resolve-nothing-versioned", []string{"-resolve", plain}, 1, "fern: -resolve: "},
		{"resolve-no-index-file", []string{"-resolve", keyed}, 1, "fern: -resolve: index "},
		{"fetch-no-manifest", []string{"-fetch", empty}, 1, "fern: -fetch: no fern.toml governs "},
		{"fetch-nothing-to-fetch", []string{"-fetch", plain}, 0, "fern: -fetch: 0 downloaded, "},
		{"vendor-no-manifest", []string{"-vendor", empty}, 1, "fern: -vendor: no fern.toml governs "},
		{"vendor-nothing-to-copy", []string{"-vendor", plain}, 0, "fern: -vendor: 0 package(s) written to "},
		{"check-directory-no-manifest", []string{"-check", empty}, 1, "fern: -check: "},
		{"check-package-no-entry", []string{"-check", plain}, 1, "fern: -check: no entry module in "},
		{"explain-unknown-code", []string{"-explain", "E999"}, 1, "fern: -explain: unknown error code \"E999\""},
		{"doc-missing-file", []string{"-doc", filepath.Join(empty, "none.fern")}, 1, "fern: -doc: cannot read "},
		{"unknown-target", []string{"-target", "nope", filepath.Join(empty, "none.fern")}, 2, "fern: unknown -target: nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runFern(t, fern, cache, tc.args...)
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want %d and %q in:\n%s", code, tc.code, tc.want, out)
			}
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if !cliMessageForm.MatchString(line) {
					t.Errorf("line %q has none of the forms of a driver message", line)
				}
			}
		})
	}
}
