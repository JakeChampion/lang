package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSocketCloseTableCensusX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	for _, explicit := range []bool{false, true} {
		for _, leak := range []bool{false, true} {
			t.Run(fmt.Sprintf("exit_%t_leak_%t", explicit, leak), func(t *testing.T) {
				dir := t.TempDir()
				source := filepath.Join(dir, "close.fern")
				finish := "return 17;"
				if explicit {
					finish = "exit(17); return 0;"
				}
				lost := ""
				if leak {
					lost = "unsafe { let lost = __alloc(24); __store_i32(lost, 7); }"
				}
				program := `function main(): i32 {
  for fd in [72, 145, 300] {
    unsafe { let s: Socket = __socket_from_fd(fd); let _ = tcp_close(s); }
  }
  ` + lost + "\n  " + finish + "\n}\n"
				if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
					t.Fatal(err)
				}
				bin, err := buildTracedX86_64(t, source, dir)
				if err != nil {
					t.Fatal(err)
				}
				_, stderr, code := runSplit(t, runX86_64Bin(runner, bin))
				if code != 17 || !strings.Contains(stderr, "rctrace a ") {
					t.Fatalf("exit %d, want 17 and allocation trace: %s", code, stderr)
				}
				n, top, err := pairRcTrace(stderr)
				want := 0
				if leak {
					want = 1
				}
				if err != nil || n != want {
					t.Fatalf("unpaired %d, want %d: %s, %v\n%s", n, want, top, err, stderr)
				}
				if !leak {
					for _, target := range []string{"arm64-linux", "arm64-darwin"} {
						cli := e2eharness.SelfHostCLI(t)
						stdlib := e2eharness.SelfHostStdlibRoot(t)
						cmd := exec.Command(cli, "-stdlib", stdlib, "-target", target, "-sanitize", "-o", filepath.Join(dir, target), source)
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("%s cross-build: %v\n%s", target, err, out)
						}
					}
				}
			})
		}
	}
}
