package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckPlaceAssignPosition(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	decls := "import \"core/map\";\nstruct In { q: i32 }\nstruct P { f: i32, i: In, w: i64 }\nstruct Box[T] { item: T }\n"
	cases := []struct{ name, body string }{
		{"index", "  a[1] = 7;\n"},
		{"index-compound", "  a[1] += 7;\n"},
		{"field", "  p.f = 7;\n"},
		{"chain-through-index", "  x = a[1] = 7;\n"},
		{"chain-into-index", "  a[0] = x = 5;\n"},
		{"chain-through-field", "  x = p.f = a[0] = 2;\n"},
		{"field-value", "  p.f = \"s\";\n"},
		{"nested-field-value", "  p.i.q = true;\n"},
		{"field-struct-value", "  p.i = 4;\n"},
		{"index-value", "  a[0] = \"s\";\n"},
		{"nested-index-value", "  g[0][0] = \"s\";\n"},
		{"tuple-field-value", "  t.1 = 3;\n"},
		{"field-literal-range", "  p.f = 5000000000;\n"},
		{"index-literal-range", "  u[0] = 300;\n"},
		{"field-compound-value", "  p.f += \"s\";\n"},
		{"chain-field-value", "  x = p.f = \"s\";\n"},
		{"unknown-field-compound", "  p.zz += 1;\n"},
		{"bad-index-compound", "  a[\"k\"] += 1;\n"},
		{"undefined-name-compound", "  zz += 1;\n"},
		{"map-index", "  m[\"k\"] = \"v\";\n"},
		{"generic-field-value", "  b.item = \"s\";\n"},
		{"field-literal-settles", "  p.w = 2;\n"},
		{"index-literal-settles", "  ws[0] = 2;\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "prog.fern")
			src := decls + "function main(): i32 {\n  let a: i32[] = [0, 0];\n  let g: i32[][] = [[0]];\n  let u: u8[] = [0];\n" +
				"  let t: (i32, string) = (0, \"\");\n  let m: Map[string, i32] = map_new(8);\n" +
				"  let b: Box[i64] = Box[i64] { item: 1 };\n  let ws: i64[] = [1];\n" +
				"  let p: P = P { f: 0, i: In { q: 0 }, w: 0 };\n  let x: i32 = 0;\n" + tc.body + "  return x + p.f + a[0];\n}\n"
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, _ := exec.Command(oracle, "-check", p).CombinedOutput()
			shOut, _ := exec.Command(cli, "-check", p, root).CombinedOutput()
			if !strings.Contains(string(goOut), "error[") {
				t.Fatalf("the Go checker drew no diagnostic\n%s", goOut)
			}
			if string(shOut) != string(goOut) {
				t.Errorf("the two checkers report differently\n--- Go ---\n%s--- self-host ---\n%s", goOut, shOut)
			}
		})
	}
}
