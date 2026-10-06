package e2ecompiler

import (
	"fmt"
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
// The rows were EQUAL when this landed, both carrying #6605's one box per call.
// #6620 then took the `...base` carry to an exact 0 and left the explicit
// spelling at 800: the override's retain on `o.inner` had no counterpart,
// because the nested-struct arm of `__field_reclaim_S` is gated on
// `structfldok:S` and the explicit field read was what disqualified the type.
// #6653 exempts that read from the whole-program scan — the successor box goes
// into the slot `o` names, so it creates no owner the old box did not have —
// which emits the arm and pairs the retain, and the rows are equal again.
const nestedFieldAliasExplicitSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9] }, n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: o.inner, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.inner.tag;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

const nestedFieldAliasCarriedSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9] }, n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { ...o, xs: o.xs.append(i), n: i };
        i = i + 1;
    }
    return o.xs.len() + o.inner.tag;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

// --- The same read on an ARRAY field (#6628) ---------------------------------
//
// A scalar-element or array-of-struct field read is exempt for the same reason
// the nested-struct one is: the override path's ExprFieldAccess arm incs it
// whenever `field_access_arr_field_type` resolves, and when it does NOT resolve
// the arm leaves `fav_ok` false and BAILS the whole lowering. There is no third
// outcome where the successor box is handed an uncounted buffer, which is what
// #6628 set out to rule out before exempting.
//
// This shape reads every carried buffer back after the loop, so an over-release
// is a wrong answer rather than a quieter number.
//
// `string`, `string[]` and array-of-ENUM fields stay marked — see
// fieldmove_selfrebind_alias.
func nestedFieldAliasArrayFieldSrc(k int, carried bool) string {
	update := "o = S { xs: o.xs.append(i), ys: o.ys, n: i };"
	if carried {
		update = "o = S { ...o, xs: o.xs.append(i), n: i };"
	}
	return fmt.Sprintf(`struct S { xs: i32[], ys: i32[], n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], ys: [5, 6, 7], n: 0 };
    let i: i32 = 0;
    while (i < k) {
        %s
        i = i + 1;
    }
    return o.xs.len() + o.ys.len() + o.ys[2];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, update, k)
}

// arrayFieldAliasExit is what nestedFieldAliasArrayFieldSrc must exit with:
// `work` returns (2 + k) + 3 + 7, ten calls are summed, and `main` masks to 6
// bits. Computing it beats a table — the point of the k sweep is that only the
// byte count is allowed to vary with k.
func arrayFieldAliasExit(k int) int {
	return (10 * (k + 12)) & 63
}

// The other field kind the exemption admits: an ARRAY-OF-STRUCT field. Same
// ExprFieldAccess arm, same `field_access_arr_field_type` gate — it recognises
// `E[]` alongside the scalar-element arrays — so it must move with them.
func nestedFieldAliasStructArraySrc(k int) string {
	return fmt.Sprintf(`struct E { a: i32, b: i32 }
struct S { xs: i32[], es: E[], n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], es: [E { a: 3, b: 4 }, E { a: 5, b: 6 }], n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), es: o.es, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.es.len() + o.es[1].b;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, k)
}

// The array read handed to a DIFFERENT local, the array sibling of the fork row
// below: `p` and `o` are both live afterwards and both show the shared buffer,
// so `o` must keep its field-move mark.
const nestedFieldAliasArrayForkSrc = `struct S { xs: i32[], ys: i32[], n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], ys: [5, 6, 7], n: k };
    let p: S = S { xs: [4], ys: o.ys, n: o.n + 1 };
    return p.ys[0] + p.ys[2] + o.ys[0] + o.ys[2] + p.n;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(2); r = r + 1; }
    return t / 10;
}`

// --- The same read on a direct ENUM field (#6653, enum route) ----------------
//
// The enum field takes the same `structfldok:` gate the nested-struct one does,
// so the exemption admits both. Its residual needed a second fix as well: the
// QUALIFIED variant-ctor spelling `V.A(7)` did not read as a fresh construction
// (variant_ctor_enum_owner only knew the bare `A(7)` callee), so the field was
// retained as if it aliased and the one box the whole shape allocates was never
// freed — 400 B here, flat in k, and present on the `...o` carry too (#6681).
// Either fix alone leaves that 400; both together reach 0.
//
// `vv` is a `let` borrow of the carried enum, so the payload is read back after
// the loop: an over-release is a wrong exit code before it is a byte count.
func enumFieldAliasSrc(k int, carried bool) string {
	update := "o = S { xs: o.xs.append(i), v: o.v, n: i };"
	if carried {
		update = "o = S { ...o, xs: o.xs.append(i), n: i };"
	}
	return fmt.Sprintf(`enum V { A(i32), B }
struct S { xs: i32[], v: V, n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], v: V.A(7), n: 0 };
    let i: i32 = 0;
    while (i < k) {
        %s
        i = i + 1;
    }
    let vv: V = o.v;
    let r: i32 = 0;
    match (vv) { V.A(x) => { r = x; }, V.B => { r = 0; } }
    return o.xs.len() + r;
}

function main(): i32 {
    let t: i32 = 0;
    let rr: i32 = 0;
    while (rr < 10) { t = t + work(%d); rr = rr + 1; }
    return t & 63;
}`, update, k)
}

// enumFieldAliasExit: `work` returns (2 + k) + 7, ten calls summed, masked to
// six bits.
func enumFieldAliasExit(k int) int { return (10 * (k + 9)) & 63 }

// The enum field read handed to a DIFFERENT local: `p` and `o` both show the
// shared box afterwards, so `o` keeps its mark and the answer must still be
// right (7 + 7 + 3 per call).
const enumFieldAliasForkSrc = `enum V { A(i32), B }
struct S { xs: i32[], v: V, n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], v: V.A(7), n: k };
    let p: S = S { xs: [4], v: o.v, n: o.n + 1 };
    let vo: V = o.v;
    let vp: V = p.v;
    let ro: i32 = 0;
    match (vo) { V.A(x) => { ro = x; }, V.B => { ro = 0; } }
    let rp: i32 = 0;
    match (vp) { V.A(x) => { rp = x; }, V.B => { rp = 0; } }
    return ro + rp + p.n;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(2); r = r + 1; }
    return t / 10;
}`

// --- A fresh variant ctor in a struct-literal enum field (#6681) -------------
//
// No rebind, no loop, one bind: `V.A(7)` builds a sole-owned rc=1 box the struct
// takes over, exactly as `A(7)` does, so neither spelling may retain it. The
// qualified one did, and the box outlived the program — 40 B on a program that
// allocates three objects.
func freshVariantCtorFieldSrc(qualified bool) string {
	ctor := "A(7)"
	if qualified {
		ctor = "V.A(7)"
	}
	return fmt.Sprintf(`enum V { A(i32), B }
struct S { xs: i32[], v: V, n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], v: %s, n: k };
    let vv: V = o.v;
    let r: i32 = 0;
    match (vv) { V.A(x) => { r = x; }, V.B => { r = 0; } }
    return o.xs.len() + o.n + r;
}

function main(): i32 { return work(2) & 63; }`, ctor)
}

// The same field read bound to a DIFFERENT local must keep marking: `p` and `o`
// are both live afterwards, so deep-dropping `o` would reach into what `p`
// shows. Only the self-rebind — where the successor IS this slot — is exempt.
const nestedFieldAliasForkSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 3, data: [9] }, n: k };
    let p: S = S { xs: [4], inner: o.inner, n: o.n + 1 };
    return p.inner.tag + p.inner.data[0] + o.inner.tag + o.inner.data[0] + p.n;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
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

// TestSelfHostNestedFieldAliasRebindX86_64 — both spellings of the same
// self-update reclaim everything, and the shapes the exemption must not reach
// still answer correctly. Every program here must end at live_bytes 0.
func TestSelfHostNestedFieldAliasRebindX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	flat := func(t *testing.T, name string, live int64) {
		t.Helper()
		if live != 0 {
			t.Errorf("%s: live_bytes=%d, want 0", name, live)
		}
	}

	explicitLive, _, explicitExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_explicit", nestedFieldAliasExplicitSrc)
	carriedLive, _, carriedExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_carried", nestedFieldAliasCarriedSrc)

	if explicitExit != 36 || carriedExit != 36 {
		t.Fatalf("exit codes: explicit=%d carried=%d, want 36 for both", explicitExit, carriedExit)
	}
	flat(t, "the `...base` carry", carriedLive)
	flat(t, "the explicit `inner: o.inner` alias (#6653)", explicitLive)

	// #6628. Every superseded box and the `xs` buffer it owned must be released,
	// so a regression grows with k.
	t.Run("array-field-alias-flat-in-k", func(t *testing.T) {
		for _, k := range []int{1, 2, 8, 32} {
			name := fmt.Sprintf("nfa_arrfield_k%d", k)
			live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, nestedFieldAliasArrayFieldSrc(k, false))
			// Every carried buffer is read back after the loop, so a released
			// live buffer shows up here before any byte count does.
			if want := arrayFieldAliasExit(k); exit != want {
				t.Fatalf("k=%d exited %d, want %d — a carried `ys` was released under a live reference", k, exit, want)
			}
			flat(t, name, live)
		}
	})

	t.Run("array-field-alias-matches-the-carry", func(t *testing.T) {
		explicit, _, _ := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfield_x", nestedFieldAliasArrayFieldSrc(8, false))
		carried, _, _ := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfield_c", nestedFieldAliasArrayFieldSrc(8, true))
		flat(t, "the `...o` array carry", carried)
		flat(t, "explicit `ys: o.ys` (#6653)", explicit)
	})

	// `work` returns (2 + k) + 2 + 6 here, so the exit is (10*(k+10)) & 63.
	t.Run("struct-array-field-alias-flat-in-k", func(t *testing.T) {
		for _, k := range []int{1, 2, 8, 32} {
			name := fmt.Sprintf("nfa_structarr_k%d", k)
			live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, nestedFieldAliasStructArraySrc(k))
			if want := (10 * (k + 10)) & 63; exit != want {
				t.Fatalf("k=%d exited %d, want %d — a carried `es` was released under a live reference", k, exit, want)
			}
			flat(t, name, live)
		}
	})

	// #6653's enum route.
	t.Run("enum-field-alias-matches-the-carry", func(t *testing.T) {
		for _, k := range []int{1, 2, 8, 32} {
			name := fmt.Sprintf("nfa_enumfield_k%d", k)
			live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, enumFieldAliasSrc(k, false))
			if want := enumFieldAliasExit(k); exit != want {
				t.Fatalf("k=%d exited %d, want %d — the carried enum box was released under the "+
					"`vv` borrow that reads its payload back", k, exit, want)
			}
			flat(t, name, live)
		}
		carried, _, _ := leakSummary(t, gcc, runner, driverBin, dir, "nfa_enumfield_c", enumFieldAliasSrc(8, true))
		flat(t, "the `...o` enum carry", carried)
	})

	// #6681: the two spellings of one fresh variant construction.
	t.Run("qualified-variant-ctor-field-is-fresh", func(t *testing.T) {
		qualified, _, qExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_qvctor", freshVariantCtorFieldSrc(true))
		bare, _, bExit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_bvctor", freshVariantCtorFieldSrc(false))
		if qExit != 11 || bExit != 11 {
			t.Fatalf("exit codes: qualified=%d bare=%d, want 11 for both", qExit, bExit)
		}
		flat(t, "the bare `A(7)` field", bare)
		flat(t, "the qualified `V.A(7)` field", qualified)
	})

	t.Run("enum-fork-to-other-local-not-exempt", func(t *testing.T) {
		live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_enumfork", enumFieldAliasForkSrc)
		if exit != 17 {
			t.Errorf("exited %d, want 17 — both locals read the shared enum box afterwards, so "+
				"only the self-rebind may be exempted", exit)
		}
		flat(t, "nfa_enumfork", live)
	})

	t.Run("array-fork-to-other-local-not-exempt", func(t *testing.T) {
		live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfork", nestedFieldAliasArrayForkSrc)
		if exit != 27 {
			t.Errorf("exited %d, want 27 — both locals show the shared `ys` buffer afterwards, so "+
				"only the self-rebind may be exempted", exit)
		}
		flat(t, "nfa_arrfork", live)
	})

	t.Run("fork-to-other-local-not-exempt", func(t *testing.T) {
		live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_fork", nestedFieldAliasForkSrc)
		if exit != 27 {
			t.Errorf("exited %d, want 27 — both locals read the shared inner box afterwards, so "+
				"only the self-rebind may be exempted from the field-move mark", exit)
		}
		flat(t, "nfa_fork", live)
	})
}
