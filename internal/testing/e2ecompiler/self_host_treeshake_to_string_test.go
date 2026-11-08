package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tree shake keeps the to_string body of a primitive type only for a
// call on that type, or for a call on a receiver of unknown type. A call on
// an i32 keeps no other primitive's body, and so no float renderer. A
// generic call keeps the bodies of the types it is called with.
func TestSelfHostTreeshakePrimitiveToString(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range []struct {
		name, src string
		kept      []string
		dropped   []string
	}{
		{
			name:    "i32",
			src:     "function main(): i32 {\n  let n = 41 + 1;\n  print(f\"n {n} \" + n.to_string());\n  return 0;\n}\n",
			kept:    []string{"__fn___primitive_i32_to_string:"},
			dropped: []string{"__fn___primitive_f64_to_string:", "__fn___primitive_u8_to_string:", "__fn___primitive_u64_to_string:"},
		},
		{
			name:    "generic",
			src:     "import \"core/cmp\";\n\nfunction show[T: cmp.Display](x: T): string {\n  return x.to_string();\n}\n\nfunction main(): i32 {\n  print(show(42) + show(2.5));\n  return 0;\n}\n",
			kept:    []string{"__fn___primitive_i32_to_string:", "__fn___primitive_f64_to_string:"},
			dropped: nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "main.s")
			if msg, err := cli.compileCmd("x86-64-linux", out, src, "-emit", "asm").CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, msg)
			}
			asm, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range c.kept {
				if !strings.Contains(string(asm), "\n"+label+"\n") {
					t.Errorf("the image has no %s", label)
				}
			}
			for _, label := range c.dropped {
				if strings.Contains(string(asm), "\n"+label+"\n") {
					t.Errorf("the image keeps %s, which no call reaches", label)
				}
			}
		})
	}
}
