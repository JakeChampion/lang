package e2eselfhost

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

// The conformance corpus' `expected.error` cases pin programs the compiler must
// REFUSE. Nothing ran them against the self-host checker.
//
// `internal/e2e`'s self-host fixture legs skip them deliberately, and the stated
// reason is that they are "front-end only, backend-agnostic, already covered
// once by TestFernFixtures". That holds for the NATIVE front end. The self-host
// carries its own checker — a second implementation, which is why
// TestSelfHostCheckerCodes* exists at all — so "covered once" covered native
// twice and the self-host zero times.
//
// What the blind spot was hiding: on 11 of the 69 the self-host checker reported
// NO diagnostic at all (7 remain; diag_e010, err_map_key_no_hash,
// err_duplicate_field and underscore_not_readable are closed).
// `err_map_key_no_hash` showed what a missing rejection costs once it reaches a
// backend — the wasm module it produced did not validate, calling a `$Has.hash`
// nothing defines. It draws native's E045 now.
//
// Measure this with the CHECKER, not the CLI's emit path. `-emit asm` exits 0
// on `diag_e034` and writes a module, yet `-check` on the same file reports its
// E034: the emit path does not gate on the checker. Counting emit exit codes
// therefore reports ~28 "accepted" and conflates two separate problems. The 11
// below are the ones where the checker itself has nothing to say.
//
// This gate pins the verdict per case, exactly in both directions:
//
//   - a case NOT in the gap file must be rejected — the 58 that work today
//     (70 cases, less the 7 gaps and the 5 `parse:` rows) can never silently
//     regress to accepted;
//   - a case IN the gap file must still be accepted — closing a gap fails here
//     until its line is deleted, so the file cannot rot into a list of
//     already-fixed things.
//
// The gap file is therefore the live list of missing self-host rejections, in
// the shape `selfhost-leak-matrix.txt` and the wasm known-divergences file use.
// It is a record of a gap, not permission for one: every line is a program the
// self-host compiles and native refuses.
//
// It reads the CHECKER, not the whole pipeline: `checkSourceModload` runs the
// self-host checker driver over the case and its imports. A case that only a
// lowering bail catches still counts as accepted here, because a diagnostic is
// what the corpus asks for and a bail is not one.
//
// A case whose source the Go loader cannot PARSE never reaches any checker, so
// it is outside what this gate reads. Those are pinned too, with a `parse:`
// prefix, rather than skipped: a skip would hide the corpus shrinking, and a
// parse case that starts parsing is a real change this should report.
//
// Unlike the row-based checker corpora next door — every row a stdlib-free
// single-file program, the weakness `docs/TEST-GATES.md` names — these are real
// corpus programs with imports.
const rejectionGapFile = "testdata/selfhost-rejection-gaps.txt"

// readRejectionGaps splits the pin file into the checker gaps (bare names) and
// the parse-only cases (`parse:` prefix).
func readRejectionGaps(t *testing.T) (gaps, parseOnly map[string]bool) {
	t.Helper()
	b, err := os.ReadFile(rejectionGapFile)
	if err != nil {
		t.Fatalf("read %s: %v", rejectionGapFile, err)
	}
	gaps, parseOnly = map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, ok := strings.CutPrefix(line, "parse:"); ok {
			parseOnly[name] = true
			continue
		}
		gaps[line] = true
	}
	return gaps, parseOnly
}

func TestSelfHostRejectsConformanceErrorCasesX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	errCases, err := filepath.Glob("../../conformance/cases/*/expected.error")
	if err != nil {
		t.Fatalf("glob conformance cases: %v", err)
	}
	// A glob that stops matching would make every assertion below vacuous.
	if len(errCases) < 60 {
		t.Fatalf("found only %d expected.error cases — the corpus moved and this gate proves nothing", len(errCases))
	}
	sort.Strings(errCases)

	gaps, parseOnly := readRejectionGaps(t)
	seen := map[string]bool{}

	for _, ec := range errCases {
		dir := filepath.Dir(ec)
		name := filepath.Base(dir)
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(dir, "main.fern"))
			if err != nil {
				t.Fatalf("read main.fern: %v", err)
			}
			if _, _, lerr := modload.LoadSource(string(src)); lerr != nil {
				// A parse error: the program has no module set to check.
				if !parseOnly[name] {
					t.Errorf("the Go loader cannot parse %s (%v) — add `parse:%s` to %s, since a parse case is outside what this gate reads", name, lerr, name, rejectionGapFile)
				}
				return
			}
			if parseOnly[name] {
				t.Errorf("%s now parses — drop its `parse:` line from %s so the checker verdict is pinned instead", name, rejectionGapFile)
				return
			}
			rejected := len(driverDiags(checkSourceModload(t, runner, driverBin, string(src)))) > 0
			switch {
			case rejected && gaps[name]:
				t.Errorf("the self-host now rejects %s — delete its line from %s (a gap file that lists fixed cases stops being a gap list)", name, rejectionGapFile)
			case !rejected && !gaps[name]:
				want, _ := os.ReadFile(ec)
				t.Errorf("the self-host reported NO diagnostic for %s and will emit code for it; native refuses it with:\n  %s",
					name, strings.TrimSpace(string(want)))
			}
		})
	}

	// A gap line naming a case that no longer exists is a stale entry: the case
	// was renamed or deleted and nothing noticed.
	for _, set := range []map[string]bool{gaps, parseOnly} {
		for name := range set {
			if !seen[name] {
				t.Errorf("%s lists %q, which is not an expected.error conformance case", rejectionGapFile, name)
			}
		}
	}
}
