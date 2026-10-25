package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostByteCopyNativeRuntime(t *testing.T) {
	cli := buildRuntimeCLI(t)
	module, err := os.ReadFile(langSrcAbs(t, "compiler/rtbytecopy.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, reuse := range []bool{true, false} {
		generator := filepath.Join(t.TempDir(), "generator.fern")
		src := string(module) + fmt.Sprintf("\nfunction main(): i32 { write(native_source(%t)); return 0; }\n", reuse)
		if err := os.WriteFile(generator, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		generated, err := runX86_64Bin(cli.runner, cli.x86Binary(t, generator)).Output()
		if err != nil {
			t.Fatalf("generate runtime: %v", err)
		}
		success := byteCopyRuntimeSuccess
		if !reuse {
			success = strings.Replace(success, "__heap_alloc_count() - before != 0i64", "__heap_alloc_count() - before != 1i64", 1)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(fmt.Sprintf("%s/reuse=%t", target, reuse), func(t *testing.T) {
				program := byteCopyStandalone(string(generated) + byteCopyRuntimeData + success)
				stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("bulk byte runtime: exit %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
			for name, call := range map[string]string{
				"source-bound": "__fern_bytes_append_range(a, src, 2, 2)",
				"target-bound": "__fern_bytes_write_range(a, 2, src, 0, 2)",
				"back-bound":   "__fern_bytes_append_back(a, 4, 2)",
				"back-zero":    "__fern_bytes_append_back(a, 0, 2)",
			} {
				t.Run(fmt.Sprintf("%s/reuse=%t/%s", target, reuse, name), func(t *testing.T) {
					program := byteCopyStandalone(string(generated) + byteCopyRuntimeData + "function main(): i32 { let a = packed_bytes(3); let src = bytes(3); let out = " + call + "; return out.len(); }")
					stderr, code := cli.exitOf(t, program, target)
					if code != 134 || !strings.Contains(stderr, "fern: array index out of range") {
						t.Fatalf("bulk byte bounds: exit %d, want array abort 134\n%s", code, stderr)
					}
				})
			}
		}
	}
}

func byteCopyStandalone(source string) string {
	return strings.ReplaceAll(source, "__fern_bytes_", "test_bytes_")
}

const byteCopyRuntimeData = `@noinline function bytes(n: i32): i32[] {
  let p: usize = __raw_arr_box((n + 7) / 8);
  __store_i64(p, n as i64);
  let i: i32 = 0;
  while (i < n) { __raw_store8(p + 8, i, i + 1); i = i + 1; }
  return __raw_array(p);
}
@noinline function packed_bytes(n: i32): i32[] {
  let p: usize = __raw_arr_box(4);
  __store_i64(p, n as i64);
  let i: i32 = 0;
  while (i < n) { __raw_store8(p + 8, i, i + 1); i = i + 1; }
  return __raw_array(p);
}
@noinline function check(a: i32[], want: i32, start: i32, source: i32): boolean {
  if (a.len() != want) { return false; }
  let i: i32 = 0;
  while (i < want) {
    let expected: i32 = i + 1;
    if (i >= start) { expected = i - start + source + 1; }
    if (__raw_load8(__raw_arr_ptr(a) + 8, i) != expected) { return false; }
    i = i + 1;
  }
  return true;
}
`

const byteCopyRuntimeSuccess = `function main(): i32 {
  let a = packed_bytes(3); let src = bytes(40);
  let before = __heap_alloc_count();
  let b = __fern_bytes_append_range(a, src, 1, 2);
  if (__heap_alloc_count() - before != 0i64 || !check(b, 5, 3, 1)) { return 1; }
  let old = b;
  let before2 = __heap_alloc_count();
  b = __fern_bytes_append_range(b, src, 2, 2);
  if (__heap_alloc_count() - before2 != 1i64 || !check(old, 5, 3, 1)) { return 2; }
  let c = packed_bytes(3);
  let before3 = __heap_alloc_count();
  let grown = __fern_bytes_append_range(c, src, 0, 40);
  if (__heap_alloc_count() - before3 != 1i64 || !check(grown, 43, 3, 0)) { return 3; }
  let d = packed_bytes(4); let dsrc = d;
  let before4 = __heap_alloc_count();
  let copied = __fern_bytes_write_range(d, 1, dsrc, 0, 3);
  if (__heap_alloc_count() - before4 != 1i64 || !check(copied, 4, 1, 0)) { return 4; }
  let z = __fern_bytes_write_range(b, 0 - 2147483647, src, 2147483647, 0);
  if (z.len() != 7) { return 5; }
  let unchanged = __fern_bytes_append_range(z, src, 0 - 2147483647, 0 - 1);
  if (unchanged.len() != 7) { return 6; }
  let rep = __fern_bytes_append_back(packed_bytes(3), 2, 30);
  if (rep.len() != 33) { return 7; }
  let i: i32 = 3;
  while (i < 33) { if (__raw_load8(__raw_arr_ptr(rep) + 8, i) != __raw_load8(__raw_arr_ptr(rep) + 8, i - 2)) { return 8; } i = i + 1; }
  if (__fern_bytes_append_back(rep, 0 - 1, 0).len() != 33) { return 9; }
  return 0;
}
`
