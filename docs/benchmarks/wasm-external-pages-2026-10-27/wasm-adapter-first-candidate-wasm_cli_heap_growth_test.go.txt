package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func TestWASMCLIComponentHeapGrowth(t *testing.T) {
	for _, size := range []int{32, 100000} {
		t.Run(fmt.Sprintf("elements%d", size), func(t *testing.T) {
			source := fmt.Sprintf(`import "std/i32";
function main(): i32 {
  print("before growth");
  let data: i32[] = [];
  let i: i32 = 0;
  while (i < %d) {
    data = data.append(i);
    i = i + 1;
  }
  i = 0;
  while (i < data.len()) {
    if (data[i] != i) { return 1; }
    i = i + 1;
  }
  print("after growth " + data[data.len() - 1].to_string());
  return 0;
}`, size)
			out, diagnostic, code := runWasmArtifact(t, buildLeakCheckCLIComponent(t, source, true), runOpts{})
			if code != 0 || strings.Contains(diagnostic, "fern-sanitizer:") {
				t.Fatalf("exit=%d\nstdout:\n%s\nstderr:\n%s", code, out, diagnostic)
			}
			if want := fmt.Sprintf("before growth\nafter growth %d\n", size-1); out != want {
				t.Fatalf("stdout=%q, want %q", out, want)
			}
			allocs, frees, live := parseLeakCheckLine(t, diagnostic)
			if allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
			t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
		})
	}
}
