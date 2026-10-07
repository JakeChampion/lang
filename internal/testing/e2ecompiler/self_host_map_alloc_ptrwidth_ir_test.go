package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapAllocPtrWidthIRProbeX86_64 checks the core/map functions that
// compose only raw-memory intrinsics (__alloc / __ptr_width / load_* / store_* /
// memcpy): each must read "ir" in the per-declaration -ir-probe report.
//
// x86-64 only (the loader driver takes argv file paths, like the other modload
// tests). The probe reports the typed lowering's verdict
// (semlower.verdict_text) over the bundled program.
func TestSelfHostMapAllocPtrWidthIRProbeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	const prog = "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 5); return m.get_or(\"a\", 0); }\n"
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	// -no-treeshake: staged-progress probe over EVERY core/map function (incl.
	// ones the driver program never reaches). The default-on stdlib-root
	// treeshake (added later) would prune those out of the report, hiding the
	// frontier this gate measures — so opt out of it.
	out, err := exec.Command(mmc, mainPath, stdlibRoot, "-no-treeshake", "-ir-probe").Output()
	if err != nil {
		t.Fatalf("ir-probe: %v", err)
	}
	report := string(out)

	// Functions that compose ONLY alloc / ptr_width / load_* / store_* / memcpy —
	// these must flip to "ir" now that __alloc + __ptr_width lower. (Each line is
	// "<fn>: ir" or "<fn>: BAIL <reasons>".)
	//
	// Bare names, not `map____map_*`: core/map's helpers keep their names in
	// the bundle under both compilers since #9608, so the alias table every
	// backend routes `map_new` / `__method_Map_get` through can name them.
	mustIR := []string{
		"__map_lookup",
		"__map_get_impl",
		"__map_get_or_impl",
		"__map_set_impl",
		"__map_clone",
		"__map_values_impl",
		"__map_iter_impl",
	}
	for _, fn := range mustIR {
		if !strings.Contains(report, fn+": ir") {
			t.Errorf("%s did not route ir after __alloc + __ptr_width lowering.\nreport:\n%s", fn, report)
		}
	}
}
