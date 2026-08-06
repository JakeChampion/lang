// The gate that keeps spec/diagnostics.md accurate.
//
// The index claims, per diagnostic code, that some conformance case
// produces it. That claim is the whole value of the document — it is
// what says a rejection rule will still be checked after the native
// freeze, when Go-side checker tests can no longer measure the
// implementation that matters. A claim nobody verifies is worth less
// than no claim, because it reads like coverage.
//
// So this test derives the truth (run every compile-error case, collect
// the codes it emits) and requires the document to match it exactly, in
// both directions: an unbacked claim fails, and so does a `—` on a code
// that some case does in fact emit.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	diagIndexPath  = "../../spec/diagnostics.md"
	explanationDir = "../diag/explanations"
)

var (
	indexRowRe = regexp.MustCompile(`^\|\s*` + "`" + `([A-Z]\d+)` + "`" + `\s*\|(.*)\|\s*(.*?)\s*\|$`)
	codeRe     = regexp.MustCompile(`error\[([A-Z]\d+)\]`)
	countRe    = regexp.MustCompile(`\*\*(\d+) of (\d+)\*\* codes are pinned`)
)

type indexRow struct {
	code, rule, pin string
}

func TestDiagnosticsIndexIsAccurate(t *testing.T) {
	rows, nPin, nAll := parseDiagnosticsIndex(t)
	emitted := codesByCase(t)

	// Which codes some conformance case actually produces.
	producedBy := map[string][]string{}
	for name, codes := range emitted {
		for _, c := range codes {
			producedBy[c] = append(producedBy[c], name)
		}
	}

	// 1. The index and the explanation catalogue must describe the same
	//    set of codes. An explanation with no row is undocumented here; a
	//    row with no explanation means `fern explain` cannot answer for a
	//    code the index claims exists.
	explained := explanationCodes(t)
	inIndex := map[string]bool{}
	for _, r := range rows {
		if inIndex[r.code] {
			t.Errorf("%s: %s appears in more than one row", diagIndexPath, r.code)
		}
		inIndex[r.code] = true
	}
	for _, c := range explained {
		if !inIndex[c] {
			t.Errorf("%s has an explanation but no row in %s", c, diagIndexPath)
		}
	}
	for _, r := range rows {
		if !contains(explained, r.code) {
			t.Errorf("%s: row for %s, which has no explanation file — `fern explain %s` cannot answer",
				diagIndexPath, r.code, r.code)
		}
	}

	// 2. Every claimed pin must hold, and every `—` must be honest.
	for _, r := range rows {
		switch {
		case r.pin == "—":
			if cases := producedBy[r.code]; len(cases) > 0 {
				t.Errorf("%s: %s is marked unpinned, but %s emits it — record the case",
					diagIndexPath, r.code, cases[0])
			}
		default:
			name := strings.Trim(r.pin, "`")
			if _, ok := emitted[name]; !ok {
				t.Errorf("%s: %s names conformance case %q, which is not a compile-error case",
					diagIndexPath, r.code, name)
				continue
			}
			if !contains(emitted[name], r.code) {
				t.Errorf("%s: %s claims to be pinned by %s, but that case emits %s",
					diagIndexPath, r.code, name, strings.Join(emitted[name], ", "))
			}
		}
	}

	// 3. The stated coverage count must be the real one, so that moving it
	//    in either direction is a visible edit rather than a silent drift.
	var gotPin int
	for _, r := range rows {
		if r.pin != "—" {
			gotPin++
		}
	}
	if gotPin != nPin || len(rows) != nAll {
		t.Errorf("%s says %d of %d codes are pinned; the table has %d of %d",
			diagIndexPath, nPin, nAll, gotPin, len(rows))
	}
}

// codesByCase runs every compile-error case in the corpus through the
// real CLI and records the codes it reports. The CLI is used rather than
// the in-process front end because only the formatted output carries the
// codes — the aggregate error value does not.
func codesByCase(t *testing.T) map[string][]string {
	t.Helper()
	bin := buildLangBinForInterp(t)

	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceCases, err)
	}
	out := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(conformanceCases, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "expected.error")); err != nil {
			continue
		}
		cmd := exec.Command(bin, "-check", filepath.Join(dir, "main.fern"))
		blob, _ := cmd.CombinedOutput()
		var codes []string
		for _, m := range codeRe.FindAllStringSubmatch(string(blob), -1) {
			if !contains(codes, m[1]) {
				codes = append(codes, m[1])
			}
		}
		sort.Strings(codes)
		out[e.Name()] = codes
	}
	if len(out) == 0 {
		t.Fatalf("no compile-error cases found — the gate is not running")
	}
	return out
}

func explanationCodes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(explanationDir)
	if err != nil {
		t.Fatalf("read %s: %v", explanationDir, err)
	}
	var out []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".md") {
			out = append(out, strings.TrimSuffix(name, ".md"))
		}
	}
	sort.Strings(out)
	return out
}

func parseDiagnosticsIndex(t *testing.T) (rows []indexRow, nPin, nAll int) {
	t.Helper()
	raw, err := os.ReadFile(diagIndexPath)
	if err != nil {
		t.Fatalf("read %s: %v", diagIndexPath, err)
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		if m := indexRowRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			rows = append(rows, indexRow{
				code: m[1],
				rule: strings.TrimSpace(m[2]),
				pin:  strings.TrimSpace(m[3]),
			})
		}
	}
	if len(rows) == 0 {
		t.Fatalf("%s: no code rows found — the table format changed and this gate stopped checking anything", diagIndexPath)
	}
	m := countRe.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s: no `**N of M** codes are pinned` sentence — the count is part of the contract", diagIndexPath)
	}
	if _, err := fmt.Sscanf(string(m[1]), "%d", &nPin); err != nil {
		t.Fatalf("bad pinned count: %v", err)
	}
	if _, err := fmt.Sscanf(string(m[2]), "%d", &nAll); err != nil {
		t.Fatalf("bad total count: %v", err)
	}
	return rows, nPin, nAll
}
