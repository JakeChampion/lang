package e2eselfhost

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

	"github.com/jakechampion/lang/internal/ir"
)

// TestSelfHostIRVerifyStructure exercises the self-host IR structure verifier
// (examples/self_host/irverify.fern, #6639 slice 1) — the port of native's
// internal/ir/verify.go.
//
// The driver asserts each check class in BOTH directions: a malformed op
// stream the pass must report, and a well-formed one it must stay silent on.
// The silent half carries the weight. A verifier that reports a problem on
// valid IR is worse than no verifier, because it fires on every real module
// and there is nothing to fix. The compile path runs the verifiers over every
// body it emits (irverifygate.fern), which is the same property measured
// against real lowered output.
//
// Exit 0 means every assertion held. A non-zero code identifies the case, so
// a regression names itself without a stdout diff.
func TestSelfHostIRVerifyStructure(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("irverify_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing assertion's id in irverify_run.fern", code)
	}
	if want := "irverify: all structural checks agree"; !strings.Contains(string(out), want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostIRVerifyStack exercises the operand-stack verifier
// (examples/self_host/irverifystack.fern, #6639 slice 2) — the port of
// native's internal/ir/verifystack.go.
//
// Same driver, same both-directions discipline as the structure pass above,
// and the same reason for it: this one models an arity per op kind, so a
// wrong entry in that table is a report on valid IR. Several of the cases are
// arities the corpus sweep caught wrong on the way in (arr_set / struct_set /
// tuple_set consume their value where the raw stores re-push it; map_new
// takes its size hint from the stack), pinned here so the sweep is not the
// only thing standing between them and a regression.
//
// Exit 0 means every assertion held; a non-zero code is the failing case's id
// in irverify_run.fern's stack_checks.
func TestSelfHostIRVerifyStack(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("irverify_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing assertion's id in irverify_run.fern", code)
	}
	if want := "irverifystack: all stack checks agree"; !strings.Contains(string(out), want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostIRVerifyFip exercises the `fip` / `fbip` allocation-budget
// verifier (examples/self_host/irfipverify.fern, #6639 slice 3) — the port of
// native's internal/ir/fip_verify.go.
//
// Same driver and the same both-directions discipline as the two passes above.
// The direction that carries the weight here is the silent one for a
// reuse-PAIRED site: charging it would report every `fbip` function whose
// claim the reuse layer actually earns, which is the whole population the
// annotation exists for.
//
// Exit 0 means every assertion held; a non-zero code is the failing case's id
// in irverify_run.fern's fip_checks.
func TestSelfHostIRVerifyFip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("irverify_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing assertion's id in irverify_run.fern", code)
	}
	if want := "irfipverify: all allocation-budget checks agree"; !strings.Contains(string(out), want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostIRVerifyProvided exercises the callee-resolution verifier
// (examples/self_host/irverifyprovided.fern, #6639 slice 4) — the port of
// native's internal/ir/verifyprovided.go.
//
// Same driver and the same both-directions discipline as the passes above.
// The silent half is the essential one here too, and for a sharper reason
// than usual: this pass rests on an INVENTORY of runtime-helper names, and an
// inventory is a thing that goes stale. A missing entry is a report on valid
// IR; a wrong prefix rule excuses a genuinely missing body. The driver's cases
// pin both edges, including the near-misses (`__c_call5`, `__struct_drop_`
// with no type, a truncated helper name) that a loose prefix rule would admit.
//
// Exit 0 means every assertion held; a non-zero code is the failing case's id
// in irverify_run.fern's provided_checks.
func TestSelfHostIRVerifyProvided(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("irverify_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing assertion's id in irverify_run.fern", code)
	}
	if want := "irverifyprovided: all callee-resolution checks agree"; !strings.Contains(string(out), want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostIRVerifyProvidedAudit is what keeps the verifier's inventory a
// SECOND record rather than a stale one.
//
// irverifyprovided.fern deliberately does not call asm_ir.is_fern_helper: a
// verifier that reads its answer out of the compiler agrees with the compiler
// by construction, which is native's stated reason for keeping
// verifyprovided.go's table separate. The cost of a copy is drift, and this
// audit closes the half of it that can be closed — every inventory entry must
// satisfy the emitter's own predicate, so an entry naming a helper no backend
// emits fails here rather than silently excusing a real missing body.
//
// The other direction is not enumerable out of a predicate. A helper the
// emitter gained and the inventory did not shows up as an unresolved callee in
// the corpus sweeps below — which is the failure this pass exists to produce,
// so it is covered, just not here.
func TestSelfHostIRVerifyProvidedAudit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "provided_audit")

	cmd := exec.Command(bin, "-provided-audit")
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("driver did not exit normally")
	}
	if cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("provided-audit failed:\n%s", out)
	}
	if !strings.Contains(string(out), "inventory entries are emitted by the backend") {
		t.Errorf("provided-audit stdout = %q, want the agreement line", out)
	}
}

// TestSelfHostIRVerifyProvidedCompilerClean runs the resolution pass over the
// self-host compiler's own sources — the largest program the lowerer sees, and
// the one whose malformed output has actually cost the most (docs/TEST-GATES.md
// on #6018).
//
// This is also the sweep that exercises the pass at scale: the compiler
// declares thousands of functions, which is why the declared set is a bucketed
// index rather than a linear scan.
func TestSelfHostIRVerifyProvidedCompilerClean(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-compiler sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProjectTyped(t)
	bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "provided_compiler")

	cmd := exec.Command(bin, filepath.Join(dir, "asm_modload_run.fern"), "-verifyprovided")
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("driver did not exit normally")
	}
	if cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("resolution pass reported problems on the compiler's own sources:\n%s", out)
	}
	checked, calls := parseProvidedTally(string(out))
	if checked < 500 {
		t.Errorf("pass checked %d functions of the compiler, expected the full set — a shrunken sweep proves nothing (out: %s)", checked, out)
	}
	if calls < 5000 {
		t.Errorf("pass resolved %d direct calls across the compiler, expected far more — a sweep that resolved nothing proves nothing (out: %s)", calls, out)
	}
}

// parseProvidedTally reads the `checked N functions, M direct calls` line out
// of a `-verifyprovided` run. A clean verdict over a program the pass barely
// looked at is not a result, so the tally is asserted alongside it.
func parseProvidedTally(out string) (int, int) {
	i := strings.Index(out, "checked ")
	if i < 0 {
		return 0, 0
	}
	var checked, calls int
	if _, err := fmt.Sscanf(out[i:], "checked %d functions, %d direct calls", &checked, &calls); err != nil {
		return 0, 0
	}
	return checked, calls
}

// TestSelfHostIRVerifyProvidedCorpusClean sweeps the conformance corpus.
//
// It runs the MODLOAD driver, because a single module's lowering leaves every
// imported `Type.method` unresolved by construction: a
// census over this corpus found 255 distinct unresolved callee names, ~200 of
// them stdlib methods reached through an import. At that ratio the noise is
// not a floor to tolerate, it is most of the signal — so the declared set has
// to come from the merged bundle, which means staging each fixture beside a
// resolvable stdlib.
func TestSelfHostIRVerifyProvidedCorpusClean(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "provided_corpus")
	testProvidedCorpus(t, bin)
}

func testProvidedCorpus(t *testing.T, bin string) {
	t.Helper()
	stdRoot := langSrcAbs(t, filepath.Join("internal", "stdlib"))
	all, err := filepath.Glob(filepath.Join(langSrcAbs(t, "conformance"), "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	// A fixture the checker rejects has no lowering to verify.
	var mains []string
	for _, main := range all {
		if _, err := os.Stat(filepath.Join(filepath.Dir(main), "expected.error")); err != nil {
			mains = append(mains, main)
		}
	}
	if len(mains) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus: a silently shrunken sweep proves nothing", len(mains))
	}

	// Each child owns one result slot. The synchronous group waits for all
	// parallel children before aggregation and keeps the top-level elapsed
	// time useful to scripts/ci-test-weights.
	type result struct {
		ran                     bool
		calls, modelled, bodies int
	}
	results := make([]result, len(mains))
	t.Run("cases", func(t *testing.T) {
		for i, main := range mains {
			name := filepath.Base(filepath.Dir(main))
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				results[i].ran = true
				stage := stageProvidedFixture(t, stdRoot, filepath.Dir(main))
				cmd := exec.Command(bin, filepath.Join(stage, "main.fern"), "-verifyprovided")
				var out []byte
				// The verifier lowers a whole imported program. Share the
				// existing process-wide memory budget with driver builds.
				err := withBuildMemoryMB(5*1024, func() error {
					var err error
					out, err = cmd.Output()
					return err
				})
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("resolution driver did not exit normally: %v\n%s", err, out)
				}
				results[i].calls, err = validateProvidedCorpusVerdict(cmd.ProcessState.ExitCode(), string(out))
				if err != nil {
					t.Errorf("invalid resolution verdict: %v\n%s", err, out)
				}
				results[i].modelled, results[i].bodies = parseStackCoverage(string(out))
			})
		}
	})
	swept, calls, modelled, bodies := 0, 0, 0, 0
	for _, result := range results {
		if result.ran {
			swept++
			calls += result.calls
			modelled += result.modelled
			bodies += result.bodies
		}
	}
	// Every body the typed lowering produces for the corpus is one the stack
	// pass models, so the floor is equality: a skip is a new op its arity
	// table does not carry, which the compile-path gate passes over silently.
	if modelled != bodies {
		t.Errorf("stack pass modelled %d of %d lowered bodies; a skip is an op the pass's table does not carry", modelled, bodies)
	}
	// A focused -run selection still checks every selected verdict. The full
	// corpus retains its aggregate coverage floor; applying that floor to a
	// deliberately filtered subset would prevent focused regression runs.
	if swept == len(mains) && calls < 2000 {
		t.Errorf("pass resolved %d direct calls across the corpus, expected far more: a sweep that resolved nothing proves nothing", calls)
	}
	t.Logf("swept %d/%d fixtures, resolved %d direct calls, stack-modelled %d/%d bodies", swept, len(mains), calls, modelled, bodies)
}

// parseStackCoverage reads the `irverifystack: modelled M/N` line out of a
// `-verifyprovided` run.
func parseStackCoverage(out string) (int, int) {
	i := strings.Index(out, "irverifystack: modelled ")
	if i < 0 {
		return 0, 0
	}
	var m, f int
	if _, err := fmt.Sscanf(out[i:], "irverifystack: modelled %d/%d", &m, &f); err != nil {
		return 0, 0
	}
	return m, f
}

var providedCorpusVerdict = regexp.MustCompile(`^irverifyprovided: (clean|[1-9][0-9]* problem\(s\)) \(checked ([0-9]+) functions, ([0-9]+) direct calls\)$`)

func validateProvidedCorpusVerdict(exitCode int, out string) (int, error) {
	// An arena trap or another driver error is never a verdict. Only the
	// verifier's own exit 1 plus diagnostic counts.
	if exitCode != 0 && exitCode != 1 {
		return 0, fmt.Errorf("driver exited %d, want verifier status 0 or 1", exitCode)
	}
	header, _, _ := strings.Cut(out, "\n")
	match := providedCorpusVerdict.FindStringSubmatch(header)
	if match == nil {
		return 0, fmt.Errorf("missing or malformed verifier tally")
	}
	checked, checkedErr := strconv.Atoi(match[2])
	calls, callsErr := strconv.Atoi(match[3])
	if checkedErr != nil || callsErr != nil || checked < 0 || calls < 0 {
		return 0, fmt.Errorf("invalid verifier counts")
	}
	dirty := match[1] != "clean"
	if dirty != (exitCode == 1) {
		return 0, fmt.Errorf("verifier header disagrees with exit %d", exitCode)
	}
	if dirty {
		return 0, fmt.Errorf("the verifier reported unresolved callees")
	}
	return calls, nil
}

func stageProvidedFixture(t *testing.T, stdRoot, caseDir string) string {
	t.Helper()
	stage := t.TempDir()
	// The fixture's own modules have to travel with it — several cases are
	// multi-file (cross_module_bounded_method, multi_file, pub_use_reexport),
	// and sweeping only main.fern would report their siblings' functions as
	// undeclared, which is the same false positive the single-module driver
	// produces at stdlib scale.
	for _, lib := range []string{"std", "core"} {
		if err := os.Symlink(filepath.Join(stdRoot, lib), filepath.Join(stage, lib)); err != nil {
			t.Fatalf("linking stdlib %s: %v", lib, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(caseDir, "*.fern"))
	if err != nil {
		t.Fatalf("globbing fixture modules: %v", err)
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if err := os.WriteFile(filepath.Join(stage, filepath.Base(file)), src, 0o644); err != nil {
			t.Fatalf("staging %s: %v", file, err)
		}
	}
	return stage
}

// TestSelfHostIRVerifyRc exercises the self-host IR ownership verifier
// (examples/self_host/irverifyrc.fern, #7791) — the mirror of native's
// internal/ir/verifyrc.go.
//
// It checks the one invariant the reuse protocol rests on: the local whose
// uniqueness was tested, the local whose box becomes the allocation token,
// and the local released on the decline arm are the same local. A mismatch
// writes the new value over a box nothing proved unique.
//
// This side matters more than native's. The bug it exists for is here —
// docs/rc-log/2026-08-29-xblock-recipient-site-key.md is emit_cross_struct_reuse
// overwriting a donor's box after a first-match lookup resolved the wrong one,
// and the self-host lowering has several independent reuse emitters where native funnels all
// three through one emitReuseToken.
//
// The driver asserts both directions on BOTH compilers' emitted shapes, and
// that every shape the pass cannot model is skipped for its own named reason
// rather than reported.
func TestSelfHostIRVerifyRc(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("irverify_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing assertion's id in irverify_run.fern", code)
	}
	if want := "irverifyrc: all reuse-donor checks agree"; !strings.Contains(string(out), want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

// skipReasonRe pulls the argument out of a skip-reason call on either side:
// native records one with c.cov.skip("..."), the self-host with
// site_skip("...").
var skipReasonRe = regexp.MustCompile(`(?:cov\.skip|site_skip)\("([^"]+)"\)`)

func skipReasons(t *testing.T, path string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, m := range skipReasonRe.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s: found no skip reasons — the extraction regexp has gone stale, "+
			"which would make this test pass vacuously", path)
	}
	return out
}

// TestSelfHostIRVerifyRcSkipReasonsMatchNative pins the two ownership
// verifiers' coverage vocabulary against each other.
//
// Both passes are fail-soft: a reuse shape they cannot model is skipped and
// counted rather than reported, and the count is what the corpus gates hold a
// floor under. That only means something if the two implementations recognise
// the same set of shapes. A reason on one side and not the other is either a
// recogniser that has drifted or a shape one of them silently stopped
// modelling — both of which read as coverage on one side and nothing on the
// other.
//
// Needs no toolchain: it is a property of the two sources.
func TestSelfHostIRVerifyRcSkipReasonsMatchNative(t *testing.T) {
	native := skipReasons(t, filepath.Join("..", "..", "internal", "ir", "verifyrc.go"))
	selfHost := skipReasons(t, filepath.Join("..", "..", "examples", "self_host", "irverifyrc.fern"))

	var onlyNative, onlySelfHost []string
	for r := range native {
		if !selfHost[r] {
			onlyNative = append(onlyNative, r)
		}
	}
	for r := range selfHost {
		if !native[r] {
			onlySelfHost = append(onlySelfHost, r)
		}
	}
	sort.Strings(onlyNative)
	sort.Strings(onlySelfHost)
	if len(onlyNative) > 0 {
		t.Errorf("verifyrc.go skips for %d reason(s) irverifyrc.fern does not: %s",
			len(onlyNative), strings.Join(onlyNative, "; "))
	}
	if len(onlySelfHost) > 0 {
		t.Errorf("irverifyrc.fern skips for %d reason(s) verifyrc.go does not: %s",
			len(onlySelfHost), strings.Join(onlySelfHost, "; "))
	}
}

// fernStringListRe pulls the elements of one `return [...]` string-array
// literal out of a named Fern function, so the parity test below reads
// the self-host's tables from its source rather than from a rebuild.
func fernStringList(t *testing.T, src, fn string) []string {
	t.Helper()
	at := strings.Index(src, "function "+fn+"(")
	if at < 0 {
		t.Fatalf("irverifyrc.fern no longer defines %s — this test is no longer comparing anything", fn)
	}
	body := src[at:]
	lo := strings.Index(body, "return [")
	if lo < 0 {
		t.Fatalf("%s: no `return [` list found", fn)
	}
	body = body[lo:]
	hi := strings.Index(body, "]")
	if hi < 0 {
		t.Fatalf("%s: unterminated list", fn)
	}
	out := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(body[:hi], -1)
	var names []string
	for _, m := range out {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatalf("%s: extracted no names — the list shape has changed", fn)
	}
	return names
}

// TestSelfHostIRVerifyRcReleaseSetMatchesNative pins the two verifiers'
// idea of what a release IS.
//
// Both passes read a reuse site's decline arm looking for the release of
// the donor, and both decide "this op is a release" from the callee's
// name. A name one side counts and the other does not is a silent
// divergence of the worst kind: the arm still parses, so nothing is
// reported — one verifier simply checks a site the other skips, or
// worse, reads a different op as the release and compares the wrong
// slot.
//
// Native's record is internal/ir/rcsigs.go, which additionally carries
// the retains, the inspection, the unmodelled helpers and a completeness
// gate against the wasm runtime registry. Only the release half is
// mirrored, and only the release half is compared here.
func TestSelfHostIRVerifyRcReleaseSetMatchesNative(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "self_host", "irverifyrc.fern")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	for _, tc := range []struct {
		fn     string
		native []string
	}{
		{"rc_release_helpers", ir.RcReleaseNames()},
		{"generated_drop_prefixes", ir.RcGeneratedDropPrefixes()},
		{"generated_drop_names", ir.RcGeneratedDropNames()},
	} {
		selfHost := map[string]bool{}
		for _, n := range fernStringList(t, src, tc.fn) {
			selfHost[n] = true
		}
		native := map[string]bool{}
		for _, n := range tc.native {
			native[n] = true
		}
		var onlyNative, onlySelfHost []string
		for n := range native {
			if !selfHost[n] {
				onlyNative = append(onlyNative, n)
			}
		}
		for n := range selfHost {
			if !native[n] {
				onlySelfHost = append(onlySelfHost, n)
			}
		}
		sort.Strings(onlyNative)
		sort.Strings(onlySelfHost)
		if len(onlyNative) > 0 {
			t.Errorf("%s: internal/ir/rcsigs.go has %d entr(ies) irverifyrc.fern does not: %s",
				tc.fn, len(onlyNative), strings.Join(onlyNative, ", "))
		}
		if len(onlySelfHost) > 0 {
			t.Errorf("%s: irverifyrc.fern has %d entr(ies) internal/ir/rcsigs.go does not: %s",
				tc.fn, len(onlySelfHost), strings.Join(onlySelfHost, ", "))
		}
	}
}
