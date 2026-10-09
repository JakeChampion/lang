// `clone_file(src, dest)`: a copy-on-write clone where the filesystem has one.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Either the clone happened and dest reads back as src, or it was refused and
// left no dest behind. Darwin's clone never replaces an existing dest.
const cloneFileSource = `import "std/errno";

function main(): i32 {
  match (clone_file("src.txt", "dst.txt")) {
    Ok(_) => {
      match (read_file("dst.txt")) {
        Ok(text) => {
          if (text != "hello\n") {
            return 1;
          }
        },
        Err(_) => {
          return 2;
        }
      }
      match (clone_file("src.txt", "dst.txt")) {
        Ok(_) => {
          if (target_os() == "darwin") {
            return 3;
          }
        },
        Err(_) => {}
      }
      print("cloned");
    },
    Err(_) => {
      match (stat("dst.txt")) {
        Ok(_) => {
          return 4;
        },
        Err(_) => {}
      }
      print("refused");
    }
  }
  match (clone_file("missing.txt", "dst2.txt")) {
    Ok(_) => {
      return 5;
    },
    Err(e) => {
      if (errno.of(e) != errno.ENOENT && errno.of(e) != errno.EOPNOTSUPP) {
        return 6;
      }
    }
  }
  return 0;
}
`

// runCloneFile builds the probe for target and runs it in a directory holding
// src.txt, returning what it printed.
func runCloneFile(t *testing.T, target string, runner []string) string {
	t.Helper()
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(cloneFileSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if o, err := exec.Command(fern, "-target", target, "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("%s build failed: %v\n%s", target, err, o)
	}
	if runner == nil {
		return ""
	}
	cmd := exec.Command(bin)
	if len(runner) > 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v (an exit code names the failing step)", err)
	}
	return string(out)
}

// APFS clones, so on Apple Silicon the probe must report one.
func TestArm64DarwinCloneFile(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		runCloneFile(t, "arm64-darwin", nil)
		return
	}
	if got := runCloneFile(t, "arm64-darwin", []string{}); got != "cloned\n" {
		t.Errorf("clone_file on APFS: %q, want cloned", got)
	}
}

// hostClones is what clone_file must answer in a test directory on this
// host: whether FICLONE works on the filesystem under it, asked by Go in a
// sibling directory, and on Darwin a clone, since APFS has one.
func hostClones(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "darwin" {
		return "cloned\n"
	}
	dir := t.TempDir()
	src, err := os.Create(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.WriteString("hello\n"); err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(filepath.Join(dir, "b"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	const ficlone = 0x40049409
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, dst.Fd(), ficlone, src.Fd()); e != 0 {
		return "refused\n"
	}
	return "cloned\n"
}

// A Linux runtime clones exactly where the filesystem can, and a refused
// clone leaves no destination behind.
func TestCloneFileX86_64(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	if runner == nil {
		runner = []string{}
	}
	if got, want := runCloneFile(t, "x86-64-linux", runner), hostClones(t); got != want {
		t.Errorf("clone_file: %q, want %q", got, want)
	}
}

// The interpreter asks the host's own call (clonefile_*.go).
func TestInterpCloneFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(cloneFileSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eharness.BuildLangBinForInterp(t), "-interp", src)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v (an exit code names the failing step)", err)
	}
	if got, want := string(out), hostClones(t); got != want {
		t.Errorf("clone_file: %q, want %q", got, want)
	}
}

// Neither WASI preview has a clone.
func TestCloneFileWasmIsUnsupported(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	if got := runCloneFile(t, "wasm32-wasi", []string{"wasmtime", "run", "--dir=."}); got != "refused\n" {
		t.Errorf("clone_file on wasm: %q, want refused", got)
	}
}

// The arm64-linux runtime asks FICLONE through its own syscall table.
func TestCloneFileArm64Linux(t *testing.T) {
	var runner []string
	if q := e2eharness.Arm64Runner(t); q != "" {
		runner = []string{q}
	} else {
		runner = []string{}
	}
	if got, want := runCloneFile(t, "arm64-linux", runner), hostClones(t); got != want {
		t.Errorf("clone_file: %q, want %q", got, want)
	}
}
