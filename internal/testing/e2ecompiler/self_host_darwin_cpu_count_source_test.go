package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostDarwinCPUCountSource(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	driver := filepath.Join(dir, "cpu_source.fern")
	if err := os.WriteFile(driver, []byte("import \"./asmcore\"; function main(): i32 { print(asmcore.rt_src_cpu_count(\"arm64-darwin\")); return 0; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "cpu_source.fern", "cpu_source")
	generated, err := runX86_64Bin(runner, bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(generated), "__fern_cpu_count", "probe_cpu")
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
  let p: usize = mib as usize;
  if (nr != 202i64 || n != 2i64 || value != 0i64 || len != 0i64) { return -22i64; }
  if (__load_i32(p) != 6 || __load_i32(p + 4) != 25 || __load_i64(size as usize) != 4i64) { return -22i64; }
  __store_i64(p + 24, 123i64);
  __store_i32(out as usize, %d);
  return %di64;
}
function main(): i32 {
  let before = __heap_alloc_count();
  if (probe_cpu() != %d) { return 1; }
  if (__load_i64(__raw_scratch(32) + 24) != 123i64) { return 2; }
  if (__heap_alloc_count() != before) { return 3; }
  return 0;
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
