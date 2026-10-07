// A trailing slash on stat and lstat resolves a symlink and names a
// directory, else ENOTDIR — POSIX, which Linux enforces and XNU does not.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const statSlashSource = `import "std/errno";

function notdir(r: Result[FileStat, IoError]): boolean {
  match (r) {
    Ok(_) => {
      return false;
    },
    Err(e) => {
      return errno.of(e) == errno.ENOTDIR;
    }
  }
}

function dir(r: Result[FileStat, IoError]): boolean {
  match (r) {
    Ok(st) => {
      return st.is_dir;
    },
    Err(_) => {
      return false;
    }
  }
}

function main(): i32 {
  if (!notdir(stat("f/"))) {
    return 1;
  }
  if (!notdir(stat("lf/"))) {
    return 2;
  }
  if (!notdir(lstat("lf/"))) {
    return 3;
  }
  if (!dir(stat("ld/"))) {
    return 4;
  }
  if (!dir(lstat("ld/"))) {
    return 5;
  }
  if (!dir(lstat("d/"))) {
    return 6;
  }
  return 0;
}
`

// statSlashTree is a file, a directory, and a symlink to each.
func statSlashTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "lf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("d", filepath.Join(dir, "ld")); err != nil {
		t.Fatal(err)
	}
}

func runStatSlash(t *testing.T, argv []string, dir string) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v (the exit code names the failing check)\n%s", err, out)
	}
}

func buildStatSlash(t *testing.T, target string) (bin, dir string) {
	t.Helper()
	fern := buildFernCLI(t)
	dir = t.TempDir()
	statSlashTree(t, dir)
	src := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(src, []byte(statSlashSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(t.TempDir(), "prog")
	if o, err := exec.Command(fern, "-target", target, "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("%s build failed: %v\n%s", target, err, o)
	}
	return bin, dir
}

// The arm64-darwin runtime corrects XNU, so Apple Silicon must agree.
func TestArm64DarwinStatTrailingSlash(t *testing.T) {
	bin, dir := buildStatSlash(t, "arm64-darwin")
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	runStatSlash(t, []string{bin}, dir)
}

func TestStatTrailingSlashX86_64(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin, dir := buildStatSlash(t, "x86-64-linux")
	runStatSlash(t, append(append([]string{}, runner...), bin), dir)
}

// The interpreter holds the same rule on whatever host it runs on.
func TestStatTrailingSlashInterp(t *testing.T) {
	fern := buildLangBinForInterp(t)
	dir := t.TempDir()
	statSlashTree(t, dir)
	src := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(src, []byte(statSlashSource), 0o644); err != nil {
		t.Fatal(err)
	}
	runStatSlash(t, []string{fern, "-interp", src}, dir)
}
