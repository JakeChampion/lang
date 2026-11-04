package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostAmbientBuiltinLength(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, body := range []string{
		`return "abc".len();`,
		`let s: string = "abc"; return s.len();`,
		`let xs: i32[] = [1, 2]; return xs.len();`,
		`return [1, 2].len();`,
	} {
		t.Run(body, func(t *testing.T) {
			src := `import "std/platform";
struct Unrelated { n: i32 }
function (u: Unrelated) len(): i32 { return args().len(); }
function measured(plat: platform.Platform): i32 { ` + body + ` }
function main(): i32 { return 0; }`
			path := filepath.Join(t.TempDir(), "length.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range []*exec.Cmd{
				exec.Command(oracle, "-check", path),
				exec.Command(cli, "-check", path, stdlib),
			} {
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
				}
			}
		})
	}
}

func TestSelfHostAmbientBuiltinJoin(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, tc := range []struct {
		name       string
		body       string
		wantEffect bool
		extra      string
	}{
		{"literal", `return ["a", "b"].join(",");`, false, ""},
		{"typed local", `let xs: string[] = ["a", "b"]; return xs.join(",");`, false, ""},
		{"inferred local", `let xs = names(); return xs.join(",");`, false, ""},
		{"call result", `return names().join(",");`, false, ""},
		{"user method", `let u = Unrelated { n: 1 }; return u.join(",");`, true, ""},
		{"user array method", `return [1, 2].join(",");`, true,
			`function (xs: i32[]) join(sep: string): string { eprint(sep); return "array"; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `import "std/platform";
` + tc.extra + `
struct Unrelated { n: i32 }
function (u: Unrelated) join(sep: string): string { eprint(sep); return "user"; }
function names(): string[] { return ["a", "b"]; }
function measured(plat: platform.Platform): string { ` + tc.body + ` }
function main(): i32 { return 0; }`
			path := filepath.Join(t.TempDir(), "join.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range []*exec.Cmd{
				exec.Command(oracle, "-check", path),
				exec.Command(cli, "-check", path, stdlib),
			} {
				out, err := cmd.CombinedOutput()
				if tc.wantEffect {
					if err == nil || !strings.Contains(string(out), "E080") {
						t.Fatalf("%s: expected E080, got %v\n%s", cmd.Path, err, out)
					}
				} else if err != nil {
					t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
				}
			}
		})
	}
}
