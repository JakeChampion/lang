package e2ecompiler

import (
	"strings"
	"testing"
)

// The self-host interpreter gives each turn of a range loop its own index,
// so a closure keeps the index of its turn.
func TestSelfHostInterpRangeIndexCaptureIsPerTurn(t *testing.T) {
	const src = `import "std/array";
import "std/i32";
function main(): i32 {
  let a: (() => i32)[] = [];
  for i in 0..3 {
    a = a.append((): i32 => i);
  }
  let b: (() => i32)[] = [];
  for i in 1..=2 {
    b = b.append((): i32 => i * 10);
  }
  let k = 0;
  let d: (() => i32)[] = [];
  for i in 0..2 {
    k = i;
    d = d.append((): i32 => k);
  }
  let all = a.concat(b).concat(d);
  print(all.map((f: () => i32): string => f().to_string()).join(","));
  return 0;
}
`
	got := strings.TrimSpace(runSelfHostInterp(t, "range_capture.fern", src))
	if want := "0,1,2,10,20,1,1"; got != want {
		t.Fatalf("self-host interp printed %q, want %q", got, want)
	}
}
