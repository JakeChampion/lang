package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostArm64DarwinASCIIByteText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, program := range []struct{ name, source string }{
		{"method", e2eharness.ASCIIByteTextProgram},
		{"legacy", e2eharness.LegacyASCIICharProgram},
	} {
		t.Run(program.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(program.source), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), "ascii")
			compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
			compile.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
			// The interpreter exposes the public byte method; legacy chr is
			// a compiled-runtime compatibility entry, absent from its host API.
			if program.name == "method" {
				if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
					t.Fatalf("interpret: %v\n%s", err, out)
				}
			}
		})
	}
}

func TestSelfHostASCIIByteText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, program := range []struct{ name, source string }{
			{"method", e2eharness.ASCIIByteTextProgram},
			{"legacy", e2eharness.LegacyASCIICharProgram},
		} {
			t.Run(target+"/"+program.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, program.source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit = %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
