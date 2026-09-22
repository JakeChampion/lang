package coreutils

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/strerror"
)

// GNU prints strerror(3), so a diagnostic naming an errno is worded by
// the host libc — and sixteen errnos read differently on Darwin. Two
// copies of that wording exist by necessity: internal/strerror, which
// the runtime reports IoError.Other from, and coreutils/lib/gnu.fern,
// which the utilities call when they name an errno GNU names without
// one coming back from a syscall. These two tests keep the second copy
// honest: the first pins every helper to the Go table, the second fails
// when a utility writes the wording out by hand instead of calling one.

// gnuFernHelper matches a `<errno>_text()` helper in coreutils/lib/gnu.fern.
var gnuFernHelper = regexp.MustCompile(
	`(?m)^pub function (\w+)_text\(\): string \{\n  if \(target_os\(\) == "darwin"\) \{ return "([^"]*)"; \}\n  return "([^"]*)";\n\}`)

func gnuFernSource(t *testing.T) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "coreutils", "lib", "gnu.fern")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return string(b)
}

func TestGnuFernErrnoTextMatchesStrerrorTable(t *testing.T) {
	ms := gnuFernHelper.FindAllStringSubmatch(gnuFernSource(t), -1)
	if len(ms) == 0 {
		t.Fatal("no <errno>_text() helpers found in coreutils/lib/gnu.fern — the extraction pattern has gone stale, which would make this test vacuous")
	}
	byName := map[string]strerror.Entry{}
	for _, e := range strerror.Table {
		byName[e.Name] = e
	}
	for _, m := range ms {
		helper, gotDarwin, gotHost := m[1], m[2], m[3]
		errno := strings.ToUpper(helper)
		e, ok := byName[errno]
		if !ok {
			t.Errorf("%s_text() names %s, which internal/strerror's Table does not carry", helper, errno)
			continue
		}
		if want := e.Text; gotHost != want {
			t.Errorf("%s_text() says %q off Darwin, Table says %q", helper, gotHost, want)
		}
		if want := e.TextFor(strerror.Darwin); gotDarwin != want {
			t.Errorf("%s_text() says %q on Darwin, Table says %q", helper, gotDarwin, want)
		}
		if e.Text == e.TextFor(strerror.Darwin) {
			t.Errorf("%s_text() branches on the target for an errno both platforms word the same (%q) — write the string", helper, e.Text)
		}
	}
}

// A utility that writes a glibc-only wording out as a literal is
// correct on Linux and wrong on Darwin, and nothing at the call site
// says so. gnu.fern's helpers are the only place those strings may
// appear.
//
// "Glibc-only" is narrower than "differs from this entry's Darwin
// wording": EOPNOTSUPP is worded "Operation not supported" by glibc and
// "Operation not supported on socket" by Darwin, but Darwin ALSO words
// its separate ENOTSUP "Operation not supported" — so that string is
// correct on both platforms and a utility may write it. Only a wording
// no platform's Darwin libc produces is one nobody may spell out.
func TestNoUtilitySpellsAGlibcOnlyErrnoText(t *testing.T) {
	root := repoRoot(t)
	darwinWordings := map[string]bool{}
	for _, e := range strerror.Table {
		if e.Darwin != 0 {
			darwinWordings[e.TextFor(strerror.Darwin)] = true
		}
	}
	glibcOnly := map[string]string{}
	for _, e := range strerror.Table {
		if e.TextFor(strerror.Darwin) != e.Text && !darwinWordings[e.Text] {
			glibcOnly[e.Text] = strings.ToLower(e.Name) + "_text()"
		}
	}
	if len(glibcOnly) == 0 {
		t.Fatal("no glibc-only wordings derived from strerror.Table — this test would pass vacuously")
	}
	helpers := filepath.Join(root, "coreutils", "lib", "gnu.fern")
	err := filepath.Walk(filepath.Join(root, "coreutils"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".fern") || p == helpers {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			// A Darwin wording can carry a glibc one as a prefix
			// ("Address family not supported by protocol family"), so the
			// Darwin ones leave the line before the glibc ones are sought.
			for d := range darwinWordings {
				line = strings.ReplaceAll(line, d, "")
			}
			for text, helper := range glibcOnly {
				if strings.Contains(line, text) {
					t.Errorf("%s:%d writes %q, which is glibc's wording and not Darwin's — call gnu.%s", rel, i+1, text, helper)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking coreutils/: %v", err)
	}
}
