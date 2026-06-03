package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// traitsCases cover trait / impl declarations through the self-hosted
// compiler. The self-host handles them the same pragmatic way it
// handles generics — by leaning on machinery it already has: an
// `impl Trait for Type { … }` block is desugared (in the shared
// parser) into ordinary receiver-method FuncDecls, and the trait
// declaration itself is parsed-and-discarded (the self-host dispatches
// `obj.method()` by the receiver's concrete type, so it needs no
// conformance table). `[T: Bound]` bound syntax is swallowed by the
// existing type-parameter discard loop. Exit codes are the behavioural
// contract. See docs/TRAITS.md (self-host slice 1).
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
	// The decisive case for std/test: ONE bounded-generic body called
	// at TWO different concrete types. The self-host erases the type
	// param, so a single emitted body must dispatch `v.show()` to the
	// right impl per call — which works because the self-host resolves
	// method calls dynamically by the receiver value's runtime type,
	// not by a statically-monomorphised name.
	{"trait-bounded-generic-multitype",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function describe[T: Show](v: T): i32 { return v.show(); } " +
			"function main(): i32 { var a: A = A { x: 7 }; var b: B = B { y: 4 }; return describe(a) + describe(b); }", 11},
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
