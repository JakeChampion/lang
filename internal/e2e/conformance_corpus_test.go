// Format gate for the conformance corpus (conformance/README.md).
//
// The fixture loader reads a fixed set of sidecar filenames and ignores
// everything else, so an unrecognised sidecar is indistinguishable from
// an absent one: `expected.exitcode` silently asserts exit 0, and a
// compile-error case that also carries `expected.stdout` asserts nothing
// about that stdout. Neither shows up as a failure anywhere — the case
// just quietly tests less than its author believed.
//
// This test costs milliseconds and compiles nothing; it exists so that
// the documented format is false-if-wrong rather than aspirational.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Sidecars the loader understands. Keep in step with loadFixture and the
// table in conformance/README.md.
var (
	runSidecars = []string{"expected.stdout", "expected.exit", "stdin", "match", "backends"}
	allSidecars = append([]string{"expected.error"}, runSidecars...)
)

func TestConformanceCorpusFormat(t *testing.T) {
	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceCases, err)
	}
	var cases []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("%s: stray file %q at the corpus root — every entry must be a case directory",
				conformanceCases, e.Name())
			continue
		}
		cases = append(cases, e.Name())
	}
	if len(cases) == 0 {
		t.Fatalf("no cases found under %s", conformanceCases)
	}
	sort.Strings(cases)

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			problems, err := checkCaseFormat(filepath.Join(conformanceCases, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range problems {
				t.Error(p)
			}
		})
	}
}

// checkCaseFormat validates one case directory against the format
// documented in conformance/README.md, returning a problem string per
// violation. It returns an error only when the directory itself cannot
// be read.
func checkCaseFormat(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	read := func(name string) string {
		b, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			report("read %s: %v", name, rerr)
			return ""
		}
		return string(b)
	}

	present := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			report("subdirectory %q: cases are flat", e.Name())
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".fern") {
			continue
		}
		if !contains(allSidecars, n) {
			report("unrecognised file %q — the loader ignores it, so whatever it asserts is not being checked (see conformance/README.md)", n)
			continue
		}
		present[n] = true
	}

	if st, serr := os.Stat(filepath.Join(dir, "main.fern")); serr != nil || st.IsDir() {
		report("no main.fern")
	}

	// Compile-error cases take a different path through the loader, which
	// ignores every run-oriented sidecar.
	if present["expected.error"] {
		if strings.TrimSpace(read("expected.error")) == "" {
			report("expected.error is empty — it must hold the required substring of the diagnostic")
		}
		for _, s := range runSidecars {
			if present[s] {
				report("compile-error case also carries %q, which is ignored on this path", s)
			}
		}
		return problems, nil
	}

	if present["expected.exit"] {
		raw := strings.TrimSpace(read("expected.exit"))
		n, cerr := strconv.Atoi(raw)
		if cerr != nil {
			report("expected.exit %q is not an integer", raw)
		} else if n < 0 || n > 255 {
			report("expected.exit %d is outside 0..255", n)
		}
	}

	if present["match"] {
		if mode := strings.TrimSpace(read("match")); mode != "exact" && mode != "contains" {
			report("match %q: want \"exact\" or \"contains\"", mode)
		}
	}

	if present["backends"] {
		var got []string
		for _, ln := range strings.Split(read("backends"), "\n") {
			if ln = strings.TrimSpace(ln); ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			got = append(got, strings.Fields(ln)...)
		}
		if len(got) == 0 {
			report("backends file selects no backends — delete it to mean \"all\"")
		}
		for _, b := range got {
			if !contains(allBackends, b) {
				report("unknown backend %q in backends file", b)
			}
		}
	}
	return problems, nil
}
