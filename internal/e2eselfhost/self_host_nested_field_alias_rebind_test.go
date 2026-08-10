package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Nested-struct field ALIASED in a self-rebind literal (#6623) ------------
//
// `o = S { xs: o.xs.append(i), inner: o.inner, n: i }` and
// `o = S { ...o, xs: o.xs.append(i) }` mean the same thing, and native is flat
// on both. On the self-host the explicit spelling cost 12.7x the carried one,
// because the whole local lost its reclaim credit rather than leaking one extra
// box: `o.inner` in a field-value position is a bare non-scalar field READ, so
// the NODEEP field-move scan marked `o` as having moved a field out, and
// slot_nodeep gates the __field_reclaim_<T> the rebind would otherwise emit. No
// reclaim was emitted at all, so the superseded ARRAY buffers leaked too — one
// per iteration, which is why the cost grew with k rather than with the call
// count.
//
// The read is not a move: the struct-literal override path retains a
// nested-struct or enum field value unconditionally unless it is a fresh
// literal / ctor, and a field read is neither — so the successor box holds a
// COUNTED reference and the superseded box's deep drop decs the dup.
//
// The assertion is the EQUALITY of the two spellings, not a flat zero: both
// still carry the separate carried-nested-field defect (#6605), which is one
// box per call on either spelling. Equality is what this fix owns, and it keeps
// holding when that one lands and takes both rows to zero.
const nestedFieldAliasExplicitSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9] }, n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: o.inner, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.inner.tag;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

const nestedFieldAliasCarriedSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9] }, n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { ...o, xs: o.xs.append(i), n: i };
        i = i + 1;
    }
    return o.xs.len() + o.inner.tag;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

// An ARRAY field read in the same position stays a move: its override retain is
// conditional (only a bare ident naming an rc-array slot incs), so exempting it
// would hand the successor box an uncounted buffer and the superseded box's deep
// drop would free it out from under the live value. The row exists to catch that
// over-reach — it reads every carried buffer back, so a release under a live
// reference is a wrong answer or a crash, not a quieter number.
const nestedFieldAliasArrayFieldSrc = `struct S { xs: i32[], ys: i32[], n: i32 }

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], ys: [5, 6, 7], n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), ys: o.ys, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.ys.len() + o.ys[2];
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

// The same field read bound to a DIFFERENT local must keep marking: `p` and `o`
// are both live afterwards, so deep-dropping `o` would reach into what `p`
// shows. Only the self-rebind — where the successor IS this slot — is exempt.
const nestedFieldAliasForkSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 3, data: [9] }, n: k };
    var p: S = S { xs: [4], inner: o.inner, n: o.n + 1 };
    return p.inner.tag + p.inner.data[0] + o.inner.tag + o.inner.data[0] + p.n;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(2); r = r + 1; }
    return t / 10;
}`

// leakSummary compiles src with the self-host driver under FERN_LEAKCHECK,
// runs it, and returns (live_bytes, allocs, exit code).
func leakSummary(t *testing.T, gcc string, runner []string, driverBin, dir, name, src string) (int64, int64, int) {
	t.Helper()
	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, name, asm)
	stderr, exit := hevRun(t, runner, progBin)
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary\n%s", name, stderr)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", name, summary, err)
	}
	if allocs == 0 {
		t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
	}
	return live, allocs, exit
}

// TestSelfHostNestedFieldAliasRebindX86_64 — the two spellings of the same
// self-update cost the same, and the shapes the exemption must not reach still
// answer correctly.
func TestSelfHostNestedFieldAliasRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	explicitLive, _, explicitExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_explicit", nestedFieldAliasExplicitSrc)
	carriedLive, _, carriedExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_carried", nestedFieldAliasCarriedSrc)

	if explicitExit != 36 || carriedExit != 36 {
		t.Fatalf("exit codes: explicit=%d carried=%d, want 36 for both", explicitExit, carriedExit)
	}
	if explicitLive != carriedLive {
		t.Errorf("live_bytes: explicit alias=%d, `...base` carry=%d — the two spellings mean "+
			"the same thing and native is flat on both, so an explicit `inner: o.inner` must "+
			"not cost the local its whole reclaim credit (#6623)", explicitLive, carriedLive)
	}

	t.Run("array-field-alias-not-exempt", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfield", nestedFieldAliasArrayFieldSrc)
		if exit != 8 {
			t.Errorf("exited %d, want 8 — an array field carried by an explicit alias is read "+
				"back after every rebind, so a wrong answer here is a released live buffer", exit)
		}
	})

	t.Run("fork-to-other-local-not-exempt", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_fork", nestedFieldAliasForkSrc)
		if exit != 27 {
			t.Errorf("exited %d, want 27 — both locals read the shared inner box afterwards, so "+
				"only the self-rebind may be exempted from the field-move mark", exit)
		}
	})
}
