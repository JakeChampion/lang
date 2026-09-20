package e2e

import (
	"strings"
	"testing"
)

// #9879: a struct returned by a call lost a field when a view of it was built
// inline as an argument to a call on the same value.
//
// The caller evaluates the enclosing call's first operand, then the inner
// call. The last-occurrence death verdict read the text and marked the inner
// occurrence the last use, so the receiver was passed as consumed — which
// licenses the callee to take a uniquely-held field buffer and blank the slot
// it came from. The enclosing call then read a null field: SIGSEGV on the
// four native backends, an out-of-bounds trap at wasm address 0xfffffffc on
// wasm (a length header read at ptr-4), and a correct answer on the
// interpreter, which does not lower any of this.
//
// Every ingredient matters, and each is one line here: the value comes from a
// call rather than a literal, the view shares one field and rebuilds the
// other, the inner call is inline, and the outer function reads BOTH handles.
const structFieldStealBody = `
import "std/i32";

struct View { shape: i32[], strides: i32[] }

function make(): View {
    var st: i32[] = [];
    st = st.append(4);
    st = st.append(1);
    return View { shape: [3, 4], strides: st };
}

function (a: View) flip(axis: i32): View {
    return View { shape: a.shape, strides: a.strides.with(axis, 0 - a.strides[axis]) };
}

function (a: View) zip(b: View): i32 {
    return a.strides[0] + b.strides[0] + a.shape[0] + b.shape[1];
}

function verdict(): i32 {
    var a: View = make();
    if (a.zip(a.flip(1)) != 15) { return 1; }
    // The same shape written as a free call, which lowers the receiver as an
    // ordinary argument and crashed identically.
    var b: View = make();
    if (zipf(b, b.flip(1)) != 15) { return 2; }
    // A view bound to a local first always worked; it is here so a fix that
    // withdrew too many deaths still has to keep this answering.
    var c: View = make();
    var v: View = c.flip(1);
    if (c.zip(v) != 15) { return 3; }
    return 42;
}

function zipf(a: View, b: View): i32 {
    return a.strides[0] + b.strides[0] + a.shape[0] + b.shape[1];
}
`

const structFieldStealSrc = structFieldStealBody + `
function main(): i32 { return verdict(); }
`

// The two SSA legs read a printed verdict rather than an exit status, the
// same way the kernel corpus does.
const structFieldStealPrintingSrc = structFieldStealBody + `
function main(): i32 {
    write(verdict().to_string());
    write("\n");
    return 0;
}
`

func structFieldStealVerdict(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got := strings.TrimSpace(lines[len(lines)-1]); got != "42" {
		t.Errorf("struct field steal verdict = %q, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)\noutput:\n%s", got, out)
	}
}

func TestArm64SSAStructFieldSteal(t *testing.T) {
	structFieldStealVerdict(t, arm64SSACorpusRunner(t)(t, structFieldStealPrintingSrc))
}

func TestX86_64SSAStructFieldSteal(t *testing.T) {
	structFieldStealVerdict(t, x86_64SSACorpusRunner(t)(t, structFieldStealPrintingSrc))
}

func TestInterpStructFieldSteal(t *testing.T) {
	if got := runInterpExit(t, structFieldStealSrc); got != 42 {
		t.Errorf("interp = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}

func TestX86_64StructFieldSteal(t *testing.T) {
	if _, got := compileAndRunX86_64(t, structFieldStealSrc); got != 42 {
		t.Errorf("x86-64 = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}

func TestArm64StructFieldSteal(t *testing.T) {
	if _, got := compileAndRunArm64(t, structFieldStealSrc); got != 42 {
		t.Errorf("arm64 = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}

func TestWASMStructFieldSteal(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, structFieldStealSrc); got != 42 {
		t.Errorf("wasm = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}
