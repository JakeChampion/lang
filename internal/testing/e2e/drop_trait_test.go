package e2e

import (
	"sort"
	"strings"
	"testing"
)

func dropsAgree(t *testing.T, src, output string, drops ...string) {
	t.Helper()
	check := func(t *testing.T, backend, got string) {
		t.Helper()
		var lines, dropped []string
		for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
			if strings.HasPrefix(l, "drop ") {
				dropped = append(dropped, l)
			} else if l != "" {
				lines = append(lines, l)
			}
		}
		if got := strings.Join(lines, "\n"); got != output {
			t.Errorf("%s output = %q, want %q", backend, got, output)
		}
		want := append([]string(nil), drops...)
		sort.Strings(want)
		sort.Strings(dropped)
		if strings.Join(dropped, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s finalizers = %q, want %q (each value once)", backend, dropped, want)
		}
	}
	t.Run("wasm32-wasi", func(t *testing.T) { check(t, "wasm", runWasmCapturingStdout(t, src)) })
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		check(t, "x86-64", got)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		check(t, "arm64", got)
	})
}

const dropPrelude = `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
`

func TestDropTraitRunsAndReadsFields(t *testing.T) {
	src := dropPrelude + `function main(): i32 {
    let a: W = W { n: 7 };
    print("use " + a.n.to_string());
    return 0;
}
`
	dropsAgree(t, src, "use 7", "drop 7")
}

func TestDropTraitAcrossContainerShapes(t *testing.T) {
	src := dropPrelude + `struct Holder { w: W }
enum E { Wrap(W), Empty }
function make(n: i32): W { return W { n: n }; }
function main(): i32 {
    let xs: W[] = [W { n: 1 }, W { n: 2 }];
    print("len " + xs.len().to_string());
    let h: Holder = Holder { w: W { n: 3 } };
    print("field " + h.w.n.to_string());
    let e: E = E.Wrap(W { n: 4 });
    match (e) { Wrap(v) => { print("payload " + v.n.to_string()); }, Empty => {} }
    let r: W = make(5);
    print("returned " + r.n.to_string());
    return 0;
}
`
	dropsAgree(t, src, "len 2\nfield 3\npayload 4\nreturned 5", "drop 1", "drop 2", "drop 3", "drop 4", "drop 5")
}

func TestDropTraitAliasFinalizesOnce(t *testing.T) {
	src := dropPrelude + `function main(): i32 {
    let a: W = W { n: 1 };
    let b: W = a;
    print("both " + a.n.to_string() + b.n.to_string());
    return 0;
}
`
	dropsAgree(t, src, "both 11", "drop 1")
}

func TestDropTraitMovedIntoCallee(t *testing.T) {
	src := dropPrelude + `function consume(w: W): i32 { print("consume " + w.n.to_string()); return w.n; }
function main(): i32 {
    let x: i32 = consume(W { n: 1 });
    print("back " + x.to_string());
    return 0;
}
`
	dropsAgree(t, src, "consume 1\nback 1", "drop 1")
}

func TestDropTraitLoopTemporary(t *testing.T) {
	src := dropPrelude + `function main(): i32 {
    let i: i32 = 0;
    while (i < 3) {
        let t: W = W { n: 10 + i };
        print("iter " + t.n.to_string());
        i = i + 1;
    }
    print("end");
    return 0;
}
`
	dropsAgree(t, src, "iter 10\niter 11\niter 12\nend", "drop 10", "drop 11", "drop 12")
	ordered := func(t *testing.T, backend, got string) {
		t.Helper()
		for _, pair := range [][2]string{{"drop 10", "iter 11"}, {"drop 11", "iter 12"}} {
			if strings.Index(got, pair[0]) > strings.Index(got, pair[1]) {
				t.Errorf("%s: %q runs after %q:\n%s", backend, pair[0], pair[1], got)
			}
		}
	}
	t.Run("per-iteration", func(t *testing.T) {
		t.Run("wasm32-wasi", func(t *testing.T) { ordered(t, "wasm", runWasmCapturingStdout(t, src)) })
		t.Run("x86_64", func(t *testing.T) { got, _ := compileAndRunX86_64(t, src); ordered(t, "x86-64", got) })
		t.Run("arm64-linux", func(t *testing.T) { got, _ := compileAndRunArm64(t, src); ordered(t, "arm64", got) })
	})
}

func TestDropTraitNotSwallowedByReuse(t *testing.T) {
	src := dropPrelude + `@noinline function passthru(w: W): W { return w; }
function main(): i32 {
    let p: W = passthru(W { n: 2 });
    print("p " + p.n.to_string());
    let i: i32 = 0;
    while (i < 2) {
        let t: W = W { n: 10 + i };
        print("iter " + t.n.to_string());
        i = i + 1;
    }
    print("end");
    return 0;
}
`
	dropsAgree(t, src, "p 2\niter 10\niter 11\nend", "drop 2", "drop 10", "drop 11")
}

func TestDropTraitOnAllScalarEnum(t *testing.T) {
	src := `import "core/mem";
import "std/i32";
enum Sig { Open(i32), Closed }
impl mem.Drop for Sig {
    function drop(self: Self): void { print("drop Sig"); }
}
function main(): i32 {
    let s: Sig = Sig.Open(7);
    match (s) { Open(v) => { print("v " + v.to_string()); }, Closed => {} }
    print("end");
    return 0;
}
`
	dropsAgree(t, src, "v 7\nend", "drop Sig")
}

func TestDropTraitSelfOverwriteFinalizesEveryValue(t *testing.T) {
	src := `import "core/mem";
import "std/i32";
struct W { v: i32[] }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop W" + self.v.len().to_string()); }
}
enum B { Keep(i32[]), Swap(i32[]) }
impl mem.Drop for B {
    function drop(self: Self): void { print("drop B"); }
}
function main(): i32 {
    let w: W = W { v: [0, 0] };
    let i: i32 = 0;
    while (i < 2) { w = W { v: [i] }; print("wi"); i = i + 1; }
    let b: B = B.Keep([0, 0]);
    let j: i32 = 0;
    while (j < 2) { b = B.Keep([j]); print("bj"); j = j + 1; }
    print("end");
    return 0;
}
`
	dropsAgree(t, src, "wi\nwi\nbj\nbj\nend", "drop W2", "drop W1", "drop W1", "drop B", "drop B", "drop B")
}
