package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// optaarrForInCases pin the #7414 widening: an `Option[<scalar-arr>][]` local
// iterated with `for o in xs` keeps its "OPTAARR:" credit when the loop variable
// stays confined to the loop, and loses it — back to the leak-safe shallow buffer
// dec — when anything reachable from the loop body could outlive it.
//
// Before the widening, arrarr_row_escapes refused EVERY `for o in xs` outright,
// so the issue's repro leaked the option boxes and their payload buffers whole:
// 150 rounds measured allocs=750 frees=150 live_bytes=24000 on the self-host
// against native's 750/750/0, a flat 160 B/round that no exit code could see.
// `match (xs[i])` next to it already balanced, so only the ITERATION was ever the
// difference.
//
// The loop var is an element BOX borrowed with no retain (measured: zero rc_inc,
// the same as the arr-of-arr row form arrarr_row_escapes_iter already admits) and
// the bind is transient — the loop ends before the exit sweep. What the widening
// has to establish is that nothing reachable from the arm outlives the loop:
// body_unsafe_for_match_borrow admits the box in exactly one position (the bare
// scrutinee of a `match (o)`), and elem_box_iter_bind_escapes vets the bindings
// that reading hands out with the STRICT binding_escapes_arm.
//
// SOUNDNESS, measured rather than argued: with those two checks stubbed out and
// nothing else changed, `box-escapes-loop-uaf` below exits 100 on the self-host —
// the payload read back as the recycling allocation's 7777 — while native exits 0,
// with allocs=1350 frees=1200 and __rc_underflow_count() at ZERO throughout. That
// is the failure mode this class sets: a use-after-free that reads plausible bytes,
// invisible to the underflow counter, and invisible to `-sanitize` too, whose
// quarantine keeps the freed block out of the recycling path and lets the stale
// read return the right answer. Each refusal row below therefore pins its LEAK: a
// zero there means the credit widened past what it can prove and the row needs
// re-proving, not re-banking.
var optaarrForInCases = []struct {
	name string
	src  string
	want int
	// balance: the row is admitted, so it must reclaim everything.
	balance bool
	// leaks: the row is refused, so the shallow fallback must still be taken.
	// Its VALUE must be exact and the underflow counter zero regardless.
	leaks bool
}{
	// The issue's repro, at two round counts. The leak was flat per round, so a
	// single count cannot tell "reclaimed" from "reclaimed less often".
	{"forin-repro-50", optaarrProg(50, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}`), 10, true, false},
	{"forin-repro-200", optaarrProg(200, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}`), 10, true, false},
	// A None element alongside the Some ones, and a `.len()` borrow of the
	// payload: the walk must handle the tag-1 box and the borrow alike.
	{"forin-none-mixed", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), None, Some([i + 2, i + 3])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0] + p.len(); }, None => { t = t + 1; } } }
    return t + keep.len();
}`), 16, true, false},
	// A second payload type from the same leak-safe-array family, so the class
	// is not pinned on i32[] alone.
	{"forin-f64-payload", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[f64[]][] = [Some([1.0, 2.0]), Some([3.0, 4.0])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p.len(); }, None => {} } }
    return t + keep.len() + i - i;
}`), 6, true, false},
	// CONTROL: the `.len()`-only form the class already admitted. It must not
	// move — the widening touches the iteration, nothing else.
	{"len-only-control", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    return keep.len();
}`), 2, true, false},
	// CONTROL: `match (xs[i])` already balanced before the widening
	// (optaarr_elem_payload_escapes admits the transient payload borrow), and
	// still must.
	{"index-match-control", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    match (keep[0]) { Some(p) => { t = t + p[0]; }, None => {} }
    return t + keep.len();
}`), 5, true, false},

	// ── refusals ────────────────────────────────────────────────────────────
	// A GUARDED arm. binding_escapes_arm does vet the guard, so this is a
	// deliberate narrowing rather than a demonstrated hazard — the same one
	// consumed_rcpayload_enum_frees makes next door, because under a guard which
	// arm ran is not syntactic and this admission's proof is per-arm.
	{"guarded-arm-refused", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) when p[0] > 100000 => { t = t + 7; }, Some(q) => { t = t + q[0]; }, None => {} } }
    return t + keep.len();
}`), 10, false, true},
	// The arm's payload binding STORED to an outer local — it outlives the arm,
	// so nothing may free the buffer it names.
	{"payload-escapes-arm-refused", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var held: i32[] = [0];
    for e in keep { match (e) { Some(p) => { held = p; }, None => {} } }
    return held[0] + keep.len();
}`), 7, false, true},
	// The same binding handed to a CALL. Every inner proof runs under an empty
	// borrowability registry, so a call argument is conservatively a retain and
	// the verdict stays registry-independent.
	{"payload-to-call-refused", optaarrProg(150, `
function sink(xs: i32[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + sink(p); }, None => {} } }
    return t + keep.len();
}`), 6, false, true},
	// The LOOP VARIABLE itself stored out. This is the row the soundness note
	// above measures: admit it and the returned option box names a payload the
	// sweep already freed, which the recycling allocations then overwrite.
	{"box-escapes-loop-uaf", `
function pick(i: i32): Option[i32[]] {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var last: Option[i32[]] = None;
    for e in keep { last = e; }
    return last;
}

function round(i: i32): i32 {
    var o: Option[i32[]] = pick(i);
    var a: i32[] = [7777, 7777];
    var b: i32[] = [7777, 7777];
    var c: i32[] = [7777, 7777];
    var t: i32 = 0 - 9;
    match (o) { Some(p) => { t = p[0]; }, None => { t = 0 - 5; } }
    return t - i - 2 + a[0] - b[0] + c[0] - 7777;
}

function main(): i32 {
    var i: i32 = 0;
    var bad: i32 = 0;
    while (i < 150) { if (round(i) != 0) { bad = bad + 1; } i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0, false, true},
	// A bare element BIND. Unchanged by the widening, which separates the
	// iteration from the index bind: this class takes no dup at the bind, so the
	// bound box is a borrow the sweep would dangle.
	{"elem-bind-refused", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var o = keep[0];
    var t: i32 = 0;
    match (o) { Some(p) => { t = t + p[0]; }, None => {} }
    return t + keep.len();
}`), 5, false, true},
	// OUT OF CLASS, both directions. A `string[]` payload is not a leak-safe
	// scalar array (is_leaksafe_array_field), and a nested `Option[Option[T[]]]`
	// element is not an option of an array at all, so optaarr_ann_is declines
	// both before any escape gate runs. They are here so a later widening of the
	// ANNOTATION cannot land silently on shapes this proof never covered.
	{"string-payload-out-of-class", optaarrProg(150, `
function round(i: i32): i32 {
    var pre: string = "abcdefgh";
    var keep: Option[string[]][] = [Some([pre + "x", pre + "y"]), Some([pre + "z"])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p.len(); }, None => {} } }
    return t + keep.len() + i - i;
}`), 5, false, true},
	{"nested-option-out-of-class", optaarrProg(150, `
function round(i: i32): i32 {
    var keep: Option[Option[i32[]]][] = [Some(Some([i, i + 1])), Some(None)];
    var t: i32 = 0;
    for e in keep { match (e) { Some(inner) => { match (inner) { Some(p) => { t = t + p[0]; }, None => { t = t + 1; } } }, None => {} } }
    return t + keep.len();
}`), 6, false, true},
}

// optaarrProg wraps a `round(i)` definition in the shared driver: `rounds`
// iterations to churn the structure, the underflow counter folded into the exit,
// then one more round as the answer so a miscompiled payload read shows up as a
// wrong exit code rather than only as a leak.
func optaarrProg(rounds int, round string) string {
	n := strconv.Itoa(rounds)
	return round + `
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) { acc = (acc + round(i)) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return round(3);
}`
}

// TestSelfHostOptaarrForInIterX86_64 is the PRIMARY leg: it is the only one with
// a leak detector, so it is the only one that can tell an admitted row from a
// refused one at all. The other two legs check that the widened credit emits
// something that still computes the right answer on their backends.
func TestSelfHostOptaarrForInIterX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optaarrForInCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optaarrforin_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the payload read back wrong; "+
					"97 = value corrupted; 139 = it read freed memory)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — an admitted row must reclaim everything", tc.name, summary)
			}
			if tc.leaks && live == 0 {
				t.Errorf("%s: %s — this row is REFUSED and must still take the shallow fallback. "+
					"A balance here means the credit reaches a shape the confinement proof does not "+
					"cover; re-prove it before re-banking the row", tc.name, summary)
			}
		})
	}
}

// TestSelfHostOptaarrForInIterArm64 — the credit and its release helper are
// shared irlower.fern / Fern-source IR, so this leg exists to catch a backend
// that lowers the widened element walk into something that computes differently,
// not to re-measure the leak (no detector here).
func TestSelfHostOptaarrForInIterArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optaarrForInCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "optaarrforin_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (99 = over-release/underflow; 100 = the payload read "+
					"back wrong; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostOptaarrForInIterWasm — the wasm twin of the arm64 leg. The class
// resolves through the same shared Fern-source release helper on this backend, so
// what this checks is that the widened element walk lowers to something wasmtime
// runs to the same answer.
func TestSelfHostOptaarrForInIterWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping optaarr for-in wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optaarrForInCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "optaarrforin_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (99 = over-release/underflow; 100 = the payload read "+
					"back wrong; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
