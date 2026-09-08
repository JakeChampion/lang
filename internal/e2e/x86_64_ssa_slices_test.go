package e2e

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestX86_64SSASlices(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"string-bytes", `function main(): i32 {
  var text: string = "a\0\xffz";
  var view: [u8] = text.as_bytes();
  if (view.len() != 4) { return 1; }
  if (view[0] != 97u8 || view[1] != 0u8 || view[2] != 255u8 || view[3] != 122u8) { return 2; }
  return 42;
}`, 42},
		{"empty-string", `function main(): i32 { var s: string = ""; return s.as_bytes().len(); }`, 0},
		{"strides-and-nesting", `function main(): i32 {
  var a: u8[] = [1u8, 2u8, 3u8, 4u8];
  var sa: [u8] = a[1:4];
  var b: i32[] = [10, 20, 30];
  var sb: [i32] = b[1:3];
  var c: i64[] = [100i64, 4294967496i64];
  var sc: [i64] = c[1:2];
  var d: string[] = ["ab", "cde"];
  var sd: [string] = d[1:2];
  var sub: [u8] = sa[1:3];
  if (sc[0] != 4294967496i64) { return 1; }
  if (sa[0] != 2u8 || sb[0] != 20 || sd[0] != "cde") { return 2; }
  if (sa.len() != 3 || sub.len() != 2 || sub[0] != 3u8) { return 3; }
  return 42;
}`, 42},
		{"empty-at-end", `function main(): i32 { var a: i32[] = [3, 4]; var s: [i32] = a[2:2]; return s.len(); }`, 0},
		{"repeated-views", `function main(): i32 {
  var a: i32[] = [7, 9, 11];
  var keep: [i32] = a[0:2];
  var sum: i32 = 0;
  var i: i32 = 0;
  while (i < 100) {
    var s: [i32] = a[1:3];
    sum = sum + s[0];
    i = i + 1;
  }
  if (sum != 900 || keep[0] != 7 || keep[1] != 9) { return 1; }
  return 42;
}`, 42},
		{"negative-index", `function main(): i32 { var a: i32[] = [3]; var s: [i32] = a[0:1]; return s[-1]; }`, 134},
		{"end-index", `function main(): i32 { var a: i32[] = [3]; var s: [i32] = a[0:1]; return s[1]; }`, 134},
		{"empty-index", `function main(): i32 { var a: u8[] = []; var s: [u8] = a[0:0]; return s[0] as i32; }`, 134},
		{"negative-low", `function main(): i32 { var a: i32[] = [3]; var s: [i32] = a[-1:1]; return s.len(); }`, 134},
		{"negative-high", `function main(): i32 { var a: i32[] = [3]; var s: [i32] = a[0:-1]; return s.len(); }`, 134},
		{"reversed", `function main(): i32 { var a: i32[] = [3, 4]; var s: [i32] = a[2:1]; return s.len(); }`, 134},
		{"past-end", `function main(): i32 { var a: i32[] = [3]; var s: [i32] = a[0:2]; return s.len(); }`, 134},
		{"nested-past-end", `function main(): i32 { var a: i32[] = [3, 4, 5]; var s: [i32] = a[1:2]; var v: [i32] = s[0:2]; return v.len(); }`, 134},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := mustWrite(t, dir, "slice.fern", tc.src)
			for _, release := range []bool{false, true} {
				name := "debug"
				if release {
					name = "release"
				}
				t.Run(name, func(t *testing.T) {
					bin := filepath.Join(dir, name)
					args := []string{"-target", "x86-64-linux", "-backend", "ssa", "-o", bin}
					if release {
						args = append(args, "-O")
					}
					if out, err := exec.Command(fern, append(args, src)...).CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					cmd := runX86Bin(qemu, bin)
					out, err := cmd.CombinedOutput()
					if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.want {
						t.Fatalf("run: %v, state=%v, output=%q; want exit %d", err, cmd.ProcessState, out, tc.want)
					}
					if len(out) != 0 {
						t.Fatalf("unexpected SSA output: %q", out)
					}
					// Pin the same values and trap status against the shipping
					// backend. Its bounds diagnostic is intentionally richer than
					// the current SSA helper's status-only trap.
					baseBin := bin + "-default"
					baseArgs := []string{"-target", "x86-64-linux", "-o", baseBin}
					if release {
						baseArgs = append(baseArgs, "-O")
					}
					if out, err := exec.Command(fern, append(baseArgs, src)...).CombinedOutput(); err != nil {
						t.Fatalf("compile default: %v\n%s", err, out)
					}
					base := runX86Bin(qemu, baseBin)
					baseOut, err := base.CombinedOutput()
					if base.ProcessState == nil || base.ProcessState.ExitCode() != tc.want || (tc.want != 134 && len(baseOut) != 0) {
						t.Fatalf("default run: %v, state=%v, output=%q; want exit %d", err, base.ProcessState, baseOut, tc.want)
					}
				})
			}
		})
	}
}
