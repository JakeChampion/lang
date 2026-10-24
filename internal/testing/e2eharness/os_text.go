package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OSTextFixture keeps malformed host names out of the Fern source.
type OSTextFixture struct{ Source, Dir string }

func MakeOSTextFixture(t *testing.T, dirMethods bool) OSTextFixture {
	t.Helper()
	dir := t.TempDir()
	var source strings.Builder
	source.WriteString(`function invalid[T](r: Result[T, IoError], path: string): boolean {
  match (r) { Ok(_) => { return false; }, Err(e) => {
    match (e) { InvalidUtf8(p) => { return p == path; }, _ => { return false; } }
  } }
  return false;
}
function main(): i32 {
`)
	step := 0
	check := func(body string) {
		step++
		fmt.Fprintf(&source, "  %s\n", strings.ReplaceAll(body, "FAIL", fmt.Sprint(step)))
	}
	valid := filepath.Join(dir, "valid")
	if err := os.Mkdir(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"é-文件", "¢€𐐀", strings.Repeat("a", 180) + "🙂"} {
		if err := os.WriteFile(filepath.Join(valid, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []string{"read_dir", "read_dir_all", "read_dir_ino", "read_dir_ino_all"} {
		check(fmt.Sprintf(`match (%s("valid")) { Err(_) => { return FAIL; }, Ok(ns) => { if (ns.len() < 3) { return FAIL; } } }`, op))
	}
	check(`match (read_dir("valid")) { Err(_) => { return FAIL; }, Ok(ns) => { let found: i32 = 0; let ni: i32 = 0; while (ni < ns.len()) { let n: string = ns[ni]; if (n == "é-文件") { found = found + 1; } if (n == "¢€𐐀") { found = found + 1; } ni = ni + 1; } if (found != 2) { return FAIL; } } }`)
	if dirMethods {
		check(`if (hostname().len() < 0 || uname_field(-1) != "") { return FAIL; }`)
		check(`match (getcwd()) { Err(_) => { return FAIL; }, Ok(p) => { if (p.len() == 0) { return FAIL; } } }`)
		check(`match (mounts()) { Err(_) => { return FAIL; }, Ok(ms) => { let i: i32 = 0; while (i < ms.len()) { let m: MountEntry = ms[i]; if (m.source.len() < 0 || m.target.len() < 0 || m.fstype.len() < 0) { return FAIL; } i = i + 1; } } }`)
	}
	bad := []string{"\x80", "\xff", "\xc0\x80", "\xc2", "\xe0\x80\x80", "\xe2\x82", "\xed\xa0\x80", "\xf0\x80\x80\x80", "\xf0\x9f\x98", "\xf4\x90\x80\x80", "\xf5\x80\x80\x80", "\xc2A", strings.Repeat("a", 180) + "\xff"}
	for i, name := range bad {
		rel := fmt.Sprintf("bad%d", i)
		path := filepath.Join(dir, rel)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "valid-字"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "bad_"+name), nil, 0o600); err != nil {
			t.Skipf("host does not permit malformed file names: %v", err)
		}
		for _, op := range []string{"read_dir", "read_dir_all", "read_dir_ino", "read_dir_ino_all"} {
			check(fmt.Sprintf(`if (!invalid(%s(%q), %q)) { return FAIL; }`, op, rel, rel))
		}
		if dirMethods {
			check(fmt.Sprintf(`match (open_dir(%q)) { Err(_) => { return FAIL; }, Ok(d) => { let ok: boolean = invalid(d.entries(), ""); let again: boolean = invalid(d.entries(), ""); d.close(); if (!ok || !again) { return FAIL; } } }`, rel))
		}
		link := fmt.Sprintf("link%d", i)
		if err := os.Symlink(name, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf(`if (!invalid(read_link(%q), %q)) { return FAIL; }`, link, link))
	}
	if err := os.Symlink("é-文件", filepath.Join(dir, "valid-link")); err != nil {
		t.Fatal(err)
	}
	check(`match (read_link("valid-link")) { Err(_) => { return FAIL; }, Ok(s) => { if (s != "é-文件") { return FAIL; } } }`)
	check(`match (read_dir("absent")) { Err(NotFound(p)) => { if (p != "absent") { return FAIL; } }, _ => { return FAIL; } }`)
	source.WriteString("  return 0;\n}\n")
	return OSTextFixture{Source: source.String(), Dir: dir}
}

func MakeInvalidCwdFixture(t *testing.T) OSTextFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cwd_\xff")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Skipf("host does not permit malformed directory names: %v", err)
	}
	return OSTextFixture{Dir: dir, Source: `function main(): i32 {
 match (getcwd()) { Err(InvalidUtf8(p)) => { if (p == "") {return 0;} }, _ => {} }
 return 1;
}
`}
}
