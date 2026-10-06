package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the actual file-output function using a private directory. The
// fixture does not read or alter passwd, utmp or any user's home directory.
func WritePinkyByteFixture(t testing.TB) string {
	t.Helper()
	data, err := os.ReadFile(RepoPath("coreutils", "pinky.fern"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, "function dot_file(")
	end := strings.Index(source, "\nfunction long_entry(")
	if start < 0 || end <= start {
		t.Fatal("missing pinky file-output function")
	}
	lib, err := os.ReadFile(RepoPath("coreutils", "lib", "gnu.fern"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gnu.fern"), lib, 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := "import \"std/io_buffered\";\nimport \"./gnu\";\n" + source[start:end] + `
function main(): i32 {
  let out: io_buffered.BufWriter = gnu.out_new();
  out = dot_file(out, "Project: ", ".", "project");
  out = dot_file(out, "Plan:\n", ".", "plan");
  gnu.finish(out);
  return 0;
}
`
	path := filepath.Join(dir, "pinky-files.fern")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func RunPinkyByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for _, size := range []int{-2, -1, 0, 1, 255, 4095, 4096, 4097, 65535, 65536, 65537, 262145} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := t.TempDir()
			var want []byte
			for _, file := range []struct{ name, label string }{{"project", "Project: "}, {"plan", "Plan:\n"}} {
				path := filepath.Join(dir, file.name)
				if size == -2 {
					continue // Open failure suppresses the label.
				}
				want = append(want, file.label...)
				if size == -1 {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					continue // Read failure after open retains the label.
				}
				data := bytes.Repeat(all, (size+255)/256)[:size]
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				want = append(want, data...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			argv := append(append([]string{}, runner...), bin)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = dir
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, diagnostic.Bytes())
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("output differs: got %d bytes, want %d", out.Len(), len(want))
			}
			if census != nil {
				census(t, diagnostic.String())
			} else if diagnostic.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", diagnostic.Bytes())
			}
		})
	}
}
