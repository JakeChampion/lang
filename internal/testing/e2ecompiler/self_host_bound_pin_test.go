package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostBoundPinRefused(t *testing.T) {
	h := selfHostCLIForHost(t)
	const decls = "trait P { type Item; function get(self: Self): Self::Item; }\n"
	cases := []struct {
		name, src string
		ok        bool
	}{
		{"pin in a bound", decls + "function g[H: P[Item = i32]](h: H): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", false},
		{"pin in a dyn", decls + "function g(d: dyn P[Item = i32]): i32 { return d.get(); }\nfunction main(): i32 { return 0; }\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, run := range []struct {
				who  string
				args []string
			}{
				{"self-host", []string{h.cli, "-check", src, h.stdlib}},
				{"native", []string{h.native, "-check", src}},
			} {
				out, err := exec.Command(run.args[0], run.args[1:]...).CombinedOutput()
				refused := err != nil && strings.Contains(string(out), `error[P001]`) && strings.Contains(string(out), `expected "]", got "="`)
				if refused == tc.ok {
					t.Errorf("%s: refused at the parse = %v, want %v\n%s", run.who, refused, !tc.ok, out)
				}
			}
		})
	}
}
