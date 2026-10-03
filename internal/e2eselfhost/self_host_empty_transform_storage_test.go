package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

var emptyTransformStorageCases = []struct {
	name, expression, want string
}{
	{"zero_count", `"x".repeat(0)`, ""},
	{"negative_count", `"x".repeat(-1)`, ""},
	{"empty_input", `"".repeat(8)`, ""},
	{"empty_input_large_count", `"".repeat(2147483647)`, ""},
	{"unicode_zero_count", `"€🙂".repeat(0)`, ""},
	{"nonempty_control", `"abcdefgh".repeat(1)`, "abcdefgh"},
	{"replace_all", `"xx".replace("x", "")`, ""},
	{"replace_unicode", `"€🙂€🙂".replace("€🙂", "")`, ""},
	{"replace_no_match", `"xy".replace("z", "")`, "xy"},
	{"replace_nonempty", `"xx".replace("x", "y")`, "yy"},
}

func emptyTransformStorageSource(expression, want string) string {
	return fmt.Sprintf(`import "std/string";
function check(s: string, want: string): i32 {
  let aliases: string[] = [s, s];
  if (aliases[0] != want || aliases[1] != want) { return 1; }
  let joined: string = s + "tail";
  if (joined != want + "tail") { return 2; }
  return 42;
}
function main(): i32 { return check(%s, %q); }
`, expression, want)
}

// Empty transforms must allocate the same size that string release accounts for.
// Retained aliases and concatenation exercise ownership of the result as well
// as its value; equal allocation/free counts alone miss a size mismatch.
func TestSelfHostEmptyTransformStorage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range emptyTransformStorageCases {
				t.Run(tc.name, func(t *testing.T) {
					stderr, code := cli.exitOf(t, emptyTransformStorageSource(tc.expression, tc.want), target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 42 {
						t.Fatalf("exit = %d, want 42\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

func TestSelfHostArm64DarwinEmptyTransformStorage(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	for _, tc := range emptyTransformStorageCases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			src := filepath.Join(work, "transform.fern")
			if err := os.WriteFile(src, []byte(emptyTransformStorageSource(tc.expression, tc.want)), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(work, "transform")
			cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd = exec.Command(bin)
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 42 {
				t.Fatalf("run: %v; want exit 42\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
	t.Run("replace_wraps_to_zero", func(t *testing.T) {
		work := t.TempDir()
		src := filepath.Join(work, "overflow.fern")
		if err := os.WriteFile(src, []byte(replaceWrapsToZeroSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(work, "overflow")
		cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		cmd = exec.Command(bin)
		out, err := cmd.CombinedOutput()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 || !strings.Contains(string(out), allocSizeCause) {
			t.Fatalf("run: %v; want exit 134 and size diagnostic\n%s", err, out)
		}
	})
}
