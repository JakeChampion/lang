package e2eharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const bigLinkMinAsmBytes = 8 << 20

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

func gccBigLinkWeightMB(asmLen int) int {
	return 500 + 6*(asmLen>>20)
}
