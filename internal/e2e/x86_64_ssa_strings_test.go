package e2e

import "testing"

// A loop that builds and drops a fresh string every iteration holds the heap
// flat: __fern_str_dec hands each block back and the next iteration's
// __str_concat takes it. The program reads the cursor through
// __heap_bump_bytes and exits with the growth in KiB, so the flat backend's
// answer (whose freelist does the same) is what the SSA build must match
// too, but the gate is the number: 10,000 strings of up to a few dozen bytes
// would move a leaking cursor by hundreds of KiB.
func TestX86_64SSAStringChurnHoldsTheHeapFlat(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)
	src := `import "std/i32";
function label(i: i32): string {
  var s: string = "item-";
  s = s + i.to_string();
  s = s + "/" + (i * 7).to_string();
  return s;
}
function main(): i32 {
  var warm: string = label(0);
  var b0: i64 = __heap_bump_bytes();
  var i: i32 = 1;
  var total: i32 = 0;
  while (i < 10000) {
    var s: string = label(i);
    total = total + s.len();
    i = i + 1;
  }
  var grown: i64 = __heap_bump_bytes() - b0;
  if (total < 100000) { return 101; }
  return (grown / 1024i64) as i32;
}
`
	ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, src, true)
	if ssaCode >= 8 {
		t.Errorf("the SSA build's heap grew %d KiB over 10,000 string rounds (101 = the length check failed):\n%s%s", ssaCode, ssaOut, ssaErr)
	}
	flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, src, false)
	if flatCode != ssaCode {
		t.Errorf("heap growth in KiB: ssa=%d flat=%d\n%s%s%s%s", ssaCode, flatCode, ssaOut, ssaErr, flatOut, flatErr)
	}
}
