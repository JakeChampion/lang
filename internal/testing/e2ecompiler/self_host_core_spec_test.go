package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	coreSpecPath  = "../../../spec/core.md"
	coreSpecBegin = "<!-- BEGIN ir_effects_run -->\n"
	coreSpecEnd   = "<!-- END ir_effects_run -->\n"
)

// TestSelfHostCoreSpecMatchesTheIR pins spec/core.md's instruction table to
// compiler/ir.fern: ir_effects_run prints one row per registered op kind from
// the same op_pops / op_pushes the verifier and the backends read, and the
// table between the markers must be exactly that output. A new op without a
// row, a row for a retired op, or an effect the table misstates all fail.
// FERN_UPDATE_GOLDEN=1 rewrites the table from the driver.
func TestSelfHostCoreSpecMatchesTheIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ir_effects_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ir_effects_run.fern", "ir_effects_run")
	out, err := runX86_64Bin(runner, bin).Output()
	if err != nil {
		t.Fatalf("ir_effects_run: %v", err)
	}

	raw, err := os.ReadFile(coreSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	begin := strings.Index(spec, coreSpecBegin)
	end := strings.Index(spec, coreSpecEnd)
	if begin < 0 || end < begin {
		t.Fatalf("%s: no %q ... %q block", coreSpecPath, strings.TrimSpace(coreSpecBegin), strings.TrimSpace(coreSpecEnd))
	}
	head := "| Op | Id | Pops | Pushes |\n| --- | --- | --- | --- |\n"
	got := spec[begin+len(coreSpecBegin) : end]
	want := head + string(out)
	if got == want {
		return
	}
	// CI-DARK: FERN_UPDATE_GOLDEN — a regeneration tool, not coverage.
	if os.Getenv("FERN_UPDATE_GOLDEN") == "1" {
		updated := spec[:begin+len(coreSpecBegin)] + want + spec[end:]
		if err := os.WriteFile(filepath.Clean(coreSpecPath), []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	gotRows := map[string]bool{}
	for _, l := range strings.Split(got, "\n") {
		gotRows[l] = true
	}
	wantRows := map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		wantRows[l] = true
		if !gotRows[l] {
			t.Errorf("spec/core.md is missing row: %s", l)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !wantRows[l] {
			t.Errorf("spec/core.md has a row compiler/ir.fern does not: %s", l)
		}
	}
	t.Errorf("spec/core.md's table is out of date or out of order; FERN_UPDATE_GOLDEN=1 rewrites it")
}
