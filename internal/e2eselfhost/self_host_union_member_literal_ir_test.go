package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// unionMemberLiteralSrc builds members of a union of structs by calling the
// member's name, `Add(Add { ... })`, beside the implicit wrap and a member
// holding a string, at every destination shape: an annotated binding, a
// return, an argument and an array element. The typed lowering read the call
// as one to a function named `Add` and refused it (`call target has no
// semantic contract: Add`, #10764).
const unionMemberLiteralSrc = `import "std/i32";
struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }
struct Named { s: string }

type Expr = Add | Mul | Lit | Named;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
        Named(n) => { return n.s.len(); },
    }
}

function make(n: i32): Expr { return Add(Add { l: n, r: 1 }); }

function main(): i32 {
    let lhs: Expr = Add(Add { l: 2, r: 3 });
    let rhs: Expr = Lit(Lit { v: 4 });
    let prod: Expr = Mul(Mul { l: eval(lhs), r: eval(rhs) });
    let wrapped: Expr = Lit { v: 1 };
    let name: Expr = Named(Named { s: "abc" + "def" });
    let xs: Expr[] = [Lit(Lit { v: 100 }), Mul(Mul { l: 2, r: 50 })];
    let sum: i32 = eval(make(9)) + eval(Lit(Lit { v: 200 }));
    for x in xs { sum = sum + eval(x); }
    print((eval(prod) + eval(wrapped) + eval(name) + sum).to_string());
    return 0;
}
`

func unionMemberLiteralWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, unionMemberLiteralSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostUnionMemberLiteralIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := unionMemberLiteralWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", unionMemberLiteralSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", unionMemberLiteralSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostUnionMemberLiteralIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := unionMemberLiteralWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", unionMemberLiteralSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostUnionMemberLiteralWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := unionMemberLiteralWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", unionMemberLiteralSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", unionMemberLiteralSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
