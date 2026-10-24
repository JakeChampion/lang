package sourcelint

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNoTrackedExecutables(t *testing.T) {
	cmd := exec.Command("git", "-C", "../../..", "ls-files", "-z")
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
		f, err := os.Open("../../../" + name)
		if err != nil {
			continue
		}
		var magic [4]byte
		n, _ := f.Read(magic[:])
		f.Close()
		if n == 4 && string(magic[:]) == "\x7fELF" {
			t.Errorf("%s is a committed ELF binary — add it to .gitignore and `git rm --cached` it", name)
		}
	}
}
