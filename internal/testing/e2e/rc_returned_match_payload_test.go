package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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

	checkCensusOnEveryTarget(t, shapes,
		"the payload a returning arm hands back leaks once per match (#11479)")
}

func checkCensusOnEveryTarget(t *testing.T, shapes []struct{ name, src string }, leakWhy string) {
	t.Helper()
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
						t.Fatalf("run: %v — exit 99 is a non-zero __rc_underflow_count(), 1 a wrong answer\n%s", err, stderr.String())
					}
					allocs, frees, live := leakSummaryIn(t, stderr.String())
					if allocs == 0 {
						t.Fatalf("no allocations — the loop is not running")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — %s",
							allocs, frees, live, leakWhy)
					}
				})
			}
		})
	}
}
