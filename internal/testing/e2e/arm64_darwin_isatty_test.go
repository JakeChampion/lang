package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

// TestArm64DarwinIsatty runs isatty against the descriptors Darwin's libc
// tells apart: a pseudo-terminal master straight from /dev/ptmx, which is a
// terminal by its device type though it refuses TIOCGETA, a slave, and
// /dev/null. Off Apple Silicon the build is what is checked.
func TestArm64DarwinIsatty(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "isatty.fern")
	prog := `function main(): i32 {
  if (!isatty(3)) {
    return 1;
  }
  if (!isatty(4)) {
    return 2;
  }
  if (isatty(5)) {
    return 3;
  }
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "isatty")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	defer ptmx.Close()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	cmd := exec.Command(out)
	cmd.ExtraFiles = []*os.File{ptmx, slave, null}
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isatty disagrees with Darwin's libc: %v\n%s", err, o)
	}
}
