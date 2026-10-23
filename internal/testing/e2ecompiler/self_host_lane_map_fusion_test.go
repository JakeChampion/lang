package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The monomorphiser maps a lane kernel over an f64[] or an f32[] with its
// f64x2 / f32x4 instance (docs/SIMD-GENERIC-KERNELS.md, "Array fusion"). The
// conformance case lanes_map_fusion holds the answers to the scalar map's on
// every engine; this pins which of its maps are fused, by the functions the
// x86-64 assembly defines.
func TestSelfHostLaneMapFusion(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fusion.s")
	entry := e2eharness.RepoPath("conformance", "cases", "lanes_map_fusion", "main.fern")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", e2eharness.TargetX86_64Linux, "-emit", "asm", "-o", out, entry, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.SelfHostChildEnv()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var fused []string
	for _, line := range strings.Split(string(asm), "\n") {
		if name, ok := strings.CutPrefix(line, "__fn___lanes_map__"); ok && strings.HasSuffix(name, ":") && !strings.Contains(name, ".") {
			fused = append(fused, strings.TrimSuffix(name, ":"))
		}
	}
	sort.Strings(fused)
	want := []string{"clamp__f32", "clamp__f64", "poly__f32", "poly__f64", "sig__f64"}
	if strings.Join(fused, " ") != strings.Join(want, " ") {
		t.Errorf("fused maps = %v, want %v", fused, want)
	}
}
