package component_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// Validate shared poll lowerings across every socket/timer combination.
// The core supplies memory and realloc for the unused canonical trampolines;
// the component validator still checks every assembled import instance.
func TestComposeSocketTimerPollCombinations(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	wat := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(wat, []byte(`(module
  (memory (export "memory") 1)
  (func (export "cabi_realloc") (param i32 i32 i32 i32) (result i32) i32.const 0)
  (func (export "run") (result i32) i32.const 0))`), 0o600); err != nil {
		t.Fatal(err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	if out, err := exec.Command("wasm-tools", "parse", wat, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("core fixture: %v\n%s", err, out)
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	for mask := 1; mask < 8; mask++ {
		for _, poll := range []bool{false, true} {
			for _, drop := range []bool{false, true} {
				req := component.ComposeRequest{Tcp: mask&1 != 0, Udp: mask&2 != 0, Timer: mask&4 != 0,
					Poll: poll, PollableDrop: drop, ExportName: "run"}
				t.Run(fmt.Sprintf("tcp=%t/udp=%t/timer=%t/poll=%t/drop=%t", req.Tcp, req.Udp, req.Timer, poll, drop), func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "composed.wasm")
					if err := os.WriteFile(path, component.Compose(core, req, "run"), 0o600); err != nil {
						t.Fatal(err)
					}
					if out, err := exec.Command("wasm-tools", "validate", path).CombinedOutput(); err != nil {
						t.Fatalf("composed imports: %v\n%s", err, out)
					}
				})
			}
		}
	}
}
