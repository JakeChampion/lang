package e2e

import (
	"bytes"
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/symname"
)

var backtraceHexRe = regexp.MustCompile(`0x[0-9a-f]{16}`)

// deepAbortSrc is the shared inner→mid→main chain: `inner` indexes past the
// end of a 3-element array, so the abort fires three frames deep and a walk
// has something to report.
// @noinline holds the chain together: the subject here is the frame WALK and
// its symbolisation, and ir.Inline would otherwise substitute both helpers into
// main and leave a correct one-frame backtrace with nothing to walk.
const deepAbortSrc = `@noinline function inner(xs: i32[]): i32 { return xs[7]; }
@noinline function mid(xs: i32[]): i32 { return inner(xs); }
function main(): i32 { var xs: i32[] = [1, 2, 3]; return mid(xs); }
`

// buildAndAbort compiles deepAbortSrc with `-g` for target (plus extraArgs,
// with extraEnv added to the COMPILER's environment — the backtrace toggle is
// a build-time switch), runs the binary, and asserts the abort's exit code and
// cause line. Returns the binary path and its stderr for the caller to make
// its backtrace-specific assertions against.
func buildAndAbort(t *testing.T, target string, run func(bin string) *exec.Cmd, extraArgs, extraEnv []string) (string, string) {
	t.Helper()
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "deep.fern")
	if err := os.WriteFile(p, []byte(deepAbortSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "deep.bin")
	args := append([]string{"-g", "-target", target, "-o", out}, extraArgs...)
	build := exec.Command(bin, append(args, p)...)
	build.Env = append(os.Environ(), extraEnv...)
	if o, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}

	cmd := run(out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	// The opt-out suppresses the backtrace, never the safety contract: an
	// out-of-range index still exits 134 and still names its cause
	// (ARRAY-BOUNDS.md).
	if code := cmd.ProcessState.ExitCode(); code != 134 {
		t.Errorf("exit = %d, want 134", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "fern: array index out of range") {
		t.Errorf("stderr missing cause line:\n%s", errOut)
	}
	return out, errOut
}

// runBacktraceCase asserts the DEFAULT build's frame-pointer backtrace (#5538
// slice 2) prints return addresses that resolve — through the -g .symtab — to
// mid, main, and _start (inner is the aborting leaf; the printed addresses are
// the *return* sites up the chain).
func runBacktraceCase(t *testing.T, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	out, errOut := buildAndAbort(t, target, run, nil, nil)
	if !strings.Contains(errOut, "backtrace:") {
		t.Fatalf("stderr missing backtrace header:\n%s", errOut)
	}

	f, err := goelf.Open(out)
	if err != nil {
		t.Fatalf("open ELF: %v", err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("Symbols() (expected .symtab under -g): %v", err)
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
	// .symtab carries the emitted symbol, so resolving an address to a source
	// name means demangling it — what any symbolising tool does with a mangled
	// table. `_start` is not a Fern function and comes back untouched.
	resolve := func(a uint64) string {
		for _, s := range syms {
			if s.Value <= a && a < s.Value+s.Size {
				if src, ok := symname.Source(s.Name); ok {
					return src
				}
				return s.Name
			}
		}
		return ""
	}

	var frames []string
	for _, m := range backtraceHexRe.FindAllString(errOut, -1) {
		a, err := strconv.ParseUint(m[2:], 16, 64)
		if err != nil {
			continue
		}
		frames = append(frames, resolve(a))
	}
	if len(frames) < 3 {
		t.Fatalf("got %d backtrace frames, want >= 3\nstderr:\n%s", len(frames), errOut)
	}
	for _, want := range []string{"mid", "main", "_start"} {
		found := false
		for _, got := range frames {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("backtrace frames %v missing %q\nstderr:\n%s", frames, want, errOut)
		}
	}
}

// TestX86_64AbortBacktrace: the x86-64 abort backtrace walks the rbp chain and
// its frames resolve to the calling functions via the -g symbol table.
func TestX86_64AbortBacktrace(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	runBacktraceCase(t, "x86-64-linux", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

// TestArm64AbortBacktrace is the arm64 parity check: the x29 frame-chain walk
// produces the same resolved backtrace as x86-64.
func TestArm64AbortBacktrace(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runBacktraceCase(t, "arm64-linux", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}

// runBacktraceOffCase is the #5538 slice-4 opt-out: with the walk suppressed at
// compile time the abort keeps its exit code and its cause line, but writes no
// backtrace header and no addresses at all. Run for both surfaces — the
// FERN_BACKTRACE=0 env var the issue names and the -backtrace=false CLI flag —
// since they set the same compile-time switch by different routes.
func runBacktraceOffCase(t *testing.T, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	for _, tc := range []struct {
		name string
		args []string
		env  []string
	}{
		{"env", nil, []string{"FERN_BACKTRACE=0"}},
		{"flag", []string{"-backtrace=false"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut := buildAndAbort(t, target, run, tc.args, tc.env)
			if strings.Contains(errOut, "backtrace:") {
				t.Errorf("backtrace header still printed with the walk suppressed:\n%s", errOut)
			}
			if got := backtraceHexRe.FindAllString(errOut, -1); len(got) != 0 {
				t.Errorf("frame addresses still printed with the walk suppressed: %v\n%s", got, errOut)
			}
		})
	}
}

// TestX86_64AbortBacktraceOff: -backtrace=false / FERN_BACKTRACE=0 drops the
// x86-64 walk without touching the exit code or the cause line.
func TestX86_64AbortBacktraceOff(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	runBacktraceOffCase(t, "x86-64-linux", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

// TestArm64AbortBacktraceOff is the arm64 parity check for the opt-out.
func TestArm64AbortBacktraceOff(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runBacktraceOffCase(t, "arm64-linux", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}
