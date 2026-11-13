package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const readFileBytesSrc = `function check(path: string, n: i32): i32 {
  if let Ok(b) = read_file_bytes(path) {
    if (b.len() != n) {
      return 1;
    }
    for i in 0..n {
      if (b[i] as i32 != (i * 7 + i / 251) & 255) {
        return 2;
      }
    }
    return 0;
  }
  return 3;
}

function same_as_text(path: string): i32 {
  if let Ok(s) = read_file(path) {
    if let Ok(b) = read_file_bytes(path) {
      if (s.len() == 0 || b.len() != s.len()) {
        return 1;
      }
      for i in 0..b.len() {
        if (b[i] as i32 != s[i] as i32) {
          return 2;
        }
      }
      return 0;
    }
    return 3;
  }
  return 4;
}

function main(): i32 {
  let r = check(%q, %d);
  if (r != 0) {
    return 10 + r;
  }
  r = check(%q, 0);
  if (r != 0) {
    return 20 + r;
  }
  r = same_as_text("/proc/self/mounts");
  if (r != 0) {
    return 30 + r;
  }
  if let Ok(_) = read_file_bytes(%q) {
    return 40;
  }
  return 0;
}
`

// TestSelfHostReadFileBytes checks that read_file_bytes gives the bytes of a
// file whose size is known, of an empty file, and of a procfs file, which
// reports size 0 and so takes the growth path. It also checks the error path.
// The leak census must balance on each target.
func TestSelfHostReadFileBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no procfs on " + runtime.GOOS)
	}
	dir := t.TempDir()
	const n = 100003
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	big := filepath.Join(dir, "big")
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(big, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	prog := fmt.Sprintf(readFileBytesSrc, big, n, empty, filepath.Join(dir, "missing"))
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
