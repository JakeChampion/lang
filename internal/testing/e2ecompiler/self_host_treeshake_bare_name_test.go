package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTreeshakeBareNameKeepsFreeFunctionOnly: a bare identifier keeps
// the free function of that name and not a method sharing it. The method
// here calls subprocess, which wasm32-wasi does not provide, so keeping it
// by name fails E066 on a program that never reaches it — what every
// wasi:http handler hit through std/async's `.handle()` method.
func TestSelfHostTreeshakeBareNameKeepsFreeFunctionOnly(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	dir := t.TempDir()
	prog := filepath.Join(dir, "main.fern")
	src := `struct D { n: i32 }
function (d: D) run(): ProcessResult { return subprocess("true", [], ""); }
function run(): i32 { return 1; }
function main(): i32 { return run(); }
`
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, "-check", "-target", "wasm32-wasi", prog, stdlib).CombinedOutput()
	if err != nil || strings.Contains(string(out), "E066") {
		t.Fatalf("the unreachable method's subprocess was counted against the target: %v\n%s", err, out)
	}
}

// A method called on a receiver of known type keeps that type's method and not
// every method sharing its name: B.get calls subprocess, which wasm32-wasi does
// not provide, so keeping it alongside A.get fails E066. A receiver bound to a
// struct literal resolves the same way, and a call through a trait-bounded type
// parameter keeps every impl's method, B's included, so it still fails, as
// does a call on a `str` view of a method declared on `string`.
func TestSelfHostTreeshakeMethodKeptByReceiver(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	const decls = `trait G { function get(self: Self): i32; }
struct A { n: i32 }
struct B { n: i32 }
impl G for A { function get(self: Self): i32 { return self.n; } }
impl G for B { function get(self: Self): i32 { return subprocess("true", [], "").exit_code; } }
function via[T: G](x: T): i32 { return x.get(); }
`
	for _, tc := range []struct {
		name, main string
		e066       bool
	}{
		{"declared", "function main(): i32 { let a: A = A { n: 1 }; return a.get(); }\n", false},
		{"literal", "function main(): i32 { let a = A { n: 1 }; return a.get(); }\n", false},
		{"bounded", "function main(): i32 { return via(A { n: 1 }); }\n", true},
		// A `str` view reaches the method declared on `string`.
		{"str-view", "function (s: string) probe(): i32 { return subprocess(\"true\", [], \"\").exit_code; }\nfunction main(): i32 { let v: str = slice_unchecked(\"ab\", 0, 1); return v.probe(); }\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(prog, []byte(decls+tc.main), 0o644); err != nil {
				t.Fatal(err)
			}
			out, _ := exec.Command(cli, "-check", "-target", "wasm32-wasi", prog, stdlib).CombinedOutput()
			if got := strings.Contains(string(out), "E066"); got != tc.e066 {
				t.Fatalf("E066 reported = %v, want %v\n%s", got, tc.e066, out)
			}
		})
	}
}
