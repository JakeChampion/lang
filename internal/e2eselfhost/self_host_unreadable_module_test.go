package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A module the self-host cannot read is reported with its path and the reason
// on one line. An unreadable module is not treated as absent: before #10142 the
// loader dropped the IoError, so an invalid-UTF-8 import printed
// "cannot read module:" with no reason, or was shadowed by a later candidate.
func TestSelfHostUnreadableModuleIsReported(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("bad.fern", "pub function f(): i32 { var s: string = \"\xb2\"; return 0; }\n")
	badEntry := write("uses_bad.fern", "import \"./bad\";\nfunction main(): i32 { return bad.f(); }\n")
	missEntry := write("uses_gone.fern", "import \"./gone\";\nfunction main(): i32 { return 0; }\n")
	// Shadowing: `std/mything` is under no stdlib root, so it resolves against
	// the importer's directory, where the direct candidate std/mything.fern is
	// present but unreadable and the flat mything.fern beside it is valid. The
	// search must stop at the first rather than compile against the second.
	if err := os.MkdirAll(filepath.Join(dir, "std"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("std", "mything.fern"), "pub function f(): i32 { var s: string = \"\xb2\"; return 1; }\n")
	write("mything.fern", "pub function f(): i32 { return 7; }\n")
	shadowEntry := write("uses_mything.fern", "import \"std/mything\";\nfunction main(): i32 { return mything.f(); }\n")
	noEntry := filepath.Join(dir, "nosuch.fern")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"invalid-utf8-module", []string{"-o", filepath.Join(dir, "a.out"), badEntry}, "fern: cannot read module " + filepath.Join(dir, "bad.fern") + ": invalid UTF-8"},
		{"missing-module", []string{"-o", filepath.Join(dir, "b.out"), missEntry}, "fern: cannot read module " + filepath.Join(dir, "gone.fern") + ": not found"},
		{"unreadable-candidate-is-not-shadowed", []string{"-o", filepath.Join(dir, "d.out"), shadowEntry}, "fern: cannot read module " + filepath.Join(dir, "std", "mything.fern") + ": invalid UTF-8"},
		{"missing-entry", []string{"-o", filepath.Join(dir, "c.out"), noEntry}, "fern: cannot read entry file " + noEntry + ": not found"},
		{"missing-entry-check", []string{"-check", noEntry}, "fern: cannot read entry file " + noEntry + ": not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(h.cli, append(c.args, h.stdlib)...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("exit 0, want a failure")
			}
			if got := strings.TrimRight(stderr.String(), "\n"); got != c.want {
				t.Errorf("stderr:\n%q\nwant:\n%q", got, c.want)
			}
		})
	}
}
