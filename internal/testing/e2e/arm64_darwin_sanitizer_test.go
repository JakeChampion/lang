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

func TestArm64DarwinSanitize(t *testing.T) {
	bin := buildFernCLI(t)
	cases := []struct {
		name       string
		src        string
		sanitize   bool
		wantExit   int
		wantStderr []string
		noStderr   []string
		census     bool
		env        []string
	}{
		{name: "clean_run_is_silent", src: sanCleanSrc, sanitize: true, wantExit: 0, census: true},
		{
			name: "use_after_free_reported", src: sanStaleTouchSrc, sanitize: true,
			wantExit:   e2eharness.ExitSanitizer,
			wantStderr: []string{"fern-sanitizer: use-after-free (touched a quarantined block)", "backtrace:"},
			noStderr:   []string{"rc over-release"},
		},
		{
			name: "double_free_reported", src: sanDoubleFreeSrc, sanitize: true, env: []string{uncertified},
			wantExit:   e2eharness.ExitSanitizer,
			wantStderr: []string{"fern-sanitizer: use-after-free (touched a quarantined block)", "backtrace:"},
		},
		{name: "use_after_free_silent_without_sanitize", src: sanStaleTouchSrc, sanitize: false, wantExit: 0},
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
			build := exec.Command(bin, args...)
			build.Env = e2eharness.ChildEnv(c.env...)
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
