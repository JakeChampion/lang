package e2ecompiler

import (
	"fmt"
	"testing"
)

func borrowedFieldArgSrc(k int) string {
	return fmt.Sprintf(`enum V { A(i32[]), B }
struct S { xs: i32[], v: V, n: i32 }

function tagof(v: V): i32 {
    match (v) {
        V.A(d) => { return d.len(); },
        V.B => { return 0; }
    }
}

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], v: V.A([9, 8, 7]), n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), v: o.v, n: i };
        i = i + 1;
    }
    return o.xs.len() + tagof(o.v);
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, k)
}

func borrowedFieldArgExit(k int) int { return (10 * (k + 5)) & 63 }

const borrowedFieldArgRetainedSrc = `enum V { A(i32[]), B }
struct S { xs: i32[], v: V, n: i32 }

function tagof(v: V): i32 {
    match (v) {
        V.A(d) => { return d.len(); },
        V.B => { return 0; }
    }
}

@noinline function keep(v: V): V { return v; }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], v: V.A([9, 8, 7]), n: 0 };
    let kept: V = keep(o.v);
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), v: o.v, n: i };
        i = i + 1;
    }
    return o.xs.len() + tagof(kept);
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

func borrowedStructFieldArgSrc(k int, viaCall bool) string {
	read := "o.inner.tag"
	if viaCall {
		read = "itag(o.inner)"
	}
	return fmt.Sprintf(`struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

function itag(v: I): i32 { return v.tag; }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9] }, n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: o.inner, n: i };
        i = i + 1;
    }
    return o.xs.len() + %s;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, read, k)
}

const borrowedStructFieldArgRetainedSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }

@noinline function keepi(v: I): I { return v; }

function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 3, data: [9] }, n: 0 };
    let p: I = keepi(o.inner);
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: o.inner, n: i };
        i = i + 1;
    }
    return o.xs.len() + p.tag + p.data[0];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

func borrowedStringFieldArgSrc(k int, viaCall bool) string {
	read := "o.name.len()"
	if viaCall {
		read = "slen(o.name)"
	}
	return fmt.Sprintf(`struct S { name: string, xs: i32[], n: i32 }

function slen(v: string): i32 { return v.len(); }

function work(k: i32): i32 {
    let o: S = S { name: "seed", xs: [1, 2], n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { name: "ab" + "cd", xs: o.xs.append(i), n: i };
        i = i + 1;
    }
    return o.xs.len() + %s;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, read, k)
}

const borrowedStringFieldArgRetainedSrc = `struct S { name: string, xs: i32[], n: i32 }

@noinline function keeps(v: string): string { return v; }

function work(k: i32): i32 {
    let o: S = S { name: "seed", xs: [1, 2], n: 0 };
    let kept: string = keeps(o.name);
    let i: i32 = 0;
    while (i < k) {
        o = S { name: "ab" + "cd", xs: o.xs.append(i), n: i };
        i = i + 1;
    }
    return o.xs.len() + kept.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

func TestSelfHostBorrowedFieldArgReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	var first int64 = -1
	for _, k := range []int{1, 2, 8, 32} {
		name := fmt.Sprintf("bfa_k%d", k)
		live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, borrowedFieldArgSrc(k))
		if want := borrowedFieldArgExit(k); exit != want {
			t.Fatalf("k=%d exited %d, want %d — the payload `tagof` reads back was released early", k, exit, want)
		}
		if first < 0 {
			first = live
			continue
		}
		if live != first {
			t.Errorf("k=%d leaked %d bytes against %d at k=1 — `tagof(o.v)` is taking the local's "+
				"reclaim credit again, so every superseded box and its `xs` buffer strands (#6691)",
				k, live, first)
		}
	}

	t.Run("retaining-callee-still-marks", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "bfa_retained", borrowedFieldArgRetainedSrc)
		if exit != 2 {
			t.Errorf("exited %d, want 2 — `keep` returns its param, so `kept` aliases the box `o` "+
				"holds and the exemption must not reach it", exit)
		}
	})

	t.Run("struct-field-arg-matches-the-direct-read", func(t *testing.T) {
		for _, k := range []int{1, 8, 32} {
			viaCall, _, callExit := leakSummary(t, gcc, runner, driverBin, dir,
				fmt.Sprintf("bsfa_call_k%d", k), borrowedStructFieldArgSrc(k, true))
			direct, _, directExit := leakSummary(t, gcc, runner, driverBin, dir,
				fmt.Sprintf("bsfa_direct_k%d", k), borrowedStructFieldArgSrc(k, false))
			if want := (10 * (k + 2)) & 63; callExit != want || directExit != want {
				t.Fatalf("k=%d exits: call=%d direct=%d, want %d for both", k, callExit, directExit, want)
			}
			if direct != 0 {
				t.Errorf("k=%d: the direct `o.inner.tag` read leaked %d bytes — it has been an exact 0 "+
					"since #6653, so the comparison below is against a moved baseline", k, direct)
			}
			if viaCall != direct {
				t.Errorf("k=%d: `itag(o.inner)` leaked %d bytes against the direct read's %d — a "+
					"borrowable param cannot retain, so the read scan must admit the type either "+
					"way (#6698)", k, viaCall, direct)
			}
		}
	})

	t.Run("retaining-callee-still-marks-in-the-read-scan", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "bsfa_retained", borrowedStructFieldArgRetainedSrc)
		if exit != 28 {
			t.Errorf("exited %d, want 28 — `keepi` returns its param, so `p` aliases the inner box "+
				"and reads it back after the loop; admitting the type would free it early", exit)
		}
	})

	t.Run("string-field-arg-matches-the-direct-read", func(t *testing.T) {
		for _, k := range []int{1, 8, 32} {
			viaCall, _, callExit := leakSummary(t, gcc, runner, driverBin, dir,
				fmt.Sprintf("bstrfa_call_k%d", k), borrowedStringFieldArgSrc(k, true))
			direct, _, directExit := leakSummary(t, gcc, runner, driverBin, dir,
				fmt.Sprintf("bstrfa_direct_k%d", k), borrowedStringFieldArgSrc(k, false))
			if want := (10 * (k + 6)) & 63; callExit != want || directExit != want {
				t.Fatalf("k=%d exits: call=%d direct=%d, want %d for both", k, callExit, directExit, want)
			}
			if direct != 0 {
				t.Errorf("k=%d: the direct `o.name.len()` read leaked %d bytes — it is the baseline "+
					"the call spelling is compared against, and it has been an exact 0", k, direct)
			}
			if viaCall != direct {
				t.Errorf("k=%d: `slen(o.name)` leaked %d bytes against the direct read's %d — a "+
					"borrowable param cannot retain (and cannot SLICE, which is the string-specific "+
					"hazard the registry already refuses), so the read scan must admit the type "+
					"either way (#6703)", k, viaCall, direct)
			}
		}
	})

	t.Run("retaining-callee-still-marks-in-the-string-scan", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "bstrfa_retained", borrowedStringFieldArgRetainedSrc)
		if exit != 12 {
			t.Errorf("exited %d, want 12 — `keeps` returns its param, so `kept` is a second reference "+
				"to `o.name`'s buffer and is read back after the loop", exit)
		}
	})
}
