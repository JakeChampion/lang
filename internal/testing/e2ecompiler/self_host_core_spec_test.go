// CI-DARK: FERN_UPDATE_GOLDEN — This flag regenerates the reference data.
package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

const (
	coreSpecPath  = "../../../spec/core.md"
	coreSpecBegin = "<!-- BEGIN ir_effects_run -->\n"
	coreSpecEnd   = "<!-- END ir_effects_run -->\n"
)

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
	if envflag.On("FERN_UPDATE_GOLDEN") {
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
