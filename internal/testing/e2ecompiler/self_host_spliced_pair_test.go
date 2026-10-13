package e2ecompiler

import (
	"path/filepath"
	"testing"
)

// On arm64-darwin `selinux.file_context` folds to `return Err(Unsupported)`,
// small enough for the tiny-leaf splice, and its paired word (sempair) is
// spliced into `chcon`'s `change_one`. The word must land in a caller's local:
// a `ret_word` left in the caller's body made the certifier read the caller as
// a paired return and refuse every unit it returned.
func TestSelfHostSplicedPairedLeafCertifies(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(repoRootFromTest(t), "coreutils", "chcon.fern")
	out := filepath.Join(t.TempDir(), "chcon")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "arm64-darwin", "-o", out, src, cli.stdlib)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host -target arm64-darwin coreutils/chcon.fern: %v\n%s", err, msg)
	}
}
