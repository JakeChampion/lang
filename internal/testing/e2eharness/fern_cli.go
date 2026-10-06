package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// FernCLI builds cmd/fern into a temp dir with SelfHostCLI beside it as
// fern-selfhost, the compiler the launcher finds there before it would build
// its own, so a `-target` compile through it runs the tree's current
// self-host.
func FernCLI(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	if err := os.Symlink(SelfHostCLI(t), filepath.Join(dir, "fern-selfhost")); err != nil {
		t.Fatal(err)
	}
	return bin
}
