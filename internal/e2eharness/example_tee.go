package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func CheckExampleTee(t *testing.T, command func(...string) *exec.Cmd, census bool) {
	t.Helper()
	input := IOAllBytesInput()
	for _, tc := range []struct {
		name         string
		append, fail bool
	}{
		{"overwrite", false, false},
		{"append", true, false},
		{"continue after failure", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			prefix := []byte{0xff, 0, 0xc0, 0x80}
			if err := os.WriteFile(filepath.Join(dir, "existing"), prefix, 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"existing", "created"}
			if tc.append {
				args = append([]string{"-a"}, args...)
			}
			if tc.fail {
				if err := os.Mkdir(filepath.Join(dir, "blocked"), 0o755); err != nil {
					t.Fatal(err)
				}
				args = append([]string{"blocked"}, args...)
			}
			cmd := command(args...)
			cmd.Dir = dir
			cmd.Stdin = bytes.NewReader(input)
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err := cmd.Run()
			if tc.fail {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("exit = %v, want 1\n%s", err, diagnostic.String())
				}
			} else if err != nil {
				t.Fatalf("run: %v\n%s", err, diagnostic.String())
			}
			if !bytes.Equal(out.Bytes(), input) {
				t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(input))
			}
			for _, path := range []string{"existing", "created"} {
				want := input
				if path == "existing" && tc.append {
					want = append(append([]byte{}, prefix...), input...)
				}
				got, err := os.ReadFile(filepath.Join(dir, path))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s differs: got %d bytes, want %d", path, len(got), len(want))
				}
			}
			if census && (strings.Contains(diagnostic.String(), "fern-sanitizer:") || !strings.Contains(diagnostic.String(), "live_bytes=0")) {
				t.Fatalf("missing clean ownership census\n%s", diagnostic.String())
			}
		})
	}
}
