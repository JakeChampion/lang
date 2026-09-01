package embed

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeTree materialises a name -> contents map under a fresh temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadKeysAreSlashRelative(t *testing.T) {
	root := writeTree(t, map[string]string{
		"index.html":     "<h1>hi</h1>",
		"html/page.html": "<p>nested</p>",
		"css/a/deep.css": "body{}",
	})
	set, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"css/a/deep.css", "html/page.html", "index.html"}
	if got := set.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	if v, ok := set.Lookup("html/page.html"); !ok || v != "<p>nested</p>" {
		t.Fatalf("Lookup(html/page.html) = %q, %v", v, ok)
	}
}

// Binary assets are the whole point of carrying an explicit byte length
// rather than relying on the .asciz terminator: a NUL mid-file must not
// truncate, and bytes >= 0x80 must survive byte-for-byte.
func TestLoadPreservesBinaryBytes(t *testing.T) {
	blob := string([]byte{0x00, 0x01, 0x7f, 0x80, 0xff, 0x00, 'e', 'n', 'd'})
	root := writeTree(t, map[string]string{"blob.bin": blob})
	set, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := set.Lookup("blob.bin")
	if !ok {
		t.Fatal("blob.bin missing")
	}
	if got != blob {
		t.Fatalf("blob round-trip: got % x, want % x", got, blob)
	}
}

func TestLoadEmptyDirIsNotAnError(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Names(); len(got) != 0 {
		t.Fatalf("Names() = %v, want none", got)
	}
	if got := set.FormatAvailable(); got != "no assets were embedded" {
		t.Fatalf("FormatAvailable() = %q", got)
	}
}

func TestLoadRejectsNonDirectory(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "x"})
	if _, err := Load(filepath.Join(root, "a.txt")); err == nil {
		t.Fatal("expected an error for a file passed as the embed root")
	}
	if _, err := Load(filepath.Join(root, "nope")); err == nil {
		t.Fatal("expected an error for a missing embed root")
	}
}

// A symlink is skipped rather than followed, so an asset tree can never
// reach outside its root or wedge the walk on a cycle.
func TestLoadSkipsSymlinks(t *testing.T) {
	root := writeTree(t, map[string]string{"real.txt": "real"})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	set, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Names(); !reflect.DeepEqual(got, []string{"real.txt"}) {
		t.Fatalf("Names() = %v, want only real.txt (symlinks must be skipped)", got)
	}
}

// The ROOT is followed, because it is the path the user typed on the command
// line and `-embed ./link-to-assets` obviously means the directory behind it.
//
// os.Stat has always followed it to decide the argument is a directory at all,
// but filepath.WalkDir lstats its own root — so before the walk resolved it,
// this combination passed the directory check and then yielded the link as one
// non-regular entry, which the skip above dropped. The result was an empty
// bundle from a directory full of files, and nothing said so: the failure only
// showed up later as `no embedded asset "x" (no assets were embedded)`.
func TestLoadFollowsASymlinkedRoot(t *testing.T) {
	real := writeTree(t, map[string]string{"a.txt": "A", "sub/b.txt": "BB"})
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	set, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Names(); !reflect.DeepEqual(got, []string{"a.txt", "sub/b.txt"}) {
		t.Fatalf("Names() = %v, want the linked directory's contents", got)
	}
	// Diagnostics echo the path as typed, not the resolved one: the user has to
	// recognise what they wrote.
	if set.Root() != link {
		t.Errorf("Root() = %q, want the path as passed (%q)", set.Root(), link)
	}
}

func TestSuggest(t *testing.T) {
	root := writeTree(t, map[string]string{
		"html/index.html": "x",
		"css/site.css":    "y",
	})
	set, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		want string
		typo string
	}{
		{"html/index.html", "html/index.htm"}, // truncated tail
		{"html/index.html", "html/indx.html"}, // mistyped middle
		{"", "totally-unrelated-xyzzy"},       // nothing close: stay quiet
	}
	for _, tc := range tests {
		if got := set.Suggest(tc.typo); got != tc.want {
			t.Errorf("Suggest(%q) = %q, want %q", tc.typo, got, tc.want)
		}
	}
}

// The nil Set is the "no -embed passed" state and is threaded through the
// whole compiler, so every accessor has to tolerate it.
func TestNilSetIsUsable(t *testing.T) {
	var s *Set
	if _, ok := s.Lookup("x"); ok {
		t.Fatal("nil Set must not resolve anything")
	}
	if s.Names() != nil || s.Root() != "" {
		t.Fatal("nil Set accessors must return zero values")
	}
	if s.Suggest("x") != "" {
		t.Fatal("nil Set must not suggest")
	}
}
