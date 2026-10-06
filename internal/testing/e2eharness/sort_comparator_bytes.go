package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteSortComparatorBytesFixture(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"byte_input", "vercmp", "ld", "gnu"} {
		body, err := os.ReadFile(RepoPath("coreutils", "lib", name+".fern"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".fern"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	source := `import "./vercmp";
import "./ld";
import "std/string";

function same(a: ld.Parse, b: ld.Parse): boolean {
  return a.end == b.end && a.erange == b.erange && a.v.kind == b.v.kind && a.v.neg == b.v.neg && a.v.hi == b.v.hi && a.v.lo == b.v.lo && a.v.e == b.v.e;
}

function main(): i32 {
  let texts: string[] = ["", " ", "-0", "+1", "1.25", "-.5", "1e-2", "0x1.fp3", "0x.p1", "1e+", "NaN", "-nan(payload)", "nan(unclosed", "INFINITY", "-inf", "1e5000", "1e-5000", "é", "3🙂"];
  let formats: ld.Format[] = [ld.binary32(), ld.binary64(), ld.x87(), ld.binary128()];
  let f: i32 = 0;
  while (f < formats.len()) {
    let i: i32 = 0;
    while (i < texts.len()) {
      let surrounded: u8[] = ("x" + texts[i] + "y").bytes();
      let held: u8[] = surrounded;
      if (!same(formats[f].strtold(texts[i]), formats[f].strtold_bytes(surrounded[1:surrounded.len()-1]))) { return 1; }
      if (!same(formats[f].strtold(texts[i]), formats[f].strtold_array(surrounded, 1, surrounded.len()-1))) { return 9; }
      if (held[0] != 120 as u8 || held[held.len()-1] != 121 as u8) { return 2; }
      i = i + 1;
    }
    let raw: u8[] = [99 as u8, 49 as u8, 46 as u8, 50 as u8, 255 as u8, 100 as u8];
    if (!same(formats[f].strtold("1.2"), formats[f].strtold_bytes(raw[1:5]))) { return 3; }
    if (formats[f].strtold_bytes(raw[4:5]).end != 0) { return 4; }
    f = f + 1;
  }
  let versions: string[] = ["", ".", "..", ".a", "a", "a0", "a00", "a1", "a2", "a10", "a1~", "a1.tar.gz", "a1.tar.xz", "aB", "aa", "a b", "é", "🙂"];
  let i: i32 = 0;
  while (i < versions.len()) {
    let j: i32 = 0;
    while (j < versions.len()) {
      let a: u8[] = ("x" + versions[i] + "y").bytes();
      let b: u8[] = ("z" + versions[j] + "w").bytes();
      let expected: i32 = vercmp.compare(versions[i], versions[j]);
      if (vercmp.compare_bytes(a[1:a.len()-1], 0, a.len()-2, b[1:b.len()-1], 0, b.len()-2) != expected) { return 5; }
      if (vercmp.compare_bytes(a[:], 1, a.len()-1, b[:], 1, b.len()-1) != expected) { return 6; }
      if (vercmp.compare_arrays(a, 1, a.len()-1, b, 1, b.len()-1) != expected) { return 8; }
      j = j + 1;
    }
    i = i + 1;
  }
  let a: u8[] = [97 as u8, 255 as u8];
  let b: u8[] = [97 as u8, 128 as u8];
  if (vercmp.compare_bytes(a, 0, 2, b, 0, 2) <= 0) { return 7; }
  if (vercmp.compare_bytes(a, 0, 1, b, 0, 1) != 0) { return 8; }
  return 0;
}
`
	path := filepath.Join(dir, "sort-comparator-bytes.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
