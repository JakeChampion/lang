package mvs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var badHashes = map[string]string{
	"traversal":     "sha256:../../../../etc/fern-pkgs/evil",
	"absolute":      "sha256:/tmp/evil",
	"non-hex":       "sha256:not-a-hash",
	"short":         "sha256:abc",
	"long":          "sha256:" + strings.Repeat("a", 65),
	"uppercase":     "sha256:" + strings.Repeat("A", 64),
	"other-scheme":  "blake3:" + strings.Repeat("a", 64),
	"missing-colon": strings.Repeat("a", 64),
}

func TestReadLockRejectsMalformedHash(t *testing.T) {
	for name, hash := range badHashes {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			lock := "[[package]]\nname = \"evil\"\nversion = \"1.0.0\"\n" +
				"url = \"https://example.invalid/x.tar.gz\"\nhash = \"" + hash + "\"\n"
			if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte(lock), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := ReadLock(dir)
			if err == nil {
				t.Fatalf("hash %q parsed, giving source %+v", hash, got["evil"].Source)
			}
			if !strings.Contains(err.Error(), "hash") {
				t.Errorf("error should name the field, got %v", err)
			}
		})
	}
}

func TestParseIndexRejectsMalformedHash(t *testing.T) {
	for name, hash := range badHashes {
		t.Run(name, func(t *testing.T) {
			src := "[evil]\n\"1.0.0\" = { url = \"https://example.invalid/x.tar.gz\", hash = \"" + hash + "\" }\n"
			ix, err := ParseIndex(src)
			if err == nil {
				s, _ := ix.SourceFor("evil", Version{1, 0, 0})
				t.Fatalf("hash %q parsed, giving source %+v", hash, s)
			}
			if !strings.Contains(err.Error(), "hash") {
				t.Errorf("error should name the field, got %v", err)
			}
		})
	}
}

func TestReadLockMalformedHashMessage(t *testing.T) {
	dir := t.TempDir()
	lock := "[[package]]\nname = \"evil\"\nversion = \"1.0.0\"\nurl = \"https://example.invalid/x.tar.gz\"\nhash = \"sha256:../../outside/evil\"\n"
	if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadLock(dir)
	want := "fern.lock line 5: hash must be `sha256:` + 64 lowercase hex digits of the archive bytes, got \"sha256:../../outside/evil\""
	if err == nil || err.Error() != want {
		t.Errorf("ReadLock error = %v, want %s", err, want)
	}
}
