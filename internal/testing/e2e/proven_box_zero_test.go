package e2e

import (
	"strings"
	"testing"
)

// A record or an enum value may still be the null word the lowering zeroes a
// slot with: json_parse_result, inlined into a loop, routes its result through
// a local that starts at zero, and the loop retains it on a path where it is
// still null. The retain of such a value keeps its null guard; reading the
// count unguarded segfaulted here.
const provenBoxZeroSrc = `import "std/json";

function main(): i32 {
  let acc: i32 = 0;
  let round: i32 = 0;
  while (round < 3) {
    match (json.json_parse("{\"a\":[1,2],\"b\":\"x\"}")) {
      Some(_) => {
        acc = acc + 1;
      },
      None => {
        acc = acc + 10;
      }
    }
    match (json.json_parse("[1,")) {
      Some(_) => {
        acc = acc + 100;
      },
      None => {
        acc = acc + 1000;
      }
    }
    round = round + 1;
  }
  return acc % 97;
}
`

func TestInterpProvenBoxZero(t *testing.T) {
	if got := runInterpExit(t, provenBoxZeroSrc); got != 93 {
		t.Errorf("got %d, want 93", got)
	}
}

func checkProvenBoxZero(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, provenBoxZeroSrc)
	// Wasm prints main's result and exits 0; the native targets exit with it.
	if !(exit == 93 && stdout == "" || exit == 0 && stdout == "93\n") {
		t.Fatalf("exit %d, stdout %q, want 93; stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
}

func TestX86_64ProvenBoxZeroSanitized(t *testing.T) {
	checkProvenBoxZero(t, runSanitizeX86_64)
}

func TestArm64ProvenBoxZeroSanitized(t *testing.T) {
	checkProvenBoxZero(t, runSanitizeArm64)
}

func TestWASMProvenBoxZeroSanitized(t *testing.T) {
	checkProvenBoxZero(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
