package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRVerifyStructure exercises the self-host IR structure verifier
// (examples/self_host/irverify.fern, #6639 slice 1) — the port of native's
// internal/ir/verify.go.
//
// The driver asserts each check class in BOTH directions: a malformed op
// stream the pass must report, and a well-formed one it must stay silent on.
// The silent half carries the weight. A verifier that reports a problem on
// valid IR is worse than no verifier, because it fires on every real module
// and there is nothing to fix; TestSelfHostIRVerifyCorpusClean below is the
// same property measured against real lowered output rather than hand-built
// streams.
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

// TestSelfHostIRVerifyStackCorpusClean runs the operand-stack verifier over
// every conformance fixture's lowered IR and requires zero problems AND full
// coverage.
//
// Coverage is asserted alongside the problems because an empty problem list
// means nothing without knowing how much was looked at: the pass abandons any
// function holding an op it does not model, so a widened op vocabulary would
// otherwise turn into silent unchecked functions rather than a failure. Every
// function in the corpus is modelled today, so the floor is equality.
func TestSelfHostIRVerifyStackCorpusClean(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	cases, err := filepath.Glob(filepath.Join(langSrcAbs(t, "conformance"), "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	if len(cases) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus — a silently shrunken sweep proves nothing", len(cases))
	}

	var dirty []string
	modelled, funcs := 0, 0
	for _, c := range cases {
		src, err := os.ReadFile(c)
		if err != nil {
			t.Fatalf("reading %s: %v", c, err)
		}
		cmd := exec.Command(bin, "-verifystack")
		cmd.Stdin = strings.NewReader(string(src))
		out, _ := cmd.Output()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Errorf("%s: driver did not exit normally", filepath.Base(filepath.Dir(c)))
			continue
		}
		if cmd.ProcessState.ExitCode() != 0 {
			dirty = append(dirty, filepath.Base(filepath.Dir(c))+": "+strings.TrimSpace(string(out)))
		}
		m, f := parseCoverage(string(out))
		modelled += m
		funcs += f
	}
	if len(dirty) > 0 {
		max := 15
		if len(dirty) < max {
			max = len(dirty)
		}
		t.Errorf("IR stack verifier reported problems on %d of %d conformance fixtures:\n  %s",
			len(dirty), len(cases), strings.Join(dirty[:max], "\n  "))
	}
	if funcs < 700 {
		t.Errorf("stack pass saw %d lowered functions across the corpus, expected the full set — a shrunken sweep proves nothing", funcs)
	}
	if modelled != funcs {
		t.Errorf("stack pass modelled %d of %d lowered functions; every one of them is modelled today, so a skip is a new op the table does not carry", modelled, funcs)
	}
}

// parseCoverage reads the `modelled M/N` tally out of a `-verifystack` run.
func parseCoverage(out string) (int, int) {
	i := strings.Index(out, "modelled ")
	if i < 0 {
		return 0, 0
	}
	var m, f int
	if _, err := fmt.Sscanf(out[i:], "modelled %d/%d", &m, &f); err != nil {
		return 0, 0
	}
	return m, f
}

// TestSelfHostIRVerifyCorpusClean runs the verifier over every conformance
// fixture's lowered IR and requires zero problems.
//
// This is the false-positive gate, and it is the one that decides whether the
// pass is usable: the corpus is known-good code, so any report here is the
// verifier being wrong about valid IR. It is also what would catch a real
// structural regression in the lowerer — a local index past the frame or an
// unbalanced scope becomes a named failure here instead of a SIGSEGV several
// stages downstream, which docs/TEST-GATES.md notes the self-referential
// fixpoint is structurally blind to.
//
// The driver lowers from a defaults-FILLED module, matching what the
// production backends see: they reach the IR through lift_lambdas, which runs
// fill_default_args_module first. Verifying lower_module's raw output instead
// reports a 1-arg call to a 3-param callee for every defaulted call —
// conformance/cases/default_args is the case that showed it.
func TestSelfHostIRVerifyCorpusClean(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	cases, err := filepath.Glob(filepath.Join(langSrcAbs(t, "conformance"), "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	if len(cases) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus — a silently shrunken sweep proves nothing", len(cases))
	}

	var dirty []string
	for _, c := range cases {
		src, err := os.ReadFile(c)
		if err != nil {
			t.Fatalf("reading %s: %v", c, err)
		}
		cmd := exec.Command(bin, "-verify")
		cmd.Stdin = strings.NewReader(string(src))
		out, _ := cmd.Output()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Errorf("%s: driver did not exit normally", filepath.Base(filepath.Dir(c)))
			continue
		}
		if cmd.ProcessState.ExitCode() != 0 {
			dirty = append(dirty, filepath.Base(filepath.Dir(c))+": "+strings.TrimSpace(string(out)))
		}
	}
	if len(dirty) > 0 {
		max := 15
		if len(dirty) < max {
			max = len(dirty)
		}
		t.Errorf("IR verifier reported problems on %d of %d conformance fixtures:\n  %s",
			len(dirty), len(cases), strings.Join(dirty[:max], "\n  "))
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

// TestSelfHostFipCensusOnNativesShapes runs the allocation-budget verifier over
// the exact programs native's internal/ir/fip_verify_test.go uses, and pins the
// self-host's per-function census against them.
//
// The point is not that the two agree — on two of these shapes they do not, and
// that is the finding. Native pairs the R1 struct self-overwrite and the R4
// consuming-match rebuild; the self-host's reuse layer pairs neither yet, so a
// bare `fbip` that native accepts needs a grade here. The R3 general pairing
// does match. Pinning the counts is what turns "the self-host is behind on two
// reuse families" from a thing someone rediscovers into a number that moves
// when the port lands — at which point this test fails and its expectations
// are the thing to update.
func TestSelfHostFipCensusOnNativesShapes(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	cases := []struct {
		name string
		src  string
		// want is the census line the named function must produce.
		want string
		// exit is the driver's exit code: 1 when a claim overruns its budget.
		exit int
	}{
		{
			// R4 consuming-match rebuild. Native pairs it, so bare `fbip`
			// verifies there; the self-host emits two fresh constructor sites.
			name: "r4-consuming-match",
			src: `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`,
			want: "map_inc claim=fbip(0) fresh=2 paired=0",
			exit: 1,
		},
		{
			// R1 struct self-overwrite on an `own` param. Native pairs it.
			name: "r1-self-overwrite",
			src: `struct P { x: i32, y: i32 }
fbip function bump(own p: P): P {
    p = P { x: p.x + 1, y: p.y };
    return p;
}
function main(): i32 { var q: P = bump(P { x: 1, y: 2 }); return q.x; }`,
			want: "bump claim=fbip(0) fresh=1 paired=0",
			exit: 1,
		},
		{
			// R3 general pairing: the second construction takes over the first
			// one's dead box. Self-host and native agree — one fresh site, one
			// paired — so `fbip(1)` verifies clean on both.
			name: "r3-general-pairing",
			src: `struct P { x: i32, y: i32 }
fbip(1) function churn(a0: i32): i32 {
    var a: P = P { x: a0, y: a0 + 1 };
    var s: i32 = a.x + a.y;
    var b: P = P { x: s + 1, y: a0 };
    return b.x + b.y;
}
function main(): i32 { return churn(3); }`,
			want: "churn claim=fbip(1) fresh=1 paired=1",
			exit: 0,
		},
		{
			// An un-paired construction under a bare claim: the shape native's
			// TestFbipVerifyUnpairedConstructionRejected pins, and the one both
			// compilers reject.
			name: "unpaired-rejected",
			src: `struct P { x: i32, y: i32 }
fbip function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 { var p: P = mk(3); return p.x; }`,
			want: "mk claim=fbip(0) fresh=1 paired=0",
			exit: 1,
		},
		{
			// A bare `fip` body that allocates nothing verifies clean, and its
			// zeroed census is what says so.
			name: "fip-clean",
			src: `fip function add2(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add2(1, 2); }`,
			want: "add2 claim=fip(0) fresh=0 paired=0",
			exit: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(bin, "-verifyfip")
			cmd.Stdin = strings.NewReader(c.src)
			out, _ := cmd.Output()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("driver did not exit normally")
			}
			if got := cmd.ProcessState.ExitCode(); got != c.exit {
				t.Errorf("exit code = %d, want %d\n%s", got, c.exit, out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Errorf("census missing %q, got:\n%s", c.want, out)
			}
		})
	}
}

// TestSelfHostFipVerifyCorpusClean runs the allocation-budget verifier over
// every conformance fixture and requires it to stay silent.
//
// This is the false-positive gate. Almost no fixture carries a fip/fbip
// annotation, so the pass verifies each function vacuously — which is exactly
// the property to pin: a claim-free function must never be charged, or the
// pass would fire on the whole corpus the moment it is wired into a build.
// The two fixtures that do report are skipped by name rather than silently
// tolerated, and the skip list says which reason each one is on.
func TestSelfHostFipVerifyCorpusClean(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	cases, err := filepath.Glob(filepath.Join(langSrcAbs(t, "conformance"), "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	if len(cases) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus — a silently shrunken sweep proves nothing", len(cases))
	}

	// The two fixtures a clean sweep must not include, each for its own
	// reason. The driver runs no checker, so it lowers a body native would
	// have rejected first — that is what makes diag_e053 a driver artifact
	// rather than a verifier finding.
	expectedDirty := map[string]bool{
		// Exists to overrun a budget: an un-paired `fbip` construction.
		"diag_e068": true,
		// `scale`'s array literal is E053 at the front end, so native never
		// lowers it; and its `map_inc` is the R4 consuming-match shape the
		// self-host reuse layer does not pair yet
		// (TestSelfHostFipCensusOnNativesShapes pins that count).
		"diag_e053": true,
	}

	var dirty []string
	seen := 0
	for _, c := range cases {
		name := filepath.Base(filepath.Dir(c))
		src, err := os.ReadFile(c)
		if err != nil {
			t.Fatalf("reading %s: %v", c, err)
		}
		cmd := exec.Command(bin, "-verifyfip")
		cmd.Stdin = strings.NewReader(string(src))
		out, _ := cmd.Output()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Errorf("%s: driver did not exit normally", name)
			continue
		}
		seen += strings.Count(string(out), " fresh=")
		if cmd.ProcessState.ExitCode() != 0 && !expectedDirty[name] {
			dirty = append(dirty, name+": "+strings.TrimSpace(string(out)))
		}
	}
	if len(dirty) > 0 {
		max := 15
		if len(dirty) < max {
			max = len(dirty)
		}
		t.Errorf("fip verifier reported problems on %d of %d conformance fixtures:\n  %s",
			len(dirty), len(cases), strings.Join(dirty[:max], "\n  "))
	}
	if seen < 700 {
		t.Errorf("fip pass censused %d lowered functions across the corpus, expected the full set — a shrunken sweep proves nothing", seen)
	}
}

// TestSelfHostIRVerifyProvided exercises the callee-resolution verifier
// (examples/self_host/irverifyprovided.fern, #6639 slice 4) — the port of
// native's internal/ir/verifyprovided.go.
//
// Same driver and the same both-directions discipline as the passes above.
// The silent half is the load-bearing one here too, and for a sharper reason
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
	dir := writeSelfHostModloadProject(t)
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

// providedCorpusExpectedDirty are the conformance fixtures the resolution pass
// reports, and is a list of two rather than a tolerance because each one is
// the pass being RIGHT about a program that is deliberately wrong.
//
// This driver runs the front end and the lowerer, not the checker — that is
// what makes it a verifier of the lowering rather than a second compiler. So a
// fixture whose whole point is a program the checker rejects reaches the
// lowerer anyway, and a program that calls a function it never declares
// genuinely does emit a call to a symbol nothing defines:
//
//   - diag_e065 calls `name()`, which the fixture never defines.
//   - diag_p004 calls `add()`, likewise.
//
// Every other fixture in the corpus resolves clean.
var providedCorpusExpectedDirty = map[string]bool{
	"diag_e065": true,
	"diag_p004": true,
}

// TestSelfHostIRVerifyProvidedCorpusClean sweeps the conformance corpus.
//
// It runs the MODLOAD driver rather than irlower_run, and that is the whole
// reason this slice arrives later than its siblings. A single module's
// lowering leaves every imported `Type.method` unresolved by construction: a
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

	stdRoot := langSrcAbs(t, filepath.Join("internal", "stdlib"))
	caseDirs, err := filepath.Glob(filepath.Join(langSrcAbs(t, "conformance"), "cases", "*"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	if len(caseDirs) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus — a silently shrunken sweep proves nothing", len(caseDirs))
	}

	work := t.TempDir()
	// The fixture's own modules have to travel with it — several cases are
	// multi-file (cross_module_bounded_method, multi_file, pub_use_reexport),
	// and sweeping only main.fern would report their siblings' functions as
	// undeclared, which is the same false positive the single-module driver
	// produces at stdlib scale.
	for _, name := range []string{"std", "core"} {
		if err := os.Symlink(filepath.Join(stdRoot, name), filepath.Join(work, name)); err != nil {
			t.Fatalf("linking stdlib %s: %v", name, err)
		}
	}

	var dirty, unexpectedlyClean []string
	swept, calls := 0, 0
	for _, cd := range caseDirs {
		name := filepath.Base(cd)
		mains, _ := filepath.Glob(filepath.Join(cd, "*.fern"))
		if len(mains) == 0 {
			continue
		}
		stage := filepath.Join(work, "case")
		if err := os.RemoveAll(stage); err != nil {
			t.Fatalf("clearing stage: %v", err)
		}
		if err := os.MkdirAll(stage, 0o755); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
		// The stage sits INSIDE work so `std/` and `core/` resolve from the
		// parent — modloader looks for `<dir>/std/x.fern`, so link them here
		// too rather than relying on a walk upward.
		for _, lib := range []string{"std", "core"} {
			if err := os.Symlink(filepath.Join(stdRoot, lib), filepath.Join(stage, lib)); err != nil {
				t.Fatalf("linking stdlib %s: %v", lib, err)
			}
		}
		var hasMain bool
		for _, f := range mains {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("reading %s: %v", f, err)
			}
			if filepath.Base(f) == "main.fern" {
				hasMain = true
			}
			if err := os.WriteFile(filepath.Join(stage, filepath.Base(f)), src, 0o644); err != nil {
				t.Fatalf("staging %s: %v", f, err)
			}
		}
		if !hasMain {
			continue
		}
		swept++
		cmd := exec.Command(bin, filepath.Join(stage, "main.fern"), "-verifyprovided")
		out, _ := cmd.Output()
		clean := cmd.ProcessState != nil && cmd.ProcessState.Exited() && cmd.ProcessState.ExitCode() == 0
		_, c := parseProvidedTally(string(out))
		calls += c
		switch {
		case !clean && !providedCorpusExpectedDirty[name]:
			dirty = append(dirty, name+": "+strings.TrimSpace(string(out)))
		case clean && providedCorpusExpectedDirty[name]:
			// A named exclusion that stopped being one is a stale list, which
			// is how a tolerance quietly becomes permanent.
			unexpectedlyClean = append(unexpectedlyClean, name)
		}
	}
	if len(dirty) > 0 {
		max := 15
		if len(dirty) < max {
			max = len(dirty)
		}
		t.Errorf("resolution pass reported problems on %d of %d conformance fixtures:\n  %s",
			len(dirty), swept, strings.Join(dirty[:max], "\n  "))
	}
	if len(unexpectedlyClean) > 0 {
		t.Errorf("these fixtures are listed as expected-dirty but resolved clean — drop them from providedCorpusExpectedDirty: %s",
			strings.Join(unexpectedlyClean, ", "))
	}
	if swept < 400 {
		t.Errorf("swept %d fixtures, expected the full corpus — a shrunken sweep proves nothing", swept)
	}
	if calls < 2000 {
		t.Errorf("pass resolved %d direct calls across the corpus, expected far more — a sweep that resolved nothing proves nothing", calls)
	}
}
