package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostOwnInTypePositionCheck(t *testing.T) {
	driverBin := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	nativeBin := e2eharness.BuildLangBinForInterp(t)
	const main = "\nfunction main(): i32 { return 0; }\n"
	cases := []struct {
		name, src string
		ok        bool
	}{
		{"handle to a resource", "resource Conn;\nfunction peek(c: borrow Conn): i32 { return 0; }\nfunction close(c: own Conn): i32 { return peek(c); }", true},
		{"consuming parameter", "function total(own xs: i32[]): i32 { return xs.len(); }", true},
		{"consuming function-type slot", "function apply(f: (own i32[]) => i32): i32 { return f([1]); }", true},
		{"consuming slot over a primitive", "function apply(f: (own string) => i32): i32 { let text = \"ab\" + \"cd\"; return f(text); }", true},
		{"consuming slot over a primitive is not a handle", "function id(x: i32): i32 { return x; }\nfunction apply(f: (own string) => i32): i32 { return 0; }\nfunction g(): i32 { return apply(id); }", false},
		{"borrow in a function-type slot", "function apply(f: (borrow i32) => i32): i32 { return f(1); }", false},
		{"bare word in a let type", "function f(): i32 { let x: own = 1; return 0; }", false},
		{"bare word in a parameter type", "function f(xs: own): i32 { return 0; }", false},
		{"bare word with an array suffix", "function f(): i32 { let x: own[] = []; return 0; }", false},
		{"own in a let type", "function f(): i32 { let x: own i32[] = [1]; return x.len(); }", false},
		{"own in a parameter type", "function f(xs: own i32[]): i32 { return xs.len(); }", false},
		{"borrow before a builtin", "function f(s: borrow string): i32 { return s.len(); }", false},
		{"own in a tuple element", "function f(p: (own i32[], i32)): i32 { return 0; }", false},
		{"own in a type argument", "function f(o: Option[own i32]): i32 { return 0; }", false},
		{"own in a nested function-type argument", "function f(o: Option[(own) => i32]): i32 { return 0; }", false},
		{"consuming slot in a type argument", "function f(o: Option[(own i32[]) => i32]): i32 { return 0; }", true},
		{"own in a view", "function f(v: [own i32]): i32 { return 0; }", false},
		{"own in a function-type result", "function f(g: (i32) => own string): i32 { return 0; }", false},
		{"own before a struct", "struct Foo { v: i32 }\nfunction f(x: own Foo): i32 { return 0; }", false},
		{"borrow before a type parameter", "function f[T](x: borrow T): i32 { return 0; }", false},
		{"own in a struct field", "struct Foo { v: i32 }\nstruct S { h: own Foo }", false},
		{"own in an enum payload", "struct Foo { v: i32 }\nenum E { A(own Foo) }", false},
		{"pub resource", "pub resource Conn;", false},
		{"package-scoped resource", "pub(package) resource Conn;", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src+main), 0o644); err != nil {
				t.Fatal(err)
			}
			nativeOut, nativeErr := exec.Command(nativeBin, "-check", src).CombinedOutput()
			shOut, shErr := exec.Command(driverBin, "-check", src, stdlib).CombinedOutput()
			if (nativeErr == nil) != tc.ok {
				t.Errorf("Go checker accepts = %v, want %v\n%s", nativeErr == nil, tc.ok, nativeOut)
			}
			if (shErr == nil) != tc.ok {
				t.Errorf("self-host checker accepts = %v, want %v\n%s", shErr == nil, tc.ok, shOut)
			}
			if got, want := diagHeaders(string(shOut)), diagHeaders(string(nativeOut)); got != want {
				t.Errorf("diagnostics differ\nself-host:\n%s\nnative:\n%s", got, want)
			}
		})
	}
}

var diagHeaderRe = regexp.MustCompile(`(?m)^\S*?:(\d+:\d+): error\[(\w+)\]: (?:at top level: |in fn '[^']*': )?(.*)$`)

func diagHeaders(out string) string {
	var lines []string
	for _, m := range diagHeaderRe.FindAllStringSubmatch(out, -1) {
		lines = append(lines, m[1]+" ["+m[2]+"] "+m[3])
	}
	return strings.Join(lines, "\n")
}
