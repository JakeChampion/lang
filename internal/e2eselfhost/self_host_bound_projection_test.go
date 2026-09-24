package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A generic's result spelled through a projection on its own type parameter
// (`first[H: Holder](h: H): H::Item`) names a concrete type only once the call
// binds H. The self-host resolved projections at parse time alone, when H is
// still a variable, so the call's result stayed untyped and `.len()` on it read
// the array header instead of the string length (#9311). Every term turns on
// the type, since an i32 payload is right whether or not the projection resolved.
const boundProjectionSrc = `trait Holder { type Item; function get(self: Self): Self::Item; }
struct SBox { v: string }
struct ABox { xs: string[] }
struct Box[T] { v: T }
impl Holder for SBox {
    type Item = string;
    function get(self: Self): Self::Item { return self.v; }
}
impl Holder for ABox {
    type Item = string[];
    function get(self: Self): Self::Item { return self.xs; }
}
impl[T] Holder for Box[T] {
    type Item = T;
    function get(self: Self): Self::Item { return self.v; }
}
function first[H: Holder](h: H): H::Item { return h.get(); }
function main(): i32 {
    var b: SBox = SBox { v: "hello" };
    var a: ABox = ABox { xs: ["p", "q", "r"] };
    var g: Box[string] = Box { v: "seven!!" };
    return first(b).len() + first(a).len() * 10 + first(a)[1].len() * 40 + first(g).len();
}
`

const boundProjectionWant = 82

func TestSelfHostBoundProjectionResult(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "bound_projection.fern")
	if err := os.WriteFile(src, []byte(boundProjectionSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, lw := range []struct{ name, env string }{{"semantic", "FERN_SEM_IR=1"}, {"ast", "FERN_SEM_IR="}} {
		t.Run("x86-64/"+lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != boundProjectionWant {
				t.Fatalf("exit=%d, want %d (stderr %q)", exit, boundProjectionWant, stderr)
			}
		})
	}
	t.Run("wasm32-wasi", func(t *testing.T) {
		stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
		if exit != boundProjectionWant {
			t.Fatalf("exit=%d, want %d\n%s", exit, boundProjectionWant, stderr)
		}
	})
}
