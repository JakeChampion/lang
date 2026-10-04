package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestTaskFrameFallback runs TaskFrameProgram through the Go compiler, whose
// blocking fallback never parks: the same figure, no park.
func TestTaskFrameFallback(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "sched.fern")
	if err := os.WriteFile(src, []byte(e2eharness.TaskFrameProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var runner []string
			switch target {
			case "x86-64-linux":
				_, runner = x86_64Tooling(t)
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			}
			bin := filepath.Join(t.TempDir(), "sched")
			compile := exec.Command(fern, "-target", target, "-o", bin, src)
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append(append([]string{}, runner...), bin)
			out, err := exec.Command(argv[0], argv[1:]...).Output()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if string(out) != e2eharness.TaskFrameFallbackWant {
				t.Fatalf("stdout:\n%s\nwant:\n%s", out, e2eharness.TaskFrameFallbackWant)
			}
		})
	}
}
