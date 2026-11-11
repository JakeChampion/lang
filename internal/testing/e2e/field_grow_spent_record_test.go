package e2e

import (
	"strings"
	"testing"
)

// Nightly fuzz seed 160218 (#12868). One arm moves the record into an array,
// which the arm then releases. After the join, the append grows the field
// that was read before the branch. The in-place grow tests the count of the
// record and writes its field, so it must not be chosen here.
const fieldGrowSpentRecordSrc = `struct S0 { f0: i32, f1: string }

function main(): i32 {
  let b: S0 = S0 { f0: 36, f1: f"jle{51}pzio" };
  let d: string = b.f1;
  let n = 0;
  if (d.len() > 100) {
    n = 1;
  } else {
    let e: S0[] = [b];
    n = e.len();
  }
  let x = d + "x";
  print(d.len() + n);
  return 0;
}
`

func checkFieldGrowSpentRecord(t *testing.T, run func(*testing.T, string) (string, string, int)) {
	t.Helper()
	stdout, stderr, exit := run(t, fieldGrowSpentRecordSrc)
	// The wasm runner prints the result of main after the program's output.
	if exit != 0 || stdout != "10\n" && stdout != "10\n0\n" {
		t.Fatalf("exit %d, stdout %q, want 10; stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64FieldGrowSpentRecordSanitized(t *testing.T) {
	checkFieldGrowSpentRecord(t, runSanitizeX86_64)
}

func TestArm64FieldGrowSpentRecordSanitized(t *testing.T) {
	checkFieldGrowSpentRecord(t, runSanitizeArm64)
}

func TestWASMFieldGrowSpentRecordSanitized(t *testing.T) {
	checkFieldGrowSpentRecord(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
