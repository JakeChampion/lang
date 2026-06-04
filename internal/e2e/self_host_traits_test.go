package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// traitsCases cover trait / impl declarations through the self-hosted
// compiler. Concrete impls desugar (in the shared parser) to ordinary
// receiver-method FuncDecls dispatched by the receiver's runtime shape.
// Trait-BOUNDED generics are monomorphised in the parser's
// `monomorphize_module` pass — cloned per concrete call-site type so a
// method call on a `T`-typed value resolves to a concrete method (this
// is required for primitive receivers, whose unboxed values carry no
// shape pointer for dynamic dispatch). Exit codes are the behavioural
// contract. See docs/TRAITS.md §7a.
var traitsCases = []struct {
	name string
	src  string
	exit int
}{
	// Trait + impl on a struct, then a direct method call.
	{"trait-impl-method",
		"trait Area { function area(self: Self): i32; } " +
			"struct Sq { side: i32 } " +
			"impl Area for Sq { function area(self: Self): i32 { return self.side * self.side; } } " +
			"function main(): i32 { var s: Sq = Sq { side: 5 }; return s.area(); }", 25},
	// Impl method taking an extra argument (Self in a non-receiver slot).
	{"trait-impl-arg",
		"trait Adder { function add(self: Self, other: Self): i32; } " +
			"struct N { v: i32 } " +
			"impl Adder for N { function add(self: Self, other: Self): i32 { return self.v + other.v; } } " +
			"function main(): i32 { var a: N = N { v: 19 }; var b: N = N { v: 23 }; return a.add(b); }", 42},
	// Two impls of the same trait for two structs; each dispatches to
	// its own method.
	{"trait-two-impls",
		"trait Tag { function tag(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Tag for A { function tag(self: Self): i32 { return self.x; } } " +
			"impl Tag for B { function tag(self: Self): i32 { return self.y + 1; } } " +
			"function main(): i32 { var a: A = A { x: 10 }; var b: B = B { y: 31 }; return a.tag() + b.tag(); }", 42},
	// `pub trait` + a trait-bounded generic function. The bound is
	// discarded with the rest of the type-param list; the body's
	// `v.area()` resolves because the only call site passes a concrete
	// Sq, whose receiver method exists.
	{"trait-bounded-generic-monotype",
		"pub trait Area { function area(self: Self): i32; } " +
			"struct Sq { side: i32 } " +
			"impl Area for Sq { function area(self: Self): i32 { return self.side * self.side; } } " +
			"function describe[T: Area](v: T): i32 { return v.area(); } " +
			"function main(): i32 { var s: Sq = Sq { side: 6 }; return describe(s); }", 36},
	// ONE bounded-generic body called at TWO different concrete types →
	// monomorphised into two clones (describe__A, describe__B), each
	// dispatching `v.show()` to its own impl.
	{"trait-bounded-generic-multitype",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function describe[T: Show](v: T): i32 { return v.show(); } " +
			"function main(): i32 { var a: A = A { x: 7 }; var b: B = B { y: 4 }; return describe(a) + describe(b); }", 11},
	// Primitive receiver through an erased bounded generic — the case
	// that crashed before monomorphisation (the dynamic shape-pointer
	// dispatch can't read a tag off an unboxed i32). The pass clones
	// `same` -> `same__i32` so the receiver's static type is concrete.
	{"trait-bounded-generic-primitive",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"function same[T: Eq](a: T, b: T): i32 { if (a.eq(b)) { return 1; } return 0; } " +
			"function main(): i32 { var n: i32 = 5; return same(n, 5) + same(n, 6); }", 1},
	// One bounded generic instantiated at BOTH a primitive and a struct
	// in the same program — two distinct clones.
	{"trait-bounded-generic-mixed",
		"trait Sized { function sz(self: Self): i32; } " +
			"struct Boxx { v: i32 } " +
			"impl Sized for i32 { function sz(self: Self): i32 { return self; } } " +
			"impl Sized for Boxx { function sz(self: Self): i32 { return self.v; } } " +
			"function getsz[T: Sized](x: T): i32 { return x.sz(); } " +
			"function main(): i32 { var b: Boxx = Boxx { v: 30 }; var n: i32 = 12; return getsz(n) + getsz(b); }", 42},
	// Array-element method dispatch through a bounded generic: `a[i].eq`
	// must dispatch on the element type. Probe for the std/test array
	// collapse.
	{"trait-bounded-generic-array-elem",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"function all_eq[T: Eq](a: T[], b: T[]): i32 { var i: i32 = 0; while (i < len(a)) { if (!a[i].eq(b[i])) { return 0; } i = i + 1; } return 1; } " +
			"function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = [1, 2, 3]; return all_eq(x, y); }", 1},
	// TWO independent type parameters → the monomorphiser infers each
	// from its own argument and mangles the clone with both concrete
	// types joined (`combine__A__B`). This is the multi-parameter path
	// the std/test `Map[K, V]` assertion collapse relies on. See
	// docs/TRAITS.md §7a.
	{"trait-bounded-generic-two-params",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function combine[P: Show, Q: Show](p: P, q: Q): i32 { return p.show() + q.show(); } " +
			"function main(): i32 { var a: A = A { x: 30 }; var b: B = B { y: 12 }; return combine(a, b); }", 42},
	// Parametric impl `impl[T: Bound] Trait for Box[T]` on a generic
	// struct. The impl's `for` type `Box[T]` strips to the base name
	// `Box` for the method symbol + dispatch shape compare, so a
	// `Box[Inner]` value dispatches `box.val()` to the impl method,
	// whose body calls `self.v.val()` on the struct-typed field (which
	// carries its own runtime shape, so the inner dispatch resolves
	// dynamically). Struct-typed type parameters need no
	// monomorphisation; primitive/string `T` is a follow-up (same
	// boundary bounded generics had). See docs/TRAITS.md §7a.
	{"trait-parametric-impl-struct-elem",
		"trait Valued { function val(self: Self): i32; } " +
			"struct Inner { n: i32 } " +
			"impl Valued for Inner { function val(self: Self): i32 { return self.n; } } " +
			"struct Box[T] { v: T } " +
			"impl[T: Valued] Valued for Box[T] { function val(self: Self): i32 { return self.v.val() + 1; } } " +
			"function main(): i32 { var b: Box[Inner] = Box { v: Inner { n: 41 } }; return b.val(); }", 42},
}

// TestSelfHostTraitsX86_64 — trait/impl support with the self-hosted
// x86-64 compiler. Trait parsing lives entirely in the shared lexer +
// parser, so the asm emitter needed no change.
func TestSelfHostTraitsX86_64(t *testing.T) {
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

	for _, tc := range traitsCases {
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

// TestSelfHostTraitsArm64 — CI-gated arm64 counterpart. Trait support
// lives entirely in the shared parser, so the arm64 emitter needed no
// change; this guards that the shared path stays sound on arm64.
func TestSelfHostTraitsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src))
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
