package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The codes harness does not load core/cmp, so these cases need the CLI.
func TestSelfHostEqOwnedAndView(t *testing.T) {
	h := selfHostCLIForHost(t)
	cases := []struct {
		name, params string
		refused      bool
	}{
		{"owned-and-view", "a: u8[], b: [u8]", true},
		{"nested-owned-and-view", "a: u8[][], b: [u8][]", true},
		{"view-and-view", "a: [u8], b: [u8]", true},
		{"owned-and-owned", "a: u8[], b: u8[]", false},
		{"nested-owned-and-owned", "a: u8[][], b: u8[][]", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "import \"core/cmp\";\nfunction f(" + c.params + "): boolean { return a == b; }\nfunction main(): i32 { return 0; }\n"
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(h.cli, "-check", path, h.stdlib).CombinedOutput()
			refused := strings.Contains(string(out), "error[E041]: cannot compare")
			if refused != c.refused || (err != nil) != c.refused {
				t.Fatalf("-check of f(%s): refused=%v err=%v, want refused=%v\n%s", c.params, refused, err, c.refused, out)
			}
		})
	}
}
