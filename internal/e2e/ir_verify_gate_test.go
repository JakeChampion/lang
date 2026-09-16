package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestVerifyGateRunsOnNativeCompile pins that FERN_IR_VERIFY=1 reaches every
// native backend's emit path (#8798): the compile succeeds on a sound
// program AND the gate's coverage line is on stderr, which is what separates
// "wired and quiet" from "never called". ir.VerifyOrRefuse's own tests cover
// the refusal; no real lowering produces malformed IR to refuse here.
func TestVerifyGateRunsOnNativeCompile(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, target+".out")
			cmd := exec.Command(bin, "-target", target, "-o", out, src)
			cmd.Env = e2eharness.ChildEnv("FERN_IR_VERIFY=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("compile under FERN_IR_VERIFY=1 failed: %v\n%s", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), "FERN_IR_VERIFY: ") {
				t.Errorf("the gate did not run on the %s path: no coverage line on stderr\n%s", target, stderr.String())
			}
		})
	}
}

// The SSA backends are a second emit path per native target, and the gate has
// to reach them by the same argument: a backend that consumes malformed IR
// without being told is the failure #8798 exists to stop. Naming the backend
// is what makes this a separate test — on x86-64-linux the default is still
// the stack machine, so the loop above never enters the SSA path at all, and
// on arm64-linux it did not enter it either until the SSA path became the
// default.
//
// The coverage line must MATCH the stack machine's for the same program. A
// gate that runs over a smaller set reports fewer functions and still looks
// wired, so the count is the assertion rather than the line's presence.
func TestVerifyGateRunsOnSSACompile(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	coverage := func(t *testing.T, target, backend string) string {
		t.Helper()
		out := filepath.Join(dir, target+"-"+backend+".out")
		cmd := exec.Command(bin, "-target", target, "-backend", backend, "-o", out, src)
		cmd.Env = e2eharness.ChildEnv("FERN_IR_VERIFY=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("compile %s -backend %s under FERN_IR_VERIFY=1 failed: %v\n%s", target, backend, err, stderr.String())
		}
		for _, line := range strings.Split(stderr.String(), "\n") {
			if strings.HasPrefix(line, "FERN_IR_VERIFY: ") {
				return line
			}
		}
		t.Fatalf("the gate did not run on the %s -backend %s path: no coverage line on stderr\n%s", target, backend, stderr.String())
		return ""
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			ssa, flat := coverage(t, target, "ssa"), coverage(t, target, "flat")
			if ssa != flat {
				t.Errorf("the gate saw a different program on the two backends:\n  ssa:  %s\n  flat: %s", ssa, flat)
			}
		})
	}
}
