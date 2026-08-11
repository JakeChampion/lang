package e2eselfhost

import (
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
