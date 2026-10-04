package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A pair-form match payload the arm RETURNS is released (#11479).
//
// `return v` on a payload binding takes the Return lowering's transfer retain,
// so the caller gets a count of its own, but the count the callee handed over
// in the (tag, payload) pair was never released: reclaimablePairFormPayload
// refused any arm that returned its binding. One payload leaked per match.
//
// Option[string] is pair-form only on x86-64, where a string is one word, so
// the string shape leaked only there; on arm64 and wasm it is a heap box whose
// deep drop already balanced it. Option[i32[]] is pair-form on every target,
// so the array shape is the same bug on all three.
//
// Built by the Go compiler: the self-host lowers this shape on its own and
// never leaked it.
func TestReturnedPairFormMatchPayloadIsReleased(t *testing.T) {
	shapes := []struct{ name, src string }{
		{"string_payload", `
import "std/i32";

function find(values: string[], name: string): Option[string] {
  if (values[0] == name) {
    return Some(values[1]);
  }
  return None;
}

function lookup(values: string[]): string {
  match (find(values, "x-echo")) {
    Some(v) => { return v; },
    None => { return "none"; },
  }
}

function main(): i32 {
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < 20) {
    let values: string[] = ["x-echo", "value-number-" + i.to_string()];
    total = total + lookup(values).len();
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (total != 290) { return 1; }
  return 0;
}`},
		{"array_payload", `
function find(values: i32[][], k: i32): Option[i32[]] {
  if (values[0][0] == k) {
    return Some(values[1]);
  }
  return None;
}

function lookup(values: i32[][]): i32[] {
  match (find(values, 7)) {
    Some(v) => { return v; },
    None => { return [0]; },
  }
}

function main(): i32 {
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < 20) {
    let values: i32[][] = [[7], [i, i, i, i, i, i, i, i, i, i, i, i, i, i]];
    total = total + lookup(values).len();
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (total != 280) { return 1; }
  return 0;
}`},
	}

	fern := buildFernCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var run func(string) *exec.Cmd
			if target == "wasm32-wasi" {
				wasmtime := e2eharness.Wasmtime(t)
				run = func(bin string) *exec.Cmd { return exec.Command(wasmtime, bin) }
			} else {
				run = nativeServerRunner(t, target)
			}
			for _, shape := range shapes {
				t.Run(shape.name, func(t *testing.T) {
					dir := t.TempDir()
					src, bin := filepath.Join(dir, "prog.fern"), filepath.Join(dir, "prog")
					if err := os.WriteFile(src, []byte(shape.src), 0o644); err != nil {
						t.Fatal(err)
					}
					compile := exec.Command(fern, "-target", target, "-o", bin, src)
					compile.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("build: %v\n%s", err, out)
					}
					cmd := run(bin)
					cmd.Env = e2eharness.ChildEnv()
					var stderr strings.Builder
					cmd.Stderr = &stderr
					if err := cmd.Run(); err != nil {
						t.Fatalf("run: %v — exit 99 is a non-zero __rc_underflow_count(), 1 a wrong total\n%s", err, stderr.String())
					}
					allocs, frees, live := leakSummaryIn(t, stderr.String())
					if allocs == 0 {
						t.Fatalf("no allocations — the loop is not running")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — "+
							"the payload a returning arm hands back leaks once per match (#11479)",
							allocs, frees, live)
					}
				})
			}
		})
	}
}
