package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostSeededRandomBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range []struct {
			name string
			env  []string
		}{
			{name: "plain"},
			{name: "ownership", env: []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				env := append([]string{"FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1"}, tc.env...)
				stderr, code := cli.exitOf(t, e2eharness.SeededRandomBytesProgram, target, env...)
				if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
					t.Fatalf("exit = %d, want 0 without a sanitizer finding\n%s", code, stderr)
				}
				if tc.name == "ownership" && (!strings.Contains(stderr, "leakcheck:") || !strings.Contains(stderr, "live_bytes=0")) {
					t.Fatalf("missing zero-live-byte heap census\n%s", stderr)
				}
			})
		}
	}
}

func TestSelfHostArm64DarwinSeededRandomBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src, bin := filepath.Join(dir, "random.fern"), filepath.Join(dir, "random")
	if err := os.WriteFile(src, []byte(e2eharness.SeededRandomBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	} else {
		assertBalancedCensus(t, string(out))
	}
	if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("interpreter: %v\n%s", err, out)
	}
}
