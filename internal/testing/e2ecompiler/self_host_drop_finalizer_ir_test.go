package e2ecompiler

import (
	"sort"
	"strings"
	"testing"
)

type dropCase struct {
	name  string
	src   string
	lines string
}

// dropFinalizerCases run a `core/mem.Drop` finalizer through the typed
// lowering (#10756). Each value's `drop` has to run exactly once and read its
// fields, so the output is compared as a multiset of lines; dropOrderCases
// pin when it runs.
var dropFinalizerCases = []dropCase{
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
@noinline function passthru(w: W): W { return w; }
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

// dropOrderCases pin that a finalizer runs at its value's death, after the
// value's last use and no earlier (#11846), so the output is compared line
// for line. A field read of a value built in the same function, once the
// producer is inlined, is the read the lowering could take off the
// construction's operands instead of the box.
var dropOrderCases = []dropCase{
	{"inlined-producer-read-by-field", `import "std/string";
import "std/i32";
import "core/mem";
struct Guard { name: string, n: i32 }
impl mem.Drop for Guard {
    function drop(self: Self): void { print(f"dropping {self.name}"); }
}
function make(n: i32): Guard {
    return Guard { name: "g" + n.to_string(), n: n };
}
function use_it(n: i32): i32 {
    let g: Guard = make(n);
    print("in use_it");
    print(g.name);
    return g.n;
}
function main(): i32 {
    let r: i32 = use_it(7);
    print("after");
    return r - 7;
}
`, "in use_it\ng7\ndropping g7\nafter"},
	{"local-construction-and-join", `import "core/mem";
import "std/i32";
struct Guard { name: string, n: i32 }
impl mem.Drop for Guard {
    function drop(self: Self): void { print("drop " + self.name); }
}
function local_fields(): i32 {
    let g: Guard = Guard { name: "local", n: 1 };
    print("local in");
    print(g.name);
    return g.n;
}
function joined(c: boolean): i32 {
    let g: Guard = Guard { name: "else", n: 11 };
    if (c) {
        g = Guard { name: "then", n: 10 };
    }
    print("joined " + g.name);
    return g.n;
}
function main(): i32 {
    print("= " + local_fields().to_string());
    print("= " + joined(true).to_string());
    print("= " + joined(false).to_string());
    return 0;
}
`, "local in\nlocal\ndrop local\n= 1\ndrop else\njoined then\ndrop then\n= 10\njoined else\ndrop else\n= 11"},
	{"argument-borrowed-and-consumed", `import "core/mem";
import "std/i32";
struct Guard { name: string, n: i32 }
impl mem.Drop for Guard {
    function drop(self: Self): void { print("drop " + self.name); }
}
function mk(s: string, n: i32): Guard { return Guard { name: s, n: n }; }
function show(g: Guard): void { print("show " + g.name); }
function consume(g: Guard): i32 { print("consume " + g.name); return g.n; }
function borrowed(): i32 {
    let g: Guard = mk("borrowed", 2);
    show(g);
    print("after show");
    return g.n;
}
function consumed(): i32 {
    let g: Guard = mk("consumed", 3);
    print("before consume");
    let r: i32 = consume(g);
    print("after consume");
    return r;
}
function main(): i32 {
    print("= " + borrowed().to_string());
    print("= " + consumed().to_string());
    return 0;
}
`, "show borrowed\nafter show\ndrop borrowed\n= 2\nbefore consume\nconsume consumed\ndrop consumed\nafter consume\n= 3"},
	{"returned-and-reassigned", `import "core/mem";
import "std/i32";
struct Guard { name: string, n: i32 }
impl mem.Drop for Guard {
    function drop(self: Self): void { print("drop " + self.name); }
}
function built_returned(): Guard {
    let g: Guard = Guard { name: "returned", n: 4 };
    print("built " + g.name);
    return g;
}
function returned(): i32 {
    let g: Guard = built_returned();
    print("got " + g.name);
    return g.n;
}
function reassigned(): i32 {
    let g: Guard = Guard { name: "first", n: 8 };
    print("re " + g.name);
    let a: i32 = g.n;
    g = Guard { name: "second", n: 9 };
    print("re " + g.name);
    return a + g.n;
}
function main(): i32 {
    print("= " + returned().to_string());
    print("= " + reassigned().to_string());
    return 0;
}
`, "built returned\ngot returned\ndrop returned\n= 4\nre first\ndrop first\nre second\ndrop second\n= 17"},
	{"containers-and-payloads", `import "core/mem";
import "std/i32";
struct Guard { name: string, n: i32 }
impl mem.Drop for Guard {
    function drop(self: Self): void { print("drop " + self.name); }
}
struct Holder { g: Guard, k: i32 }
enum Slot { Full(Guard), Empty }
enum Sig { Open(i32), Closed }
impl mem.Drop for Sig {
    function drop(self: Self): void { print("drop Sig"); }
}
function in_struct(): i32 {
    let h: Holder = Holder { g: Guard { name: "field", n: 5 }, k: 1 };
    print("holder");
    print(h.g.name);
    return h.g.n + h.k;
}
function in_array(): i32 {
    let xs: Guard[] = [Guard { name: "a0", n: 6 }, Guard { name: "a1", n: 7 }];
    print("array");
    print(xs[0].name);
    return xs[0].n + xs[1].n;
}
function payload(): i32 {
    let s: Slot = Slot.Full(Guard { name: "payload", n: 12 });
    print("slot");
    match (s) { Full(v) => { print(v.name); return v.n; }, Empty => { return 0; } }
}
function sig(): i32 {
    let s: Sig = Sig.Open(13);
    print("sig");
    match (s) { Open(v) => { return v; }, Closed => { return 0; } }
}
function main(): i32 {
    print("= " + in_struct().to_string());
    print("= " + in_array().to_string());
    print("= " + payload().to_string());
    print("= " + sig().to_string());
    return 0;
}
`, "holder\nfield\ndrop field\n= 6\narray\na0\ndrop a0\ndrop a1\n= 13\nslot\npayload\ndrop payload\n= 12\nsig\ndrop Sig\n= 13"},
}

func sortedLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// runDropCases runs both tables through run, which compiles and executes a
// program for one target.
func runDropCases(t *testing.T, run func(t *testing.T, src string) (int, string)) {
	check := func(cases []dropCase, norm func(string) string, how string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				code, out := run(t, tc.src)
				if code != 0 || norm(out) != norm(tc.lines) {
					t.Errorf("exit %d; lines %q, want %s %q", code, out, how, tc.lines)
				}
			})
		}
	}
	check(dropFinalizerCases, sortedLines, "the multiset of")
	check(dropOrderCases, func(s string) string { return strings.TrimRight(s, "\n") }, "in order")
}

func TestSelfHostDropFinalizersRunOnceIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	runDropCases(t, func(t *testing.T, src string) (int, string) {
		return cli.runX86(t, cli.emit(t, "x86-64-linux", src))
	})
}

func TestSelfHostDropFinalizersRunOnceIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	runDropCases(t, func(t *testing.T, src string) (int, string) {
		return runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src))
	})
}

func TestSelfHostDropFinalizersRunOnceWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	runDropCases(t, func(t *testing.T, src string) (int, string) {
		return runWasm(t, cli.emit(t, "wasm32-wasi", src))
	})
}
