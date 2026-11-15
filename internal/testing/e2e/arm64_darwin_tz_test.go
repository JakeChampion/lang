package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestArm64DarwinTz(t *testing.T) {
	bin := buildFernCLI(t)
	out := filepath.Join(t.TempDir(), "tz_darwin_test")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, langSrcAbs(t, "tests/stdlib/tz_darwin_test.fern")).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	e2eharness.CheckTAPPlan(t, string(got))
	for _, w := range []string{"# Suite: tz-darwin", "# fail 0"} {
		if !strings.Contains(string(got), w) {
			t.Errorf("output missing %q\n%s", w, got)
		}
	}
}
