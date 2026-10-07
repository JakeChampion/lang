package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLiteralArgReclaimWasmIR is the wasm port of
// TestSelfHostLiteralArgReclaimIRX86_64 (#4355 slice 6). On wasm a string
// literal is DATA-SECTION (no per-call box), so there was never a leak here —
// but the reclaim is emitted at the IR layer for every backend, so this pins
// that the wasm $__fern_str_free mapping ($__fern_arr_dec, heap-base-guarded)
// no-ops safely on the data-section pointer: boundedness + detector zero +
// the retained-value safety.
func TestSelfHostLiteralArgReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host literal-arg-reclaim wasm IR e2e")
	}
	l := newWasmStdlibLoader(t)
	dir := t.TempDir()

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// The fresh PRODUCER CALL in argument position. No heap-flatness
		// assertion on this leg — the WAT driver's own allocations sit between
		// any two probes — so __rc_underflow_count() plus the value is the witness
		// that the release is balanced rather than over-eager.
		// The ARRAY sibling. No flatness assertion on this leg — the WAT
		// driver's own allocations sit between any two probes — so
		// __rc_underflow_count() plus the values are the witness.
		{"producer-call-arr-arg-borrowable-wasm", `function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function size(d: i32[]): i32 { return d.len(); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) { if (size(mk(i)) != 3) { bad = 1; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"producer-call-arr-arg-returned-safe-wasm", `function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
@noinline function pick(d: i32[]): i32[] { return d; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) { let r: i32[] = pick(mk(i)); if (r.len() != 3 || r[2] != i + 2) { bad = 1; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"producer-call-arg-borrowable-wasm", `import "std/i32";
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function size(s: string): i32 { return s.len(); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) { if (size(mks(i)) < 41) { bad = 1; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		// Refused: the callee returns the argument, so the result aliases the
		// temp and is read back.
		{"producer-call-arg-returned-safe-wasm", `import "std/i32";
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
@noinline function pick(s: string): string { return s; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) { let r: string = pick(mks(i)); if (r.len() < 41) { bad = 1; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"literal-arg-borrowable-flat-wasm", `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + readit("ab"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 1500) { acc = acc + readit("ab"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 3400) { return 97; }
    return 0;
}`, 0},
		{"literal-arg-retained-safe-wasm", `@noinline function keepit(nm: string): string { return nm; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let got: string = keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		// #6544: the same reclaim at a METHOD call. The release lands at the IR
		// layer, so wasm emits it too — and its literals are data-section, so
		// what this pins is that the $__fern_arr_dec mapping stays a guarded
		// no-op there rather than a leak fix.
		{"method-literal-arg-borrowable-flat-wasm", `function (s: string) readit(nm: string): i32 { return s.len() + nm.len(); }
function main(): i32 {
    let recv: string = "rr";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + recv.readit("ab"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 1500) { acc = acc + recv.readit("ab"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (recv.len() != 2) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 6800) { return 97; }
    return 0;
}`, 0},
		{"method-literal-arg-retained-safe-wasm", `function (s: string) keepit(nm: string): string { return nm; }
function main(): i32 {
    let recv: string = "rr";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let got: string = recv.keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"method-literal-arg-consumed-safe-wasm", `struct Box { tag: string, n: i32 }
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let b: Box = Box { tag: "start", n: i % 8 };
        let r: Box = b.relabel("fresh-tag-value");
        if (r.tag.len() != 15) { bad = 1; }
        if (b.tag.len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		// The gate that makes the counted-retain STRING tier sound, on the leg
		// where the release is a $__fern_arr_dec on a data-section pointer: `esc`
		// disqualifies C from the STRFLDOK scan, so the field is stored with no
		// retain and the caller's box is its only reference.
		{"counted-retain-str-arg-uncounted-store-safe-wasm", `struct C { name: string, args: string }
function mk(name: string, args: string): C { return C { name: name, args: args }; }
function esc(c: C): string[] { let o: string[] = []; return o.append(c.name); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let a: C = mk("fetch", "GET /a");
        let e: string[] = esc(a);
        if (e[0].len() != 5) { bad = 1; }
        if (a.args.len() != 6) { bad = 1; }
        if (a.args[0] != 71) { bad = 1; }
        if (a.name[0] != 102) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		// The byte-index arm of the string vocabulary — `t[0]` is a value copy,
		// so the parameter stays credited and the struct's field survives the
		// release.
		{"counted-retain-str-arg-index-read-wasm", `struct Q { tag: string, k: i32 }
function mkq2(t: string, k: i32): Q { return Q { tag: t, k: k + (t[0] as i32) }; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let c: Q = mkq2("tag", i);
        if (c.tag != "tag") { bad = 1; }
        if (c.k != i + 116) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := l.emit(t, tc.src)
			if len(wat) == 0 {
				t.Fatalf("no WAT for %q", tc.src)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("literal-arg-reclaim wasm IR %q = %d, want %d (98 = leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", tc.name, got, tc.expected)
			}
		})
	}
}
