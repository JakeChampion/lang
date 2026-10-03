package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func breBytesFixture(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"lib/bre.fern", "testdata/" + fixture} {
		src, err := os.ReadFile(filepath.Join("../../coreutils", name))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "testdata", fixture)
}

func TestSelfHostBREPrefixBytes(t *testing.T) {
	testSelfHostBREBytes(t, "bre_prefix_bytes.fern")
}

func TestSelfHostBREInputBytes(t *testing.T) {
	testSelfHostBREBytes(t, "bre_input_bytes.fern")
}

func TestSelfHostBREPatternBytes(t *testing.T) {
	testSelfHostBREBytes(t, "bre_pattern_bytes.fern")
}

func testSelfHostBREBytes(t *testing.T, fixture string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	src := breBytesFixture(t, fixture)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostArm64DarwinBREPrefixBytes(t *testing.T) {
	testSelfHostArm64DarwinBREBytes(t, "bre_prefix_bytes.fern")
}

func TestSelfHostArm64DarwinBREInputBytes(t *testing.T) {
	testSelfHostArm64DarwinBREBytes(t, "bre_input_bytes.fern")
}

func TestSelfHostArm64DarwinBREPatternBytes(t *testing.T) {
	testSelfHostArm64DarwinBREBytes(t, "bre_pattern_bytes.fern")
}

func testSelfHostArm64DarwinBREBytes(t *testing.T, fixture string) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src := breBytesFixture(t, fixture)
	bin := filepath.Join(t.TempDir(), "bre")
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	assertBalancedCensus(t, string(out))
}
