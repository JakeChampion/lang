package e2ecompiler

import (
	"sort"
	"strings"
	"testing"
)

// dropFinalizerCases run a `core/mem.Drop` finalizer through the typed
// lowering (#10756). Each value's `drop` has to run exactly once and read its
// fields, so the output is compared as a multiset of lines: when a finalizer
// runs relative to the program's own prints is a separate question.
var dropFinalizerCases = []struct {
	name  string
	src   string
	lines string
}{
	// An array element, a field, an enum payload and a returned value.
	{"every-container-shape", `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
struct Holder { w: W }
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
`, "len 2\nfield 3\npayload 4\nreturned 5\ndrop 1\ndrop 2\ndrop 3\ndrop 4\ndrop 5"},
	// A literal of scalar fields, which is otherwise a static box, reached through two names.
	{"static-constant-and-alias", `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
function main(): i32 {
    let a: W = W { n: 1 };
    let b: W = a;
    print("both " + a.n.to_string() + b.n.to_string());
    return 0;
}
`, "both 11\ndrop 1"},
	// An enum whose payloads own nothing: the finalizer is the only reason it has a release.
	{"all-scalar-enum", `import "core/mem";
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
`, "v 7\ndrop Sig\nend"},
	// A rebinding that would otherwise refill the old box in place.
	{"self-overwrite", `import "core/mem";
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
`, "drop W2\nwi\ndrop W1\nwi\ndrop B\nbj\ndrop B\nbj\nend\ndrop W1\ndrop B"},
	// A dying box a later construction of the same shape would otherwise take.
	{"reuse-donor", `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
function passthru(w: W): W { return w; }
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
`, "p 2\niter 10\ndrop 10\niter 11\nend\ndrop 2\ndrop 11"},
}

func sortedLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestSelfHostDropFinalizersRunOnceIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range dropFinalizerCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src))
			if code != 0 || sortedLines(out) != sortedLines(tc.lines) {
				t.Errorf("exit %d; lines %q, want the multiset of %q", code, out, tc.lines)
			}
		})
	}
}

func TestSelfHostDropFinalizersRunOnceIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range dropFinalizerCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src))
			if code != 0 || sortedLines(out) != sortedLines(tc.lines) {
				t.Errorf("arm64: exit %d; lines %q, want the multiset of %q", code, out, tc.lines)
			}
		})
	}
}

func TestSelfHostDropFinalizersRunOnceWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range dropFinalizerCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src))
			if code != 0 || sortedLines(out) != sortedLines(tc.lines) {
				t.Errorf("wasm: exit %d; lines %q, want the multiset of %q", code, out, tc.lines)
			}
		})
	}
}
