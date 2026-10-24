package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckPlaceAssignPosition(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	decls := "struct P { f: i32 }\n"
	cases := []struct{ name, body string }{
		{"index", "  a[1] = 7;\n"},
		{"index-compound", "  a[1] += 7;\n"},
		{"field", "  p.f = 7;\n"},
		{"chain-through-index", "  x = a[1] = 7;\n"},
		{"chain-into-index", "  a[0] = x = 5;\n"},
		{"chain-through-field", "  x = p.f = a[0] = 2;\n"},
	}
	diag := regexp.MustCompile(`(?m)^[^\s:]+:(\d+:\d+): error\[(\w+)\]`)
	diags := func(out []byte) []string {
		var ds []string
		for _, m := range diag.FindAllStringSubmatch(string(out), -1) {
			ds = append(ds, m[2]+"@"+m[1])
		}
		return ds
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "prog.fern")
			src := decls + "function main(): i32 {\n  let a: i32[] = [0, 0];\n  let p: P = P { f: 0 };\n  let x: i32 = 0;\n" + tc.body + "  return x + p.f + a[0];\n}\n"
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, _ := exec.Command(oracle, "-check", p).CombinedOutput()
			shOut, _ := exec.Command(cli, "-check", p, root).CombinedOutput()
			want, got := diags(goOut), diags(shOut)
			if len(want) == 0 {
				t.Fatalf("the Go checker drew no positioned diagnostic\n%s", goOut)
			}
			if !slices.Equal(got, want) {
				t.Errorf("self-host %v, Go checker %v\n--- Go ---\n%s--- self-host ---\n%s", got, want, goOut, shOut)
			}
		})
	}
}
