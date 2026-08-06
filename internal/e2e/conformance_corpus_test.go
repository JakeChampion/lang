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
// table in conformance/README.md. `meta` is read by this gate rather than
// by the loader: it justifies a case that asserts less than the maximum.
var (
	runSidecars = []string{"expected.stdout", "expected.exit", "stdin", "match", "backends"}
	allSidecars = append([]string{"expected.error", "meta"}, runSidecars...)
)

// Waiver kinds a case may claim in its `meta` file. A waiver says why the
// case asserts less than byte-exact output on all four backends — the
// distinction being which of these four things is actually true, since
// they call for completely different follow-up.
var waiverKinds = []string{
	"implementation-gap", // a backend has not implemented it yet; needs an issue
	"harness-limit",      // the runner cannot observe the behaviour there
	"unspecified",        // the language deliberately grants the freedom
	"harness-self-test",  // the case exercises the runner, not the language
}

type caseMeta struct {
	waiver string
	issue  string
	reason string
}

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

	// A case "weakens" when it asserts less than byte-exact stdout across
	// all four backends. Both ways of doing that are claims about the
	// language, so both have to be justified in `meta` — see below.
	weakened := false

	if present["match"] {
		mode := strings.TrimSpace(read("match"))
		switch mode {
		case "exact":
		case "contains":
			weakened = true
		default:
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
		known := 0
		for _, b := range got {
			if !contains(allBackends, b) {
				report("unknown backend %q in backends file", b)
				continue
			}
			known++
		}
		switch {
		case known == len(allBackends):
			// Selecting everything is what an absent file already means, so
			// the list carries nothing; only its comment does, and a comment
			// belongs in main.fern with the rest of the case's rationale.
			report("backends file selects all %d backends, which is what omitting it means — delete it, moving any note into main.fern", len(allBackends))
		case known > 0:
			weakened = true
		}
	}

	problems = append(problems, checkMeta(dir, present["meta"], weakened, read)...)
	return problems, nil
}

// checkMeta enforces the rule that carries this file's weight: a case may
// assert less than the maximum, but not silently. Either direction is a
// problem — an unjustified weakening hides an implementation gap behind
// what looks like a passing case, and a stale waiver on a case that no
// longer weakens is how an obsolete exclusion survives for years (see
// f64_sqrt, which sat excluded from two backends over a libm link that
// had stopped being required).
func checkMeta(dir string, haveMeta, weakened bool, read func(string) string) []string {
	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if !haveMeta {
		if weakened {
			report("asserts less than byte-exact output on all four backends (contains mode and/or a backends subset) but has no meta file saying why — add one with a waiver: %s", strings.Join(waiverKinds, " | "))
		}
		return problems
	}

	m, errs := parseMeta(read("meta"))
	for _, e := range errs {
		report("meta: %s", e)
	}
	if m.waiver == "" {
		report("meta has no waiver: — a meta file exists only to justify a weakened assertion")
		return problems
	}
	if !contains(waiverKinds, m.waiver) {
		report("meta: unknown waiver %q — want one of %s", m.waiver, strings.Join(waiverKinds, " | "))
	}
	if !weakened {
		report("meta claims waiver %q but the case already asserts byte-exact output on all four backends — delete the meta file", m.waiver)
	}
	if strings.TrimSpace(m.reason) == "" {
		report("meta: waiver %q has no reason: — a waiver without a stated reason is indistinguishable from an oversight", m.waiver)
	}
	if m.waiver == "implementation-gap" && m.issue == "" {
		report("meta: waiver \"implementation-gap\" needs issue: — an unimplemented backend is tracked work, and an untracked gap is a permanent one")
	}
	if m.waiver != "implementation-gap" && m.issue != "" {
		report("meta: issue: is only meaningful for waiver \"implementation-gap\", not %q", m.waiver)
	}
	return problems
}

// parseMeta reads the `key: value` meta format. A line indented relative
// to its key continues that key's value, so a reason can wrap.
func parseMeta(src string) (caseMeta, []string) {
	var m caseMeta
	var errs []string
	fields := map[string]*string{"waiver": &m.waiver, "issue": &m.issue, "reason": &m.reason}

	var cur *string
	for _, raw := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(raw); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if raw != strings.TrimLeft(raw, " \t") && cur != nil {
			*cur += " " + strings.TrimSpace(raw)
			continue
		}
		key, val, ok := strings.Cut(raw, ":")
		if !ok {
			errs = append(errs, fmt.Sprintf("line %q is not `key: value`", strings.TrimSpace(raw)))
			cur = nil
			continue
		}
		key = strings.TrimSpace(key)
		dst, known := fields[key]
		if !known {
			errs = append(errs, fmt.Sprintf("unknown key %q — want waiver, issue or reason", key))
			cur = nil
			continue
		}
		if *dst != "" {
			errs = append(errs, fmt.Sprintf("duplicate key %q", key))
		}
		*dst = strings.TrimSpace(val)
		cur = dst
	}

	if m.issue != "" {
		if _, err := strconv.Atoi(strings.TrimPrefix(m.issue, "#")); err != nil {
			errs = append(errs, fmt.Sprintf("issue %q is not a number", m.issue))
		} else if strings.HasPrefix(m.issue, "#") {
			errs = append(errs, "issue: takes a bare number, without the leading #")
		}
	}
	return m, errs
}
