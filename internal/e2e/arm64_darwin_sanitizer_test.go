package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestArm64DarwinSanitize runs the sanitizer probes from sanitizer_test.go
// through the CLI (`-target arm64-darwin -sanitize`) and the in-process
// Mach-O writer. The Linux legs prove the arm64 generator carries the whole
// mode; this one proves the Darwin image does too, on the one host that can
// launch it, with the same message text and exit status as x86-64. It is
// also the only sanitizer gate that runs natively on an Apple Silicon dev
// machine. Builds everywhere; executes only on Apple Silicon.
func TestArm64DarwinSanitize(t *testing.T) {
	bin := buildFernCLI(t)
	cases := []struct {
		name       string
		src        string
		sanitize   bool
		wantExit   int
		wantStderr []string
		noStderr   []string
		// census: stderr is exactly one balanced leakcheck line and
		// nothing else, which is what a clean sanitized run prints.
		census bool
	}{
		{name: "clean_run_is_silent", src: sanCleanSrc, sanitize: true, wantExit: 0, census: true},
		{
			name: "use_after_free_reported", src: sanUseAfterFreeSrc, sanitize: true,
			wantExit:   arm64codegen.ExitSanitizer,
			wantStderr: []string{"fern-sanitizer: use-after-free (touched a quarantined block)", "backtrace:"},
			noStderr:   []string{"rc over-release"},
		},
		{
			name: "double_free_reported", src: sanDoubleFreeSrc, sanitize: true,
			wantExit:   arm64codegen.ExitSanitizer,
			wantStderr: []string{"fern-sanitizer: rc over-release (double free)", "backtrace:"},
		},
		// Flag off, the same stale touch recycles the block and bumps a
		// recycled rc word: exit 0 and nothing on stderr.
		{name: "use_after_free_silent_without_sanitize", src: sanUseAfterFreeSrc, sanitize: false, wantExit: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, "prog")
			args := []string{"-target", "arm64-darwin"}
			if c.sanitize {
				args = append(args, "-sanitize")
			}
			args = append(args, "-o", out, src)
			// The compiler reads FERN_SANITIZE / FERN_LEAKCHECK at init,
			// so an ambient setting would turn the flag-off leg on.
			build := exec.Command(bin, args...)
			build.Env = e2eharness.ChildEnv()
			if o, err := build.CombinedOutput(); err != nil {
				t.Fatalf("arm64-darwin build failed: %v\n%s", err, o)
			}
			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
				t.Skip("execution check only runs on Apple Silicon")
			}
			stdout, stderr, code := runSplit(t, exec.Command(out))
			if code != c.wantExit {
				t.Errorf("exit=%d, want %d (stderr=%q)", code, c.wantExit, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout=%q, want empty (reports go to stderr only)", stdout)
			}
			for _, want := range c.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q: %q", want, stderr)
				}
			}
			for _, bad := range c.noStderr {
				if strings.Contains(stderr, bad) {
					t.Errorf("stderr names %q, the wrong diagnosis: %q", bad, stderr)
				}
			}
			switch {
			case c.census:
				allocs, frees, live := parseLeakCheckLine(t, stderr)
				if allocs == 0 || allocs != frees || live != 0 {
					t.Errorf("got allocs=%d frees=%d live=%d, want non-zero and balanced", allocs, frees, live)
				}
			case !c.sanitize && stderr != "":
				t.Errorf("stderr=%q, want empty (an unsanitized build must not report)", stderr)
			}
		})
	}
}
