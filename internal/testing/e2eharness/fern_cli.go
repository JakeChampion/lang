package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func FernCLI(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	sib := filepath.Join(dir, "fern-selfhost")
	if err := os.Symlink(SelfHostCLI(t), sib); err != nil {
		t.Fatal(err)
	}
	key, err := exec.Command(bin, "-selfhost-key").Output()
	if err != nil {
		t.Fatalf("fern -selfhost-key: %v", err)
	}
	if err := os.WriteFile(sib+".key", key, 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}
