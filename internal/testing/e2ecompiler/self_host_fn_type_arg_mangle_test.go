package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A function type as a generic's type argument is part of the instance's
// symbol. sanitize_key passed its `=>` through, so `G[(i32) => i32]` emitted
// labels GNU as rejected (the self-host's own assembler took them); the arrow
// is now an `fto` token that take_piece reads back as part of one argument. Found by fernsmith's generic
// struct and enum productions.
var fnTypeArgMangleCases = []struct {
	name, src, want string
}{
	{"one-param", `import "std/i32";
struct G[T] { v: T, n: i32 }
function (g: G[T]) get(): T { return g.v; }
function (g: G[T]) bump(k: i32): G[T] { return G { ...g, n: g.n + k }; }
function mk[T](v: T, n: i32): G[T] { return G { v: v, n: n }; }
function main(): i32 {
    let f: (i32) => i32 = ((q: i32) => q * 5);
    let a: G[(i32) => i32] = mk(f, 3);
    let b: G[(i32) => i32] = a.bump(2);
    print("r=" + (b.get()(2) + b.n).to_string());
    return 0;
}
`, "r=15"},
	{"two-params-and-tuple", `import "std/i32";
struct G[T] { v: T, n: i32 }
function (g: G[T]) get(): T { return g.v; }
function main(): i32 {
    let f: (i32, i64) => i64 = ((q: i32, r: i64) => r + (q as i64));
    let a: G[(i32, i64) => i64] = G { v: f, n: 3 };
    let t: ((i32) => i32, i32) = (((q: i32) => q + 1), 5);
    let b: G[((i32) => i32, i32)] = G { v: t, n: 4 };
    print("r=" + ((a.get()(1, 2) as i32) + b.get().0(b.get().1)).to_string());
    return 0;
}
`, "r=9"},
	{"explicit-call-type-arg", `import "std/i32";
function id[T](x: T): T { return x; }
function main(): i32 {
    let f: (i32) => i32 = id[(i32) => i32](((q: i32) => q * 3));
    let t: (i32, string) = id[(i32, string)]((4, "a"));
    print("r=" + (f(t.0)).to_string() + t.1);
    return 0;
}
`, "r=12a"},
}

func TestSelfHostFnTypeArgMangle(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, c := range fnTypeArgMangleCases {
		t.Run(c.name, func(t *testing.T) {
			if got := interpStdout(t, interpBin, c.src); got != c.want {
				t.Fatalf("interpreter = %q, want %q", got, c.want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
			t.Run("x86-64-gnu-as", func(t *testing.T) {
				if stdout := gnuAsRun(t, gcc, selfHostBin, stdlibRoot, c.src); stdout != c.want {
					t.Fatalf("stdout = %q, want %q", stdout, c.want)
				}
			})
		})
	}
}

// gnuAsRun compiles src to x86-64 assembly, assembles and links it with gcc
// as the differential legs do, and returns the program's stdout.
func gnuAsRun(t *testing.T, gcc, fernBin, stdlibRoot, src string) string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	asm := filepath.Join(dir, "prog.s")
	bin := filepath.Join(dir, "prog")
	if msg, err := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asm, in, stdlibRoot).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, msg)
	}
	if msg, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asm, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("GNU as rejected the assembly: %v\n%s", err, msg)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimSpace(string(out))
}
