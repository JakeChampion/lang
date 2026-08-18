package e2eselfhost

import (
	"os/exec"
	"testing"
)

// #6758: a struct factory that builds its array field in a LOCAL first —
// `var xs: i32[] = [k, 8]; return Q { xs: xs, pos: 1 };` — was not strict-fresh,
// because return_value_is_strictfresh_struct admitted only a direct array
// LITERAL in the field. So the factory never entered return_fresh_struct_ret_fns,
// every caller's `var q: Q = mkq(i)` earned no reclaim credit, and the box AND
// its buffer leaked per call: 88 B/iteration on both register backends, 56 on
// wasm, unbounded, where native is flat.
//
// The proof the widening rests on is the one "ARR:" already uses one container
// out: the local is literal-initialised, only ever self-appended to, and its
// single escape is the returned literal itself — so the returned box reaches the
// caller as the sole owner of that buffer, exactly as a direct literal would.
//
// The local may also be seeded by a CALL to an "ARR:" producer, which proves the
// same sole-ownership one call further out; a producer that hands back its own
// parameter earns no such entry, and that is what keeps the caller's buffer safe.
//
// The negatives below are the ways that proof can fail, and each must stay
// DECLINED rather than merely happen to work: a field seeded from a PARAM
// (freeing it would free the caller's buffer), a field seeded from a producer
// that returns one, and one local answering for TWO fields (one rc, two field
// drops — a double free the literal form cannot even express).
var selfHostFreshRetLocalArrCases = []struct {
	name string
	src  string
	exit int
}{
	// The leak gate: two identical churns, bump delta across the second. 8800 B
	// on the parent commit, under 256 after.
	{"local-built-array-field", `struct Q { xs: i32[], pos: i32 }
function mkq(k: i32): Q { var xs: i32[] = [k, 8]; return Q { xs: xs, pos: 1 }; }
function churn(k: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) { var q: Q = mkq(i); t = t + q.pos + q.xs[1]; i = i + 1; }
    return t;
}
function main(): i32 {
    var w: i32 = churn(100);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(100);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 900 || x != 900) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	// The accumulator spelling — how a producer is normally written. 10400 B on
	// the parent commit.
	{"accumulator-producer", `struct Q { xs: i32[], pos: i32 }
function build(n: i32): Q {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i * 2); i = i + 1; }
    return Q { xs: xs, pos: n };
}
function churn(k: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) { var q: Q = build(4); t = t + q.xs[3] + q.pos; i = i + 1; }
    return t;
}
function main(): i32 {
    var w: i32 = churn(100);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(100);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 1000 || x != 1000) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	// The producer-CALL spelling: the field's local is seeded by a call to an
	// "ARR:" function rather than by a literal. That registry proves exactly what
	// the literal does one call out — every return is a sole-owned scalar-element
	// buffer the callee's frame allocated — so the returned box still reaches the
	// caller as that buffer's only owner. 10400 B over the second churn on the
	// parent commit (104 B/iteration), 0 after.
	{"producer-call-array-field", `struct Q { xs: i32[], pos: i32 }
function mk(n: i32): i32[] { var out: i32[] = []; var i: i32 = 0; while (i < 4) { out = out.append(n + i); i = i + 1; } return out; }
function mkq(k: i32): Q { var xs: i32[] = mk(k); return Q { xs: xs, pos: 1 }; }
function churn(k: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) { var q: Q = mkq(i); t = t + q.pos + q.xs[3]; i = i + 1; }
    return t;
}
function main(): i32 {
    var w: i32 = churn(100);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(100);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 5350 || x != 5350) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	// Negative: the producer HANDS BACK ITS PARAMETER, so its result is the
	// caller's buffer and not this frame's. It earns no "ARR:" entry, which is
	// what has to keep the factory declined — crediting it would free `base`
	// under the loop that keeps reading it. This is the boundary the
	// producer-call row rests on, so it is carried beside it.
	{"passthru-producer-declined", `struct Q { xs: i32[], pos: i32 }
function passthru(p: i32[]): i32[] { return p; }
function mkq(p: i32[]): Q { var xs: i32[] = passthru(p); return Q { xs: xs, pos: 1 }; }
function work(k: i32): i32 {
    var base: i32[] = [11, 22, 33];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var q: Q = mkq(base);
        t = t + q.pos;
        var churn: i32[] = [i, i + 1, i + 2];
        if (churn[0] != i) { return 96; }
        i = i + 1;
    }
    if (base[0] != 11 || base[1] != 22 || base[2] != 33) { return 97; }
    return t;
}
function main(): i32 {
    if (work(60) != 60) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Negative: the field is the caller's own buffer. Crediting the factory here
	// would have the caller's reclaim free `base`, which the loop keeps reading —
	// hence the churn array between the reads, so a freed block is really
	// recycled before `base` is checked (97).
	{"param-array-field-declined", `struct Q { xs: i32[], pos: i32 }
function mk_from_param(p: i32[]): Q { return Q { xs: p, pos: 1 }; }
function work(k: i32): i32 {
    var base: i32[] = [11, 22, 33];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var q: Q = mk_from_param(base);
        t = t + q.pos;
        var churn: i32[] = [i, i + 1, i + 2];
        if (churn[0] != i) { return 96; }
        i = i + 1;
    }
    if (base[0] != 11 || base[1] != 22 || base[2] != 33) { return 97; }
    return t;
}
function main(): i32 {
    if (work(60) != 60) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Negative: one local, two fields. The box carries a single rc and
	// __struct_drop_Q would free the buffer once per field.
	{"one-local-two-fields-declined", `struct Q { xs: i32[], ys: i32[], pos: i32 }
function mk2(k: i32): Q { var xs: i32[] = [k, 8]; return Q { xs: xs, ys: xs, pos: 1 }; }
function work(k: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var q: Q = mk2(i);
        t = t + q.xs[1] + q.ys[1];
        var churn: i32[] = [i, i + 1, i + 2];
        if (churn[0] != i) { return 96; }
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    if (work(60) != 960) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostFreshRetLocalArrFieldIRX86_64 — the production x86-64 IR path. The
// two leak rows exit 98 on the parent commit; the two negatives exit 0 there and
// here, which is the point of carrying them.
func TestSelfHostFreshRetLocalArrFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFreshRetLocalArrCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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
				t.Errorf("%s exited %d, want %d (98 = the leak gate, 97 = a value read back wrong, 99 = over-release)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFreshRetLocalArrFieldIRArm64 — the same cases on arm64, where the
// leak measured identically (88 B/iteration) before the widening.
func TestSelfHostFreshRetLocalArrFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFreshRetLocalArrCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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
