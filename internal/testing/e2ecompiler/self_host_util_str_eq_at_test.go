package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// util.str_eq_at and util.str_range_eq compare bytes in place. An offset
// inside a character, or outside the string, gives false and does not stop
// the program.
func TestSelfHostUtilStrEqAtComparesBytes(t *testing.T) {
	src := `import "./util";

function shown(b: boolean): string {
  if (b) {
    return "1";
  }
  return "0";
}

function main(): i32 {
  let s = "aé東京b";
  let out = "";
  out += shown(util.str_eq_at(s, 0, "a"));
  out += shown(util.str_eq_at(s, 1, "é"));
  out += shown(util.str_eq_at(s, 3, "東京b"));
  out += shown(util.str_eq_at(s, s.len(), ""));
  out += shown(util.str_eq_at(s, 0, s));
  out += " ";
  out += shown(util.str_eq_at(s, 2, "é"));
  out += shown(util.str_eq_at(s, 4, "京"));
  out += shown(util.str_eq_at(s, -1, "a"));
  out += shown(util.str_eq_at(s, s.len(), "b"));
  out += shown(util.str_eq_at(s, s.len() + 1, ""));
  out += shown(util.str_eq_at("a", 0, s));
  out += " ";
  out += shown(util.str_range_eq(s, 1, 3, "é"));
  out += shown(util.str_range_eq(s, 3, 9, "東京"));
  out += shown(util.str_range_eq(s, 0, 0, ""));
  out += " ";
  out += shown(util.str_range_eq(s, 1, 2, "é"));
  out += shown(util.str_range_eq(s, 1, 4, "é"));
  out += shown(util.str_range_eq(s, 2, 4, "é"));
  print(out);
  return 0;
}
`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "util.fern")
	if err := os.WriteFile(filepath.Join(dir, "eqat.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "eqat.fern", "eqat")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("eqat: %v\n%s", err, out)
	}
	if want := "11111 000000 111 000\n"; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
