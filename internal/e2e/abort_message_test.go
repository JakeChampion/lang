package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// abortCase is a program that aborts (or, for the control, does not) with an
// expected exit code and stderr substring.
type abortCase struct {
	name     string
	src      string
	wantExit int
	wantErr  string // substring expected on stderr ("" = expect none)
}

var abortCases = []abortCase{
	{
		name:     "array_oob",
		src:      `function main(): i32 { var xs: i32[] = [10, 20, 30]; return xs[7]; }`,
		wantExit: 134,
		wantErr:  "array index out of range",
	},
	{
		name:     "string_slice_oob",
		src:      `function main(): i32 { var s: string = "hi"; var t: str = s[1:9]; return t.len(); }`,
		wantExit: 134,
		wantErr:  "string index out of range",
	},
	{
		name:     "slice_range_oob",
		src:      `function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: [i32] = xs[1:9]; return ys.len(); }`,
		wantExit: 134,
		wantErr:  "slice range out of bounds",
	},
	{
		name:     "in_bounds_ok",
		src:      `function main(): i32 { var xs: i32[] = [10, 20, 30]; return xs[1]; }`,
		wantExit: 20,
		wantErr:  "",
	},
}

// runAbortCases builds each case for `target` and runs it via `run`, asserting
// the exit code and that the abort's cause is named on stderr (or, for the
// in-bounds control, that stderr stays clean). Both native backends must
// produce the SAME diagnostic (#5538) — the shared case table pins that.
func runAbortCases(t *testing.T, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	bin := buildFernCLI(t)
	dir := t.TempDir()
	for _, tc := range abortCases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, target+"_"+tc.name+".fern")
			if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, target+"_"+tc.name+".bin")
			if o, err := exec.Command(bin, "-target", target, "-o", out, p).CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, o)
			}
			cmd := run(out)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("exit = %d, want %d", code, tc.wantExit)
			}
			errOut := stderr.String()
			if tc.wantErr == "" {
				if errOut != "" {
					t.Errorf("stderr = %q, want empty", errOut)
				}
				return
			}
			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want to contain %q", errOut, tc.wantErr)
			}
			if !strings.HasPrefix(errOut, "fern: ") {
				t.Errorf("stderr = %q, want a 'fern: ' diagnostic prefix", errOut)
			}
		})
	}
}

// TestX86_64AbortMessages: a fatal abort (#5538) writes a diagnostic naming its
// cause to stderr before exiting, instead of exiting with a bare code.
func TestX86_64AbortMessages(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	runAbortCases(t, "x86-64-linux", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

// TestArm64AbortMessages is the arm64 parity check: the same programs abort
// with the same diagnostics as x86-64 (identical text — pinned by the shared
// abortCases table).
func TestArm64AbortMessages(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runAbortCases(t, "arm64-linux", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}
