// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3).
package e2eharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// bigLinkMinAsmBytes is the size above which a link of SELF-HOST-emitted
// asm reserves its estimated peak against the build-memory budget. The
// overwhelming majority of links are a few KB and take milliseconds; the
// compiler compiling itself (the stage-2 self-compile, a ~100 MB listing)
// is the kind of link that crosses this line.
const bigLinkMinAsmBytes = 8 << 20

// BuildBinArm64 assembles+links arm64 asm into dir/name with the aarch64
// gcc toolchain and returns its path — the arm64 sibling of BuildBin (note:
// no `-no-pie` — some aarch64 gcc builds reject it). A big listing links
// under a build-memory reservation sized to GNU as's measured peak, so it
// cannot stack with a concurrent heavy build.
func BuildBinArm64(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	binPath := filepath.Join(dir, name)
	asmPath := filepath.Join(dir, name+".s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	gccLink := func() error {
		if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			return fmt.Errorf("gcc %s: %v\n%s", name, err, out)
		}
		return nil
	}
	var err error
	if len(asm) >= bigLinkMinAsmBytes {
		err = withBuildMemory(gccBigLinkWeightMB(len(asm)), gccLink)
	} else {
		err = gccLink()
	}
	if err != nil {
		t.Fatal(err)
	}
	return binPath
}

// gccBigLinkWeightMB estimates GNU as+ld's peak RSS for a big `.s`: measured
// 392 MB on the compiler's 100 MB x86-64 listing and 590 MB on its 102 MB
// arm64 listing, so ~6 MB per MB of asm plus slack covers it.
func gccBigLinkWeightMB(asmLen int) int {
	return 500 + 6*(asmLen>>20)
}
