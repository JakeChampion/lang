package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The array-of-enums class reaches its siblings' reclaim shapes ------------
//
// `ARRENUM` (#5474) was the last array-of-X element kind to get an element walk
// at all, and it arrived with only ONE admitted shape: `var xs: E[] = [E.A(..)]`,
// a non-empty literal of fresh ctors, never reassigned. Both other ways to build
// one leaked their whole structure, on the shapes ordinary code has to use:
//
//	var xs: E[] = mk(i)                       producer, 3 allocs / 1 free
//	var xs: E[] = []; xs = xs.append(E.A(..)) append-built, 4 allocs / 2 frees
//	var xs: E[] = mk(i)  // mk append-built   both, 4 allocs / 2 frees
//
// 80 bytes a round each, unbounded, against 0 on native and interp. The struct
// side had both of these — `ARRSTRUCTF:` and `ARRSTRUCTA:` (#6535) — so this is
// three transcriptions rather than three designs, and they compose: the third
// shape is the first two together, the same composition #7548 made for arrstruct.
//
// The per-element rule is the one the literal already applied
// (`fresh_rcpayload_enum_init`): a bare-ident element would alias a live enum box
// this class's walk could dangle, and that walk FREES the element box rather than
// deccing it (`emit_enum_variant_drops` zeroes the slot), so a wrong admission
// here is a double free rather than a leak. `append_bare_ident_elem` pins the
// refusal.
//
// One deliberate non-transcription: `with` is not admitted, though arrstruct's
// self-store predicate takes it. `with` REPLACES, so the superseded element has
// to be released at the store, and this class's release is a variant dispatch
// that frees the box. That needs its own emitter, so it is its own slice.
//
// The class's escape gate stays as tight as it was — only `xs.len()` — because
// an element extraction risks a double free here rather than a leak.
// `append_then_extract` pins that it still refuses, now that the credit reaches
// further. Only the self-append rebind is newly permitted, and only for a
// candidate that earned the append-built credit.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type arrenumProdCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const arrenumProdMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrenumProdDecl = "enum E { A(i32[]), B }\n"

func arrenumProdCases() []arrenumProdCase {
	return []arrenumProdCase{
		{
			// Both new shapes at once: an append-built local handed back by a
			// producer. 1000 allocs / 400 frees before, 22400 bytes over 200
			// rounds, against native's 800/800.
			name: "producer_returns_local",
			src: arrenumProdDecl + `function mk(i: i32): E[] { var xs: E[] = []; xs = xs.append(E.A([i, i + 1])); xs = xs.append(E.B); return xs; }
function round(i: i32): i32 { var v: E[] = mk(i); return v.len(); }` + arrenumProdMain,
			want: 68, balance: true,
		},
		{
			// The producer registry alone: every return a fresh literal. 800
			// allocs / 200 frees before — a QUARTER reclaimed, because the
			// consumer's slot took the bare buffer dec and no element was walked.
			name: "producer_returns_literal",
			src: arrenumProdDecl + `function mk(i: i32): E[] { return [E.A([i, i + 1]), E.B]; }
function round(i: i32): i32 { var v: E[] = mk(i); return v.len(); }` + arrenumProdMain,
			want: 68, balance: true,
		},
		{
			// The append-built local alone, no producer. 1000/400 before.
			name: "append_built_local",
			src: arrenumProdDecl + `function round(i: i32): i32 { var v: E[] = []; v = v.append(E.A([i, i + 1])); v = v.append(E.B); return v.len(); }` +
				arrenumProdMain,
			want: 68, balance: true,
		},
		{
			// The one shape that was already credited. Must stay so.
			name: "literal_init",
			src: arrenumProdDecl + `function round(i: i32): i32 { var v: E[] = [E.A([i, i + 1]), E.B]; return v.len(); }` +
				arrenumProdMain,
			want: 68, balance: true,
		},
		{
			// THE DOUBLE-FREE GUARD. The appended element is a bare ident naming a
			// live enum local. The walk frees the element box, so admitting this
			// would free `e`'s box under its own owner — not a leak, a corruption.
			// Stays a safe leak.
			name: "append_bare_ident_elem",
			src: arrenumProdDecl + `function round(i: i32): i32 { var e: E = E.A([i, i + 1]); var v: E[] = []; v = v.append(e); return v.len(); }` +
				arrenumProdMain,
			want: 34,
		},
		{
			// The class's tight escape gate must keep refusing an element
			// extraction — `var e: E = v[0]` binds an element box the walk would
			// free — even now that the credit reaches the append-built shape.
			name: "append_then_extract",
			src: arrenumProdDecl + `function round(i: i32): i32 { var v: E[] = []; v = v.append(E.A([i, i + 1])); var e: E = v[0]; return v.len() + match (e) { E.A(p) => p.len(), E.B => 0 }; }` +
				arrenumProdMain,
			want: 19,
		},
		{
			// Refused: a reassignment that is not a self-append. `v` may hold
			// another producer's structure by the end.
			name: "append_foreign_rebind",
			src: arrenumProdDecl + `function other(i: i32): E[] { return [E.A([i]), E.B]; }
function round(i: i32): i32 { var v: E[] = []; v = v.append(E.A([i, i + 1])); if (i % 3 == 0) { v = other(i); } return v.len(); }` + arrenumProdMain,
			want: 18,
		},
		{
			// Refused: the producer's local escapes by a route other than the
			// return, so the callee cannot promise the caller owns it outright.
			name: "producer_local_escapes",
			src: arrenumProdDecl + `function sink(vs: E[]): i32 { return vs.len(); }
function mk(i: i32): E[] { var xs: E[] = []; xs = xs.append(E.A([i, i + 1])); var n: i32 = sink(xs); return xs; }
function round(i: i32): i32 { var v: E[] = mk(i); return v.len(); }` + arrenumProdMain,
			want: 34,
		},
		{
			// THE OVER-RELEASE GUARD, the shape #7335 recorded as the one a
			// careless widening breaks: two same-named `v`, one from the producer
			// and one a bare alias of a parameter. The credit is site-keyed, so
			// the alias cannot inherit it — 99 here would be main's `b` freed
			// under it, and no byte count would say so. 203/101 before, 203/201
			// now: the producer-fed binding is credited and the alias still is
			// not, which is the right answer rather than a partial one.
			name: "sibling_alias",
			src: arrenumProdDecl + `function mk(i: i32): E[] { var xs: E[] = []; xs = xs.append(E.A([i, i + 1])); return xs; }
function round(base: E[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: E[] = mk(i);  t = t + v.len(); }
    if (i % 2 == 1) { var v: E[] = base;   t = t + v.len(); }
    return t;
}
function main(): i32 { var b: E[] = [E.A([7, 8])]; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17,
		},
	}
}

// TestSelfHostArrEnumProducerX86_64 — an array-of-enums built by a producer or by
// self-append is reclaimed, and the shapes whose elements this frame does not own
// keep refusing the credit.
func TestSelfHostArrEnumProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrenumProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the element walk "+
					"freed a box another owner still holds)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0. The element boxes and "+
					"their payload arrays are most of the allocations here, so a "+
					"withheld walk shows as frees at a fraction of allocs", tc.name, summary)
			}
		})
	}
}

// TestSelfHostArrEnumProducerWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostArrEnumProducerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arrenum producer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrenumProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "arrenumprod_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s: wasm exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
