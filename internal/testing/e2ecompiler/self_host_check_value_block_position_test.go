package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostCheckValueBlockPosition holds the position `-check` prints for
// a value if, match or block refused as an argument to the Go checker's: the
// expression's opening token, not none.
func TestSelfHostCheckValueBlockPosition(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	decls := "enum Bx[T] { Full(T), Empty }\nfunction get(b: Bx[i64]): i32 { return 0; }\n"
	cases := []struct{ name, body string }{
		{"if", "function f(c: boolean, n: i32): i32 { return get(if (c) { Full(n) } else { Full(1) }); }\n"},
		{"if-literal-first", "function f(c: boolean, n: i32): i32 { return get(if (c) { Full(1) } else { Full(n) }); }\n"},
		{"match", "function f(k: i32, n: i32): i32 { return get(match (k) { 0 => Full(n), _ => Full(1) }); }\n"},
		{"block", "function f(n: i32): i32 { return get({ let m: i32 = n; Full(m) }); }\n"},
	}
	lead := regexp.MustCompile(`(?m)^[^\s:]+:(\d+):(\d+): error\[E038\]`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(decls+tc.body+"function main(): i32 { return 0; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, _ := exec.Command(oracle, "-check", p).CombinedOutput()
			shOut, _ := exec.Command(cli, "-check", p, root).CombinedOutput()
			want := lead.FindStringSubmatch(string(goOut))
			if want == nil {
				t.Fatalf("the Go checker drew no positioned E038\n%s", goOut)
			}
			got := lead.FindStringSubmatch(string(shOut))
			if got == nil || got[1] != want[1] || got[2] != want[2] {
				t.Errorf("self-host E038 not at the Go checker's %s:%s\n--- Go ---\n%s--- self-host ---\n%s", want[1], want[2], goOut, shOut)
			}
		})
	}
}
