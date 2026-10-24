package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		{"operand-released", `struct P { x: i32, xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
@noinline function f(i: i32): i32 { let a: P = P { x: i, xs: id([i]) }; return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 4, ""},
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
		{"update-unread", `struct P { x: i32, y: i32 }
@noinline function mk(i: i32): P { return P { x: i, y: 1 }; }
@noinline function f(i: i32): i32 { let a: P = mk(i); let b: P = P { ...a, x: 2 }; return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 4, ""},
		{"update-read-base-overridden", `struct P { x: i32, y: i32 }
@noinline function get(p: P): i32 { return p.x + p.y; }
@noinline function f(i: i32): i32 { let a: P = P { x: i, y: 1 }; let b: P = P { ...a, x: 2, y: 1 }; return get(b) + i; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 18, 4, ""},
		{"finalizer-kept", `import "core/mem";
struct G { n: i32 }
impl mem.Drop for G { function drop(self: Self): void { eprint("dropped"); } }
@noinline function f(i: i32): i32 { let g: G = G { n: i }; return i * 2; }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; }`, 12, 4, "dropped\ndropped\ndropped\ndropped\n"},
		{"loop-carried-unread", `struct P { xs: i32[], note: string, n: i32 }
function put(p: P, i: i32, v: i32): P { return P { ...p, xs: p.xs.with(i, v) }; }
@noinline function f(k: i32): i32 {
    let p: P = P { xs: [0, 0, 0, 0], note: "carried", n: k };
    let s: i32 = 0;
    let r: i32 = 0;
    while (r < 3) {
        let i: i32 = 0;
        while (i < 4) { p = put(p, i, i + r + k); i = i + 1; }
        s = s + p.xs[r];
        r = r + 1;
    }
    return s;
}
function main(): i32 { return f(1) + f(2); }`, 21, 2, ""},
		{"loop-carried-finalizer-kept", `import "core/mem";
struct G { n: i32 }
impl mem.Drop for G { function drop(self: Self): void { eprint("dropped"); } }
@noinline function f(k: i32): i32 { let g: G = G { n: k }; let s: i32 = 0; let i: i32 = 0; while (i < 2) { g = G { ...g, n: g.n + 1 }; s = s + g.n; i = i + 1; } return s; }
function main(): i32 { return f(1) + f(2); }`, 12, 6, "dropped\ndropped\ndropped\ndropped\ndropped\ndropped\n"},
	}
	t.Run("debug-binding-eliminated", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(cases[0].src), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			flags  []string
			allocs int64
		}{{[]string{"-g"}, 0}, {nil, 0}} {
			asm := filepath.Join(t.TempDir(), "out.s")
			args := append(append([]string{}, tc.flags...), "-target", "x86-64-linux", "-emit", "asm", "-o", asm, src, cli.stdlib)
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile %v: %v\n%s", tc.flags, err, msg)
			}
			bin := filepath.Join(filepath.Dir(asm), "prog")
			if msg, err := exec.Command(cli.gcc, "-nostdlib", "-static", "-o", bin, asm).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, msg)
			}
			var eb bytes.Buffer
			run := runX86_64Bin(cli.runner, bin)
			run.Stderr = &eb
			_ = run.Run()
			allocs, frees, _ := parseLeakcheck(t, "debug-binding-eliminated", eb.String())
			if allocs != tc.allocs || frees != allocs {
				t.Errorf("flags %v: allocs=%d frees=%d, want %d balanced", tc.flags, allocs, frees, tc.allocs)
			}
		}
	})
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
