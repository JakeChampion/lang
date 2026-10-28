package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func TestSelfHostDarwinCPUCountSource(t *testing.T) {
	generated := internalRuntimeSource(t, "arm64-darwin", "__fern_cpu_count", 1, true)
	body := strings.ReplaceAll(generated, "__fern_cpu_count", "probe_cpu")
	body = strings.ReplaceAll(body, "__syscall6", "fake_sysctl")
	cli := buildRuntimeCLI(t)
	for _, tc := range []struct {
		name      string
		count, rc int
		want      int
	}{
		{"available", 8, 0, 8},
		{"zero", 0, 0, 0},
		{"refused", 777, -5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := body + fmt.Sprintf(`
function fake_sysctl(nr: i64, mib: i64, n: i64, out: i64, size: i64, value: i64, len: i64): i64 {
  unsafe {
    let p: usize = mib as usize;
    if (nr != 202i64 || n != 2i64 || value != 0i64 || len != 0i64) { return -22i64; }
    if (__load_i32(p) != 6 || __load_i32(p + 4) != 25 || __load_i64(size as usize) != 4i64) { return -22i64; }
    __store_i64(p + 24, 123i64);
    __store_i32(out as usize, %d);
    return %di64;
  }
}
function main(): i32 {
  unsafe {
    let before = __heap_alloc_count();
    if (probe_cpu() != %d) { return 1; }
    if (__load_i64(__raw_scratch(32) + 24) != 123i64) { return 2; }
    if (__heap_alloc_count() != before) { return 3; }
    return 0;
  }
}
`, tc.count, tc.rc, tc.want)
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				t.Run(target, func(t *testing.T) {
					out, code := cli.exitOf(t, source, target, "FERN_SANITIZE=1")
					if code != 0 {
						t.Fatalf("exit %d, want 0:\n%s", code, out)
					}
				})
			}
		})
	}
}
