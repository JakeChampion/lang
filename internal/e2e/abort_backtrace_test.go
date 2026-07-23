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
)

var backtraceHexRe = regexp.MustCompile(`0x[0-9a-f]{16}`)

// runBacktraceCase builds a nested inner→mid→main chain that aborts in `inner`
// with `-g` for `target`, runs it via `run`, and asserts the frame-pointer
// backtrace (#5538 slice 2) prints return addresses that resolve — through the
// -g .symtab — to mid, main, and _start (inner is the aborting leaf; the
// printed addresses are the *return* sites up the chain).
func runBacktraceCase(t *testing.T, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	src := `function inner(xs: i32[]): i32 { return xs[7]; }
function mid(xs: i32[]): i32 { return inner(xs); }
function main(): i32 { var xs: i32[] = [1, 2, 3]; return mid(xs); }
`
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "deep.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "deep.bin")
	if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, p).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}

	cmd := run(out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 134 {
		t.Errorf("exit = %d, want 134", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "fern: array index out of range") {
		t.Errorf("stderr missing cause line:\n%s", errOut)
	}
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
	resolve := func(a uint64) string {
		for _, s := range syms {
			if s.Value <= a && a < s.Value+s.Size {
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
	runBacktraceCase(t, "x86-64", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

// TestArm64AbortBacktrace is the arm64 parity check: the x29 frame-chain walk
// produces the same resolved backtrace as x86-64.
func TestArm64AbortBacktrace(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runBacktraceCase(t, "arm64", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}
