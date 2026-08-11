package e2eselfhost

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
// spelling at 800: the override's retain on `o.inner` has no counterpart,
// because the nested-struct arm of `__field_reclaim_S` is gated on
// `structfldok:S` and the explicit field read is what disqualifies the type.
// That is #6653 — a third defect on this shape, not a regression of this one —
// so the assertion is now an EXACT pin of the residual rather than equality.
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
    var o: S = S { xs: [1, 2], ys: [5, 6, 7], n: 0 };
    var i: i32 = 0;
    while (i < k) {
        %s
        i = i + 1;
    }
    return o.xs.len() + o.ys.len() + o.ys[2];
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
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
    var o: S = S { xs: [1, 2], es: [E { a: 3, b: 4 }, E { a: 5, b: 6 }], n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), es: o.es, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.es.len() + o.es[1].b;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, k)
}

// The array read handed to a DIFFERENT local, the array sibling of the fork row
// below: `p` and `o` are both live afterwards and both show the shared buffer,
// so `o` must keep its field-move mark.
const nestedFieldAliasArrayForkSrc = `struct S { xs: i32[], ys: i32[], n: i32 }

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], ys: [5, 6, 7], n: k };
    var p: S = S { xs: [4], ys: o.ys, n: o.n + 1 };
    return p.ys[0] + p.ys[2] + o.ys[0] + o.ys[2] + p.n;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(2); r = r + 1; }
    return t / 10;
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
	if carriedLive != 0 {
		t.Errorf("the `...base` carry leaked %d bytes — #6620 took it to an exact 0", carriedLive)
	}
	// 800 = one `I` box plus its one-element `data` buffer, per call, ten calls.
	// It is the unbalanced override retain (#6653), NOT the credit loss #6623
	// fixed: that one was per-ITERATION and read 10160 here. Anything above 800
	// means the credit is gone again; 0 means #6653 landed, and then this becomes
	// `explicitLive != carriedLive` again.
	if explicitLive != 800 {
		t.Errorf("live_bytes: explicit alias=%d, want exactly 800 — the `...base` carry is at "+
			"%d and the two spellings mean the same thing, so the gap is #6653's unbalanced "+
			"retain and nothing else", explicitLive, carriedLive)
	}

	// #6628. The defect is PER-ITERATION — every superseded box and the `xs`
	// buffer it owned was stranded — so the k curve is the discriminator, not the
	// absolute count: 1440 / 2160 / 9840 / 98160 bytes at k = 1 / 2 / 8 / 32
	// before, one flat residual after.
	t.Run("array-field-alias-flat-in-k", func(t *testing.T) {
		var first int64 = -1
		for _, k := range []int{1, 2, 8, 32} {
			name := fmt.Sprintf("nfa_arrfield_k%d", k)
			live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, nestedFieldAliasArrayFieldSrc(k, false))
			// Every carried buffer is read back after the loop, so a released
			// live buffer shows up here before any byte count does.
			if want := arrayFieldAliasExit(k); exit != want {
				t.Fatalf("k=%d exited %d, want %d — a carried `ys` was released under a live reference", k, exit, want)
			}
			if first < 0 {
				first = live
				continue
			}
			if live != first {
				t.Errorf("k=%d leaked %d bytes against %d at k=1 — the per-iteration strand is "+
					"back: the local lost its __field_reclaim_S credit (#6628)", k, live, first)
			}
		}
	})

	// What the flat residual IS, pinned exactly so it cannot grow quietly. The
	// override path incs the aliased `ys`, but __field_reclaim_ cow-SKIPS a field
	// that is pointer-equal in old and new, so that inc is never balanced: one
	// stranded `ys` buffer per call, 48 B for three i32 elements. The `...o`
	// spelling reaches an exact 0 because its base-copy path emits no inc for an
	// array field. Same unbalanced retain as the nested-struct row above, by a
	// different route — #6653.
	t.Run("array-field-alias-residual-is-the-unbalanced-inc", func(t *testing.T) {
		explicit, _, _ := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfield_x", nestedFieldAliasArrayFieldSrc(8, false))
		carried, _, _ := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfield_c", nestedFieldAliasArrayFieldSrc(8, true))
		if carried != 0 {
			t.Errorf("the `...o` array carry leaked %d bytes — it reached an exact 0 before this "+
				"change and nothing here touches its path", carried)
		}
		if explicit != 480 {
			t.Errorf("explicit array alias leaked %d bytes, want exactly 480 (one 3-element `ys` "+
				"buffer per call, 10 calls). Lower means the cow-skipped inc got balanced — "+
				"delete this row and assert equality with the carry instead; higher means a "+
				"second buffer joined it", explicit)
		}
	})

	// `work` returns (2 + k) + 2 + 6 here, so the exit is (10*(k+10)) & 63.
	t.Run("struct-array-field-alias-flat-in-k", func(t *testing.T) {
		var first int64 = -1
		for _, k := range []int{1, 2, 8, 32} {
			name := fmt.Sprintf("nfa_structarr_k%d", k)
			live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, nestedFieldAliasStructArraySrc(k))
			if want := (10 * (k + 10)) & 63; exit != want {
				t.Fatalf("k=%d exited %d, want %d — a carried `es` was released under a live reference", k, exit, want)
			}
			if first < 0 {
				first = live
				continue
			}
			if live != first {
				t.Errorf("k=%d leaked %d bytes against %d at k=1 — an array-of-struct field takes "+
					"the same ExprFieldAccess retain a scalar-element one does (#6628)", k, live, first)
			}
		}
	})

	t.Run("array-fork-to-other-local-not-exempt", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "nfa_arrfork", nestedFieldAliasArrayForkSrc)
		if exit != 27 {
			t.Errorf("exited %d, want 27 — both locals show the shared `ys` buffer afterwards, so "+
				"only the self-rebind may be exempted", exit)
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
