package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// mapFileSource maps files of each kind and compares the text with the bytes
// that read_file_bytes gives: a file of 300,000 bytes with bytes that are not
// UTF-8, a file of one byte, an empty file, a missing file, a directory, and
// on Linux a file of /proc, which reports a size of 0 and is read. The large
// map is made three times, so that a map that is not released shows in the
// leak census or in the count of maps.
const mapFileSource = `import "core/error";

function same(path: string): i32 {
  let text = match (unsafe { map_file_unchecked(path) }) {
    Ok(t) => t,
    Err(_) => {
      return 10;
    },
  };
  let bytes = match (read_file_bytes(path)) {
    Ok(b) => b,
    Err(_) => {
      return 11;
    },
  };
  if (text.len() != bytes.len()) {
    return 12;
  }
  for i in 0..bytes.len() {
    if (text[i] != bytes[i]) {
      return 13;
    }
  }
  return 0;
}

function main(): i32 {
  for k in 0..3 {
    let r = same("big.bin");
    if (r != 0) {
      return r;
    }
  }
  let one = same("one.txt");
  if (one != 0) {
    return 20 + one;
  }
  let empty = same("empty.txt");
  if (empty != 0) {
    return 40 + empty;
  }
  match (unsafe { map_file_unchecked("missing.txt") }) {
    Ok(_) => {
      return 60;
    },
    Err(e) => print(e.to_string()),
  }
  match (unsafe { map_file_unchecked("dir") }) {
    Ok(_) => {
      return 61;
    },
    Err(e) => print(e.to_string()),
  }
  if (target_os() == "linux") {
    match (unsafe { map_file_unchecked("/proc/self/stat") }) {
      Ok(t) => {
        if (t.len() == 0) {
          return 62;
        }
      },
      Err(_) => {
        return 63;
      },
    }
  }
  return 0;
}
`

const mapFileWant = "not found\nIs a directory\n"

func mapFileDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	big := make([]byte, 300000)
	for i := range big {
		big[i] = byte(i*131 + i/7)
	}
	files := map[string][]byte{"big.bin": big, "one.txt": []byte("x"), "empty.txt": nil, "prog.fern": []byte(mapFileSource)}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runMapFile builds the program for `target` with the leak census and runs
// it in a directory of test files. It returns the standard output and the
// standard error.
func runMapFile(t *testing.T, target string, runner []string) (string, string) {
	t.Helper()
	dir := mapFileDir(t)
	bin := filepath.Join(dir, "prog")
	build := exec.Command(buildFernCLI(t), "-target", target, "-o", bin, filepath.Join(dir, "prog.fern"))
	build.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
	if o, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s build failed: %v\n%s", target, err, o)
	}
	cmd := exec.Command(bin)
	if len(runner) > 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	cmd.Dir = dir
	cmd.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v (an exit code names the failing step)\n%s", err, stderr.String())
	}
	return string(out), stderr.String()
}

func checkMapFile(t *testing.T, out, stderr string, census bool) {
	t.Helper()
	if out != mapFileWant {
		t.Errorf("stdout = %q, want %q", out, mapFileWant)
	}
	if census && !strings.Contains(stderr, "live_bytes=0") {
		t.Errorf("leak census: %q, want live_bytes=0", stderr)
	}
}

func TestMapFileX86_64(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	out, stderr := runMapFile(t, "x86-64-linux", runner)
	checkMapFile(t, out, stderr, true)
}

func TestMapFileArm64Linux(t *testing.T) {
	var runner []string
	if q := e2eharness.Arm64Runner(t); q != "" {
		runner = []string{q}
	}
	out, stderr := runMapFile(t, "arm64-linux", runner)
	checkMapFile(t, out, stderr, true)
}

func TestMapFileArm64Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		dir := mapFileDir(t)
		build := exec.Command(buildFernCLI(t), "-target", "arm64-darwin", "-o", filepath.Join(dir, "prog"), filepath.Join(dir, "prog.fern"))
		if o, err := build.CombinedOutput(); err != nil {
			t.Fatalf("arm64-darwin build failed: %v\n%s", err, o)
		}
		return
	}
	out, stderr := runMapFile(t, "arm64-darwin", []string{})
	checkMapFile(t, out, stderr, true)
}

func TestMapFileWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Fatal("requires wasmtime: eval \"$(scripts/toolchain-env)\" installs it")
	}
	out, stderr := runMapFile(t, "wasm32-wasi", []string{"wasmtime", "run", "--dir=."})
	checkMapFile(t, out, stderr, false)
}

func TestInterpMapFile(t *testing.T) {
	dir := mapFileDir(t)
	cmd := exec.Command(e2eharness.BuildLangBinForInterp(t), "-interp", filepath.Join(dir, "prog.fern"))
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v (an exit code names the failing step)", err)
	}
	checkMapFile(t, string(out), "", false)
}
