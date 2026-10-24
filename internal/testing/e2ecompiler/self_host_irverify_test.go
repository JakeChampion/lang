package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostIRVerifyStructure(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

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

func TestSelfHostIRVerifyStack(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

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

func TestSelfHostIRVerifyFip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

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

func TestSelfHostIRVerifyProvided(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

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

func TestSelfHostIRVerifyProvidedAudit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "provided_audit")

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

func TestSelfHostIRVerifyProvidedCompilerClean(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-compiler sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProjectTyped(t)
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "provided_compiler")

	cmd := exec.Command(bin, filepath.Join(dir, "drivers/asm_modload_run.fern"), "-verifyprovided")
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

func TestSelfHostIRVerifyProvidedCorpusClean(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "provided_corpus")
	testProvidedCorpus(t, bin)
}

func testProvidedCorpus(t *testing.T, bin string) {
	t.Helper()
	stdRoot := langSrcAbs(t, filepath.Join("internal", "stdlib"))
	all, err := filepath.Glob(filepath.Join(langSrcAbs(t, "conformance"), "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	var mains []string
	for _, main := range all {
		if _, err := os.Stat(filepath.Join(filepath.Dir(main), "expected.error")); err != nil {
			mains = append(mains, main)
		}
	}
	if len(mains) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus: a silently shrunken sweep proves nothing", len(mains))
	}

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
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				var out []byte
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
					t.Errorf("invalid resolution verdict: %v\n%s\nstderr:\n%s", err, out, stderr.String())
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
	if modelled != bodies {
		t.Errorf("stack pass modelled %d of %d lowered bodies; a skip is an op the pass's table does not carry", modelled, bodies)
	}
	if swept == len(mains) && calls < 2000 {
		t.Errorf("pass resolved %d direct calls across the corpus, expected far more: a sweep that resolved nothing proves nothing", calls)
	}
	t.Logf("swept %d/%d fixtures, resolved %d direct calls, stack-modelled %d/%d bodies", swept, len(mains), calls, modelled, bodies)
}

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

func TestSelfHostIRVerifyRc(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

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
