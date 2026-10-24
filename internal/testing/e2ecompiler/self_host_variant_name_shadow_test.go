package e2ecompiler

import "testing"

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
    let h: Held = Held.Boxed(5, 30);
    let w: Wrap = Wrap.Tagged(4);
    return held_n(h) + held_q(h) + held_n(Bare(6)) + wrap_n(w)
        + boxed_d(Boxed { d: 1, s: 2 }) + tagged_v(Tagged { v: 7 });
}`

const variantNameShadowExit = 90

func TestSelfHostVariantNameShadowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interp := buildLangBinForInterp(t)
	if got := interpExit(t, interp, variantNameShadowSrc); got != variantNameShadowExit {
		t.Fatalf("interpreter = %d, want %d", got, variantNameShadowExit)
	}
	asm := hevCompile(t, runner, driverBin, variantNameShadowSrc, []string{"FERN_STRICT_IR=1"})
	bin := buildBin(t, gcc, dir, "variant_name_shadow", asm)
	stderr, exit := hevRun(t, runner, bin)
	if exit != variantNameShadowExit {
		t.Fatalf("exit = %d, want %d — an arm read its payload through the shadowing struct?\n%s",
			exit, variantNameShadowExit, stderr)
	}
}
