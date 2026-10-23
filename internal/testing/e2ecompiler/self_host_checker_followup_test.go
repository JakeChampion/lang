package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckerFollowupPositions(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	native := buildLangBinForInterp(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	cases := []struct{ name, source string }{
		{"raw-data", `function main(): i32 { return __raw_data("a") as i32; }`},
		{"raw-store", `function main(): i32 { __raw_store8(0 as usize, 0, 0); return 0; }`},
		{"unbound-result", `function mk[T](): T[] { return []; }
function main(): i32 { let z = mk(); return 0; }`},
		{"literal-pattern", `function main(): i32 { match (true) { 1 => { return 1; }, _ => { return 0; } } }`},
		{"inferred-try", `function f(x: Option[i32]) { let y = x?; }
function main(): i32 { return 0; }`},
		{"minus-string", `function main(): i32 { let x = -"s"; return 0; }`},
		{"plus-string", `function main(): i32 { let x = "s" + 1; return 0; }`},
		{"mixed-float", `function main(): i32 { let x = 1f32 + 1f64; return 0; }`},
		{"if-branches", `function main(): i32 { let x = if (true) { 1 } else { "s" }; return 0; }`},
		{"match-arms", `function main(): i32 { let x = match (true) { true => 1, false => "s" }; return 0; }`},
		{"block-value", `function main(): i32 { let x: i32 = { let a: i32 = 1; a + 1; }; return 0; }`},
		{"dyn-parameter", `trait Bad { function copy(self: Self): Self; }
function f(x: dyn Bad): i32 { return 0; }
function main(): i32 { return 0; }`},
		{"dyn-return", `trait Bad { function copy(self: Self): Self; }
function f(): dyn Bad { return 0; }
function main(): i32 { return 0; }`},
		{"dyn-local", `trait Bad { function copy(self: Self): Self; }
function main(): i32 { let d: dyn Bad = 0; return 0; }`},
		{"dyn-repeated", `trait Bad { function copy(self: Self): Self; }
function f(x: dyn Bad, y: dyn Bad): i32 { return 0; }
function main(): i32 { let d: dyn Bad = 0; return 0; }`},
		{"dyn-self-argument", `trait Bad { function copy(self: Self, other: Self): i32; }
function f(x: dyn Bad): i32 { return 0; }
function main(): i32 { return 0; }`},
		{"use-noncallback", `function take(n: i32): i32 { return n; }
function main(): i32 { use x <- take(1); return 0; }`},
		{"use-empty-callback", `function take(f: () => i32): i32 { return f(); }
function main(): i32 { use x <- take(); return 0; }`},
		{"use-nonsignature", `function main(): i32 { let take = 1; use x <- take(); return 0; }`},
	}
	headers := func(t *testing.T, cmd *exec.Cmd) string {
		t.Helper()
		out, err := cmd.CombinedOutput()
		matches := diagHeader.FindAllStringSubmatch(string(out), -1)
		if err == nil || len(matches) == 0 {
			t.Fatalf("expected coded diagnostics: %v\n%s", err, out)
		}
		lines := make([]string, 0, len(matches))
		for _, m := range matches {
			lines = append(lines, m[1])
		}
		return strings.Join(lines, "\n")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "case.fern")
			if err := os.WriteFile(path, []byte(tc.source+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			want := headers(t, exec.Command(native, "-check", path))
			got := headers(t, exec.Command(cli, "-check", path, root))
			if got != want {
				t.Errorf("self-host:\n%s\noracle:\n%s", got, want)
			}
		})
	}
}
