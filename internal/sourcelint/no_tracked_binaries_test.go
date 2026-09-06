package sourcelint

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestNoTrackedExecutables fails when a compiled binary is committed.
//
// .gitignore names each generated command individually (/fern, /unicodegen,
// …), so a NEW command under cmd/ is not covered until someone remembers to
// add it — and `go build ./cmd/x` drops its output in the repo root, where
// `git add -A` sweeps it up. cmd/x86tblgen reached main that way as a 2.6 MB
// ELF file. Nothing else notices: the binary builds, the tests pass, and the
// only symptom is the clone getting bigger every time it is rebuilt.
func TestNoTrackedExecutables(t *testing.T) {
	cmd := exec.Command("git", "-C", "../..", "ls-files", "-z")
	// An inherited GIT_DIR outranks -C, which would list some other
	// repository's files and say nothing about this one.
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	names := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(names) < 100 {
		t.Fatalf("git ls-files returned %d paths; the listing is not the repository", len(names))
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		f, err := os.Open("../../" + name)
		if err != nil {
			continue // a path this checkout does not materialise
		}
		var magic [4]byte
		n, _ := f.Read(magic[:])
		f.Close()
		if n == 4 && string(magic[:]) == "\x7fELF" {
			t.Errorf("%s is a committed ELF binary — add it to .gitignore and `git rm --cached` it", name)
		}
	}
}
