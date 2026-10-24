package e2ecompiler

import (
	"strings"
	"testing"
)

type soleLoopWithCase struct {
	name   string
	fn     string
	proved bool
	src    string
}

var soleLoopWithCases = []soleLoopWithCase{
	{"fill-fresh", "fill", true, `@noinline
function fill(n: i32): i32[] {
    let out: i32[] = __alloc_i32(n);
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, 0 - 1 - i);
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let a: i32[] = fill(40);
    return (a[0] + a[39] + 100) % 101;
}`},
	{"fill-reads-back", "prefix", true, `@noinline
function prefix(n: i32): i32[] {
    let out: i32[] = __alloc_i32(n);
    let i: i32 = 1;
    while (i < out.len()) {
        out = out.with(i, out[i - 1] + i);
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let a: i32[] = prefix(10);
    return a[9];
}`},
	{"two-loops", "twice", true, `@noinline
function twice(n: i32): boolean[] {
    let seen: boolean[] = __alloc_bool(n);
    let i: i32 = 0;
    while (i < n) {
        if (i % 3 == 0) {
            seen = seen.with(i, true);
        }
        i = i + 1;
    }
    i = 0;
    while (i < n) {
        seen = seen.with(i, !seen[i]);
        i = i + 2;
    }
    return seen;
}
function main(): i32 {
    let s: boolean[] = twice(12);
    let c: i32 = 0;
    for b in s { if (b) { c = c + 1; } }
    return c;
}`},
	{"append-fresh", "build", true, `@noinline
function build(n: i32): i32[] {
    let out: i32[] = [];
    let i: i32 = 0;
    while (i < n) {
        out = out.append(i * 3);
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let a: i32[] = build(30);
    return (a.len() + a[29]) % 101;
}`},
	{"append-then-with", "grow_set", true, `@noinline
function grow_set(n: i32): i32[] {
    let out: i32[] = [n, n + 1];
    let i: i32 = 0;
    while (i < n) {
        out = out.append(i);
        i = i + 1;
    }
    i = 0;
    while (i < out.len()) {
        out = out.with(i, out[i] * 2);
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let a: i32[] = grow_set(9);
    return a[0] + a[1] + a[10];
}`},
	{"fresh-helper", "squares", true, `@noinline
function zeros(n: i32): i32[] {
    let out: i32[] = [];
    let i: i32 = 0;
    while (i < n) {
        out = out.append(0);
        i = i + 1;
    }
    return out;
}
@noinline
function squares(n: i32): i32[] {
    let m: i32[] = zeros(n);
    let i: i32 = 0;
    while (i < n) {
        m = m.with(i, i * i);
        i = i + 1;
    }
    return m;
}
function main(): i32 {
    let a: i32[] = squares(10);
    return a[9];
}`},
	{"fresh-chain", "cubes", true, `@noinline
function zeros(n: i32): i32[] {
    return __alloc_i32(n);
}
@noinline
function table(n: i32): i32[] {
    let t: i32[] = zeros(n + 1);
    return t;
}
@noinline
function cubes(n: i32): i32[] {
    let m: i32[] = table(n);
    let i: i32 = 0;
    while (i < n) {
        m = m.with(i, i * i * i);
        i = i + 1;
    }
    return m;
}
function main(): i32 {
    let a: i32[] = cubes(5);
    return a[4] % 101;
}`},
	{"passed-through", "fill_put", true, `@noinline
function put(own xs: i32[], i: i32, v: i32): i32[] {
    if (i < xs.len()) {
        return xs.with(i, v);
    }
    return xs.append(v);
}
@noinline
function fill_put(n: i32): i32[] {
    let m: i32[] = [];
    let i: i32 = 0;
    while (i < n) {
        m = put(m, i, i * 2);
        i = i + 1;
    }
    i = 0;
    while (i < n) {
        m = m.with(i, m[i] + 1);
        i = i + 1;
    }
    return m;
}
function main(): i32 {
    let a: i32[] = fill_put(8);
    return a[7];
}`},
	{"through-helper-with", "marks", true, `@noinline
function set(own bits: i32[], i: i32): i32[] {
    return bits.with(i, 1);
}
@noinline
function marks(n: i32): i32[] {
    let row: i32[] = __alloc_i32(n * 2);
    let i: i32 = 0;
    while (i < n) {
        row = set(row, i);
        row = row.with(n + i, 2);
        i = i + 1;
    }
    return row;
}
function main(): i32 {
    let r: i32[] = marks(4);
    return r[3] * 10 + r[7];
}`},
	{"moved-into-record", "snap_last", true, `struct Held { xs: i32[] }
@noinline
function snap_last(n: i32): Held {
    let out: i32[] = __alloc_i32(n);
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, i + 3);
        i = i + 1;
    }
    return Held { xs: out };
}
function main(): i32 {
    let h: Held = snap_last(6);
    return h.xs[5];
}`},
	{"aliased-in-loop", "alias", false, `@noinline
function alias(n: i32): i32 {
    let out: i32[] = __alloc_i32(n);
    let keep: i32[] = out;
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, i + 1);
        keep = out;
        out = out.with(i, 0);
        i = i + 1;
    }
    return keep[n - 1] * 10 + out[n - 1];
}
function main(): i32 {
    return alias(5);
}`},
	{"lent-to-call", "lent", false, `@noinline
function total(xs: i32[]): i32 {
    let t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}
@noinline
function lent(n: i32): i32 {
    let out: i32[] = __alloc_i32(n);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, i);
        t = t + total(out);
        i = i + 1;
    }
    return t % 101;
}
function main(): i32 {
    return lent(12);
}`},
	{"stored-in-record", "store", false, `struct Snap { xs: i32[] }
@noinline
function store(n: i32): i32 {
    let out: i32[] = __alloc_i32(n);
    let snap: Snap = Snap { xs: out };
    let i: i32 = 0;
    while (i < n) {
        snap = Snap { xs: out };
        out = out.with(i, 7);
        i = i + 1;
    }
    return snap.xs[n - 1] * 10 + out[n - 1];
}
function main(): i32 {
    return store(4);
}`},
	{"append-aliased", "kept_len", false, `@noinline
function kept_len(n: i32): i32 {
    let out: i32[] = [];
    let keep: i32[] = out;
    let i: i32 = 0;
    while (i < n) {
        keep = out;
        out = out.append(i);
        i = i + 1;
    }
    return keep.len() * 10 + out.len();
}
function main(): i32 {
    return kept_len(6);
}`},
	{"constant-literal", "bump", false, `@noinline
function bump(n: i32): i32 {
    let a: i32[] = [1, 2, 3];
    a = a.with(0, a[0] + n);
    return a[0];
}
function main(): i32 {
    return bump(10) + bump(10);
}`},
	{"append-parameter", "more", false, `@noinline
function more(own xs: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) {
        xs = xs.append(i);
        i = i + 1;
    }
    return xs;
}
function main(): i32 {
    let a: i32[] = __alloc_i32(4);
    let b: i32[] = a;
    let c: i32[] = more(a, 3);
    return b.len() * 10 + c.len();
}`},
	{"helper-returns-parameter", "via", false, `@noinline
function same(own xs: i32[]): i32[] {
    return xs;
}
@noinline
function via(n: i32): i32 {
    let a: i32[] = __alloc_i32(n);
    let b: i32[] = a;
    let c: i32[] = same(a);
    let i: i32 = 0;
    while (i < n) {
        c = c.with(i, 5);
        i = i + 1;
    }
    return b[0] * 10 + c[0];
}
function main(): i32 {
    return via(3);
}`},
	{"helper-returns-constant", "bump_base", false, `@noinline
function base(): i32[] {
    return [1, 2, 3];
}
@noinline
function bump_base(n: i32): i32 {
    let a: i32[] = base();
    a = a.with(0, a[0] + n);
    return a[0];
}
function main(): i32 {
    return bump_base(10) + bump_base(10);
}`},
	{"parameter", "fill_in", false, `@noinline
function fill_in(own xs: i32[]): i32[] {
    let i: i32 = 0;
    while (i < xs.len()) {
        xs = xs.with(i, 3);
        i = i + 1;
    }
    return xs;
}
function main(): i32 {
    let a: i32[] = __alloc_i32(4);
    let b: i32[] = a;
    let c: i32[] = fill_in(a);
    return b[0] * 10 + c[3];
}`},
}

func TestSelfHostSoleLoopWithX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range soleLoopWithCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			tests := rcIsUniqueSites(emittedFn(t, string(asm), tc.fn))
			if tc.proved && tests != 0 {
				t.Errorf("%s carries %d uniqueness test(s), want none: its array is one only this frame holds", tc.fn, tests)
			}
			if !tc.proved && tests == 0 {
				t.Errorf("%s carries no uniqueness test, want one: another holder may see its array", tc.fn)
			}
			bin := buildBin(t, gcc, dir, "slw_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("exited %d under the sanitizer, want %d (interp oracle; 124 = fatal sanitizer check, 99 = rc underflow)", code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Error(line)
				}
			}
		})
	}
}
