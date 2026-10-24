package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashSelfHostSourcesIgnoresStrayFiles(t *testing.T) {
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	const libV1 = "function helper(): i32 { return 1; }\n"
	const entry = "import \"./lib\";\nfunction main(): i32 { return helper(); }\n"

	dirA := t.TempDir()
	write(dirA, "lib.fern", libV1)
	write(dirA, "entry.fern", entry)
	keyA := hashSelfHostSources(t, dirA, "entry.fern")

	dirB := t.TempDir()
	write(dirB, "lib.fern", libV1)
	write(dirB, "entry.fern", entry)
	write(dirB, "stray.fern", "import \"./lib\";\nfunction other(): i32 { return 99; }\n")
	keyB := hashSelfHostSources(t, dirB, "entry.fern")
	if keyA != keyB {
		t.Errorf("stray .fern changed the key: %s != %s (closure hashing must ignore non-imported files)", keyA, keyB)
	}

	dirC := t.TempDir()
	write(dirC, "lib.fern", "function helper(): i32 { return 2; }\n")
	write(dirC, "entry.fern", entry)
	keyC := hashSelfHostSources(t, dirC, "entry.fern")
	if keyA == keyC {
		t.Errorf("a change to an imported file did NOT change the key (%s) — would serve stale asm", keyA)
	}

	write(dirA, "entry2.fern", entry)
	if k2 := hashSelfHostSources(t, dirA, "entry2.fern"); k2 == keyA {
		t.Errorf("distinct entry names collided to one key (%s)", keyA)
	}
}

func TestSelfHostImportClosureFollowsTransitively(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("a.fern", "import \"std/io\";\nimport \"./b\";\nfunction main(): i32 { return 0; }\n")
	write("b.fern", "import \"./c\";\nfunction bf(): i32 { return 0; }\n")
	write("c.fern", "function cf(): i32 { return 0; }\n")
	write("unused.fern", "function uf(): i32 { return 0; }\n")

	got := map[string]bool{}
	for _, p := range selfHostImportClosure(t, dir, "a.fern") {
		got[filepath.Base(p)] = true
	}
	for _, want := range []string{"a.fern", "b.fern", "c.fern"} {
		if !got[want] {
			t.Errorf("closure missing transitively-imported %s", want)
		}
	}
	if got["unused.fern"] {
		t.Errorf("closure included unused.fern (not imported)")
	}
	if got["io.fern"] {
		t.Errorf("closure resolved std/io to a local file unexpectedly")
	}
}
