package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestX86_64AbortMessages checks that a fatal abort (#5538) writes a diagnostic
// naming its cause to stderr before exiting, instead of exiting with a bare
// code. Covers the array-index, string-slice, and slice-range abort paths; the
// in-bounds control confirms the reporter never fires on a healthy run.
func TestX86_64AbortMessages(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()

	cases := []struct {
		name     string
		src      string
		wantExit int
		wantErr  string // substring expected on stderr ("" = expect none)
	}{
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, tc.name+".bin")
			if o, err := exec.Command(bin, "-target", "x86-64", "-o", out, p).CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, o)
			}
			cmd := runX86Bin(qemu, out)
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
