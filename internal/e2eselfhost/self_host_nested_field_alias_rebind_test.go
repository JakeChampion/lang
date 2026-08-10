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
// The assertion is that the explicit spelling's cost does not scale with the
// ITERATION count — that is exactly what losing the credit cost, and exactly what
// this fix owns. Before it: 10160 B at k=8 and 98480 at k=32. After: 800 at both.
//
// It is deliberately not an absolute number and not equality with the carried
// spelling. #6620 took the carried row to zero while the explicit row kept one
// box per call, because its release covers the base-copy path and not the
// override alias — a residual filed separately. Pinning either the number or the
// equality would make this suite fail on someone else's unrelated RC landing;
// pinning the k-invariance keeps testing this fix and nothing else.
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

// The same program at k=32. A per-ITERATION leak scales with it; the per-call
// residual does not.
const nestedFieldAliasExplicitK32Src = `struct I { tag: i32, data: i32[] }
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
    while (r < 10) { t = t + work(32); r = r + 1; }
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

	k8Live, _, k8Exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_explicit_k8", nestedFieldAliasExplicitSrc)
	k32Live, _, k32Exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_explicit_k32", nestedFieldAliasExplicitK32Src)
	carriedLive, _, carriedExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_carried", nestedFieldAliasCarriedSrc)

	if k8Exit != 36 || carriedExit != 36 {
		t.Fatalf("exit codes: explicit=%d carried=%d, want 36 for both", k8Exit, carriedExit)
	}
	if k32Exit != 20 {
		t.Fatalf("explicit k=32 exited %d, want 20", k32Exit)
	}
	if k8Live != k32Live {
		t.Errorf("live_bytes: k=8 %d, k=32 %d — quadrupling the ITERATION count must not cost "+
			"more, since every superseded array buffer is the local's to release. A difference "+
			"here is the reclaim credit lost to the explicit `inner: o.inner` again (#6623)",
			k8Live, k32Live)
	}
	if carriedLive != 0 {
		t.Errorf("the `...base` carry leaked %d bytes — that spelling is the control and #6605 "+
			"took it to zero; a regression there invalidates the comparison above", carriedLive)
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
