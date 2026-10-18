package e2ecompiler

import (
	"fmt"
	"testing"
)

// The allocation shapes measured on the TLS record path (#12780,
// #12781), each counted by __heap_alloc_count over 1,000 rounds and reported
// as the program's exit code: 0 when every count is what the plan owes.

// A slice of a byte array handed to a borrowed [u8] parameter keeps its view
// descriptor in the frame on the register backends (ssaunits.frame_views),
// as the whole array handed as a view costs nothing. Wasm has no data
// pointer to share, so its slice copies the bytes, one block per slice,
// which the frame releases (#12780).
const byteViewSliceProgram = `@noinline function viewlen(v: [u8]): i32 { return v.len(); }
@noinline function mk(n: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) { out = out.append((i & 255) as u8); i = i + 1; }
  return out;
}
function main(): i32 {
  let pt: u8[] = mk(100);
  let acc: i32 = 0;
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) { acc = acc + viewlen(pt[1:90]); i = i + 1; }
  if (__heap_alloc_count() - before != %di64) { return 1; }
  before = __heap_alloc_count();
  i = 0;
  while (i < 1000) { acc = acc + viewlen(pt); i = i + 1; }
  if (__heap_alloc_count() - before != 0i64) { return 2; }
  if (acc != 189000) { return 3; }
  return 0;
}
`

func TestSelfHostRecordPathAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		sliceAllocs := 0
		if target == "wasm32-wasi" {
			sliceAllocs = 1000
		}
		for _, probe := range []struct {
			name, program string
		}{
			{"byte-view-slice", fmt.Sprintf(byteViewSliceProgram, sliceAllocs)},
		} {
			t.Run(target+"/"+probe.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, probe.program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
