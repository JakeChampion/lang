package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// The scale-kernel map rewrite on the self-host (#9735 step 4, #6638).
//
// `xs.map((x: f64): f64 => x * 2.0)` IS the `scale_f64` shape. The self-host
// had the intrinsic on all eight backends and no pass to reach it from `map`,
// so it ran the scalar loop and paid an indirect call per element unless the
// source named the wrapper.
//
// Two halves have to be proved, and neither is enough alone:
//
//   - the rewrite FIRES. Three signals were tried and discarded before one
//     discriminated. `mulpd` present proves nothing (std/array's scale_f64
//     wrapper is in the bundle either way), a missing `__method_Array_map`
//     label proves nothing (that name is in neither build -- the call is
//     `__arrm_map__<elem>`), and the exit code proves nothing (the scalar
//     loop returns the same answer). What does discriminate is reading the
//     emitted body of the rewritten function: it calls the map helper if the
//     rewrite declined, and inlines the kernel if it fired.
//   - it still COMPUTES the same thing. Swapping a scalar loop for a
//     vectorised kernel is exactly the transform that gives a
//     plausible-looking wrong answer, so the program carries its own
//     hand-written reference and compares against it.
//
// scaleMapKeptSrc is the control: an `x + 2.0` element function, which the
// pass declines, so its map call must still be there.

const scaleMapSrc = `import "std/array";

// The shape the pass takes: an inline lambda whose body is one multiply by a
// literal.
function scale2(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * 2.0); }

// A NAMED element function, which this slice declines -- its body is not in
// hand at the call site, so recognising it needs a module lookup this pass
// does not do yet. Here to prove the declined shape still runs and still
// answers what the loop answers.
function half(x: f64): f64 { return x * 0.5; }
function scale_named(xs: f64[]): f64[] { return xs.map(half); }

// The same transform as a loop. Not a map, so nothing rewrites it.
function loop_scale(xs: f64[], k: f64): f64[] {
    var out: f64[] = [];
    var i: i32 = 0;
    while (i < xs.len()) { out = out.append(xs[i] * k); i = i + 1; }
    return out;
}

// All NaNs count as equal: the payload is not guaranteed across backends.
function same_f64(a: f64, b: f64): boolean {
    if (a != a) { return b != b; }
    return a == b;
}

function same(a: f64[], b: f64[]): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) {
        if (!same_f64(a[i], b[i])) { return false; }
        i = i + 1;
    }
    return true;
}

// Values a scale can go wrong on: sign, zero, a fraction, and an infinity
// whose product with zero is NaN.
function build(n: i32): f64[] {
    var inf: f64 = 1.0e308 * 10.0;
    var seed: f64[] = [1.0, 0.0 - 2.0, 0.0, 0.5, 0.0 - 0.25, 1000000.0, inf, 0.0 - 1.0, 7.5];
    var xs: f64[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(seed[i]); i = i + 1; }
    return xs;
}

function main(): i32 {
    // 0..9 covers two whole blocks and every tail remainder of both a 4-lane
    // (AVX2) and a 2-lane (SSE2 / NEON / v128) body.
    var n: i32 = 0;
    while (n <= 9) {
        var xs: f64[] = build(n);
        if (!same(scale2(xs), loop_scale(xs, 2.0))) { return 10 + n; }
        if (!same(scale_named(xs), loop_scale(xs, 0.5))) { return 30 + n; }
        n = n + 1;
    }

    // The kernel's result is an ordinary array afterwards: the length header
    // is right and it indexes. A buffer-out kernel gets the header wrong
    // before it gets the arithmetic wrong.
    var ys: f64[] = scale2(build(3));
    if (ys.len() != 3) { return 91; }
    if (ys[1] != 0.0 - 4.0) { return 92; }

    // The receiver is borrowed, not consumed.
    var src: f64[] = build(5);
    var out: f64[] = scale2(src);
    if (!same(src, build(5))) { return 93; }
    if (out.len() != 5) { return 94; }
    return 42;
}
`

// The control: `x + 2.0` is not a multiply, so the pass declines and the map
// helper survives. Same imports and same shape otherwise.
const scaleMapKeptSrc = `import "std/array";

function bump(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x + 2.0); }

function main(): i32 {
    var xs: f64[] = [1.0, 2.0];
    var ys: f64[] = bump(xs);
    if (ys[0] != 3.0) { return 1; }
    if (ys[1] != 4.0) { return 2; }
    return 42;
}
`

// The call a declined map makes. After monomorphisation a receiver method is a
// free call: `xs.m(ys)` folds to `__arrm_m[T]` and the worklist clones one
// `__arrm_m__<elem>` per element type. Not `__method_Array_map`, which is the
// auto-discovered helper form std/array's map is not, and not `array__map`,
// which is the module mangling it would have had as a plain function.
const arrayMapCall = "call __fn___arrm_map__"

// emittedBody returns the lines of one emitted function, label included.
func emittedBody(t *testing.T, asm, label string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) != label+":" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasSuffix(strings.TrimSpace(lines[j]), ".cfi_endproc") {
				return strings.Join(lines[i:j+1], "\n")
			}
		}
		return strings.Join(lines[i:], "\n")
	}
	t.Fatalf("no %s label in the emitted assembly", label)
	return ""
}

func TestSelfHostScaleMapX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	runProg := func(t *testing.T, name, src string) (string, int) {
		t.Helper()
		asm, progDir := compileSourceModload(t, runner, driverBin, src)
		if len(asm) == 0 {
			t.Fatal("self-host compiler emitted 0 bytes")
		}
		progBin := buildBin(t, gcc, progDir, name, asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return string(asm), cmd.ProcessState.ExitCode()
	}

	t.Run("rewritten", func(t *testing.T) {
		asm, code := runProg(t, "scale_map", scaleMapSrc)
		// 1x/3x = the length at which a scaled shape disagreed with the loop;
		// 9x = the result-shape and borrowed-receiver checks.
		if code != 42 {
			t.Errorf("scale map program exited %d, want 42", code)
		}
		body := emittedBody(t, asm, "__fn_scale2")
		if strings.Contains(body, arrayMapCall) {
			t.Errorf("__fn_scale2 still calls the map helper, so the rewrite declined:\n%s", body)
		}
		// The kernel splats its factor across the lanes. Scoped to this
		// function, so std/array's own wrapper cannot supply it.
		if !strings.Contains(body, "unpcklpd") {
			t.Errorf("__fn_scale2 has no lane-wise splat, so the kernel was not "+
				"inlined even though the map call is gone:\n%s", body)
		}
	})

	// Without this, "no map call in __fn_scale2" would also pass on a build
	// where the map call had moved or been renamed for unrelated reasons.
	t.Run("declined-keeps-the-call", func(t *testing.T) {
		asm, code := runProg(t, "scale_map_kept", scaleMapKeptSrc)
		if code != 42 {
			t.Errorf("declined-map program exited %d, want 42", code)
		}
		body := emittedBody(t, asm, "__fn_bump")
		if !strings.Contains(body, arrayMapCall) {
			t.Errorf("__fn_bump does not call the map helper, and its element "+
				"function is an ADD the pass must decline, so the rewritten "+
				"case's absence check proves nothing:\n%s", body)
		}
	})
}
