package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	out, err := exec.Command(mmc, mainPath, stdlibRoot, "-no-treeshake", "-ir-probe").Output()
	if err != nil {
		t.Fatalf("ir-probe: %v", err)
	}
	report := string(out)

	mustIR := []string{
		"__map_lookup",
		"__map_get_impl",
		"__map_get_or_impl",
		"__map_set_impl",
		"__map_clone",
		"__map_keys_impl",
		"__map_values_impl",
	}
	for _, fn := range mustIR {
		if !strings.Contains(report, fn+": ir") {
			t.Errorf("%s did not route ir after __alloc + __ptr_width lowering.\nreport:\n%s", fn, report)
		}
	}
}
