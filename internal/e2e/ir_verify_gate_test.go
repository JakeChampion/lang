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
