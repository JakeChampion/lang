package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTypeArgsGrammar(t *testing.T) {
	h := selfHostCLIForHost(t)
	const prog = "enum Tag[T] { One(T), Empty }\nstruct Box[T] { v: T }\nfunction id[T](x: T): T { return x; }\nfunction main(): i32 {\n  let r: i32 = 0;\n  %s\n  return r;\n}\n"
	accepted := []string{
		"r = id[i32](0);",
		"let b: Box[i32] = Box[i32] { v: 0 }; r = b.v;",
		"let t: Tag[i32] = Tag.One(0); match (t) { Tag.One(n) => { r = n; }, Tag.Empty => {} }",
		"let f: (i32) => i32 = id[(i32) => i32]((q: i32) => q); r = f(0);",
		"let p: (i32, string) = id[(i32, string)]((0, \"a\")); r = p.0;",
		"let fs: ((i32) => i32)[] = [(q: i32) => q]; r = fs[(r)](0);",
	}
	rejected := []string{
		"let t: Tag[i32] = Tag[i32].One(1);",
		"let t: Tag[i32] = Tag[i32].Empty;",
		"let f: (i32) => i32 = id[i32];",
		"r = Box[i32].v;",
	}
	dir := t.TempDir()
	check := func(bin, src string, extra ...string) (string, error) {
		out, err := exec.Command(bin, append([]string{"-check", src}, extra...)...).CombinedOutput()
		return string(out), err
	}
	for i, stmt := range accepted {
		src := filepath.Join(dir, fmt.Sprintf("ok%d.fern", i))
		if err := os.WriteFile(src, []byte(fmt.Sprintf(prog, stmt)), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := check(h.cli, src, h.stdlib); err != nil {
			t.Errorf("self-host rejects `%s`: %v\n%s", stmt, err, out)
		}
		if out, err := check(h.native, src); err != nil {
			t.Errorf("native rejects `%s`: %v\n%s", stmt, err, out)
		}
	}
	for i, stmt := range rejected {
		src := filepath.Join(dir, fmt.Sprintf("bad%d.fern", i))
		if err := os.WriteFile(src, []byte(fmt.Sprintf(prog, stmt)), 0o644); err != nil {
			t.Fatal(err)
		}
		col := 2 + strings.LastIndex(stmt, "[i32]") + 2
		want := fmt.Sprintf(":6:%d: error[P001]", col)
		if out, err := check(h.cli, src, h.stdlib); err == nil || !strings.Contains(out, want) {
			t.Errorf("self-host on `%s`: want %q, got (%v)\n%s", stmt, want, err, out)
		}
		if out, err := check(h.native, src); err == nil || !strings.Contains(out, want) {
			t.Errorf("native on `%s`: want %q, got (%v)\n%s", stmt, want, err, out)
		}
	}
}
