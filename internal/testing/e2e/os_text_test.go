package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestInterpreterOSText(t *testing.T) {
	checkInterpreterOSText(t, e2eharness.MakeOSTextFixture(t, true))
}

func TestInterpreterOSTextGetcwd(t *testing.T) {
	checkInterpreterOSText(t, e2eharness.MakeInvalidCwdFixture(t))
}

func checkInterpreterOSText(t *testing.T, f e2eharness.OSTextFixture) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "os-text.fern")
	if err := os.WriteFile(src, []byte(f.Source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-interp", src)
	cmd.Dir = f.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
}

func TestInterpreterOSBytes(t *testing.T) {
	checkInterpreterOSText(t, e2eharness.MakeOSBytesFixture(t, true, true))
}
