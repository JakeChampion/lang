package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapMemsetFreeIRProbeX86_64 verifies stage 2 of lowering core/map
// through the IR path: the __memset and __free raw-memory intrinsics (new
// op_memset / op_free). After stage 1 (__alloc + __ptr_width) 28/32 core/map
// functions routed IR; the four that still bailed needed __memset (map_new_impl /
// __map_grow / __map_clear_impl, which zero/-1-fill their bucket arrays and free
// the old buffer on grow) or the RC array helpers (__map_dec_value — a later
// stage). With __memset + __free lowered, the three __memset/__free users flip
// from "BAIL call" to "ir", leaving only __map_dec_value. (The module verdict
// stays AST until that last function lowers too — the whole module routes AST if
// ANY function bails; the per-function -ir-probe is the staged-progress gate.)
//
// x86-64 only (the loader driver takes argv file paths, like the modload tests).
func TestSelfHostMapMemsetFreeIRProbeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
	// -no-treeshake: this is a staged-progress probe that inspects the
	// IR-eligibility of EVERY core/map function (incl. ones the tiny driver
	// program never reaches, like __map_clear_impl). The default-on stdlib-root
	// treeshake (added later) would prune those unreached functions out of the
	// report, hiding the very frontier this gate measures — so opt out of it.
	out, err := exec.Command(mmc, mainPath, stdlibRoot, "-no-treeshake", "-ir-probe").Output()
	if err != nil {
		t.Fatalf("ir-probe: %v", err)
	}
	report := string(out)

	// The __memset / __free users — must flip to "ir" now that both intrinsics lower.
	for _, fn := range []string{"map__map_new_impl", "map____map_grow", "map____map_clear_impl"} {
		if !strings.Contains(report, fn+": ir") {
			t.Errorf("%s did not route ir after __memset + __free lowering.\nreport:\n%s", fn, report)
		}
	}
}
