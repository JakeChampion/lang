package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type darwinSanCase struct {
	name       string
	src        string
	sanitize   bool
	wantExit   int
	wantStderr []string
	noStderr   []string
	census     bool
	env        []string
}

// darwinAtomicSrc runs one atomic at offset bytes into a 16-byte raw block,
// then releases the block.
func darwinAtomicSrc(offset int, call string) string {
	return "function main(): i32 {\n  unsafe {\n    let p = __alloc(16) as usize;\n    let q = p + " +
		strconv.Itoa(offset) + " as usize;\n    " + call + ";\n    __free(p, 16);\n  }\n  return 0;\n}\n"
}

func TestArm64DarwinSanitize(t *testing.T) {
	bin := buildFernCLI(t)
	cases := []darwinSanCase{
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
		{name: "aligned_atomics_are_silent", src: darwinAtomicSrc(0, "__atomic_cas(q, 0i64, 1i64)"), sanitize: true, wantExit: 0, census: true},
	}
	for name, call := range map[string]string{
		"load":      "__atomic_load_acquire(q)",
		"store":     "__atomic_store_release(q, 1i64)",
		"fetch_add": "__atomic_fetch_add(q, 1i64)",
		"cas":       "__atomic_cas(q, 0i64, 1i64)",
	} {
		cases = append(cases, darwinSanCase{
			name: "misaligned_atomic_" + name + "_reported", src: darwinAtomicSrc(4, call), sanitize: true,
			wantExit:   e2eharness.ExitSanitizer,
			wantStderr: []string{"fern-sanitizer: misaligned atomic (an atomic access of an address that is not a multiple of 8)", "backtrace:"},
		})
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
