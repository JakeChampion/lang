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

// MakeOSBytesFixture checks byte readers alongside the validating text readers.
// WASI components expose host names through WIT strings, so malformed byte
// preservation is a native filesystem property; valid byte names work on WASI.
func MakeOSBytesFixture(t *testing.T, handles bool, malformed bool) OSTextFixture {
	t.Helper()
	f := MakeOSTextFixture(t, handles)
	end := strings.LastIndex(f.Source, "return 0;")
	if end < 0 {
		t.Fatal("OS text fixture has no success return")
	}
	var extra strings.Builder
	extra.WriteString(`match (read_link_bytes("valid-link")) { Ok(b) => { if (string_from_bytes_unchecked(b) != "é-文件") { return 200; } }, Err(_) => { return 201; } }
match (read_dir_bytes("valid")) { Ok(names) => { if (names.len() != 3) { return 202; } let found: boolean = false; for n in names { if (string_from_bytes_unchecked(n) == "é-文件") { found = true; } } if (!found) { return 203; } }, Err(_) => { return 204; } }
match (read_link_bytes("absent")) { Err(NotFound(_)) => {}, _ => { return 205; } }
match (read_dir_bytes("absent")) { Err(NotFound(_)) => {}, _ => { return 206; } }
`)
	if malformed {
		bad := []string{"\x80", "\xff", "\xc0\x80", "\xc2", "\xe0\x80\x80", "\xe2\x82", "\xed\xa0\x80", "\xf0\x80\x80\x80", "\xf0\x9f\x98", "\xf4\x90\x80\x80", "\xf5\x80\x80\x80", "\xc2A", strings.Repeat("a", 180) + "\xff"}
		bytesLiteral := func(s string) string {
			parts := make([]string, len(s))
			for i, b := range []byte(s) {
				parts[i] = fmt.Sprintf("%du8", b)
			}
			return "[" + strings.Join(parts, ", ") + "]"
		}
		for i, name := range bad {
			fmt.Fprintf(&extra, `match (read_link_bytes("link%d")) { Ok(b) => { if (!same_bytes(b, %s)) { return 210; } }, Err(_) => { return 211; } }
match (read_dir_bytes("bad%d")) { Ok(names) => { let found: boolean = false; for n in names { if (same_bytes(n, %s)) { found = true; } } if (names.len() != 2 || !found) { return 212; } }, Err(_) => { return 213; } }
`, i, bytesLiteral(name), i, bytesLiteral("bad_"+name))
			if handles {
				fmt.Fprintf(&extra, `match (open_dir("bad%d")) { Ok(d) => {
match (d.entries()) { Err(InvalidUtf8(_)) => {}, _ => { return 214; } }
let i: i32 = 0; while (i < 2) {
match (d.entries_bytes()) { Ok(names) => { let found: boolean = false; for n in names { if (same_bytes(n, %s)) { found = true; } } if (names.len() != 2 || !found) { return 215; } }, Err(_) => { return 216; } }
i = i + 1; } d.close(); }, Err(_) => { return 217; } }
`, i, bytesLiteral("bad_"+name))
			}
		}
	}
	f.Source = `function same_bytes(a: u8[], b: u8[]): boolean {
 if (a.len() != b.len()) { return false; } let i: i32 = 0;
 while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; } return true;
}
` + f.Source[:end] + extra.String() + f.Source[end:]
	return f
}
