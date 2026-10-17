package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the actual generated XNU bodies with a mock kernel on both native
// ISAs. Darwin's sysinfo gate separately checks their real sysctl emission.
func TestSelfHostDarwinSysinfoSource(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	const driver = `import "./asmcore";
function main(): i32 {
  print(asmcore.rt_src_hostname("arm64-darwin"));
  print(asmcore.rt_src_uname_field("arm64-darwin"));
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "sysinfo_source.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "sysinfo_source.fern", "sysinfo_source")
	generated, err := runX86_64Bin(runner, bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReplacer(
		"__fern_hostname", "probe_hostname",
		"__fern_uname_field", "probe_uname",
		"__syscall6", "fake_sysctl",
		"__raw_scratch(400)", "fixture_scratch()",
	).Replace(string(generated))
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name, call    string
		domain, field int
		length, rc    int
		calls         int
	}{
		{"hostname", "probe_hostname()", 1, 10, 9, 0, 1},
		{"system", "probe_uname(0)", 1, 1, 9, 0, 1},
		{"node", "probe_uname(1)", 1, 10, 9, 0, 1},
		{"release", "probe_uname(2)", 1, 2, 9, 0, 1},
		{"version", "probe_uname(3)", 1, 4, 9, 0, 1},
		{"machine", "probe_uname(4)", 6, 1, 9, 0, 1},
		{"refused-hostname", "probe_hostname()", 1, 10, 0, -5, 1},
		{"refused-field", "probe_uname(3)", 1, 4, 0, -5, 1},
		{"negative-index", "probe_uname(-1)", 0, 0, 0, 0, 0},
		{"past-last-index", "probe_uname(5)", 0, 0, 0, 0, 0},
		{"full-buffer", "probe_hostname()", 1, 10, 384, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := body + fmt.Sprintf(`
function fixture_scratch(): usize {
  return __load_i64(__raw_scratch(32)) as usize;
}
function fake_sysctl(nr: i64, mib: i64, count: i64, out: i64, size: i64, value: i64, len: i64): i64 {
  let p: usize = fixture_scratch();
  let counter: usize = __raw_scratch(32) + 8;
  __store_i64(counter, __load_i64(counter) + 1i64);
  if (nr != 202i64 || count != 2i64 || value != 0i64 || len != 0i64) { return -22i64; }
  if (mib != p as i64 || out != (p + 16) as i64 || size != (p + 8) as i64) { return -22i64; }
  if (__load_i32(p) != %d || __load_i32(p + 4) != %d || __load_i64(p + 8) != 384i64) { return -22i64; }
  let i: i32 = 0;
  while (i < %d) { __raw_store8(out as usize, i, 120); i = i + 1; }
  if (%d < 384) { __raw_store8(out as usize, %d, 0); }
  if (%d == 9) {
    __raw_store8(out as usize, 0, 110); __raw_store8(out as usize, 1, 111);
    __raw_store8(out as usize, 2, 100); __raw_store8(out as usize, 3, 101);
    __raw_store8(out as usize, 4, 45); __raw_store8(out as usize, 5, 240);
    __raw_store8(out as usize, 6, 159); __raw_store8(out as usize, 7, 153);
    __raw_store8(out as usize, 8, 130);
  }
  return %di64;
}
function main(): i32 {
  let storage: usize = __raw_alloc(408);
  __store_i64(__raw_scratch(32), storage as i64);
  __store_i64(__raw_scratch(32) + 8, 0i64);
  __store_i64(storage + 400, 1234567i64);
  let before = __heap_alloc_count();
  let got: string = %s;
  if (__heap_alloc_count() != before + 1) { return 1; }
  if (got.len() != %d) { return 2; }
  if (got.len() == 9 && got != "node-🙂") { return 3; }
  if (got.len() == 384) {
    let j: i32 = 0;
    while (j < got.len()) { if (got[j] != 120) { return 4; } j = j + 1; }
  }
  if (__load_i64(storage + 400) != 1234567i64) { return 5; }
  if (__load_i64(__raw_scratch(32) + 8) != %di64) { return 6; }
  let storage_owner: string = __raw_string(storage, 408);
  return 0;
}
`, tc.domain, tc.field, tc.length, tc.length, tc.length, tc.length, tc.rc, tc.call, tc.length, tc.calls)
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				t.Run(target, func(t *testing.T) {
					out, code := cli.exitOf(t, source, target, "FERN_SANITIZE=1")
					if code != 0 {
						t.Fatalf("exit %d:\n%s", code, out)
					}
				})
			}
		})
	}
}
