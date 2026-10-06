package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type XattrBytesFixture struct {
	Source string
	Path   string
	Writes map[string]string
}

// MakeXattrBytesFixture uses host-seeded values, including every byte and
// malformed UTF-8 classes. The caller also verifies writes through the host.
func MakeXattrBytesFixture(t *testing.T) XattrBytesFixture {
	t.Helper()
	seed := seedXattrBytesValue
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	link := filepath.Join(dir, "link")
	missing := filepath.Join(dir, "missing")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", link); err != nil {
		t.Fatal(err)
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	seed(t, path, "user.all", string(all))
	seed(t, path, "user.unicode", "é日本\x00tail")
	seed(t, path, "user.empty", "")
	bad := []string{"\x80", "\xff", "\xc0\x80", "\xc2", "\xe0\x80\x80", "\xe2\x82", "\xed\xa0\x80", "\xf0\x80\x80\x80", "\xf0\x9f\x98", "\xf4\x90\x80\x80", "\xf5\x80\x80\x80", "\xc2A", "good\x00\xff"}
	var source strings.Builder
	source.WriteString("import \"std/string\";\nfunction main(): i32 {\n")
	step := 0
	check := func(body string) {
		step++
		fmt.Fprintf(&source, "  %s\n", strings.ReplaceAll(body, "FAIL", fmt.Sprint(step)))
	}
	for i, value := range bad {
		attr := fmt.Sprintf("user.bad%d", i)
		seed(t, path, attr, value)
		for _, getter := range []string{"getxattr", "lgetxattr"} {
			check(fmt.Sprintf("match (%s(%q, %q)) { Err(InvalidUtf8(p)) => { if (p != %q) { return FAIL; } }, _ => { return FAIL; } }", getter, path, attr, path))
			var literal strings.Builder
			literal.WriteByte('[')
			for j, b := range []byte(value) {
				if j > 0 {
					literal.WriteString(", ")
				}
				fmt.Fprintf(&literal, "%d as u8", b)
			}
			literal.WriteByte(']')
			check(fmt.Sprintf("match (%s_bytes(%q, %q)) { Ok(v) => { let want: u8[] = %s; if (v.len() != want.len()) { return FAIL; } let i: i32 = 0; while (i < v.len()) { if (v[i] != want[i]) { return FAIL; } i = i + 1; } }, Err(_) => { return FAIL; } }", getter, path, attr, literal.String()))
		}
	}
	for _, getter := range []string{"getxattr", "lgetxattr"} {
		check(fmt.Sprintf("match (%s(%q, \"user.unicode\")) { Ok(v) => { if (v != \"é日本\\x00tail\") { return FAIL; } }, Err(_) => { return FAIL; } }", getter, path))
		check(fmt.Sprintf("match (%s(%q, \"user.empty\")) { Ok(v) => { if (v.len() != 0) { return FAIL; } }, Err(_) => { return FAIL; } }", getter, path))
		check(fmt.Sprintf("match (%s_bytes(%q, \"user.empty\")) { Ok(v) => { if (v.len() != 0) { return FAIL; } }, Err(_) => { return FAIL; } }", getter, path))
		check(fmt.Sprintf("match (%s_bytes(%q, \"user.all\")) { Err(NotFound(p)) => { if (p != %q) { return FAIL; } }, _ => { return FAIL; } }", getter, missing, missing))
		check(fmt.Sprintf("match (%s_bytes(%q, \"user.absent\")) { Err(Other(p, _)) => { if (p != %q) { return FAIL; } }, _ => { return FAIL; } }", getter, path, path))
	}
	check(fmt.Sprintf("match (lgetxattr_bytes(%q, \"user.all\")) { Err(_) => {}, Ok(_) => { return FAIL; } }", link))
	source.WriteString("  let retained: u8[] = [];\n")
	check(fmt.Sprintf("match (getxattr_bytes(%q, \"user.all\")) { Ok(v) => { retained = v; }, Err(_) => { return FAIL; } }", link))
	check("if (retained.len() != 256) { return FAIL; } let i: i32 = 0; while (i < 256) { if (retained[i] != i as u8) { return FAIL; } i = i + 1; }")
	check(fmt.Sprintf("match (setxattr_bytes(%q, \"user.rawset\", retained)) { Ok(_) => {}, Err(_) => { return FAIL; } }", link))
	check(fmt.Sprintf("match (lsetxattr_bytes(%q, \"user.rawlset\", retained)) { Ok(_) => {}, Err(_) => { return FAIL; } }", path))
	check("if (retained[255] != 255 as u8 || retained[0] != 0 as u8) { return FAIL; } retained = [];")
	for _, setter := range []string{"setxattr_bytes", "lsetxattr_bytes"} {
		check(fmt.Sprintf("match (%s(%q, %q, [])) { Ok(_) => {}, Err(_) => { return FAIL; } }", setter, path, "user."+setter))
		check(fmt.Sprintf("match (%s(%q, \"user.missing\", [255 as u8])) { Err(NotFound(p)) => { if (p != %q) { return FAIL; } }, _ => { return FAIL; } }", setter, missing, missing))
	}
	check(fmt.Sprintf("match (lsetxattr_bytes(%q, \"user.linkraw\", [255 as u8])) { Ok(_) => {}, Err(_) => {} } match (getxattr_bytes(%q, \"user.linkraw\")) { Err(_) => {}, Ok(_) => { return FAIL; } }", link, path))
	source.WriteString("  return 0;\n}\n")
	return XattrBytesFixture{Source: source.String(), Path: path, Writes: map[string]string{"user.rawset": string(all), "user.rawlset": string(all), "user.setxattr_bytes": "", "user.lsetxattr_bytes": ""}}
}

func (fixture XattrBytesFixture) CheckWrites(t *testing.T) {
	t.Helper()
	for name, want := range fixture.Writes {
		if got, ok := readXattrBytesValue(t, fixture.Path, name); !ok || got != want {
			t.Errorf("host reads %s = %q (present %v), want %q", name, got, ok, want)
		}
	}
	if _, ok := readXattrBytesValue(t, fixture.Path, "user.linkraw"); ok {
		t.Error("lsetxattr_bytes followed the symlink")
	}
}
