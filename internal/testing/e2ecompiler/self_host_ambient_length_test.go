package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
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
