package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostUnusedMapKeyMethodsStayUnreachable(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `import "core/cmp";
import "core/map";
struct Unused { code: i32 }
impl cmp.Hash for Unused {
  function hash(self: Unused): i32 { subprocess("true", [], ""); return 0; }
}
function main(): i32 { MAIN }
`
	for _, tc := range []struct{ name, main string }{
		{"without map", "return 0;"},
		{"integer map", "let m: Map[i32, i32] = map_new(4); m = m.insert(1, 7); return m.get_or(1, 0) - 7;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "unused.fern")
			if err := os.WriteFile(src, []byte(strings.ReplaceAll(source, "MAIN", tc.main)), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-check", "-target", "wasm32-wasi", src, cli.stdlib)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("unreachable hash method contributed target effects: %v\n%s", err, out)
			}
		})
	}
}
