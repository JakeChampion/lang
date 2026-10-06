package e2ecompiler

import "testing"

// A union alias with type parameters, and members written with arguments
// (#10764). The self-host parser read a member as a bare name, so
// `type Tree[T] = Leaf[T] | Pair[T] | Lit;` did not parse; it now desugars
// into the generic enum the Go checker desugars every union into, and the
// enum monomorphiser clones one `Tree` per instantiation. The program is
// `TestX86_64GenericUnions` with a second instantiation and a member of a
// non-generic union written with arguments.
const genericUnionSrc = `import "std/i32";
struct Leaf[T] { v: T }
struct Pair[T] { a: T, b: T }
struct Lit { v: i32 }
struct Hold[T] { v: T }
struct Blank { w: i32 }

type Tree[T] = Leaf[T] | Pair[T] | Lit;
type Slot = Hold[i32] | Blank;

function leafOf(t: Tree[i32]): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Pair(p) => { return p.a + p.b; },
        Lit(x) => { return x.v; },
    }
}

function label(t: Tree[string]): string {
    match (t) {
        Leaf(l) => { return l.v; },
        Pair(p) => { return p.a + p.b; },
        Lit(x) => { return x.v.to_string(); },
    }
}

function held(s: Slot): i32 {
    match (s) {
        Hold(c) => { return c.v; },
        Blank(b) => { return b.w; },
    }
}

function main(): i32 {
    let a: Tree[i32] = Tree.Leaf(Leaf[i32] { v: 4 });
    let b: Tree[i32] = Pair[i32] { a: 5, b: 6 };
    let c: Tree[i32] = Lit { v: 7 };
    let d: Tree[string] = Leaf[string] { v: "le" + "af" };
    let e: Tree[string] = Tree.Pair(Pair[string] { a: "pa", b: "ir" });
    let f: Slot = Hold[i32] { v: 30 };
    let g: Slot = Blank { w: 12 };
    print((leafOf(a) + leafOf(b) + leafOf(c)).to_string() + ":" + label(d) + ":" + label(e) + ":" + (held(f) + held(g)).to_string());
    return 0;
}
`

func genericUnionWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, genericUnionSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostGenericUnionIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := genericUnionWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", genericUnionSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", genericUnionSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostGenericUnionIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := genericUnionWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", genericUnionSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostGenericUnionWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := genericUnionWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", genericUnionSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
