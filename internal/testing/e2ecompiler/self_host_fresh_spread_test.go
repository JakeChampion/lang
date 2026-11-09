package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const freshSpreadOwnedPrelude = `
struct Buffer { values: i32[], tag: i32 }
function fresh(): Buffer { return Buffer { values: [7, 8], tag: 0 }; }
function (own value: Buffer) consume(): i32 { return value.values[0] + value.tag; }
`

const freshSpreadFactory = `
pub struct Buffer { values: i32[], tag: i32 }
pub function fresh(n: i32): Buffer { return Buffer { values: [n, n + 1], tag: 0 }; }
pub function (own value: Buffer) sum(): i32 { return value.values[0] + value.values[1] + value.tag; }
`

const freshSpreadOwnedProgram = `
import "./factory";
function result(n: i32): Result[factory.Buffer, string] { return Ok(factory.fresh(n)); }
function tried(): Result[i32, string] {
  let value = factory.Buffer { ...(result(11)?), tag: 5 };
  return Ok(value.sum());
}
function branch(flag: boolean): i32 {
  let value = factory.Buffer { ...(if (flag) { factory.fresh(7) } else { factory.fresh(9) }), tag: 5 };
  return value.sum();
}
function main(): i32 {
  let total = 0;
  for i in 0..2000 {
    let value = factory.Buffer { ...factory.fresh(i), tag: 5 };
    total += value.sum();
  }
  if (total != 4010000 || branch(true) != 20 || branch(false) != 24) { return 1; }
  let Ok(answer) = tried() else { return 2; };
  if (answer != 28) { return 3; }
  let nested = factory.Buffer { ...factory.Buffer { ...factory.fresh(7), tag: 3 }, tag: 5 };
  if (nested.sum() != 20) { return 4; }
  let literal = factory.Buffer { ...factory.Buffer { values: [7, 8], tag: 0 }, tag: 5 };
  if (literal.sum() != 20) { return 5; }
  let source = factory.fresh(100);
  let replaced = factory.Buffer { ...factory.fresh(7), values: source.values, tag: 5 };
  if (replaced.sum() != 206 || source.values[0] != 100 || source.values[1] != 101) { return 6; }
  print("fresh spread ownership controls passed");
  return 0;
}
`

func TestSelfHostFreshSpreadOwnedReceiver(t *testing.T) {
	dir := t.TempDir()
	for name, source := range map[string]string{"main.fern": freshSpreadOwnedProgram, "factory.fern": freshSpreadFactory} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostFile(t, target, filepath.Join(dir, "main.fern"), []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "fresh spread ownership controls passed") {
				t.Fatalf("fresh spread receiver: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}
