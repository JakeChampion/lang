package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTraitIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
