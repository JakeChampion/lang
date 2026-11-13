package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const checkedAllocLengthValid = `@noinline
function length(n: i64): i32 { return __alloc_len(n); }
function main(): i32 {
  for n in [0i64, 1i64, 65535i64, 1048576i64, 2147483647i64] {
    if (length(n) as i64 != n) { return 7; }
  }
  return 0;
}
`

var checkedAllocLengthInvalid = []string{
	"-1i64", "-9223372036854775807i64 - 1i64", "2147483648i64",
	"4294967296i64", "9223372036854775807i64",
}

const checkedAllocLengthNoHeap = `@noinline
function length(n: i64): i32 { return __alloc_len(n); }
@noinline
function make(n: i32): u8[] { return __alloc_u8(n); }
function main(): i32 {
  let values = [0i64, 1i64, 65535i64, 1048576i64, 2147483647i64];
  let before = __heap_alloc_count();
  let marker = make(args().len() + 3);
  marker = marker.with(0, 7u8);
  if (marker[0] != 7u8 || __heap_alloc_count() <= before) { return 8; }
  let count = __heap_alloc_count();
  for n in values {
    if (length(n) as i64 != n) { return 9; }
  }
  if (__heap_alloc_count() != count) { return 10; }
  return 0;
}
`

func checkedAllocLengthBad(n string) string {
	return fmt.Sprintf(`@noinline
function length(n: i64): i32 { return __alloc_len(n); }
function main(): i32 { print(length(%s)); return 0; }
`, n)
}

func TestCheckedAllocationLengthInterp(t *testing.T) {
	if out, code := runInterpExitCode(t, checkedAllocLengthValid); code != 0 {
		t.Fatalf("valid lengths: exit %d, %s", code, out)
	}
	for _, n := range checkedAllocLengthInvalid {
		t.Run(n, func(t *testing.T) {
			out, code := runInterpExitCode(t, checkedAllocLengthBad(n))
			if code != 134 || !strings.Contains(out, "allocation size out of range") {
				t.Fatalf("length %s: exit %d, %s", n, code, out)
			}
		})
	}
}

func checkedAllocLengthNative(t *testing.T, arm bool) {
	t.Helper()
	run := func(t *testing.T, src string) (string, int) { return compileAndRunX86_64(t, src) }
	if arm {
		run = compileAndRunArm64
	}
	if out, code := run(t, checkedAllocLengthValid); code != 0 {
		t.Fatalf("valid lengths: exit %d, %s", code, out)
	}
	for _, n := range checkedAllocLengthInvalid {
		t.Run(n, func(t *testing.T) {
			out, code := run(t, checkedAllocLengthBad(n))
			if code != 134 || !strings.Contains(out, "allocation size out of range") {
				t.Fatalf("length %s: exit %d, %s", n, code, out)
			}
			if strings.TrimSpace(withoutAbortDiag(out)) != "" {
				t.Fatalf("length %s reached print: %q", n, out)
			}
		})
	}
}

func TestCheckedAllocationLengthX86_64(t *testing.T) {
	checkedAllocLengthNative(t, false)
}

func TestCheckedAllocationLengthArm64(t *testing.T) {
	checkedAllocLengthNative(t, true)
}

func TestCheckedAllocationLengthWasm(t *testing.T) {
	if stdout, stderr, code := runWasmArtifact(t, buildCLIComponent(t, checkedAllocLengthValid), runOpts{}); code != 0 {
		t.Fatalf("valid lengths: exit %d, stdout %q, %s", code, stdout, stderr)
	}
	for _, n := range checkedAllocLengthInvalid {
		t.Run(n, func(t *testing.T) {
			stdout, stderr, code := runWasmArtifact(t, buildCLIComponent(t, checkedAllocLengthBad(n)), runOpts{})
			if code == 0 || !strings.Contains(stderr, "allocation size out of range") || stdout != "" {
				t.Fatalf("length %s: exit %d, stdout %q, %s", n, code, stdout, stderr)
			}
		})
	}
}

func TestCheckedAllocationLengthNoHeap(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var out string
			var code int
			switch target {
			case "x86-64-linux":
				out, code = compileAndRunX86_64(t, checkedAllocLengthNoHeap)
			case "arm64-linux":
				out, code = compileAndRunArm64(t, checkedAllocLengthNoHeap)
			default:
				stdout, stderr, exit := runWasmArtifact(t, buildCLIComponent(t, checkedAllocLengthNoHeap), runOpts{})
				out, code = stdout+stderr, exit
			}
			if code != 0 {
				t.Fatalf("length check allocates: exit %d, %s", code, out)
			}
		})
	}
}

func checkedAllocLengthEntry(t *testing.T, src string) string {
	t.Helper()
	entry := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestCheckedAllocationLengthFreestanding(t *testing.T) {
	entry := checkedAllocLengthEntry(t, checkedAllocLengthValid)
	for _, target := range []string{"x86-64-freestanding", "arm64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-emit", "asm",
				"-o", filepath.Join(t.TempDir(), "core.s"), entry, e2eharness.SelfHostStdlibRoot(t))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("core length check: %v\n%s", err, out)
			}
		})
	}
}

func TestCheckedAllocationLengthCLIInterp(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	entry := checkedAllocLengthEntry(t, checkedAllocLengthValid)
	if out, err := exec.Command(cli, "-interp", entry, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
		t.Fatalf("valid lengths: %v\n%s", err, out)
	}
	for _, n := range checkedAllocLengthInvalid {
		t.Run(n, func(t *testing.T) {
			entry := checkedAllocLengthEntry(t, checkedAllocLengthBad(n))
			cmd := exec.Command(cli, "-interp", entry, e2eharness.SelfHostStdlibRoot(t))
			out, _ := cmd.CombinedOutput()
			if cmd.ProcessState.ExitCode() != 134 || !strings.Contains(string(out), "allocation size out of range") {
				t.Fatalf("length %s: exit %d, %s", n, cmd.ProcessState.ExitCode(), out)
			}
		})
	}
}
