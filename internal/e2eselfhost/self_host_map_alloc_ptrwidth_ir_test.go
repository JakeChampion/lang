package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapAllocPtrWidthIRProbeX86_64 verifies the first stage of lowering
// core/map through the IR path: the __alloc and __ptr_width raw-memory intrinsics
// (new op_alloc dispatch + new op_ptr_width). core/map's hashmap functions used
// to all `BAIL call` (no IR lowering for these intrinsics); with op_alloc +
// op_ptr_width, the ~28 functions that compose only the now-lowered raw-memory
// intrinsics (alloc / ptr_width / load_* / store_* / memcpy) flip from "BAIL
// call" to "ir" in the per-function -ir-probe report. (The module verdict stays
// AST until the remaining __memset / __free / RC-helper intrinsics also lower —
// follow-up stages; the whole module routes AST if ANY function bails. This test
// gates the per-function progress that is the staged-PR validation signal.)
//
// x86-64 only (the loader driver takes argv file paths, like the other modload
// tests). The probe runs asm_ir.eligibility_report over the bundled program.
func TestSelfHostMapAllocPtrWidthIRProbeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	const prog = "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 5); return m.get_or(\"a\", 0); }\n"
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
	mustIR := []string{
		"map____map_lookup",
		"map____map_get_impl",
		"map____map_get_or_impl",
		"map____map_set_impl",
		"map____map_clone",
		"map____map_values_impl",
		"map____map_iter_impl",
	}
	for _, fn := range mustIR {
		if !strings.Contains(report, fn+": ir") {
			t.Errorf("%s did not route ir after __alloc + __ptr_width lowering.\nreport:\n%s", fn, report)
		}
	}
}
