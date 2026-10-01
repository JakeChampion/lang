package e2eselfhost

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSelfHostArm64DarwinStringConcatCopySearch(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "range.fern")
	if err := os.WriteFile(src, []byte(e2eharness.StringConcatCopySearchProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Run("typed="+mode, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "range")
			compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
			compile.Env = append(os.Environ(), "FERN_SEM_IR="+mode, "FERN_SEM_IR_STRICT="+mode, "FERN_SEM_IR_ONLY=", "FERN_SEM_IR_SKIP=", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			if out, err := exec.Command(bin).CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			} else {
				assertBalancedCensus(t, string(out))
			}
		})
	}
	t.Run("interp", func(t *testing.T) {
		if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
}
