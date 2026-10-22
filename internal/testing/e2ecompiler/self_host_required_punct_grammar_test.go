package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The punctuation and keywords a construct's grammar requires are required by
// both front ends (#12659): `match (e) {`, `if (c)`, `while (c)`, the `=>` of a
// match arm, the `else` of an if-expression, the `=` of an `if let` or a
// destructure, and the `= E else` of a let-else. The self-host reports each
// missing one where native does, in native's words.
func TestSelfHostRequiredPunctGrammar(t *testing.T) {
	h := selfHostCLIForHost(t)
	const prelude = "struct P { x: i32 }\nconst K: i32 = 1;\nfunction main(): i32 {\n  let n: i32 = 3;\n  let o: Option[i32] = Some(3);\n  let t: (i32, i32) = (1, 2);\n  let q: P = P { x: 1 };\n"
	const postlude = "\n  return 0;\n}\n"
	accepted := []string{
		"let r: i32 = match (n) { _ => 0 };",
		"match (n) { 3 => { n = 1; }, _ => { n = 2; } }",
		"match (o) { Some(v) => { n = v; }, None => { n = 0; } }",
		"let r: i32 = if (n == 3) { 1 } else { 2 };",
		"if (n == 3) { n = 1; }",
		"while (n < 3) { n = n + 1; }",
		"if let Some(v) = o { n = v; }",
		"if let (a, b) = t { n = a + b; }",
		"if let 3 = n { n = 1; }",
		"if let P { x } = q { n = x; }",
		"let (a, b) = (1, 2);",
		"let P { x } = q;",
		"for (let i = 0; i < 3; i = i + 1) { n = n + i; }",
		"let Some(v) = o else { return 1; };",
		"let 3 = n else { return 1; };",
	}
	// Each rejected statement is the whole of one program's body line; native's
	// first P001 names the position and the words the self-host must match.
	// The two rows that close `main` early put a declaration between it and a
	// second function the postlude finishes.
	rejected := []string{
		"return 0;\n}\nconst J: i32 2;\nfunction g(): i32 {",
		"return 0;\n}\ntrait H { type Item; }\nimpl H for P { type Item i32; }\nfunction g(): i32 {",
		"let r: i32 = match n { _ => 0 };",
		"let r: i32 = match n) { _ => 0 };",
		"let r: i32 = match (n) _ => 0 };",
		"let r: i32 = match (n) { _ 0 };",
		"let r: i32 = match (n) { 3 0 };",
		"let r: i32 = match (o) { Some(v) => v _ => 0 };",
		"let r: i32 = match (q) { P { x } x };",
		"match n { _ => { n = 1; } }",
		"match n) { _ => { n = 1; } }",
		"match (n) _ => { n = 1; } }",
		"match (n) { _ { n = 1; } }",
		"match (n) { 3 { n = 1; } }",
		"match (t) { (1, 2) { n = 1; } }",
		"let r: i32 = if n == 3) { 1 } else { 2 };",
		"let r: i32 = if (n == 3 { 1 } else { 2 };",
		"let r: i32 = if (n == 3) { 1 } { 2 };",
		"if n == 3) { n = 1; }",
		"if (n == 3 { n = 1; }",
		"while n < 3) { n = n + 1; }",
		"while (n < 3 { n = n + 1; }",
		"for (let i = 0; i < 3; i = i + 1 { n = n + i; }",
		"if let Some(v) o { n = v; }",
		"if let (a, b) t { n = a + b; }",
		"if let 3 n { n = 1; }",
		"if let P { x } q { n = x; }",
		"let (a, b) (1, 2);",
		"let P { x } q;",
		"let Some(v) o else { return 1; };",
		"let Some(v) = o;",
		"let Some(v) = o { return 1; };",
		"let 3 = n;",
		"let 3 = n { return 1; };",
	}
	dir := t.TempDir()
	check := func(bin, src string, extra ...string) (string, error) {
		out, err := exec.Command(bin, append([]string{"-check", src}, extra...)...).CombinedOutput()
		return string(out), err
	}
	for i, stmt := range accepted {
		src := filepath.Join(dir, fmt.Sprintf("ok%d.fern", i))
		if err := os.WriteFile(src, []byte(prelude+"  "+stmt+postlude), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := check(h.cli, src, h.stdlib); err != nil {
			t.Errorf("self-host rejects `%s`: %v\n%s", stmt, err, out)
		}
		if out, _ := check(h.native, src); strings.Contains(out, "error[P001]") {
			t.Errorf("native does not parse `%s`:\n%s", stmt, out)
		}
	}
	nativeP001 := regexp.MustCompile(`:(\d+:\d+): error\[P001\]: (.*)`)
	for i, stmt := range rejected {
		src := filepath.Join(dir, fmt.Sprintf("bad%d.fern", i))
		if err := os.WriteFile(src, []byte(prelude+"  "+stmt+postlude), 0o644); err != nil {
			t.Fatal(err)
		}
		nout, nerr := check(h.native, src)
		m := nativeP001.FindStringSubmatch(nout)
		if nerr == nil || m == nil {
			t.Errorf("native on `%s`: want a P001, got (%v)\n%s", stmt, nerr, nout)
			continue
		}
		want := regexp.MustCompile(`:` + m[1] + `: error\[P001\]: [^\n]*: ` + regexp.QuoteMeta(m[2]))
		out, err := check(h.cli, src, h.stdlib)
		if err == nil || !want.MatchString(out) {
			t.Errorf("self-host on `%s`: want %q at %s as native reports, got (%v)\n%s", stmt, m[2], m[1], err, out)
		}
	}
	// A plain binding always binds, so an `else` after it is refused where
	// native refuses it, in the same words (native gives that error no code).
	src := filepath.Join(dir, "plain_else.fern")
	if err := os.WriteFile(src, []byte(prelude+"  let w = o else { return 1; };"+postlude), 0o644); err != nil {
		t.Fatal(err)
	}
	const alwaysBinds = "`let w` always binds, so it takes no `else`; a pattern that can miss goes after `let`, e.g. `let Some(w) = …`"
	if out, err := check(h.native, src); err == nil || !strings.Contains(out, ":8:13: error: "+alwaysBinds) {
		t.Errorf("native on a plain binding's else: got (%v)\n%s", err, out)
	}
	if out, err := check(h.cli, src, h.stdlib); err == nil || !strings.Contains(out, ":8:13: error[P001]: in fn 'main': "+alwaysBinds) {
		t.Errorf("self-host on a plain binding's else: got (%v)\n%s", err, out)
	}
}
