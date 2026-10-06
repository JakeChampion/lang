package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteFnmatchBytesFixture exercises raw pattern bytes and borrowed subviews.
func WriteFnmatchBytesFixture(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	lib, err := os.ReadFile(RepoPath("coreutils", "lib", "fnmatch.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fnmatch.fern"), lib, 0o644); err != nil {
		t.Fatal(err)
	}
	source := `import "./fnmatch";
import "std/string";
function main(): i32 {
  let i: i32 = 0;
  while (i < 256) {
    let pat: u8[] = [99 as u8, 92 as u8, i as u8, 100 as u8];
    let text: u8[] = [98 as u8, i as u8, 97 as u8];
    let held: u8[] = text;
    if (!fnmatch.fnmatch_bytes(pat[1:3], text[1:2])) { return 1; }
    if (!fnmatch.fnmatch_bytes([63 as u8], text[1:2])) { return 2; }
    if (!fnmatch.fnmatch_bytes([42 as u8], text)) { return 3; }
    let range: u8[] = [91 as u8, 128 as u8, 45 as u8, 255 as u8, 93 as u8];
    if (fnmatch.fnmatch_bytes(range, text[1:2]) != (i >= 128)) { return 4; }
    if (held[0] != 98 as u8 || held[1] != i as u8 || held[2] != 97 as u8) { return 5; }
    if (pat[0] != 99 as u8 || pat[3] != 100 as u8) { return 6; }
    i = i + 1;
  }
  let pats: string[] = ["", "?", "*", "a*b?", "[[:digit:]]", "[[:digit:]]", "[![:digit:]]", "[[:digit:]", "[[.a.]-c]", "[[=a=]]", "[[:bogus:]]", "[![:bogus:]]", "abc\\", "[]]", "[", "?", "???", "a/*"];
  let texts: string[] = ["", "", "", "axxxby", "7", "x", "x", "[d", "b", "a", "b", "b", "abc", "]", "[", "€", "€", "a/.x/y"];
  let collates: boolean = target_os() != "darwin";
  let wants: boolean[] = [true, false, true, true, true, false, true, true, collates, collates, false, false, false, true, true, false, true, true];
  i = 0;
  while (i < pats.len()) {
    if (fnmatch.fnmatch(pats[i], texts[i]) != wants[i]) { return 10 + i; }
    if (fnmatch.fnmatch_bytes(pats[i].as_bytes(), texts[i].as_bytes()) != wants[i]) { return 40 + i; }
    if (fnmatch.fnmatch_bytes_text(pats[i].as_bytes(), texts[i]) != wants[i]) { return 80 + i; }
    let stored: fnmatch.BytePattern = fnmatch.BytePattern { data: ("x" + pats[i] + "y").bytes(), lo: 1, hi: 1 + pats[i].len() };
    if (stored.matches(texts[i]) != wants[i]) { return 140 + i; }
    i = i + 1;
  }
  // A malformed UTF-8 class name is invalid even in a negated bracket.
  let bad: u8[] = [91 as u8, 33 as u8, 91 as u8, 58 as u8, 255 as u8, 58 as u8, 93 as u8, 93 as u8];
  if (fnmatch.fnmatch_bytes(bad, [97 as u8])) { return 70; }
  if (fnmatch.fnmatch_bytes_text(bad, "a")) { return 72; }
  let stored_bad: fnmatch.BytePattern = fnmatch.BytePattern { data: bad, lo: 0, hi: bad.len() };
  bad = [];
  if (stored_bad.matches("a")) { return 73; }
  let symbol: u8[] = [91 as u8, 91 as u8, 46 as u8, 255 as u8, 46 as u8, 93 as u8, 93 as u8];
  if (fnmatch.fnmatch_bytes(symbol, [255 as u8]) != collates) { return 71; }
  // GNU's Darwin fallback treats these as an ordinary bracket followed
  // by literal bytes. Pin both matching and nonmatching interpretations.
  let fallback_pats: string[] = ["[[.a.]]", "[[=a=]]", "[[.a.]-c]", "[[=a=]b]", "[[.ab.]]"];
  let fallback_texts: string[] = ["a]", "=]", "a-c]", "ab]", "b]"];
  i = 0;
  while (i < fallback_pats.len()) {
    if (fnmatch.fnmatch(fallback_pats[i], fallback_texts[i]) == collates) { return 100 + i; }
    if (fnmatch.fnmatch_bytes(fallback_pats[i].as_bytes(), fallback_texts[i].as_bytes()) == collates) { return 110 + i; }
    if (fnmatch.fnmatch_bytes_text(fallback_pats[i].as_bytes(), fallback_texts[i]) == collates) { return 120 + i; }
    i = i + 1;
  }
  return 0;
}
`
	path := filepath.Join(dir, "fnmatch-bytes.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
