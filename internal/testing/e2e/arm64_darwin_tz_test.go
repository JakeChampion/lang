package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestArm64DarwinTz runs tests/stdlib/tz_darwin_test.fern, std/tz under
// tzcode's rules, which only a Darwin build follows. Off Apple Silicon the
// build is what is checked.
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
	for _, w := range []string{"# Suite: tz-darwin", "1..12", "# pass 12", "# fail 0"} {
		if !strings.Contains(string(got), w) {
			t.Errorf("output missing %q\n%s", w, got)
		}
	}
}
