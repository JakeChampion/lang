//go:build linux

package sourcelint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLibcCtypeGeneratorWritesGlibcModule builds scripts/gen-libc-ctype.c
// with the C compiler of the host and runs it in C.UTF-8. On glibc it must
// write coreutils/lib/glibc_ctype.fern byte for byte. The macOS lane makes
// the same check for coreutils/lib/darwin_ctype.fern.
func TestLibcCtypeGeneratorWritesGlibcModule(t *testing.T) {
	root := mustRepoRoot(t)
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Fatalf("no C compiler for scripts/gen-libc-ctype.c: %v", err)
	}
	gen := filepath.Join(t.TempDir(), "gen-libc-ctype")
	if out, err := exec.Command(cc, "-O2", "-Wall", "-Wextra", "-Werror", "-o", gen, filepath.Join(root, "scripts", "gen-libc-ctype.c")).CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}
	cmd := exec.Command(gen)
	cmd.Env = ciEnv("LC_ALL=C.UTF-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("gen-libc-ctype: %v\n%s", err, stderr.Bytes())
	}
	want, err := os.ReadFile(filepath.Join(root, "coreutils", "lib", "glibc_ctype.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, want) {
		return
	}
	gotLines := strings.Split(string(got), "\n")
	wantLines := strings.Split(string(want), "\n")
	if gotLines[1] != wantLines[1] {
		t.Fatalf("the module is from another libc than the libc of the host:\n  host:   %s\n  module: %s", gotLines[1], wantLines[1])
	}
	for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("glibc_ctype.fern:%d is not the output of the generator:\n  got:  %q\n  want: %q", i+1, gotLines[i], wantLines[i])
		}
	}
	t.Fatalf("the generator writes %d lines, and glibc_ctype.fern has %d", len(gotLines), len(wantLines))
}
