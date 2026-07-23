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

// TestX86_64AbortBacktrace checks the frame-pointer backtrace (#5538 slice 2):
// on a fatal abort the runtime walks the rbp chain and prints each return
// address, and — with `-g`'s .symtab — those addresses resolve to the calling
// functions. A nested inner→mid→main chain aborting in `inner` must produce a
// backtrace whose frames land in mid, main, and _start (inner itself is the
// aborting leaf; its own frame's return address is the first printed).
func TestX86_64AbortBacktrace(t *testing.T) {
	src := `function inner(xs: i32[]): i32 { return xs[7]; }
function mid(xs: i32[]): i32 { return inner(xs); }
function main(): i32 { var xs: i32[] = [1, 2, 3]; return mid(xs); }
`
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "deep.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "deep.bin")
	if o, err := exec.Command(bin, "-g", "-target", "x86-64", "-o", out, p).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}

	cmd := runX86Bin(qemu, out)
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

	// Resolve each printed frame address against the -g symbol table.
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

	hexRe := regexp.MustCompile(`0x[0-9a-f]{16}`)
	var frames []string
	for _, m := range hexRe.FindAllString(errOut, -1) {
		a, err := strconv.ParseUint(m[2:], 16, 64)
		if err != nil {
			continue
		}
		frames = append(frames, resolve(a))
	}
	if len(frames) < 3 {
		t.Fatalf("got %d backtrace frames, want >= 3\nstderr:\n%s", len(frames), errOut)
	}
	// The chain inner→mid→main→_start: the printed return addresses land in
	// mid (inner's return), main (mid's return), and _start (main's return).
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
