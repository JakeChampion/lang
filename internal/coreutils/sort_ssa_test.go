package coreutils

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// sort asks for args repeatedly while constructing its help and option scanner.
// The SSA args cache used to be freed by one caller while the others still held
// it, corrupting options or crashing on a later allocation. Keep the real GNU
// corpus on this backend as well as the reduced runtime regression.
func TestSortSSAParity(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || len(crossPrefix()) != 0 {
		t.Skip("SSA sort corpus requires native ARM64 Linux")
	}
	fern := e2eharness.BuildLangBinForInterp(t)
	src := filepath.Join(repoRoot(t), "coreutils", "sort.fern")
	for _, mode := range []struct {
		name  string
		flags []string
	}{
		{name: "debug"},
		{name: "release", flags: []string{"-O"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "sort-ssa")
			args := append([]string{"-target", "arm64-linux", "-backend", "ssa", "-o", bin}, mode.flags...)
			args = append(args, src)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile SSA sort: %v\n%s", err, out)
			}
			requireParityBinary(t, "sort", bin, sortCases(t))
		})
	}
}
