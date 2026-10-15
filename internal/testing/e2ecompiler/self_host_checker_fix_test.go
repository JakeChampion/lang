package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostCheckerPrintsFixes holds `-check`'s `help:` lines — the title
// of the fix the checker attaches to a diagnostic — to the Go renderer's for
// each diagnostic the Go checker attaches one to (internal/syntax/diag's
// Suggestion): E001 for a name read, called or assigned, and for a retired
// name, and E043 for a literal's field, an access's field or method, a struct
// pattern's field and a destructuring `let`'s field. A diagnostic with no fix
// prints none. The span each fix replaces is held by the code-action cases of
// self_host_lsp_refs_test.go.
func TestSelfHostCheckerPrintsFixes(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	for _, c := range []struct {
		name, src string
		help      []string
	}{
		{"read", "function main(): i32 { let count = 1; return kount; }\n", []string{"replace `kount` with `count`"}},
		{"call", "function helper(n: i32): i32 { return n; }\nfunction main(): i32 { return halper(1); }\n", []string{"replace `halper` with `helper`"}},
		{"assign", "function main(): i32 {\n  let count: i32 = 1;\n  kount = 2;\n  return count;\n}\n", []string{"replace `kount` with `count`"}},
		{"retired", "function main(): i32 { return string_from_bytes([]).len(); }\n", []string{"replace `string_from_bytes` with `string_from_bytes_unchecked` (or `std/utf8.from_bytes`, which validates and returns `Option[string]`)"}},
		{"literal-field", "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, yy: 2 };\n  return p.x;\n}\n", []string{"replace `yy` with `y`"}},
		{"member", "struct Point { x: i32, y: i32 }\nfunction (p: Point) push(n: i32): i32 { return p.x + n; }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, y: 2 };\n  return p.puzh(2) + p.yy;\n}\n", []string{"replace `puzh` with `push`", "replace `yy` with `y`"}},
		{"pattern-field", "struct P { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: P = P { x: 1, y: 2 };\n  match (p) {\n    P { x, yy } => { return x; }\n  }\n}\n", []string{"replace `yy` with `y`"}},
		{"destructure-field", "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, y: 2 };\n  let Point { x, z } = p;\n  return x;\n}\n", []string{"replace `z` with `x`"}},
		{"nothing-close", "function main(): i32 { return zzzzzz; }\n", nil},
		{"type-error", "function main(): i32 {\n  let x: i32 = \"s\";\n  return x;\n}\n", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.fern")
			writeTree(t, dir, map[string]string{"main.fern": c.src})
			out, ok := checkVerdict(exec.Command(cli, "-check", path, root))
			if ok {
				t.Fatalf("-check accepted the program:\n%s", out)
			}
			var help []string
			lines := strings.Split(out, "\n")
			for i, l := range lines {
				if title, found := strings.CutPrefix(l, "    help: "); found {
					help = append(help, title)
					// The help line follows the caret under the source line.
					if i < 2 || !strings.HasSuffix(strings.TrimSpace(lines[i-1]), "^") {
						t.Errorf("help line %q does not follow a caret:\n%s", l, out)
					}
				}
			}
			if !reflect.DeepEqual(help, c.help) {
				t.Errorf("help lines = %q, want %q\n%s", help, c.help, out)
			}
		})
	}
}
