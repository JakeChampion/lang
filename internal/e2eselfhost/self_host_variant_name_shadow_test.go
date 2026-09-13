package e2eselfhost

import "testing"

// A struct and an enum VARIANT may share a name — native accepts it and the
// language has no rule against it — and the self-host decl table, keyed by the
// bare name, then holds two decls under one key. A match arm has to read its
// payload through the decl the SCRUTINEE's enum owns.
//
// It did not. The arm took the pattern's `Enum.` qualifier as its owner and
// only fell through to the scrutinee when no decl of that name carried that
// owner — but an ABSENT qualifier is the empty string, and a plain struct's
// enum_owner is the empty string too, so an unqualified `Boxed(_, k)` beside a
// `struct Boxed` resolved to the STRUCT. Its field 0 is not `__ev`, so the arm
// took the struct-union member form: it bound only the first binder, to the
// whole box typed `Boxed`, and never bound the second. `k` then read as an
// unresolved name and lowered as a function ADDRESS — `const_func
// $binding$2$k`, a symbol nothing defines — which `FERN_STRICT_IR` catches and
// a plain build emits. At one payload the single binder was bound with the
// struct's type instead, so `k + 1` dispatched to a `Boxed.add` method that
// does not exist.
//
// The resolution is in irlower, ahead of instruction selection, so one backend
// carries the signal for all of them.
//
// 31 + 35 + 6 + 8 + 3 + 7 = 90, and the same program is 90 under the
// interpreter and the Go compiler.
const variantNameShadowSrc = `struct Boxed { d: i32, s: i32 }
struct Tagged { v: i32 }
enum Held { Bare(i32), Boxed(i32, i32) }
enum Wrap { Empty, Tagged(i32) }

@noinline function held_n(h: Held): i32 {
    match (h) { Bare(k) => { return k; }, Boxed(_, k) => { return k + 1; } }
    return 0 - 1;
}

@noinline function held_q(h: Held): i32 {
    match (h) { Held.Bare(k) => { return k * 10; }, Held.Boxed(j, k) => { return j + k; } }
    return 0 - 1;
}

@noinline function wrap_n(w: Wrap): i32 {
    match (w) { Empty => { return 0; }, Tagged(k) => { return k * 2; } }
    return 0 - 1;
}

@noinline function boxed_d(b: Boxed): i32 { return b.d + b.s; }
@noinline function tagged_v(t: Tagged): i32 { return t.v; }

function main(): i32 {
    var h: Held = Held.Boxed(5, 30);
    var w: Wrap = Wrap.Tagged(4);
    return held_n(h) + held_q(h) + held_n(Bare(6)) + wrap_n(w)
        + boxed_d(Boxed { d: 1, s: 2 }) + tagged_v(Tagged { v: 7 });
}`

const variantNameShadowExit = 90

func TestSelfHostVariantNameShadowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interp := buildLangBinForInterp(t)
	if got := interpExit(t, interp, variantNameShadowSrc); got != variantNameShadowExit {
		t.Fatalf("interpreter = %d, want %d", got, variantNameShadowExit)
	}
	// FERN_STRICT_IR turns the bail this shape used to take into a named
	// refusal, so a regression is a build failure here rather than a wrong
	// number that happens to survive. No leakcheck: what this pins is which
	// declaration the arm reads, not who releases the enum box.
	asm := hevCompile(t, runner, driverBin, variantNameShadowSrc, []string{"FERN_STRICT_IR=1"})
	bin := buildBin(t, gcc, dir, "variant_name_shadow", asm)
	stderr, exit := hevRun(t, runner, bin)
	if exit != variantNameShadowExit {
		t.Fatalf("exit = %d, want %d — an arm read its payload through the shadowing struct?\n%s",
			exit, variantNameShadowExit, stderr)
	}
}
