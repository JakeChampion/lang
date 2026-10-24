package e2ecompiler

import (
	"path/filepath"
	"testing"
)

func TestSelfHostSplicedPairedLeafCertifies(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(repoRootFromTest(t), "coreutils", "chcon.fern")
	out := filepath.Join(t.TempDir(), "chcon")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "arm64-darwin", "-o", out, src, cli.stdlib)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host -target arm64-darwin coreutils/chcon.fern: %v\n%s", err, msg)
	}
}
