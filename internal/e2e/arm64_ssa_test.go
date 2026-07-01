// E2E tests for the experimental SSA-direct arm64 backend
// (`-target arm64-ssa`). Builds the fern CLI from this checkout,
// compiles small Fern programs with the new target, and runs the
// resulting static AArch64 ELF under qemu-aarch64, asserting the
// process exit code (main's return value, low byte).
//
// SKIPs when qemu-aarch64 isn't on PATH so the suite stays green on
// machines without the emulator.
package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestArm64SSACliRoundtrip drives the whole parse → check → ir →
// ssa.LiftFromIR → arm64ssa.EmitAsmModule → linkNative pipeline through
// the CLI and runs each binary under qemu, exercising the SSA register
// allocator's real output on cross-function calls, recursion, control
// flow, memory, and strings.
func TestArm64SSACliRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	qemu := ""
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if qemu == "" {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-ssa e2e")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "call",
			src: `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(40, 2); }`,
			want: 42,
		},
		{
			name: "loop_and_recursion",
			src: `function fib(n: i32): i32 {
  if (n < 2) { return n; }
  return fib(n - 1) + fib(n - 2);
}
function main(): i32 {
  var i: i32 = 0;
  var s: i32 = 0;
  while (i < 10) { s = s + i; i = i + 1; }
  return s + fib(8);
}`,
			want: 66, // 45 + fib(8)=21
		},
		{
			name: "div_rem_shift",
			src: `function main(): i32 {
  var a: i32 = 47;
  var b: i32 = 5;
  return (a / b) * 10 + (a % b) + (1 << 3);
}`,
			want: 100, // 9*10 + 2 + 8
		},
		{
			name: "string_len",
			src: `function main(): i32 {
  var s: string = "Hello";
  return s.len();
}`,
			want: 5,
		},
		{
			// f64 arithmetic through a cross-function call — exercises the FP
			// sequences and the call-result width propagation (an f64 return must
			// not be masked back to i32).
			name: "float_call",
			src: `function scale(x: f64): f64 { return x * 2.0; }
function main(): i32 { return scale(3.5) as i32; }`,
			want: 7,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, c.name+".bin")
			emit := exec.Command(bin, "-target", "arm64-ssa", "-o", out, srcPath)
			var eb bytes.Buffer
			emit.Stderr = &eb
			if err := emit.Run(); err != nil {
				t.Fatalf("fern -target arm64-ssa: %v\nstderr:\n%s", err, eb.String())
			}
			run := exec.Command(qemu, out)
			err := run.Run()
			got := 0
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					got = ee.ExitCode()
				} else {
					t.Fatalf("run under qemu: %v", err)
				}
			}
			if got != c.want {
				t.Errorf("%s: exit=%d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestArm64SSACoverageGapErrors confirms a language construct outside the
// SSA renderer's subset (here a closure, which lowers to OpMakeClosure /
// OpCallIndirect) fails with a clean compile error rather than a
// miscompile — the experimental-backend contract that lets the epic widen
// coverage incrementally.
func TestArm64SSACoverageGapErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}

	srcPath := filepath.Join(dir, "closure.fern")
	src := `function main(): i32 {
  var add = (x: i32) => x + 1;
  return add(41);
}`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "closure.bin")
	emit := exec.Command(bin, "-target", "arm64-ssa", "-o", out, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	err := emit.Run()
	if err == nil {
		t.Fatalf("expected a coverage-gap error for f64 arithmetic, got success")
	}
	if !bytes.Contains(eb.Bytes(), []byte("arm64-ssa")) {
		t.Errorf("error not attributed to arm64-ssa:\n%s", eb.String())
	}
}
