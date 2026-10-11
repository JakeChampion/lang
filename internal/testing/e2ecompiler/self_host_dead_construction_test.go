package e2ecompiler

import (
	"strings"
	"testing"
)

// A record, tuple or variant construction nothing reads is not built
// (semdead, #11912): each case returns a value the interpreter agrees on,
// runs under the sanitizer and the leak census on every target, and pins
// how many boxes it allocates. The operands of a removed construction are
// still released, a construction that is read is still built, and one whose
// type has a finalizer is built so the finalizer runs.
func TestSelfHostDeadConstruction(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name   string
		src    string
		want   int
		allocs int64
		stderr string
	}{
		{"record-unread", `struct P { x: i32, y: i32 }
@noinline function f(i: i32): i32 { let a: P = P { x: i, y: 1 }; return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 0, ""},
		// The array the construction took is still released where it died.
		{"operand-released", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
@noinline function f(i: i32): i32 { let a: P = P { x: i, xs: id([i]) }; return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 4, ""},
		// Removing the tuple leaves the record it held unread, and it goes too.
		{"nested-unread", `struct P { x: i32, y: i32 }
@noinline function f(i: i32): i32 { let t: (P, i32) = (P { x: i, y: 1 }, i); return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 0, ""},
		{"variant-unread", `enum E { A(i32, i32), B }
@noinline function f(i: i32): i32 { let e: E = A(i, 1); return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 0, ""},
		{"read-kept", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
@noinline function get(p: P): i32 { return p.x + p.xs[0]; }
@noinline function f(i: i32): i32 { let a: P = P { x: i, xs: id([i]) }; return get(a); }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 8, ""},
		{"finalizer-kept", `import "core/mem";
struct G { n: i32 }
impl mem.Drop for G { function drop(self: Self): void { eprint("dropped"); } }
@noinline function f(i: i32): i32 { let g: G = G { n: i }; return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 4, "dropped\ndropped\ndropped\ndropped\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
				if code != tc.want {
					t.Fatalf("%s: exit %d, want %d\n%s", target, code, tc.want, stderr)
				}
				allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
				if allocs != frees || live != 0 {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
				}
				if allocs != tc.allocs {
					t.Errorf("%s: allocs=%d, want %d", target, allocs, tc.allocs)
				}
				if tc.stderr != "" && !strings.HasPrefix(stderr, tc.stderr) {
					t.Errorf("%s: stderr %q, want it to start with %q", target, stderr, tc.stderr)
				}
			}
		})
	}
}
