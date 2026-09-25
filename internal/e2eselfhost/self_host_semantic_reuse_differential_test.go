package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The typed (semantic) lowering's reuse pairing, gated the way the AST path's
// is: a firing count that must be met, a switch that must take it to zero, and
// the two builds' answers compared against each other.
//
// TestSelfHostReuseDifferentialX86_64 does NOT cover this. Its driver is
// asm_run.fern, which runs lexer -> parser -> asm_ir -> irlower and never
// touches semlower, so neither of its arms reaches ssarc. fern.fern is the
// only driver that routes through the semantic path, which is why this suite
// builds the whole CLI rather than the demo driver.
//
// Each case must produce WHOLE on the typed path. Production is all-or-nothing
// per module, so one refused declaration sends every function to the AST
// lowering and the counts below would then be measuring irlower, not ssarc —
// which is exactly the mistake this check exists to prevent. Every intrinsic
// a case calls must have a semantic contract for the same reason, as
// `__rc_underflow_count` does.
var semanticReuseCases = []struct {
	name string
	src  string
	want int
	// Firings of the reuse call the typed path must emit, and `astPath` what
	// the AST lowering emits for the same source. Where astPath is 0 the
	// pairing is reachable ONLY through ssarc, so the count cannot be
	// satisfied by irlower and the case witnesses this layer alone.
	firings int
	astPath int
	// Allocations the program makes under FERN_LEAKCHECK with the pairing on
	// and off. A FIRING IS A CALL: `__fern_alloc_reuse` hands the donor block
	// back only when its cap matches what the construction asks for, and
	// returns it to its freelist and allocates fresh when it does not. So the
	// count above cannot tell a reuse from a pairing that declines, and the
	// difference between these two can — it is the allocations the reuse
	// actually saved. Nothing in this suite could see that until it was asked
	// (rc-log/2026-09-18-a-firing-is-not-a-reuse.md).
	allocsOn  int64
	allocsOff int64
	// A case whose pairing is admitted statically and declined at RUN time, so
	// it saves nothing by design. Every other case must save, or it is pinning
	// machinery that never fires.
	degrades bool
	// A case where NO pairing may be admitted — a third class the suite could
	// not express, and the one the degenerate self-donor needed. `degrades` is
	// a pairing the runtime turns down; this is a pairing the compiler must
	// never offer, so saving nothing is the correct answer rather than evidence
	// of machinery that never fires.
	noPair bool
}{
	// A record owning a string and an array, rebuilt from its own fields. The
	// donor dies at its last field read and the construction below builds in
	// its box; the `__sem_drop_` helper releases the string and the array
	// first, since the runtime frees a mismatched donor block shallowly.
	{"owns-references", `struct R { tag: string, cells: i32[], n: i32 }
function step(seed: i32): R {
    var a: R = R { tag: "aa", cells: [seed, seed + 1], n: seed };
    var s: i32 = a.n + a.cells[0] + a.cells[1] + a.tag.len();
    return R { tag: "bbb", cells: [s, s + 2], n: s };
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { var r: R = step(i); t = t + r.n + r.cells[0] + r.tag.len(); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 72, 1, 0, 12, 16, false, false},

	// The degrade path: an array holds a second count on the donor, so the
	// pairing is static but `__fern_rc_is_unique` fails and the construction
	// allocates fresh. That fresh box carries no shape word of its own, so a
	// pairing that does not write one corrupts it here and nowhere else.
	{"shared-donor-degrades", `struct R { tag: string, cells: i32[], n: i32 }
function shared(n: i32): i32 {
    var keep: R[] = [];
    var a: R = R { tag: "dd", cells: [n], n: n };
    keep = keep.append(a);
    var s: i32 = a.n + a.cells[0];
    var b: R = R { tag: "cc", cells: [s], n: s };
    return keep[0].n + b.n + b.tag.len();
}
function main(): i32 {
    var v: i32 = shared(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return v;
}`, 17, 1, 0, 6, 6, true, false},

	// The donor and the recipient are different TYPES and the same number of
	// SLOTS, which is the only thing a box has to agree on. Mote and Glyph are
	// members of a struct-union, so the match reads the shape word each
	// construction wrote: a recipient inheriting the donor's takes the wrong
	// arm, which is why the answer separates this from a pairing that merely
	// reuses the storage. cross_wide is the third candidate and it does NOT
	// pair — a three-slot donor against a four-slot construction — so the
	// count of 2 over three sites is what pins that the pairing asks. The AST
	// path pairs none of them.
	{"cross-type", `struct Mote { text: string, k: i32 }
struct Glyph { xs: i32[], k: i32 }
type Sigil = Mote | Glyph;
struct Trio { a: string, b: i32[], c: i32 }
function sigil_code(g: Sigil): i32 {
    match (g) {
        Mote(m) => { return m.k; },
        Glyph(y) => { return y.xs[1] + 100; }
    }
    return 0 - 1;
}
function cross_step(seed: i32): Sigil {
    var a: Mote = Mote { text: "nn", k: seed };
    var s: i32 = a.k + a.text.len();
    return Glyph { xs: [s, s + 1], k: s };
}
function cross_back(seed: i32): Sigil {
    var a: Glyph = Glyph { xs: [seed], k: seed };
    var s: i32 = a.k + a.xs[0];
    return Mote { text: "mm", k: s };
}
function cross_wide(n: i32): i32 {
    var p: Mote = Mote { text: "pp", k: n };
    var s: i32 = p.k + p.text.len();
    var w: Trio = Trio { a: "qq", b: [s], c: s };
    return w.c + w.b[0] + w.a.len();
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { t = t + sigil_code(cross_step(i)); i = i + 1; }
    t = t + sigil_code(cross_back(5)) + cross_wide(3);
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 251;
}`, 189, 2, 0, 13, 18, false, false},

	// A tuple box is one word per element and no shape word, so it is storage
	// a donor of any form can be and storage any form can take: tuple to
	// tuple, a record's box to a tuple, and a tuple's to a record. The two
	// cross-form ones are a two-field record against a three-element tuple,
	// because the pairing matches SLOTS and a record box carries a shape word
	// the tuple's does not. The AST path pairs none of the three.
	{"tuple-form", `struct Parcel { a: string, b: i32 }
function tuple_step(seed: i32): (i32, string) {
    var a: (string, i32[]) = ("aa", [seed, seed + 1]);
    var s: i32 = a.1[0] + a.1[1] + a.0.len();
    return (s, "bb");
}
function tuple_loop(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var p: (i32, string) = tuple_step(i);
        t = t + p.0 + p.1.len();
        i = i + 1;
    }
    return t;
}
function tuple_from_rec(n: i32): i32 {
    var d: Parcel = Parcel { a: "cc", b: n };
    var s: i32 = d.b + d.a.len();
    var q: (i32, string, i32) = (s, "dd", s + 1);
    return q.0 + q.1.len() + q.2;
}
function rec_from_tuple(n: i32): i32 {
    var d: (string, i32, i32) = ("ee", n, n + 1);
    var s: i32 = d.1 + d.2 + d.0.len();
    var q: Parcel = Parcel { a: "ff", b: s };
    return q.b + q.a.len();
}
function main(): i32 {
    var t: i32 = tuple_loop(4) + tuple_from_rec(3) + rec_from_tuple(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 60, 3, 0, 10, 16, false, false},

	// A UNION donor: a declared enum whose variants agree on field count, so
	// its box is a count this frame can state, dying at a call before a record
	// of the same slot count is built. Its box becomes the record's, so the
	// program allocates two fewer blocks than with the pairing off.
	//
	// The live variant's payload is an ARRAY rather than a string, and that is
	// the half of this case that tests the token's child release: the enum's
	// own drop helper has to free it before the box is handed on, since the
	// runtime frees a size-mismatched donor block SHALLOWLY. Written with a
	// string literal instead, the child is a static box whose release is a
	// no-op, and the fixture passes with `drop_children` deleted — measured,
	// which is why it is not written that way.
	{"union-donor", `enum Duo { Two(i32, i32[]), Both(i32[], i32) }
struct Trip { a: string, b: i32 }
function duo_size(d: Duo): i32 {
    match (d) {
        Two(n, xs) => { return n + xs[0]; },
        Both(ys, n2) => { return n2 + ys[0]; }
    }
    return 0 - 1;
}
function union_donor(seed: i32): i32 {
    var u: Duo = Two(seed, [seed, seed + 1]);
    var s: i32 = duo_size(u);
    var r: Trip = Trip { a: "vv", b: s };
    return r.b + r.a.len();
}
function main(): i32 {
    var t: i32 = union_donor(4) + union_donor(9);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 30, 1, 0, 4, 6, false, false},

	// The two rules that decide WHICH dying box a construction builds in,
	// neither of which the cases above can see: every one of them puts the
	// matching construction immediately after the donor.
	//
	// `hold_over` is the hold: a three-slot donor dies at an ordinary
	// instruction, a FOUR-slot construction follows it, and the three-slot one
	// that can take it comes after that. Holding the donor past a construction
	// that cannot serve it is the whole of the pairing there — taken for the
	// next construction alone it fires 0.
	//
	// `chain_up` is the same-instruction pairing: a chain of functional
	// updates of one type, where each superseded box dies AT the construction
	// that supersedes it rather than before it, so a rule that only looks
	// backwards never sees it. 3 firings; 0 without that rule. This is the
	// shape the two native assemblers are made of — `a = X86Asm { ...a, code:
	// … }` — and 795 of the compiler's constructions have it.
	{"pairing-reach", `struct A3 { p: string, q: i32 }
struct B4 { x: string, y: i32, z: i32 }
function hold_over(seed: i32): i32 {
    var a: A3 = A3 { p: "aa", q: seed };
    var s: i32 = a.q + a.p.len();
    var b: B4 = B4 { x: "bb", y: s, z: s + 1 };
    var c: A3 = A3 { p: "cc", q: b.y + b.z };
    return c.q + c.p.len() + b.z;
}
function chain_up(seed: i32): i32 {
    var a: A3 = A3 { p: "a1", q: seed };
    a = A3 { ...a, q: a.q + 1 };
    a = A3 { ...a, q: a.q + 2 };
    a = A3 { ...a, q: a.q + 3 };
    return a.q + a.p.len();
}
function main(): i32 {
    var t: i32 = hold_over(3) + chain_up(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 32, 4, 4, 3, 7, false, false},

	// A record of scalars: pure storage, with no children to release at the
	// token. The AST path pairs this shape too, so the count alone does not
	// separate the layers here — the switch and the answer do.
	{"scalar-fields", `struct P { x: i32, y: i32 }
function bump(seed: i32): i32 {
    var p: P = P { x: seed, y: seed + 1 };
    var s: i32 = p.x + p.y;
    var q: P = P { x: s, y: s + 1 };
    return q.x + q.y;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { t = t + bump(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 21, 1, 1, 3, 6, false, false},
	// The frame has ONE token slot, and this case is the shape that made that
	// matter: a 3-slot donor held for a later 3-slot construction, with a
	// 4-slot self-update between them. The self pairing's token overwrote the
	// held donor's box, so the later construction was handed a box the update
	// had already built into and was returning live. It read as a LEAK
	// (`live_bytes=24` where the same program balances with the pairing off)
	// because the two boxes differ in size class, so `__fern_alloc_reuse`
	// declined and put a live block on its freelist — the same clobber lands
	// as an alias whenever the sizes agree.
	//
	// Nothing in the compiler's own sources has this overlap: the firing count
	// over the whole tree is 1,167 either way, which is why every suite passed
	// while it was wrong. Only a fixture built for it can fail.
	{"token-slot-overlap", `struct A3 { p: string, q: i32 }
struct B4 { x: string, y: i32, z: i32 }
function f(seed: i32): i32 {
    var a: A3 = A3 { p: "aa", q: seed };
    var s: i32 = a.q + a.p.len();
    var b: B4 = B4 { x: "bb", y: seed, z: seed + 1 };
    b = B4 { ...b, y: s };
    var c: A3 = A3 { p: "cc", q: b.y + b.z };
    return c.q + c.p.len() + b.z;
}
function main(): i32 {
    var t: i32 = f(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 21, 1, 2, 3, 4, false, false},
	// A construction whose value is never read has its drop at its own step, so
	// the value dying there IS the construction's result. The first cut of the
	// same-instruction pairing took it as a donor for itself: it read a token
	// slot for a value that does not exist yet, always declined — and it cost
	// the module its last ALLOCATING op, so the heap runtime block stopped
	// being emitted and the link failed on an undefined `__fn___fern_arr_dec`.
	//
	// Found by `TestDifferential_SelfHostSemanticX86_64` at seed 452, reduced to
	// this. The suite could not express it before: the point is not that the
	// pairing declines but that it must never be OFFERED, which is what
	// `noPair` says.
	{"degenerate-self-donor", `struct S0 { f0: i32, f1: i64, f2: boolean }
function main(): i32 {
    var v0: S0 = S0 { f0: 687i32, f1: 942i64, f2: false };
    if (__rc_underflow_count() != 0) { return 99; }
    return 326i32 & 255i32;
}`, 70, 0, 0, 1, 1, false, true},
}

func TestSelfHostSemanticReuseDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// emit compiles src with the given environment and returns the assembly
	// along with the semantic lowering's own report of what it produced.
	emit := func(t *testing.T, proj, src, tag string, extraEnv ...string) (string, string) {
		t.Helper()
		mainPath := filepath.Join(proj, tag+".fern")
		if werr := os.WriteFile(mainPath, []byte(src), 0o644); werr != nil {
			t.Fatalf("write %s: %v", mainPath, werr)
		}
		asmPath := filepath.Join(proj, tag+".s")
		cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath)
		cmd.Env = childEnv(append([]string{"FERN_SEM_IR_REPORT=1"}, extraEnv...)...)
		var report strings.Builder
		cmd.Stderr = &report
		if out, cerr := cmd.Output(); cerr != nil {
			t.Fatalf("compile %s (env %v): %v\n%s\n%s", tag, extraEnv, cerr, out, report.String())
		}
		asm, rerr := os.ReadFile(asmPath)
		if rerr != nil {
			t.Fatalf("read %s: %v", asmPath, rerr)
		}
		return string(asm), report.String()
	}
	link := func(t *testing.T, proj, tag, asm string) int {
		t.Helper()
		asmPath := filepath.Join(proj, tag+".link.s")
		if werr := os.WriteFile(asmPath, []byte(asm), 0o644); werr != nil {
			t.Fatalf("write %s: %v", asmPath, werr)
		}
		binPath := filepath.Join(proj, tag+".bin")
		if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
			t.Fatalf("link %s: %v (%s)", tag, lerr, out)
		}
		rcmd := runX86_64Bin(runner, binPath)
		_ = rcmd.Run()
		return rcmd.ProcessState.ExitCode()
	}
	// allocs builds the case under FERN_LEAKCHECK and runs it, answering what
	// the program allocated. Balance is checked here too: a pairing that hands
	// a box to a construction not entitled to it shows as a free the program
	// cannot account for.
	allocs := func(t *testing.T, proj, src, tag string, extraEnv ...string) int64 {
		t.Helper()
		asm, _ := emit(t, proj, src, tag, append([]string{"FERN_LEAKCHECK=1"}, extraEnv...)...)
		asmPath := filepath.Join(proj, tag+".leak.s")
		if werr := os.WriteFile(asmPath, []byte(asm), 0o644); werr != nil {
			t.Fatalf("write %s: %v", asmPath, werr)
		}
		binPath := filepath.Join(proj, tag+".leak.bin")
		if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
			t.Fatalf("link %s: %v (%s)", tag, lerr, out)
		}
		rcmd := runX86_64Bin(runner, binPath)
		out, _ := rcmd.CombinedOutput()
		summary := leakSummaryLine(string(out))
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary in:\n%s", tag, out)
		}
		var a, f, live int64
		if _, err := fmtSscan(summary, &a, &f, &live); err != nil {
			t.Fatalf("%s: %v (%q)", tag, err, summary)
		}
		if a != f || live != 0 {
			t.Fatalf("%s: unbalanced: %s", tag, summary)
		}
		return a
	}

	for _, tc := range semanticReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			asmOn, report := emit(t, proj, tc.src, "on")
			// Without this the counts below can be irlower's: a module that
			// refuses anywhere is lowered whole by the AST path.
			if !strings.Contains(report, "declarations") || strings.Contains(report, "the AST lowering stands") {
				t.Fatalf("%s: module did not produce whole on the typed path, so the counts below would measure the AST lowering:\n%s", tc.name, report)
			}
			asmOff, _ := emit(t, proj, tc.src, "off", "FERN_SELFHOST_NO_REUSE=1")
			asmAST, _ := emit(t, proj, tc.src, "ast", "FERN_SEM_IR=")

			// The runtime helper's own body is emitted either way, so the CALL
			// is the firing and its label is not.
			const witness = "call __fn___fern_alloc_reuse"
			if got := strings.Count(asmOn, witness); got != tc.firings {
				t.Errorf("%s: typed path emitted %d reuse calls, want %d", tc.name, got, tc.firings)
			}
			if got := strings.Count(asmOff, witness); got != 0 {
				t.Errorf("%s: FERN_SELFHOST_NO_REUSE=1 still emitted %d reuse calls — the switch does not reach the typed pairing", tc.name, got)
			}
			if got := strings.Count(asmAST, witness); got != tc.astPath {
				t.Errorf("%s: AST lowering emitted %d reuse calls, want %d — the split between the layers has moved", tc.name, got, tc.astPath)
			}

			// The allocations the reuse SAVED. A firing that declines saves
			// none, so this is what separates a pairing the runtime can
			// honour from one that only emits its machinery.
			onAllocs := allocs(t, proj, tc.src, "leakon")
			offAllocs := allocs(t, proj, tc.src, "leakoff", "FERN_SELFHOST_NO_REUSE=1")
			if onAllocs != tc.allocsOn || offAllocs != tc.allocsOff {
				t.Errorf("%s: allocations on/off were %d/%d, want %d/%d — the reuse saved %d where %d was measured",
					tc.name, onAllocs, offAllocs, tc.allocsOn, tc.allocsOff, offAllocs-onAllocs, tc.allocsOff-tc.allocsOn)
			}
			if !tc.degrades && !tc.noPair && tc.allocsOff <= tc.allocsOn {
				t.Errorf("%s: the case is pinned to save no allocation, so it witnesses a pairing that declines", tc.name)
			}
			if tc.degrades && tc.allocsOff != tc.allocsOn {
				t.Errorf("%s: marked as a runtime decline, but it saves %d allocations", tc.name, tc.allocsOff-tc.allocsOn)
			}
			if tc.noPair && (tc.firings != 0 || tc.allocsOff != tc.allocsOn) {
				t.Errorf("%s: marked as admitting no pairing, but it is pinned to %d firings and %d saved allocations",
					tc.name, tc.firings, tc.allocsOff-tc.allocsOn)
			}

			gotOn, gotOff := link(t, proj, "on", asmOn), link(t, proj, "off", asmOff)
			if gotOn != gotOff {
				t.Errorf("%s: OBSERVATIONAL DIVERGENCE — reuse-on exited %d, reuse-off %d", tc.name, gotOn, gotOff)
			}
			if gotOn != tc.want {
				t.Errorf("%s: reuse-on exited %d, want %d", tc.name, gotOn, tc.want)
			}
			if gotOff != tc.want {
				t.Errorf("%s: reuse-off exited %d, want %d", tc.name, gotOff, tc.want)
			}
		})
	}
}
