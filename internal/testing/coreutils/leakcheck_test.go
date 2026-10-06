package coreutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestUtilitiesFreeWhatTheyAllocate builds utilities with FERN_LEAKCHECK and
// requires each invocation to free every allocation it made. The parity
// suites compare output with GNU and cannot see a leak; `ls -l` and `stat`
// leaked the string builder behind every mode column they printed, and every
// directory listing lost 8 bytes per name whose length is a multiple of 8.
func TestUtilitiesFreeWhatTheyAllocate(t *testing.T) {
	// A name of every length from 1 to 33: a name buffer sized off by a byte
	// is freed into the wrong size class only when its length is a multiple
	// of 8.
	dir := t.TempDir()
	for n := 1; n <= 33; n++ {
		seedWrite(t, dir, strings.Repeat("f", n), "contents\n")
	}
	seedMkdir(t, dir, "sub")
	for _, c := range []struct {
		name string
		util string
		args []string
	}{
		{"ls_-l", "ls", []string{"-l", dir}},
		{"ls_-la", "ls", []string{"-la", dir}},
		{"stat", "stat", []string{filepath.Join(dir, "f"), filepath.Join(dir, "ffffffff"), filepath.Join(dir, "sub")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			argv := crossArgv(leakcheckBin(t, c.util), c.args...)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = baseEnv()
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%s failed: %v\n%s", c.util, err, stderr.String())
			}
			census := ""
			for _, line := range strings.Split(stderr.String(), "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					census = line
				}
			}
			var allocs, frees, live int64
			if _, err := fmt.Sscanf(census, "leakcheck: allocs=%d frees=%d live_bytes=%d", &allocs, &frees, &live); err != nil || allocs == 0 {
				t.Fatalf("no leakcheck summary: %v\n%s", err, stderr.String())
			}
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want every allocation freed", allocs, frees, live)
			}
		})
	}
}

// leakcheckBin compiles coreutils/<util>.fern with FERN_LEAKCHECK, which the
// compiler reads at emit time, so it is a separate build from fernBin's.
func leakcheckBin(t *testing.T, util string) string {
	t.Helper()
	root := repoRoot(t)
	e2eharness.TrackFernSources(t, filepath.Join(root, "coreutils"), util+".fern")
	bin := filepath.Join(t.TempDir(), util)
	cmd := exec.Command(e2eharness.BuildLangBinForInterp(t), "-target", fernTarget(t), "-o", bin,
		filepath.Join(root, "coreutils", util+".fern"))
	cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s with FERN_LEAKCHECK: %v\n%s", util, err, out)
	}
	return bin
}
