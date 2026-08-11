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
