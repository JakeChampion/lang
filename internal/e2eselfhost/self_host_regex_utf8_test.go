package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The general self-host fixture lane requires a native x86-64 host. Use
// the primary CLI harness here so all regex callers also run on arm64 hosts.
func TestSelfHostRegexConformance(t *testing.T) {
	cli := buildSelfHostCLI(t)
	paths, err := filepath.Glob("../../conformance/cases/regex*/main.fern")
	if err != nil || len(paths) == 0 {
		t.Fatalf("regex fixtures: %v (%d paths)", err, len(paths))
	}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile(filepath.Join(filepath.Dir(path), "expected.exit"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := strconv.Atoi(strings.TrimSpace(string(expected)))
		if err != nil {
			t.Fatal(err)
		}
		const main = "function main(): i32"
		if strings.Count(string(src), main) != 1 {
			t.Fatalf("%s: expected one main declaration", path)
		}
		// Compare inside the program so WASI's exit-code cap cannot hide
		// a failing bitmask assertion in the older fixtures.
		program := strings.Replace(string(src), main, "function regex_fixture_main(): i32", 1)
		program += fmt.Sprintf("\nfunction main(): i32 { if (regex_fixture_main() == %d) { return 0; } return 1; }\n", want)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(filepath.Base(filepath.Dir(path))+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, program, target, "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit = %d\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}

func TestSelfHostRegexUTF8(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, mode := range []string{"", "1"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run("legacy-env="+mode+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, e2eharness.RegexUTF8Program, target, "FERN_SEM_IR="+mode, "FERN_SEM_IR_STRICT="+mode, "FERN_SEM_IR_ONLY=", "FERN_SEM_IR_SKIP=", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit = %d\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}

func TestSelfHostArm64DarwinRegexUTF8(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "range.fern")
	if err := os.WriteFile(src, []byte(e2eharness.RegexUTF8Program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "1"} {
		t.Run("legacy-env="+mode, func(t *testing.T) {
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
		if err := os.WriteFile(src, []byte(e2eharness.RegexUTF8Program), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
}
