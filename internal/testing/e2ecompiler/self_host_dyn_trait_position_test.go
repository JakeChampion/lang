package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckUnknownDynTraitPosition(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	cases := []struct{ name, src string }{
		{"param", "function f(x: dyn Nope): i32 {\n  return 0;\n}\n"},
		{"return", "function f(): dyn Nope {\n  return 0;\n}\n"},
		{"local", "function f(): i32 {\n  let x: dyn Nope = 0;\n  return 0;\n}\n"},
		{"local-after-known-param", "trait Known {}\nfunction f(k: dyn Known): i32 {\n  let x: dyn Known + Nope = k;\n  return 0;\n}\n"},
	}
	lead := regexp.MustCompile(`(?m)^[^\s:]+:(\d+):(\d+): error\[E021\]`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(tc.src+"function main(): i32 { return 0; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, _ := exec.Command(oracle, "-check", p).CombinedOutput()
			shOut, _ := exec.Command(cli, "-check", p, root).CombinedOutput()
			want := lead.FindStringSubmatch(string(goOut))
			if want == nil {
				t.Fatalf("the Go checker drew no positioned E021\n%s", goOut)
			}
			got := lead.FindStringSubmatch(string(shOut))
			if got == nil || got[1] != want[1] || got[2] != want[2] {
				t.Errorf("self-host E021 not at the Go checker's %s:%s\n--- Go ---\n%s--- self-host ---\n%s", want[1], want[2], goOut, shOut)
			}
		})
	}
}
