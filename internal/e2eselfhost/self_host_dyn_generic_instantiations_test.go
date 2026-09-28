package e2eselfhost

import (
	"path/filepath"
	"strings"
	"testing"
)

// A dyn over one instantiation of a generic trait dispatches to the impls of
// that instantiation only. Two impls of Pair at different arguments give their
// methods different signatures, and taking both as arms of either dyn made the
// typed lowering refuse the program as disagreeing implementations (#10596).

// dynGenericTwoInstantiationsSrc: the smallest shape, a method whose result is
// the trait's second argument.
const dynGenericTwoInstantiationsSrc = `import "std/i32";
trait Pair[A, B] { function snd(self: Self): B; }
struct P { s: string }
struct Q { b: u32 }
impl Pair[i32, u32] for P { function snd(self: Self): u32 { return self.s.len() as u32; } }
impl Pair[i32, i32] for Q { function snd(self: Self): i32 { return self.b as i32; } }
function main(): i32 {
    var d: dyn Pair[i32, u32] = P { s: "abc" };
    var e: dyn Pair[i32, i32] = Q { b: 4 as u32 };
    print(((d.snd() as i32) + e.snd()).to_string());
    return 0;
}
`

// dynGenericTwoInstantiationsMapsSrc: both dyn types held in routed maps,
// iterated, and in a dyn array beside a bool array.
const dynGenericTwoInstantiationsMapsSrc = `import "core/map";
import "std/i32";
trait Pair[A, B] { function fst(self: Self): A; function snd(self: Self): B; }
struct P { a: i32, s: string }
struct Q { t: string, b: u32 }
impl Pair[i32, u32] for P { function fst(self: Self): i32 { return self.a; } function snd(self: Self): u32 { return self.s.len() as u32; } }
impl Pair[i32, i32] for Q { function fst(self: Self): i32 { return self.t.len(); } function snd(self: Self): i32 { return self.b as i32; } }
function main(): i32 {
    var m: Map[string, dyn Pair[i32, u32]] = Map {};
    var n: Map[string, dyn Pair[i32, i32]] = Map {};
    var i: i32 = 0;
    while (i < 4) {
        m = m.insert("k" + (i % 3).to_string(), P { a: i, s: "p" + i.to_string() });
        n = n.insert("k" + i.to_string(), Q { t: "q" + i.to_string(), b: i as u32 });
        i = i + 1;
    }
    var t: i32 = 0;
    for (k, v) in m { t = t + v.fst() + (v.snd() as i32); }
    for (k, v) in n { t = t + v.fst() + v.snd(); }
    var ds: dyn Pair[i32, i32][] = [Q { t: "x" + "y", b: 1 as u32 }];
    var flags: boolean[] = [true, false];
    t = t + ds[0].fst() + flags.len();
    print(t.to_string());
    return 0;
}
`

func TestSelfHostDynGenericTwoInstantiations(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct{ name, src, want string }{
		{"method_result", dynGenericTwoInstantiationsSrc, "7"},
		{"maps", dynGenericTwoInstantiationsMapsSrc, "30"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_SANITIZE=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					if strings.Contains(stderr, "fern-sanitizer:") || (strings.Contains(stderr, "leakcheck:") && !strings.Contains(stderr, "live_bytes=0")) {
						t.Fatalf("heap finding:\n%s", stderr)
					}
				})
			}
		})
	}
}
