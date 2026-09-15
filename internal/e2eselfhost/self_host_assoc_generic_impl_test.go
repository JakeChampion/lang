package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A PARAMETRIC impl binding an associated type to its own type parameter
// (`impl[T] Carrier for Box[T] { type Ok = T; }`) — the shape no
// associated-type test in either compiler covered, every one of which binds a
// concrete type on a NON-generic impl.
//
// The self-host resolves projections in the parser, by rewriting the spelling
// `Base::Name` to the impl's binding. Three things defeated it here, and the
// cases below are chosen so each is load-bearing:
//
//   - the base recovery scanned back over identifier bytes only, so the
//     `Box[T]::Ok` that `subst_self` produces for every method of a parametric
//     impl recovered the base "T" and matched nothing;
//   - the impl lookup compared the `for` spelling verbatim, so `Box[T]` was
//     never found for a `Box[i32]` base;
//   - a bound written in the impl's own parameter needs the base's type
//     arguments substituted into it, which nothing did.
//
// Erasure hides the first two whenever the answer does not depend on the type:
// the self-host gave the RIGHT exit code for an i32 payload while resolving
// nothing, because its uniform 8-byte slot is correct either way. So the
// corpus turns on the type — `.len()` on a string payload is what an
// unresolved projection gets wrong — rather than on the value alone.
var assocGenericImplCases = []struct {
	name string
	src  string
	exit int
}{
	// The failing case before the fix: the self-host returned 0, native 5.
	// `b.get()` must be a string for `.len()` to mean string length.
	{"binding-typed-use-site", `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 {
    var b: Box[string] = Box { v: "hello" };
    return b.get().len();
}`, 5},
	// The same binding read through a bounded generic rather than directly.
	{"bounded-generic-projection", `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function unwrap[C: Carrier](c: C): C::Ok { return c.get(); }
function main(): i32 {
    var b: Box[i32] = Box { v: 20 };
    return b.get() + unwrap(b);
}`, 40},
	// A projection written on a CONCRETE base, which is what makes the
	// type-argument substitution observable: the binding is `T`, the answer
	// is i32. Also the only case that needs `Box[i32]::Ok` to PARSE — the
	// type parser took a `::` only before a bracket group, never after one.
	{"explicit-concrete-base", `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function twice(x: Box[i32]::Ok): i32 { return x + x; }
function main(): i32 {
    var b: Box[i32] = Box { v: 21 };
    return twice(b.get());
}`, 42},
	// A binding that is a COMPOSITE of the parameter rather than the bare
	// parameter, so the substitution has to reach inside the spelling.
	{"composite-binding", `trait Holder {
    type Item;
    function take(self: Self): Self::Item;
}
struct Box[T] { v: T }
impl[T] Holder for Box[T] {
    type Item = Option[T];
    function take(self: Self): Self::Item { return Some(self.v); }
}
function main(): i32 {
    var b: Box[i32] = Box { v: 41 };
    var o: Option[i32] = b.take();
    match (o) { Some(v) => { return v + 1; }, None => { return 0; } }
}`, 42},
	// Two parameters, each bound to the OTHER one's associated type, so a
	// substitution that matched positionally by accident still fails.
	{"parameters-bound-out-of-order", `trait Two {
    type A;
    type B;
    function fst(self: Self): Self::A;
    function snd(self: Self): Self::B;
}
struct P[X, Y] { a: X, b: Y }
impl[X, Y] Two for P[X, Y] {
    type A = Y;
    type B = X;
    function fst(self: Self): Self::A { return self.b; }
    function snd(self: Self): Self::B { return self.a; }
}
function main(): i32 {
    var p: P[string, i32] = P { a: "hi", b: 40 };
    return p.fst() + p.snd().len();
}`, 42},
}

// TestSelfHostAssocGenericImplX86_64 — the corpus through the self-hosted
// x86-64 compiler. Exit codes are cross-checked against the native compiler by
// TestAssociatedTypesGenericImplBinding / the internal/e2e engines.
func TestSelfHostAssocGenericImplX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range assocGenericImplCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostAssocGenericImplArm64 — the arm64 counterpart. Projection
// resolution is a parser pass shared by every backend, so this guards that the
// shared path stays sound rather than testing the emitter.
func TestSelfHostAssocGenericImplArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range assocGenericImplCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
