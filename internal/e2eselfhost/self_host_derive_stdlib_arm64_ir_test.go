package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// arm64 parity for TestSelfHostDeriveStdlibIR: the full @derive family through
// the REAL loaded stdlib on the self-hosted aarch64 IR path. The treeshake
// prune and the IR-eligibility decision are target-agnostic (they run on the
// merged module before backend selection), so arm64 routes exactly as x86-64;
// this gate confirms the aarch64 emitter then produces correct code. The arm64
// driver (asm_load_run -target arm64-linux) is built as a native x86 host binary that EMITS
// aarch64 asm; aarch64 gcc assembles + links; qemu-aarch64 runs it. Each case
// is pinned to the "ir" route (observed on the x86 host, no qemu) and
// oracle-checked against the native interpreter. Reuses deriveStdlibCases.
func TestSelfHostDeriveStdlibIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 derive-stdlib gate needs a native x86 host to run the driver")
	}
	dir := copySelfHostTree(t)
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range deriveStdlibCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "dsa_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// Oracle: the native interpreter's exit code.
			_, want := runFixtureInterp(t, entry, "")
			// Routing is target-agnostic; observe it on the x86 host (no qemu).
			out, _ := exec.Command(mmc, entry, root, "-target", "arm64-linux", "-decide").Output()
			if strings.TrimSpace(string(out)) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(string(out)))
			}
			// Emit aarch64 asm (treeshake auto-applies with the root), assemble +
			// link with aarch64 gcc, run under qemu — must match the oracle.
			asm, err := exec.Command(mmc, entry, root, "-target", "arm64-linux").Output()
			if err != nil {
				t.Fatalf("%s: self-host compile failed: %v", tc.name, err)
			}
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "dsa_"+tc.name, string(asm))
			rc := runArm64Bin(qemu, bin)
			_ = rc.Run()
			if code := rc.ProcessState.ExitCode(); code != want {
				t.Errorf("%s aarch64 run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
