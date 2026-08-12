package mvs

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.1", "1.0.0", 1},
	}
	for _, c := range cases {
		a, _ := ParseVersion(c.a)
		b, _ := ParseVersion(c.b)
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s cmp %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if _, err := ParseVersion("1.2"); err == nil {
		t.Error("two-part version should error")
	}
	// A part must be plain digits. strconv.Atoi accepts these; a version
	// whose String() spells it differently can never be looked up in the
	// index it keys, and manifest.isVersion rejects them already.
	for _, s := range []string{"1.+2.3", "1.-0.3", "1..3", "1.2.x"} {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) should error", s)
		}
	}
}

func TestParseIndex(t *testing.T) {
	ix, err := ParseIndex(`# registry
[foo]
"1.0.0" = { path = "../foo-1.0.0" }
"1.2.0" = { url = "https://x/foo.tar.gz", hash = "sha256:abc" }
[bar]
"2.1.0" = { path = "bar" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ix.Latest("foo"); !ok || v.String() != "1.2.0" {
		t.Errorf("latest foo = %v %v", v, ok)
	}
	s, ok := ix.SourceFor("foo", Version{1, 2, 0})
	if !ok || s.URL != "https://x/foo.tar.gz" || s.Hash != "sha256:abc" {
		t.Errorf("foo 1.2.0 source = %+v", s)
	}
}

// MVS keeps the max of the minimums, expanding each selected version's
// own requirements to a fixpoint. Here root wants foo>=1.0.0 and
// bar>=1.0.0; foo@1.1.0 requires bar>=2.0.0, so bar is raised to 2.0.0.
func TestResolveMaxOfMins(t *testing.T) {
	ix, _ := ParseIndex(`[foo]
"1.0.0" = { path = "foo1" }
"1.1.0" = { path = "foo11" }
[bar]
"1.0.0" = { path = "bar1" }
"2.0.0" = { path = "bar2" }
`)
	deps := map[string]map[string]string{
		"foo@1.1.0": {"bar": "2.0.0"},
	}
	depsOf := func(pkg string, v Version, _ Source) (map[string]string, error) {
		return deps[pkg+"@"+v.String()], nil
	}
	sel, err := Resolve(map[string]string{"foo": "1.1.0", "bar": "1.0.0"}, nil, ix, depsOf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range sel {
		got[s.Name] = s.Version.String()
	}
	want := map[string]string{"foo": "1.1.0", "bar": "2.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolved = %v, want %v (bar should be raised to 2.0.0 by foo's requirement)", got, want)
	}
}

// A diamond: two deps demand different minimums of a shared dep; MVS
// takes the higher.
func TestResolveDiamond(t *testing.T) {
	ix, _ := ParseIndex(`[a]
"1.0.0" = { path = "a" }
[b]
"1.0.0" = { path = "b" }
[c]
"1.0.0" = { path = "c1" }
"1.5.0" = { path = "c15" }
"2.0.0" = { path = "c2" }
`)
	deps := map[string]map[string]string{
		"a@1.0.0": {"c": "1.5.0"},
		"b@1.0.0": {"c": "2.0.0"},
	}
	depsOf := func(pkg string, v Version, _ Source) (map[string]string, error) {
		return deps[pkg+"@"+v.String()], nil
	}
	sel, err := Resolve(map[string]string{"a": "1.0.0", "b": "1.0.0"}, nil, ix, depsOf)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sel {
		if s.Name == "c" && s.Version.String() != "2.0.0" {
			t.Errorf("diamond c = %s, want 2.0.0 (max of 1.5.0, 2.0.0)", s.Version)
		}
	}
}

// A demanded version absent from the index is a precise error, never a
// silent round-up.
func TestResolveMissingVersionErrors(t *testing.T) {
	ix, _ := ParseIndex("[foo]\n\"1.0.0\" = { path = \"foo\" }\n")
	depsOf := func(string, Version, Source) (map[string]string, error) { return nil, nil }
	_, err := Resolve(map[string]string{"foo": "1.2.0"}, nil, ix, depsOf)
	if err == nil {
		t.Fatal("demanding an absent version should error")
	}
}

// A top-level exclude on the demanded version rounds up to the next
// higher non-excluded indexed version ("1.9 is broken"), whether the
// demand comes from the root or a transitive dep.
func TestResolveExcludeRoundsUp(t *testing.T) {
	ix, _ := ParseIndex(`[foo]
"1.0.0" = { path = "foo1" }
[bar]
"1.9.0" = { path = "bar19" }
"1.9.1" = { path = "bar191" }
"2.0.0" = { path = "bar2" }
`)
	deps := map[string]map[string]string{
		"foo@1.0.0": {"bar": "1.9.0"},
	}
	depsOf := func(pkg string, v Version, _ Source) (map[string]string, error) {
		return deps[pkg+"@"+v.String()], nil
	}
	excludes := map[string][]string{"bar": {"1.9.0", "1.9.1"}}
	sel, err := Resolve(map[string]string{"foo": "1.0.0"}, excludes, ix, depsOf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range sel {
		got[s.Name] = s.Version.String()
	}
	if got["bar"] != "2.0.0" {
		t.Errorf("bar = %s, want 2.0.0 (1.9.0 and 1.9.1 excluded, round up past both)", got["bar"])
	}
}

// Excluding every version at or above the demand is a precise error.
func TestResolveExcludeNoHigherErrors(t *testing.T) {
	ix, _ := ParseIndex("[foo]\n\"1.0.0\" = { path = \"foo\" }\n")
	depsOf := func(string, Version, Source) (map[string]string, error) { return nil, nil }
	_, err := Resolve(map[string]string{"foo": "1.0.0"}, map[string][]string{"foo": {"1.0.0"}}, ix, depsOf)
	if err == nil {
		t.Fatal("excluding the only available version should error")
	}
}

// An exclude on a version nothing demands is a no-op — the max-of-mins
// outcome is unchanged.
func TestResolveExcludeUnrelatedNoOp(t *testing.T) {
	ix, _ := ParseIndex(`[foo]
"1.0.0" = { path = "foo1" }
"1.1.0" = { path = "foo11" }
`)
	depsOf := func(string, Version, Source) (map[string]string, error) { return nil, nil }
	sel, err := Resolve(map[string]string{"foo": "1.1.0"}, map[string][]string{"foo": {"1.0.0"}}, ix, depsOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 1 || sel[0].Version.String() != "1.1.0" {
		t.Errorf("resolved = %+v, want foo 1.1.0", sel)
	}
}

func TestLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sel := []Selected{
		{Name: "foo", Version: Version{1, 2, 0}, Source: Source{URL: "https://x/foo.tar.gz", Hash: "sha256:abc"}},
		{Name: "bar", Version: Version{2, 0, 0}, Source: Source{Path: filepath.FromSlash("/pkgs/bar")}},
	}
	if err := WriteLock(dir, sel); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["foo"].Version.String() != "1.2.0" || got["foo"].Source.Hash != "sha256:abc" {
		t.Errorf("foo round-trip wrong: %+v", got["foo"])
	}
	if got["bar"].Source.Path != filepath.FromSlash("/pkgs/bar") {
		t.Errorf("bar round-trip wrong: %+v", got["bar"])
	}
}

func TestReadLockAbsent(t *testing.T) {
	got, err := ReadLock(t.TempDir())
	if err != nil || got != nil {
		t.Errorf("absent lock should be (nil,nil), got %v %v", got, err)
	}
}
