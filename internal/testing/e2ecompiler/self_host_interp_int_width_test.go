package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The shipped interpreter computes every integer at its static type's width,
// as the oracle and compiled code do (#12686): u8 arithmetic wraps, the
// smallest i32 literal is an i32, a radix literal takes the width its context
// gives it, and a literal tree is evaluated at the type of the destination it
// is written to.
func TestSelfHostInterpIntegerWidths(t *testing.T) {
	cli := buildSelfHostCLI(t)

	for _, name := range []string{"int_saturating_ops", "int_min_arithmetic", "hex_literal_width", "usize_wraps_at_64_bits"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("../../../conformance/cases", name)
			want, _ := os.ReadFile(filepath.Join(dir, "expected.stdout"))
			wantExit := 0
			if b, err := os.ReadFile(filepath.Join(dir, "expected.exit")); err == nil {
				wantExit, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			}
			out, code := stdoutAndExit(t, runX86_64Bin(cli.runner, cli.bin, "-interp", filepath.Join(dir, "main.fern"), cli.stdlib))
			if out != string(want) || code != wantExit {
				t.Errorf("exit %d, stdout:\n%s\nwant exit %d, stdout:\n%s", code, out, wantExit, want)
			}
		})
	}

	t.Run("destinations", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		prog := `import "std/i32";
import "std/i64";
import "std/u64";
import "core/int";

function ret(): i64 { return 2147483647 + 1; }
function arg(x: i64): i64 { return x; }

function main(): i32 {
  let a: i64 = 2147483647 + 1;
  let b: i64 = 0xFFFFFFFF;
  let c: u64 = 2147483647 * 4;
  let d: i64 = 0;
  d = 2147483647 + 2;
  let f: usize = 2147483647 + 1;
  print(a.to_string());
  print(b.to_string());
  print(c.to_string());
  print(d.to_string());
  print(f.to_string());
  print(ret().to_string());
  print(arg(2147483647 * 2).to_string());
  let w: u8 = 250u8 + 10u8;
  let z: u8 = 0u8 - 1u8;
  print((w as i32).to_string());
  print((z as i32).to_string());
  if (w < 10u8) {
    print("wrapped");
  }
  let mn: i32 = -2147483648;
  print((mn - 1).to_string());
  if (0 - mn != mn) {
    return 1;
  }
  return 0;
}
`
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		const want = "2147483648\n4294967295\n8589934588\n2147483649\n2147483648\n2147483648\n4294967294\n4\n255\nwrapped\n2147483647\n"
		oracle, ocode := stdoutAndExit(t, exec.Command(buildLangBinForInterp(t), "-interp", src))
		if oracle != want || ocode != 0 {
			t.Fatalf("the Go interpreter disagrees with this test's expectation: exit %d, stdout:\n%s", ocode, oracle)
		}
		out, code := stdoutAndExit(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib))
		if out != want || code != 0 {
			t.Errorf("exit %d, stdout:\n%s\nwant exit 0, stdout:\n%s", code, out, want)
		}
	})
}

// stdoutAndExit runs cmd and returns its stdout and exit status.
func stdoutAndExit(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	return string(out), 0
}
