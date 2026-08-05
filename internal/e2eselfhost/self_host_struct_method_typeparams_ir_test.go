package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructMethodTypeParamsIRX86_64 pins #6007: a method on a generic
// struct that introduces type parameters of its OWN —
// `(b: Box[T]) map[U](f: (T) => U): Box[U]`.
//
// monomorphize_structs clones such a method once per RECEIVER instantiation and
// drops the method's own type params, so `U` survived into the clone as a free
// variable; `to_concrete_struct_ty` then mangled the return `Box[U]` to
// `Box__U` against a THROWAWAY accumulator, so no pass ever generated that
// struct or its methods. Every one of these programs bailed the whole module
// with `call to unknown symbol Box__U.get` — a hard error since #3457 deleted
// the AST emitters.
//
// register_struct_method_generics folds the method into a free generic
// `__smm_<Base>_<name>[<receiver vars>, <own vars>]` with the receiver as
// arg0, the same shape the array (`__arrm_`) and map (`__mapm_`) folds already
// use, so the proven free-generic worklist clones it per instantiation with a
// CONCRETE return for monomorphize_structs to mangle.
//
// Each program's expected value was taken from `bin/fern -interp`, and each was
// confirmed to bail on the pre-fix compiler — except `own-var-in-param-only`,
// which compiled before (its own var reaches no container type) and is here as
// the must-not-regress control.
func TestSelfHostStructMethodTypeParamsIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// The shape the fixture uses, widened: one own-var instantiation per
	// argument kind — a named function reference, a function whose return type
	// differs from the receiver's element (`Box[i32]` -> `Box[string]`), and an
	// inline lambda. The named-reference case is what needed lambda_ret_of to
	// look past `ExprLambda`: `U` appears ONLY in the closure param's return
	// position, so with a bare `dbl` it stayed unbound and the call kept the
	// un-instantiated template.
	run(t, `struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) get(): T { return b.v; }
function dbl(x: i32): i32 { return x * 2; }
function label(x: i32): string { if (x > 5) { return "big"; } return "small"; }
function main(): i32 {
    var b: Box[i32] = Box { v: 7 };
    var d: Box[i32] = b.map(dbl);
    var s: Box[string] = b.map(label);
    var lm: Box[i32] = b.map((x: i32) => x + 1);
    if (s.get() != "big") { return 90; }
    return d.get() + lm.get();
}`, "map-own-typaram", 22)

	// A CHAINED call: the second `.map` has no annotation to type its receiver,
	// so it resolves only if mono_infer instantiates the fold to recover the
	// first call's concrete result type.
	run(t, `struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) get(): T { return b.v; }
function dbl(x: i32): i32 { return x * 2; }
function shout(x: i32): string { return "n"; }
function main(): i32 {
    var b: Box[i32] = Box { v: 7 };
    var c: Box[string] = b.map(dbl).map(shout);
    if (c.get() != "n") { return 91; }
    return 33;
}`, "map-chained", 33)

	// TWO receiver vars plus an own var: the fold's key is the receiver's vars
	// followed by the method's, so `Pair[string, i32].remap[W]` clones as
	// `__smm_Pair_remap__string__i32__i32`. Pre-fix this bailed naming
	// `Pair__string__W.val`.
	run(t, `struct Pair[K, V] { k: K, v: V }
function (p: Pair[K, V]) remap[W](f: (V) => W): Pair[K, W] { return Pair { k: p.k, v: f(p.v) }; }
function (p: Pair[K, V]) val(): V { return p.v; }
function neg(x: i32): i32 { return 0 - x; }
function main(): i32 {
    var p: Pair[string, i32] = Pair { k: "a", v: 5 };
    var q: Pair[string, i32] = p.remap(neg);
    return 100 + q.val();
}`, "two-receiver-vars", 95)

	// Control: an own var that reaches no container type (`tag[E](e: E): E`)
	// compiled BEFORE the fix — `to_concrete_struct_ty` leaves a bare `E`
	// alone, so nothing dangled. It now rides the fold instead, and must keep
	// its value.
	run(t, `struct Box[T] { v: T }
function (b: Box[T]) tag[E](e: E): E { return e; }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    var b: Box[i32] = Box { v: 3 };
    var t: i32 = b.tag(40);
    return t + b.get();
}`, "own-var-in-param-only", 43)

	// A generic struct whose methods have NO own type params must stay on the
	// monomorphize_structs path untouched — the fold's gate keys on the
	// signature's free vars, and a `Box[T]` method that mentions only `T` has
	// none.
	run(t, `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function (b: Box[T]) swap(x: T): Box[T] { return Box { v: x }; }
function main(): i32 {
    var b: Box[i32] = Box { v: 4 };
    var c: Box[i32] = b.swap(9);
    var s: Box[string] = Box { v: "hi" };
    if (s.get() != "hi") { return 92; }
    return b.get() + c.get();
}`, "no-own-typaram-unchanged", 13)
}
