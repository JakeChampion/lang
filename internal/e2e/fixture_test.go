// Data-driven end-to-end fixtures. Each directory under
// `conformance/cases/<name>/` is a self-contained program checked against
// its expected stdout and exit code: here through the interpreter, and
// through the self-host compiler on x86-64, arm64 and wasm in
// fixture_selfhost_test.go. This is the declarative counterpart to the
// inline-source backend tests: to add a case you drop a `.fern` file plus
// a few sidecar files, no Go code required.
//
// Layout of a fixture directory:
//
//	main.fern        (required) entry point; `main(): i32`. May import
//	                 sibling `.fern` files (`import "./helper";`) and
//	                 stdlib modules.
//	*.fern           (optional) sibling modules pulled in by main.fern.
//	expected.stdout  (optional) expected stdout. Compared byte-for-byte
//	                 in the default "exact" mode; treated as a list of
//	                 required substrings (one per line) in "contains"
//	                 mode. Defaults to empty.
//	expected.exit    (optional) expected process exit code. Defaults to 0.
//	stdin            (optional) bytes fed to the program's stdin.
//	match            (optional) "exact" (default) or "contains".
//	backends         (optional) whitespace-separated subset of
//	                 {interp, x86_64, arm64, wasm}; lines starting with
//	                 '#' are comments. Defaults to all four.
//
// Compile-error fixtures: if a directory contains an `expected.error`
// file, the fixture is NOT run. Instead it is expected to FAIL the
// front-end (parse / module-load / type-check). This gives declarative
// coverage of the checker's rejection paths. Such fixtures ignore
// expected.stdout / expected.exit / stdin / match / backends.
//
// `expected.error` holds one line per expected diagnostic, and both
// directions are asserted: every line must be reported, and every code
// reported must be named by some line. See checkExpectedDiagnostics for
// why the second half is over distinct codes and what it found.
//
// Lowering-error fixtures: `expected.lowering-error` is the sibling for
// a rejection the front end does NOT make. Such a fixture must be
// ACCEPTED by parse / module-load / type-check and then rejected by
// ir.LowerWith, at both pointer widths. E068 (a `fbip` function that
// allocates without a donor to reuse) is the case in point: it comes
// out of internal/ir/fip_verify.go during lowering, so the front-end
// path above can never reach it. Asserting both halves is what stops a
// program the checker already rejects from masquerading as a lowering
// rule.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
)

type fixtureSpec struct {
	name         string
	mainPath     string
	stdin        string
	wantOut      string   // exact mode: full expected stdout
	contains     []string // contains mode: required substrings
	exact        bool     // true → byte-for-byte stdout match
	wantExit     int
	backends     map[string]bool
	compileError bool // true → expected to fail the front-end
	// loweringError → expected to PASS the front-end and fail during
	// lowering. Kept separate from compileError because which stage
	// rejected the program is exactly what such a case asserts.
	loweringError bool
	wantErrors    []string // one required substring per expected diagnostic

	// reclaimObservable marks a case whose output is DELIBERATELY different
	// with reclamation off — a case about the allocator rather than about the
	// language. The *FixturesFreeMatchesNoFree gates invert for these: instead
	// of requiring identical output they require DIFFERENT output, so the
	// marker is a claim to verify rather than a check to skip. A case that
	// carries it and does not diverge is a failure, which is what stops it
	// being reached for to silence an unrelated free-off divergence.
	reclaimObservable bool
}

var allBackends = []string{"interp", "x86_64", "arm64", "wasm"}

// rejectionCase reports whether the fixture asserts a diagnostic rather
// than a run. Such a case has no output, no exit code and no backends,
// so every runner that walks the corpus expecting to execute something
// has to skip it. One predicate rather than a growing disjunction: a
// third rejection kind would otherwise be skipped only by accident of
// its empty backends set, which is the kind of implicit coupling that
// lets a new case break a gate nobody thought to run.
func (f *fixtureSpec) rejectionCase() bool { return f.compileError || f.loweringError }

func loadFixture(t *testing.T, dir string) *fixtureSpec {
	t.Helper()
	f := &fixtureSpec{
		name:     filepath.Base(dir),
		mainPath: filepath.Join(dir, "main.fern"),
		exact:    true,
		backends: map[string]bool{},
	}

	// Compile-error fixture: expected to fail the front-end. Skip all
	// the run-oriented sidecar parsing.
	if raw, ok := readOptionalFile(dir, "expected.error"); ok {
		f.compileError = true
		f.wantErrors = expectedDiagnosticLines(raw)
		if len(f.wantErrors) == 0 {
			t.Fatalf("%s: expected.error file is empty", f.name)
		}
		return f
	}

	// Lowering-error fixture: expected to PASS the front-end and fail
	// during lowering. The two halves are both the point — a program the
	// checker already rejects would be pinning a front-end rule, and
	// belongs in expected.error.
	if raw, ok := readOptionalFile(dir, "expected.lowering-error"); ok {
		f.loweringError = true
		f.wantErrors = expectedDiagnosticLines(raw)
		if len(f.wantErrors) == 0 {
			t.Fatalf("%s: expected.lowering-error file is empty", f.name)
		}
		return f
	}

	if raw, ok := readOptionalFile(dir, "stdin"); ok {
		f.stdin = raw
	}

	mode := strings.TrimSpace(readOptionalFileDefault(dir, "match", "exact"))
	if mode == "contains" {
		f.exact = false
	} else if mode != "exact" {
		t.Fatalf("%s: unknown match mode %q (want \"exact\" or \"contains\")", f.name, mode)
	}

	stdout, _ := readOptionalFile(dir, "expected.stdout")
	if f.exact {
		f.wantOut = stdout
	} else {
		for _, ln := range strings.Split(stdout, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				f.contains = append(f.contains, s)
			}
		}
	}

	exitStr := strings.TrimSpace(readOptionalFileDefault(dir, "expected.exit", "0"))
	n, err := strconv.Atoi(exitStr)
	if err != nil {
		t.Fatalf("%s: bad expected.exit %q: %v", f.name, exitStr, err)
	}
	f.wantExit = n

	if _, ok := readOptionalFile(dir, "reclaim-observable"); ok {
		f.reclaimObservable = true
	}

	if raw, ok := readOptionalFile(dir, "backends"); ok {
		for _, ln := range strings.Split(raw, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			for _, tok := range strings.Fields(ln) {
				f.backends[tok] = true
			}
		}
		for b := range f.backends {
			if !contains(allBackends, b) {
				t.Fatalf("%s: unknown backend %q in backends file", f.name, b)
			}
		}
		// A `backends` file of only blanks and comments selects no backend, so
		// the fixture's sub-test runs zero legs and PASSes. Omitting the file
		// is how a case says "every backend"; an empty one says nothing.
		if len(f.backends) == 0 {
			t.Fatalf("%s: backends file names no backend, so the case would run on none of them "+
				"(delete the file to opt into all of %v)", f.name, allBackends)
		}
	} else {
		for _, b := range allBackends {
			f.backends[b] = true
		}
	}
	return f
}

// TestFernFixtures discovers every directory under conformance/cases and
// runs it across the backends it opts into.
func TestFernFixtures(t *testing.T) {
	root := conformanceCases
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no fixtures found under %s", root)
	}

	for _, name := range names {
		dir, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("abs %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "main.fern")); err != nil {
			t.Errorf("fixture %s has no main.fern", name)
			continue
		}
		f := loadFixture(t, dir)
		t.Run(name, func(t *testing.T) {
			if f.compileError {
				t.Run("check", func(t *testing.T) {
					errText, failed := runFixtureCompileError(f.mainPath)
					if !failed {
						t.Errorf("expected a compile error containing %q, but the program compiled cleanly", f.wantErrors[0])
						return
					}
					checkExpectedDiagnostics(t, "compile", f.wantErrors, errText)
				})
				return
			}
			if f.loweringError {
				// Both widths: a lowering rejection that fires on one
				// target and not the other is a portability defect in its
				// own right, so the case has to hold at each.
				for _, w := range []int{4, 8} {
					w := w
					t.Run(fmt.Sprintf("lower%d", w), func(t *testing.T) {
						errText, stage := runFixtureLoweringError(f.mainPath, w)
						switch stage {
						case loweringRejected:
							checkExpectedDiagnostics(t, "lowering", f.wantErrors, errText)
						case frontEndRejected:
							t.Errorf("the front end rejected this program, so it pins a front-end rule and belongs "+
								"in expected.error rather than expected.lowering-error\nfront-end error:\n%s", errText)
						case loweredCleanly:
							t.Errorf("expected lowering to fail with %q, but the program lowered cleanly", f.wantErrors[0])
						}
					})
				}
				return
			}
			if f.backends["interp"] {
				t.Run("interp", func(t *testing.T) {
					stdout, exit := runFixtureInterp(t, f.mainPath, f.stdin)
					f.check(t, stdout, exit)
				})
			}
		})
	}
}

func (f *fixtureSpec) check(t *testing.T, stdout string, exit int) {
	t.Helper()
	if f.exact {
		if stdout != f.wantOut {
			t.Errorf("stdout mismatch\n got: %q\nwant: %q", stdout, f.wantOut)
		}
	} else {
		for _, sub := range f.contains {
			if !strings.Contains(stdout, sub) {
				t.Errorf("stdout missing %q\nfull stdout:\n%s", sub, stdout)
			}
		}
	}
	if exit != f.wantExit {
		t.Errorf("exit = %d, want %d\nstdout:\n%s", exit, f.wantExit, stdout)
	}
}

// runFixtureArm64 compiles the fixture with the self-host compiler for
// arm64-linux and runs it under qemu.
func runFixtureArm64(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	_, qemu := arm64Tooling(t)
	bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetArm64Linux, mainPath, nil)
	return runBin(runArm64Bin(qemu, bin), stdin)
}

// runFixtureX86_64 compiles the fixture with the self-host compiler for
// x86-64-linux and runs it.
func runFixtureX86_64(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, mainPath, nil)
	if len(runner) == 0 {
		return runBin(exec.Command(bin), stdin)
	}
	return runBin(exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...), stdin)
}

// runFixtureCompileError runs the front-end (modload → constfold →
// check) and returns the first error's text plus whether any stage
// failed. Backend-agnostic: parse / module-load / type errors are the
// same regardless of target, so this runs once per fixture.
func runFixtureCompileError(mainPath string) (string, bool) {
	// Rendered the way the CLI renders it, which is what puts the `error[EXXX]`
	// code on each diagnostic. `err.Error()` alone gives "type error at L:C:
	// message" with no code at all, so the completeness check in
	// checkExpectedDiagnostics had nothing to read and a second diagnostic was
	// invisible to the harness as well as to the sidecar (#9601).
	render := func(err error) string {
		src, rerr := os.ReadFile(mainPath)
		if rerr != nil {
			return err.Error()
		}
		return diag.Format(mainPath, string(src), err)
	}
	prog, _, err := modload.Load(mainPath)
	if err != nil {
		return render(err), true
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return render(err), true
	}
	if _, err := checker.Check(prog); err != nil {
		return render(err), true
	}
	return "", false
}

// loweringStage is which stage a lowering-error fixture actually
// reached. Distinguishing "the checker rejected it" from "lowering
// rejected it" is the whole reason this path exists: only the second
// is what such a case claims.
type loweringStage int

const (
	loweringRejected loweringStage = iota
	frontEndRejected
	loweredCleanly
)

// runFixtureLoweringError requires the front end to accept the program
// and lowering to reject it. Unlike the front-end path this is
// per-pointer-width, because lowering is where the target starts to
// matter.
func runFixtureLoweringError(mainPath string, ptrW int) (string, loweringStage) {
	prog, _, err := modload.Load(mainPath)
	if err != nil {
		return err.Error(), frontEndRejected
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err.Error(), frontEndRejected
	}
	info, err := checker.Check(prog)
	if err != nil {
		return err.Error(), frontEndRejected
	}
	if _, err := ir.LowerWith(prog, info, ptrW); err != nil {
		return err.Error(), loweringRejected
	}
	return "", loweredCleanly
}

func linkAsm(t *testing.T, gcc, asm string, flags ...string) string {
	t.Helper()
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	args := append(append([]string{}, flags...), asmPath, "-o", binPath)
	if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	return binPath
}

func readOptionalFile(dir, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func readOptionalFileDefault(dir, name, def string) string {
	if s, ok := readOptionalFile(dir, name); ok {
		return s
	}
	return def
}

// diagCodeLine matches the first line of a diagnostic — `path:line:col:
// error[E123]: message` — and captures the code. The lines that follow are the
// source echo and the caret, which belong to the diagnostic above them.
var diagCodeLine = regexp.MustCompile(`^.*: error\[(E\d+)\]: `)

// expectedDiagnosticLines splits an `expected.error` / `expected.lowering-error`
// sidecar into one required substring per non-blank line.
//
// One line per expected DIAGNOSTIC is the contract (#9601). A message that
// wraps across lines still works: each part is individually a substring of the
// same diagnostic, so it matches the same one and contributes the same code.
func expectedDiagnosticLines(raw string) []string {
	var out []string
	for _, ln := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// splitDiagnostics cuts an error text into one block per coded diagnostic,
// returning each block's code alongside its full text.
func splitDiagnostics(errText string) (codes []string, blocks []string) {
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, ln := range strings.Split(errText, "\n") {
		if m := diagCodeLine.FindStringSubmatch(ln); m != nil {
			flush()
			codes = append(codes, m[1])
		}
		if len(codes) > 0 {
			cur = append(cur, ln)
		}
	}
	flush()
	return codes, blocks
}

// checkExpectedDiagnostics asserts both halves of a negative fixture's claim:
// every expected substring is reported, and every code reported is expected.
//
// The second half is what #9601 added. A negative fixture used to pass on a
// substring match alone, so a SECOND diagnostic — from the functions a case
// includes to show what IS allowed — was reported, matched past, and never
// seen. `diag_e053` documented an in-place array write as the allocation-free
// `fip` shape for months that way; the program did not compile, and the E056
// saying so sat in the output the whole time.
//
// Repeats of the pinned code are the case doing its job — `diag_e034` reports
// its E034 once per offending element — so the rule is over distinct CODES,
// not over the number of diagnostics. A second code is either a cascade worth
// recording in the sidecar or a defect worth fixing, and telling those apart is
// a judgement per case rather than something a matcher can do.
func checkExpectedDiagnostics(t *testing.T, stage string, want []string, errText string) {
	t.Helper()
	codes, blocks := splitDiagnostics(errText)
	matched := map[string]bool{}
	for _, w := range want {
		hit := false
		for i, b := range blocks {
			if strings.Contains(b, w) {
				matched[codes[i]] = true
				hit = true
			}
		}
		// A sidecar line may also name something outside any coded block —
		// a parse error with no code, say — so fall back to the whole text.
		if !hit && strings.Contains(errText, w) {
			hit = true
		}
		if !hit {
			t.Errorf("%s error does not contain %q\nfull error:\n%s", stage, w, errText)
		}
	}
	var unexpected []string
	seen := map[string]bool{}
	for _, c := range codes {
		if matched[c] || seen[c] {
			continue
		}
		seen[c] = true
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		t.Errorf("%s reported %s that no line of the sidecar expects — either the case is "+
			"documenting something that does not work (which is what #9601 found in diag_e053), "+
			"or the cascade is legitimate and belongs in the sidecar, one line per diagnostic"+
			"\nfull error:\n%s",
			stage, strings.Join(unexpected, ", "), errText)
	}
}
