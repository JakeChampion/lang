package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTraitIRPathX86_64 asserts the typed lowering produces every trait
// case whole: `asm_load_run -decide` prints "ir" for each. The trait cases
// themselves only assert exit codes (TestSelfHostTraitsX86_64), so without this
// a trait program the drivers refuse would show up only as a failed compile.
//
// It runs the loading driver with the stdlib root, as the CLI compiles them:
// several cases call into std/i32 or core/cmp (a derived Display or Json
// reaches i32.to_string), which a stdin driver cannot load.
func TestSelfHostTraitIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "trait_"+strings.ReplaceAll(tc.name, "-", "_")+".fern")
			if err := os.WriteFile(entry, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			out, err := runX86_64Bin(runner, driverBin, entry, stdlibRoot, "-decide").Output()
			if err != nil {
				t.Fatalf("decide failed: %v", err)
			}
			if got := strings.TrimSpace(string(out)); got != "ir" {
				t.Errorf("%s: -decide = %q, want \"ir\"", tc.name, got)
			}
		})
	}
}
