package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host checker gives E084 for the main of an imported module, in
// the file of that module, as the Go checker does (#14286). A module with a
// valid main checks clean.
func TestSelfHostMainInImportedModule(t *testing.T) {
	for _, tc := range []struct {
		name, lib, want string
	}{
		{"parameter", "function main(n: i32): void {}\npub function f(): i32 { return 1; }\n", "lib.fern:1:1: error[E084]: main takes no parameters"},
		{"valid", "function main(): i32 { return 0; }\npub function f(): i32 { return 1; }\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(tc.lib), 0o644); err != nil {
				t.Fatal(err)
			}
			app := filepath.Join(dir, "app.fern")
			if err := os.WriteFile(app, []byte("import \"./lib\";\nfunction main(): i32 { return lib.f() - 1; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-check", app, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("fern -check: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("fern -check: got %v\n%s\nwant %q", err, out, tc.want)
			}
		})
	}
}
