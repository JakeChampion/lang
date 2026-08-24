package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The BUILTIN string[] producers — `s.split(sep)` and `s.lines()` — build an
// array whose element boxes the runtime creates and stores nowhere else. Nothing
// credited them: collect_fresh_strarr_names walks a string[] local's elements and
// requires each to be a fresh expression it can see, and a call's result is not
// one, so a split/lines local fell through to the shallow buffer-only dec and
// every element box leaked.
//
// The credit rides a prefix of its own ("SARRB:") because it needs a gate the
// existing one does not have. reclaimable_names_of is a NAME-level pass: it sees
// `var xs = recv.split(sep)` and has no type for `recv`. A user method named
// `split` on some other type answers to the same name, and its elements may be
// aliases of something the receiver still owns. So the name-level pass collects
// candidates, the BINDING SITE — the one place that knows the receiver's type —
// records whether the producer was really the builtin, and slot_is_reclaimable_
// strarr requires both halves.
//
// That type gate is witnessed by fault, not just by emission. strArrBuiltinType
// GateSrc below is the shape it stops: a `(h: Holder) split` returning a fresh
// array of h.xs's element boxes. A compiler with the name matched and the type
// confirmation dropped over-releases those boxes — rc underflow (99) on wasm,
// and on x86-64 a churn string is handed the freed box and the read-back fails
// (97). Both compilers here return 0.
//
// Measured, 400 rounds of the harness below; each arrow is a pair of compilers
// built from the same commit:
//
//	case                  x86-64                    wasm
//	split          172800 -> 0            204800 -> 67200
//	lines            9600 -> 0             57600 ->  9600
//	split + lines  182400 -> 0            262400 -> 76800
//
// The two columns were fixed by two different pieces. wasm's split COPIES, so
// crediting the class was enough there. The register backends' split used to
// yield zero-copy views needing a view-aware walk; since #7230 the segments
// are OWNED COPIES and the class takes the ordinary __fern_str_arr_free.
//
// What is left on wasm (67200 for the 18-part split) is a separate question from
// this one and is not the element boxes.
//
// One honest limit, on the NON-ESCAPE half rather than the type half. Building a
// compiler with strarr_unsafe_for's verdict dropped for this class does change
// emission — `first_of` below goes from __fern_arr_dec to the element walk, so
// the guard demonstrably decides something — but no probe here faults under it,
// including with slice decoys sized to the 24-byte class a freed element box
// lands in. So the escape cases below are correctness gates, not fault
// witnesses; the half they rest on is the one the `SARR:` class already carries,
// unchanged.

const strArrBuiltinPrelude = `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

// strArrBuiltinHeap wraps a `round` body in the churn/heap-delta harness, with
// the ceiling baked in per leg. The register legs sit at 0 and are gated tight;
// the wasm ones carry a residue that is not the element boxes, so their ceilings
// are set between the fixed number and the parent's.
func strArrBuiltinHeap(body string, limit int) string {
	return strArrBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
` + body + `
}
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= ` + fmt.Sprint(limit) + `) { return 98; }
    return 0;
}`
}

var strArrBuiltinHeapCases = []struct {
	name    string
	body    string
	regMax  int
	wasmMax int
}{
	// 204800 -> 67200 on wasm. What is left is the buffer plus the boxes the
	// per-element walk still declines; the elements themselves are gone.
	{"strarr-builtin-split", `    var parts: string[] = base.split("-");
    return parts.len();`, 4096, 100000},
	// 57600 -> 9600. One line, so one element — the smallest shape that shows the
	// walk happening at all.
	{"strarr-builtin-lines", `    var ls: string[] = base.lines();
    return ls.len();`, 4096, 16000},
	// Reading the elements does not disturb the credit: an index READ is not an
	// escape, and strarr_unsafe_for says so.
	{"strarr-builtin-split-elements-read", `    var parts: string[] = base.split("-");
    var n: i32 = parts.len();
    if (parts[0] != "abcdefgh") { return 0 - 1; }
    if (parts[1] != "a") { return 0 - 2; }
    if (!parts[n - 1].starts_with("0123")) { return 0 - 3; }
    return n;`, 4096, 100000},
	// Two builtin producers in one frame, so the sweep has to credit both slots
	// rather than the first one it meets.
	{"strarr-builtin-split-and-lines", `    var parts: string[] = base.split("-");
    var ls: string[] = base.lines();
    return parts.len() + ls.len();`, 4096, 110000},
}

// strArrBuiltinTypeGateSrc is the type gate's witness. `parts` never escapes
// `round`, so the non-escape half of the proof holds and only the receiver's
// type stands between the name `split` and a reclaim of boxes `keep` still owns.
const strArrBuiltinTypeGateSrc = strArrBuiltinPrelude + `struct Holder { xs: string[] }
function (h: Holder) split(sep: string): string[] {
    var out: string[] = [];
    out = out.append(h.xs[0]);
    out = out.append(h.xs[1]);
    return out;
}
function round(h: Holder): i32 {
    var parts: string[] = h.split("-");
    return parts.len();
}
function churn(pre: string): i32 { var a: string = w(pre + "1"); var b: string = w(pre + "2"); var c: string = w(pre + "3"); return a.len() + b.len() + c.len(); }
function main(): i32 {
    var keep: Holder = Holder { xs: [w("aaaa"), w("bbbb"), w("cccc")] };
    var i: i32 = 0;
    while (i < 300) {
        if (round(keep) != 2) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!keep.xs[0].starts_with("aaaa-")) { return 97; }
        if (!keep.xs[1].starts_with("bbbb-")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`

// The correctness set: every case must exit 0 on every backend. No heap ceiling —
// the ESCAPE cases here are ones where the credit is correctly withheld, and
// pinning their leak would make the register follow-up fail a gate for fixing it.
var strArrBuiltinFaultCases = []struct {
	name string
	src  string
}{
	{"strarr-builtin-type-gate", strArrBuiltinTypeGateSrc},
	// An element BOUND OUT of the array outlives it, so strarr_unsafe_for
	// withholds the credit and the box must survive the sweep.
	{"strarr-builtin-element-bound-out", strArrBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var parts: string[] = base.split("-");
    var keep: string = parts[3];
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (keep != "payload") { return 0 - 1; }
    return parts.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 18) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// The array itself escapes by return: not reclaimable, and the caller's read
	// of the elements has to find them. The source string is the callee's PARAM,
	// so it outlives the frame — a local source is a different (and on the
	// register backends currently broken) shape, tracked separately.
	{"strarr-builtin-array-returned", strArrBuiltinPrelude + `function parts_of(base: string): string[] {
    var parts: string[] = base.split("-");
    return parts;
}
function round(pre: string): i32 {
    var base: string = w(pre);
    var ps: string[] = parts_of(base);
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (ps[0] != "abcdefgh") { return 0 - 1; }
    if (ps[2] != "wide") { return 0 - 2; }
    return ps.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 18) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// The SOURCE is a .rodata literal, so its element views point outside the
	// arena and the view free's heap-range guard has to decline them; and the
	// second half overwrites one view element with a fresh string, leaving a MIXED
	// array the walk has to reclaim completely and exactly once (str_view_free
	// tail-jumps to str_free for a non-immortal rc).
	{"strarr-builtin-literal-source-and-mixed", strArrBuiltinPrelude + `function litround(): i32 {
    var parts: string[] = "alpha-beta-gamma-delta".split("-");
    var n: i32 = parts.len();
    if (parts[0] != "alpha") { return 0 - 1; }
    if (parts[3] != "delta") { return 0 - 2; }
    return n;
}
function mixround(pre: string): i32 {
    var base: string = w(pre);
    var parts: string[] = base.split("-");
    parts = parts.with(1, w("MMMMMMMM"));
    var n: i32 = parts.len();
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (parts[0] != "abcdefgh") { return 0 - 1; }
    if (!parts[1].starts_with("MMMMMMMM-")) { return 0 - 2; }
    if (parts[2] != "wide") { return 0 - 3; }
    return n;
}
function main(): i32 {
    var pre: string = "abcdefgh";
    var i: i32 = 0;
    while (i < 1500) {
        if (litround() != 4) { return 96; }
        if (mixround(pre) != 18) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`},
	// Separator absent: ONE part covering the whole source. If the runtime handed
	// back the source's own box rather than a fresh view over it, freeing that box
	// would destroy `base` and its scope-exit dec would double-free.
	{"strarr-builtin-whole-source-single-part", strArrBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var parts: string[] = base.split("|");
    var n: i32 = parts.len();
    var ls: string[] = base.lines();
    var m: i32 = ls.len();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (n != 1) { return 0 - 1; }
    if (m != 1) { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    if (base.index_of("XXXX") >= 0) { return 0 - 4; }
    return n;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 1) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// An ELEMENT outliving the array: the credit must be withheld, or the sweep
	// frees the box the caller is holding. The decoys are slices, so they allocate
	// from the same 24-byte class a freed element box lands in.
	{"strarr-builtin-escaping-element", strArrBuiltinPrelude + `function first_of(base: string): string {
    var parts: string[] = base.split("-");
    return parts[0];
}
function churn(src: string): i32 {
    var a: string = slice_unchecked(src, 1, 9);
    var b: string = slice_unchecked(src, 2, 10);
    var c: string = slice_unchecked(src, 3, 11);
    var d: string = slice_unchecked(src, 4, 12);
    return a.len() + b.len() + c.len() + d.len();
}
function round(pre: string): i32 {
    var base: string = w(pre);
    var head: string = first_of(base);
    if (churn(w("QQQQQQQQ")) < 0) { return 0; }
    if (head != "abcdefgh") { return 0 - 1; }
    return head.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 8) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// LIVENESS across both producers at once, with every element read after decoy
	// allocations that would be handed a freed box if the sweep landed early.
	{"strarr-builtin-elements-live", strArrBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var parts: string[] = base.split("-");
    var ls: string[] = base.lines();
    var n: i32 = parts.len();
    var first: i32 = (parts[0][0] as i32);
    var joinlen: i32 = parts[1].len() + parts[n - 1].len();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (parts[0] != "abcdefgh") { return 0 - 1; }
    if (parts[1] != "a") { return 0 - 2; }
    if (!parts[n - 1].starts_with("0123")) { return 0 - 3; }
    if (parts[0].index_of("XXXX") >= 0) { return 0 - 4; }
    if (ls.len() != 1) { return 0 - 5; }
    if (ls[0].len() != base.len()) { return 0 - 6; }
    if (first != 97) { return 0 - 7; }
    if (joinlen != 11) { return 0 - 8; }
    return n;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { if (round(pre) != 18) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
}

const strArrBuiltinExitHint = "98 = the element boxes were stranded; 99 = over-release; 97 = value corrupted; 96/95 = the probe's own guards"

// strArrBuiltinSources pairs each case name with the source for one leg.
func strArrBuiltinSources(wasm bool) []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range strArrBuiltinHeapCases {
		limit := tc.regMax
		if wasm {
			limit = tc.wasmMax
		}
		out = append(out, struct{ name, src string }{tc.name, strArrBuiltinHeap(tc.body, limit)})
	}
	for _, tc := range strArrBuiltinFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

// TestSelfHostStrArrBuiltinReclaimIRX86_64 is the x86-64 leg. The heap ceilings
// here do not move with this change — see the header on why the register half is
// a separate piece of work — so this leg pins the CORRECTNESS set, and above all
// the type gate, which faults here as a value corruption without it.
func TestSelfHostStrArrBuiltinReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrBuiltinSources(false) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strArrBuiltinExitHint)
			}
		})
	}
}

// TestSelfHostStrArrBuiltinReclaimIRArm64 is the arm64 leg, the register path's
// twin and the project's default target.
func TestSelfHostStrArrBuiltinReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrBuiltinSources(false) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strArrBuiltinExitHint)
			}
		})
	}
}

// TestSelfHostStrArrBuiltinReclaimWasmIR is the leg this change is for: every
// heap case fails with 98 on the parent, where the element copies are stranded.
func TestSelfHostStrArrBuiltinReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping builtin string[] reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strArrBuiltinSources(true) {
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
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, strArrBuiltinExitHint)
			}
		})
	}
}
