package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

var firstP001 = regexp.MustCompile(`:(\d+:\d+): error\[P001\]: (?:at top level: |in fn '[^']*': )?(.*)`)

func TestSelfHostAttributeGrammar(t *testing.T) {
	h := selfHostCLIForHost(t)
	const main = "\nfunction main(): void {}\n"
	accepted := []string{
		"import \"core/cmp\";\n@derive(cmp.Eq, cmp.Display)\nstruct S { a: i32 }" + main,
		"import \"core/cmp\";\n@derive(cmp.Eq)\nenum E { A, B }" + main,
		"import \"core/cmp\";\n@derive(cmp.Eq)\npub opaque struct S { a: i32 }" + main,
		"@try\nenum R { Good(i32), Bad(string) }" + main,
		"@try\npub enum R { Good(i32), Bad(string) }" + main,
		"@must_consume\nstruct S { a: i32 }" + main,
		"@must_consume\npub(package) enum E { A, B }" + main,
		"@inline\nfunction f(): i32 { return 1; }" + main,
		"@noinline\npub function f(): i32 { return 1; }" + main,
		"@inline\nfip function f(x: i32): i32 { return x; }" + main,
		"@import(\"a:b/c\", \"d\")\nfunction f(): void;" + main,
		"@import(\"a:b/c\", \"d\")\nresource R;" + main,
		"@export(\"a:b/c\", \"d\")\nfunction f(): void {}" + main,
	}
	rejected := []string{
		"@@@",
		"@\nstruct S { a: i32 }",
		"function main(): void {}\n@",
		"@derive(Eq)\nfunction f(): void {}",
		"@(Eq)\nstruct S { a: i32 }",
		"@derive Eq\nstruct S { a: i32 }",
		"@derive()\nstruct S { a: i32 }",
		"@derive(Eq\nstruct S { a: i32 }",
		"@derive(Eq,)\nstruct S { a: i32 }",
		"@derive(1)\nstruct S { a: i32 }",
		"@if\nstruct S { a: i32 }",
		"@struct\nstruct S { a: i32 }",
		"@frob\nstruct S { a: i32 }",
		"@frob(1)\nstruct S { a: i32 }",
		"@must_consume(1)\nstruct S { a: i32 }",
		"@import\nfunction f(): void;",
		"@import(\"a:b/c\")\nfunction f(): void;",
		"@import(1, \"d\")\nfunction f(): void;",
		"@import(\"a:b/c\", \"d\"\nfunction f(): void;",
		"@export\nfunction f(): void {}",
		"@derive(Eq)",
		"@derive(Eq)\nconst X: i32 = 1;",
		"@derive(Eq)\ntrait T {}",
		"struct S { a: i32 }\ntrait T {}\n@derive(Eq)\nimpl T for S {}",
		"@derive(Eq)\ntype U = i32;",
		"@derive(Eq)\nresource R;",
		"@derive(Eq)\nimport \"core/cmp\";",
		"@derive(Eq)\npub use \"./helper\".{ answer };",
		"@derive(Eq)\n@must_consume\nstruct S { a: i32 }",
		"@must_consume\nfunction f(): void {}",
		"@must_consume\nconst X: i32 = 1;",
		"@must_consume\nresource R;",
		"@must_consume",
		"@try\nstruct S { a: i32 }" + main,
		"@try\nfunction f(): void {}",
		"@try\npub function f(): void {}",
		"@try\nconst X: i32 = 1;",
		"@try",
		"@inline\nstruct S { a: i32 }",
		"@noinline\nenum E { A, B }",
		"@inline\nresource R;",
		"@inline\n@noinline\nfunction f(): void {}",
		"@inline",
		"@import(\"a:b/c\", \"d\")\nstruct S { a: i32 }",
		"@export(\"a:b/c\", \"d\")\nstruct S { a: i32 }",
		"@export(\"a:b/c\", \"d\")\nresource R;",
		"pub @derive(Eq) struct S { a: i32 }",
		"pub let x: i32 = 1;",
		"pub(foo) function f(): void {}",
		"pub(package function f(): void {}",
		"function main(): void {\n  @derive(Eq)\n  let x: i32 = 1;\n}",
	}
	dir := t.TempDir()
	check := func(bin, src string, extra ...string) (string, error) {
		out, err := exec.Command(bin, append([]string{"-check", src}, extra...)...).CombinedOutput()
		return string(out), err
	}
	for i, prog := range accepted {
		src := filepath.Join(dir, fmt.Sprintf("ok%d.fern", i))
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := check(h.native, src); err != nil {
			t.Errorf("native rejects %q: %v\n%s", prog, err, out)
		}
		if out, err := check(h.cli, src, h.stdlib); err != nil {
			t.Errorf("self-host rejects %q: %v\n%s", prog, err, out)
		}
	}
	for i, prog := range rejected {
		src := filepath.Join(dir, fmt.Sprintf("bad%d.fern", i))
		if err := os.WriteFile(src, []byte(prog+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		nout, nerr := check(h.native, src)
		want := firstP001.FindStringSubmatch(nout)
		if nerr == nil || want == nil {
			t.Errorf("native on %q: want a P001, got (%v)\n%s", prog, nerr, nout)
			continue
		}
		sout, serr := check(h.cli, src, h.stdlib)
		got := firstP001.FindStringSubmatch(sout)
		if serr == nil || got == nil || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("self-host on %q: want %s %q (native), got (%v)\n%s", prog, want[1], want[2], serr, sout)
		}
	}
}

func TestSelfHostFmtWriteRefusesMalformedAttribute(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "attr.fern")
	if err := os.WriteFile(src, []byte("@@@\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-fmt", "-w", src).CombinedOutput()
	if err == nil {
		t.Errorf("-fmt -w accepted `@@@`:\n%s", out)
	}
	if b, rerr := os.ReadFile(src); rerr != nil || string(b) != "@@@\n" {
		t.Errorf("-fmt -w rewrote the file to %q (%v)", b, rerr)
	}
}
