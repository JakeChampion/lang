package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	write("bad.fern", "pub function f(): i32 { let s: string = \"\xb2\"; return 0; }\n")
	badEntry := write("uses_bad.fern", "import \"./bad\";\nfunction main(): i32 { return bad.f(); }\n")
	missEntry := write("uses_gone.fern", "import \"./gone\";\nfunction main(): i32 { return 0; }\n")
	midModule := write("mid.fern", "import \"./gone\";\npub function f(): i32 { return 1; }\n")
	transEntry := write("uses_mid.fern", "import \"./mid\";\nfunction main(): i32 { return mid.f(); }\n")
	write("mything.fern", "pub function f(): i32 { let s: string = \"\xb2\"; return 1; }\n")
	write("mything.fern.md", "```fern\n<<*>>=\npub function f(): i32 { return 7; }\n```\n")
	shadowEntry := write("uses_mything.fern", "import \"./mything\";\nfunction main(): i32 { return mything.f(); }\n")
	noEntry := filepath.Join(dir, "nosuch.fern")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"invalid-utf8-module", []string{"-o", filepath.Join(dir, "a.out"), badEntry}, badEntry + ":1:1: error[E086]: cannot resolve import \"./bad\": " + filepath.Join(dir, "bad.fern") + ": invalid UTF-8"},
		{"missing-module", []string{"-o", filepath.Join(dir, "b.out"), missEntry}, missEntry + ":1:1: error[E086]: cannot resolve import \"./gone\": not found"},
		{"missing-module-check-transitive", []string{"-check", transEntry}, midModule + ":1:1: error[E086]: cannot resolve import \"./gone\": not found"},
		{"unreadable-candidate-is-not-shadowed", []string{"-o", filepath.Join(dir, "d.out"), shadowEntry}, shadowEntry + ":1:1: error[E086]: cannot resolve import \"./mything\": " + filepath.Join(dir, "mything.fern") + ": invalid UTF-8"},
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
			if got, _, _ := strings.Cut(stderr.String(), "\n"); got != c.want {
				t.Errorf("stderr:\n%q\nwant its first line:\n%q", stderr.String(), c.want)
			}
		})
	}
}
