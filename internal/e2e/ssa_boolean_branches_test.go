package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Keep loops and branch-selected values live across short-circuit joins.
// Division by zero must stay skipped on both && and || paths.
const ssaBooleanBranchesSource = `function main(): i32 {
  var sum: i32 = 0;
  var i: i32 = -2;
  while (i < 7) {
    if ((i >= 0 && i < 4) || i == 5) { sum = sum + i; }
    i = i + 1;
  }
  if (sum != 11) { return 1; }
  var n: i32 = -2;
  while (n <= 2) {
    var result: i32 = 0;
    if (n != 0 && 100 / n > 0) { result = result + 1; }
    if (n == 0 || 100 / n > 0) { result = result + 2; }
    if (n == 0 && result != 2) { return 2; }
    if (n > 0 && result != 3) { return 3; }
    if (n < 0 && result != 0) { return 4; }
    n = n + 1;
  }
  return 42;
}`

func testNativeSSABooleanBranches(t *testing.T, target, qemu string, run func(string, string, ...string) *exec.Cmd) {
	t.Helper()
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := mustWrite(t, dir, "boolean_branches.fern", ssaBooleanBranchesSource)
	for _, optimize := range []bool{false, true} {
		name := "debug"
		if optimize {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			bin := filepath.Join(dir, name)
			args := []string{"-target", target, "-backend", "ssa", "-o", bin}
			if optimize {
				args = append(args, "-O")
			}
			args = append(args, src)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd := run(qemu, bin)
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 42 || len(out) != 0 {
				t.Fatalf("run: %v, output=%q, state=%v; want exit 42 and no output", err, out, cmd.ProcessState)
			}
		})
	}
}

func TestArm64SSABooleanBranches(t *testing.T) {
	testNativeSSABooleanBranches(t, "arm64-linux", arm64QemuOrEmpty(t), runArm64Bin)
}

func TestX86_64SSABooleanBranches(t *testing.T) {
	testNativeSSABooleanBranches(t, "x86-64-linux", x86QemuOrEmpty(t), runX86Bin)
}

func TestWasmSSABooleanBranches(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := mustWrite(t, dir, "boolean_branches.fern", ssaBooleanBranchesSource)
	bin := filepath.Join(dir, "branches.wasm")
	if out, err := exec.Command(fern, "-O", "-target", "wasm32-wasi", "-backend", "ssa", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtime, "run", "--invoke", "main", bin).Output(); err != nil || strings.TrimSpace(string(out)) != "42" {
		t.Fatalf("wasmtime: %v, output=%q; want 42", err, out)
	}
}
