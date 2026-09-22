package coreutils

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// gnuFernHelper matches a `<errno>_text()` helper in coreutils/lib/gnu.fern,
// and gnuFernHelperDecl matches only its signature. The two are compared so a
// helper the formatter reshapes fails the test rather than going unmatched:
// a pattern that silently selects nothing is the way a gate like this dies.
var (
	gnuFernHelper = regexp.MustCompile(
		`(?m)^pub function (\w+)_text\(\): string \{\s*\n\s*if \(target_os\(\) == "darwin"\) \{\s*\n\s*return "([^"]*)";\s*\n\s*\}\s*\n\s*return "([^"]*)";\s*\n\}`)
	gnuFernHelperDecl = regexp.MustCompile(`(?m)^pub function \w+_text\(\): string \{`)
)

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
	src := gnuFernSource(t)
	ms := gnuFernHelper.FindAllStringSubmatch(src, -1)
	if len(ms) == 0 {
		t.Fatal("no <errno>_text() helpers found in coreutils/lib/gnu.fern — the extraction pattern has gone stale, which would make this test vacuous")
	}
	if n := len(gnuFernHelperDecl.FindAllString(src, -1)); n != len(ms) {
		t.Fatalf("coreutils/lib/gnu.fern declares %d <errno>_text() helpers but %d match the body pattern — reshaped helpers are not being checked", n, len(ms))
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

// gnu.fern also carries two errno NUMBERS, because exec_errno_text maps
// raw errnos rather than an IoError, and the two platforms disagree on
// where ENAMETOOLONG and ELOOP sit. Both come from the same Go table.
func TestGnuFernErrnoNumbersMatchStrerrorTable(t *testing.T) {
	src := gnuFernSource(t)
	for _, tc := range []struct{ helper, errno string }{
		{"enametoolong_errno", "ENAMETOOLONG"},
		{"eloop_errno", "ELOOP"},
	} {
		m := regexp.MustCompile(`(?s)pub function ` + tc.helper + `\(\): i32 \{\s*if \(target_os\(\) == "darwin"\) \{\s*return (\d+);\s*\}\s*return (\d+);`).FindStringSubmatch(src)
		if m == nil {
			t.Errorf("no %s() found in coreutils/lib/gnu.fern — the extraction pattern has gone stale", tc.helper)
			continue
		}
		if want := strconv.Itoa(strerror.Number(strerror.Darwin, tc.errno)); m[1] != want {
			t.Errorf("%s() says %s on darwin, Table says %s for %s", tc.helper, m[1], want, tc.errno)
		}
		if want := strconv.Itoa(strerror.Number(strerror.Linux, tc.errno)); m[2] != want {
			t.Errorf("%s() says %s off darwin, Table says %s for %s", tc.helper, m[2], want, tc.errno)
		}
	}
	m := regexp.MustCompile(`(?s)pub function unknown_errno_text\(n: i32\): string \{\s*if \(target_os\(\) == "darwin"\) \{\s*return "([^"]*)" \+ n\.to_string\(\);\s*\}\s*return "([^"]*)" \+ n\.to_string\(\);`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("no unknown_errno_text() found in coreutils/lib/gnu.fern — the extraction pattern has gone stale")
	}
	if m[1] != strerror.DarwinUnknownPrefix {
		t.Errorf("unknown_errno_text() says %q on darwin, Go says %q", m[1], strerror.DarwinUnknownPrefix)
	}
	if m[2] != strerror.UnknownPrefix {
		t.Errorf("unknown_errno_text() says %q off darwin, Go says %q", m[2], strerror.UnknownPrefix)
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
