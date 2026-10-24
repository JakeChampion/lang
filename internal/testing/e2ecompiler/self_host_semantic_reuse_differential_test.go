package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var semanticReuseCases = []struct {
	name      string
	src       string
	want      int
	firings   int
	allocsOn  int64
	allocsOff int64
	degrades  bool
	noPair    bool
}{
	{"owns-references", `struct R { tag: string, cells: i32[], n: i32 }
function step(seed: i32): R {
    let a: R = R { tag: "aa", cells: [seed, seed + 1], n: seed };
    let s: i32 = a.n + a.cells[0] + a.cells[1] + a.tag.len();
    return R { tag: "bbb", cells: [s, s + 2], n: s };
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { let r: R = step(i); t = t + r.n + r.cells[0] + r.tag.len(); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 72, 1, 12, 16, false, false},

	{"shared-donor-degrades", `struct R { tag: string, cells: i32[], n: i32 }
function shared(n: i32): i32 {
    let keep: R[] = [];
    let a: R = R { tag: "dd", cells: [n], n: n };
    keep = keep.append(a);
    let s: i32 = a.n + a.cells[0];
    let b: R = R { tag: "cc", cells: [s], n: s };
    return keep[0].n + b.n + b.tag.len();
}
function main(): i32 {
    let v: i32 = shared(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return v;
}`, 17, 1, 5, 5, true, false},

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
    let a: Mote = Mote { text: "nn", k: seed };
    let s: i32 = a.k + a.text.len();
    return Glyph { xs: [s, s + 1], k: s };
}
function cross_back(seed: i32): Sigil {
    let a: Glyph = Glyph { xs: [seed], k: seed };
    let s: i32 = a.k + a.xs[0];
    return Mote { text: "mm", k: s };
}
function cross_wide(n: i32): i32 {
    let p: Mote = Mote { text: "pp", k: n };
    let s: i32 = p.k + p.text.len();
    let w: Trio = Trio { a: "qq", b: [s], c: s };
    return w.c + w.b[0] + w.a.len();
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { t = t + sigil_code(cross_step(i)); i = i + 1; }
    t = t + sigil_code(cross_back(5)) + cross_wide(3);
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 251;
}`, 189, 2, 13, 18, false, false},

	{"tuple-form", `struct Parcel { a: string, b: i32 }
function tuple_step(seed: i32): (i32, string) {
    let a: (string, i32[]) = ("aa", [seed, seed + 1]);
    let s: i32 = a.1[0] + a.1[1] + a.0.len();
    return (s, "bb");
}
function tuple_loop(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let p: (i32, string) = tuple_step(i);
        t = t + p.0 + p.1.len();
        i = i + 1;
    }
    return t;
}
function tuple_from_rec(n: i32): i32 {
    let d: Parcel = Parcel { a: "cc", b: n };
    let s: i32 = d.b + d.a.len();
    let q: (i32, string, i32) = (s, "dd", s + 1);
    return q.0 + q.1.len() + q.2;
}
function rec_from_tuple(n: i32): i32 {
    let d: (string, i32, i32) = ("ee", n, n + 1);
    let s: i32 = d.1 + d.2 + d.0.len();
    let q: Parcel = Parcel { a: "ff", b: s };
    return q.b + q.a.len();
}
function main(): i32 {
    let t: i32 = tuple_loop(4) + tuple_from_rec(3) + rec_from_tuple(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 60, 3, 10, 16, false, false},

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
    let u: Duo = Two(seed, [seed, seed + 1]);
    let s: i32 = duo_size(u);
    let r: Trip = Trip { a: "vv", b: s };
    return r.b + r.a.len();
}
function main(): i32 {
    let t: i32 = union_donor(4) + union_donor(9);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 30, 1, 4, 6, false, false},

	{"pairing-reach", `struct A3 { p: string, q: i32 }
struct B4 { x: string, y: i32, z: i32 }
function hold_over(seed: i32): i32 {
    let a: A3 = A3 { p: "aa", q: seed };
    let s: i32 = a.q + a.p.len();
    let b: B4 = B4 { x: "bb", y: s, z: s + 1 };
    let c: A3 = A3 { p: "cc", q: b.y + b.z };
    return c.q + c.p.len() + b.z;
}
function chain_up(seed: i32): i32 {
    let a: A3 = A3 { p: "a1", q: seed };
    a = A3 { ...a, q: a.q + 1 };
    a = A3 { ...a, q: a.q + 2 };
    a = A3 { ...a, q: a.q + 3 };
    return a.q + a.p.len();
}
function main(): i32 {
    let t: i32 = hold_over(3) + chain_up(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 32, 4, 3, 7, false, false},

	{"scalar-fields", `struct P { x: i32, y: i32 }
function bump(seed: i32): i32 {
    let p: P = P { x: seed, y: seed + 1 };
    let s: i32 = p.x + p.y;
    let q: P = P { x: s, y: s + 1 };
    return q.x + q.y;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) { t = t + bump(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 21, 1, 3, 6, false, false},
	{"token-slot-overlap", `struct A3 { p: string, q: i32 }
struct B4 { x: string, y: i32, z: i32 }
function f(seed: i32): i32 {
    let a: A3 = A3 { p: "aa", q: seed };
    let s: i32 = a.q + a.p.len();
    let b: B4 = B4 { x: "bb", y: seed, z: seed + 1 };
    b = B4 { ...b, y: s };
    let c: A3 = A3 { p: "cc", q: b.y + b.z };
    return c.q + c.p.len() + b.z;
}
function main(): i32 {
    let t: i32 = f(5);
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}`, 21, 2, 2, 4, false, false},
	{"update-keeps-fields", `struct Acc { names: string[], tag: string, n: i32 }
@noinline function ids(s: string): string { return s; }
function add(own a: Acc, s: string): Acc {
    a = Acc { ...a, names: a.names.append(s), n: a.n + 1 };
    return a;
}
function main(): i32 {
    let a: Acc = Acc { names: [], tag: ids("t"), n: 0 };
    let i: i32 = 0;
    while (i < 6) { a = add(a, "x"); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.n * 10 + a.names.len() + a.tag.len();
}`, 67, 1, 3, 9, false, false},

	{"update-keeps-shared-base", `struct Acc { names: string[], tag: string, n: i32 }
@noinline function ids(s: string): string { return s; }
function add(own a: Acc, s: string): Acc {
    a = Acc { ...a, names: a.names.append(s), n: a.n + 1 };
    return a;
}
function main(): i32 {
    let a: Acc = Acc { names: [ids("p")], tag: "t", n: 0 };
    let keep: Acc = a;
    a = add(a, "x");
    a = add(a, "y");
    if (__rc_underflow_count() != 0) { return 99; }
    return a.names.len() * 10 + keep.names.len() + keep.tag.len() + a.tag.len() + keep.n;
}`, 33, 1, 5, 6, false, false},

	{"update-keeps-field-read-later", `struct Acc { names: string[], tag: string, n: i32 }
@noinline function ids(s: string): string { return s; }
function add(own a: Acc, s: string): Acc {
    let t: string = a.tag;
    a = Acc { names: a.names.append(s), tag: t, n: a.n + t.len() };
    return a;
}
function main(): i32 {
    let a: Acc = Acc { names: [], tag: ids("tag"), n: 0 };
    let i: i32 = 0;
    while (i < 6) { a = add(a, "x"); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.n * 10 + a.names.len() + a.tag.len();
}`, 189, 1, 3, 9, false, false},

	{"update-duplicate-operand", `struct P { a: i32[], b: i32[], n: i32 }
function dup(own p: P): P { p = P { ...p, b: p.a, n: p.n + 1 }; return p; }
function run(k: i32): i32 {
    let p: P = P { a: [k, k + 1], b: [k + 2], n: 0 };
    let i: i32 = 0;
    while (i < 4) { p = dup(p); i = i + 1; }
    return p.n * 10 + p.a.len() + p.b.len();
}
function main(): i32 {
    let r: i32 = run(3);
    if (__rc_underflow_count() != 0) { return 99; }
    return r;
}`, 44, 1, 3, 7, false, false},

	{"update-local-in-two-slots", `struct P { a: i32[], b: i32[], n: i32 }
function twice(own p: P): P {
    let x: i32[] = p.a;
    p = P { ...p, a: x, b: x, n: p.n + 1 };
    return p;
}
function run(k: i32): i32 {
    let p: P = P { a: [k, k + 1], b: [k + 2], n: 0 };
    let i: i32 = 0;
    while (i < 4) { p = twice(p); i = i + 1; }
    return p.n * 10 + p.a.len() + p.b.len();
}
function main(): i32 {
    let r: i32 = run(3);
    if (__rc_underflow_count() != 0) { return 99; }
    return r;
}`, 44, 1, 3, 7, false, false},

	{"update-keeps-no-field-held-twice", `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
@noinline function hold(p: P): i32 { return 0; }
function grab(i: i32): string[] {
    let q: P = P { f: mkv(), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    if (hold(p) != 0) { return mkv(); }
    return tt;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { let xs: string[] = grab(i); let y: string[] = mkv(); t = t + xs[0].len() + y[1].len(); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 101;
}`, 46, 1, 28, 32, false, false},

	{"update-keeps-scalar-fields", `struct Acc { names: string[], n: i32, on: boolean, big: i64, w: f64 }
@noinline function ids(s: string): string { return s; }
function add(own a: Acc, s: string): Acc {
    a = Acc { ...a, names: a.names.append(s), n: a.n + 1 };
    return a;
}
function main(): i32 {
    let a: Acc = Acc { names: [ids("p")], n: 0, on: true, big: 5000000000i64, w: 2.5 };
    let i: i32 = 0;
    while (i < 6) { a = add(a, "x"); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    let r: i32 = a.n * 10 + a.names.len();
    if (a.on) { r = r + 1; }
    if (a.big == 5000000000i64) { r = r + 2; }
    if (a.w == 2.5) { r = r + 4; }
    return r;
}`, 74, 1, 5, 11, false, false},

	{"update-keeps-scalars-shared-base", `struct Acc { names: string[], n: i32, on: boolean, big: i64, w: f64 }
@noinline function ids(s: string): string { return s; }
function add(own a: Acc, s: string): Acc {
    a = Acc { ...a, names: a.names.append(s), n: a.n + 1 };
    return a;
}
function main(): i32 {
    let a: Acc = Acc { names: [ids("p")], n: 0, on: true, big: 5000000000i64, w: 2.5 };
    let keep: Acc = a;
    a = add(a, "x");
    a = add(a, "y");
    if (__rc_underflow_count() != 0) { return 99; }
    let r: i32 = a.n * 10 + a.names.len() + keep.n + keep.names.len() * 100;
    if (a.on && keep.on) { r = r + 1; }
    if (a.big == keep.big) { r = r + 2; }
    if (a.w == keep.w) { r = r + 4; }
    return r;
}`, 130, 1, 5, 6, false, false},

	{"update-keeps-scalar-read-later", `struct Acc { names: string[], n: i32, big: i64 }
@noinline function ids(s: string): string { return s; }
function step(own a: Acc, s: string): Acc {
    let big: i64 = a.big;
    a = Acc { ...a, names: a.names.append(s), n: a.n + (big % 7i64) as i32 };
    return a;
}
function main(): i32 {
    let a: Acc = Acc { names: [ids("p")], n: 0, big: 12i64 };
    let i: i32 = 0;
    while (i < 4) { a = step(a, "x"); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.n * 10 + a.names.len() + a.big as i32;
}`, 217, 1, 5, 9, false, false},

	{"update-keeps-scalar-only-record", `struct P3 { x: i32, y: i32, z: i64 }
@noinline function mk(v: i32): P3 { return P3 { x: v, y: v + 1, z: 7i64 }; }
@noinline function bump(own p: P3): P3 { return P3 { ...p, x: p.x + 1 }; }
function main(): i32 {
    let p: P3 = mk(1);
    let i: i32 = 0;
    while (i < 5) { p = bump(p); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return p.x * 10 + p.y + p.z as i32;
}`, 69, 1, 1, 6, false, false},

	{"degenerate-self-donor", `struct S0 { f0: i32, f1: i64, f2: boolean }
@noinline function hold(v: S0): i32 { return 0; }
@noinline function seed(): i32 { return 687i32; }
function main(): i32 {
    let v0: S0 = S0 { f0: seed(), f1: 942i64, f2: false };
    if (__rc_underflow_count() != 0) { return 99; }
    return (326i32 & 255i32) + hold(v0);
}`, 70, 0, 1, 1, false, true},
}

func TestSelfHostSemanticReuseDifferentialX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	emit := func(t *testing.T, proj, src, tag string, extraEnv ...string) (string, string) {
		t.Helper()
		mainPath := filepath.Join(proj, tag+".fern")
		if werr := os.WriteFile(mainPath, []byte(src), 0o644); werr != nil {
			t.Fatalf("write %s: %v", mainPath, werr)
		}
		asmPath := filepath.Join(proj, tag+".s")
		cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot)
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

	keptDropCases := map[string]bool{"update-keeps-fields": true, "update-keeps-field-read-later": true}

	for _, tc := range semanticReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			asmOn, report := emit(t, proj, tc.src, "on")
			if !strings.Contains(report, "declarations") {
				t.Fatalf("%s: module did not produce whole on the typed path:\n%s", tc.name, report)
			}
			if keptDropCases[tc.name] {
				inside, outside := splitFunction(asmOn, "__fn___sem_release_Acc:\n")
				if got := strings.Count(outside, "call __fn___sem_drop_Acc"); got != 0 {
					t.Errorf("%s: %d calls to the children-drop helper outside its release helper, want 0 — the update's reuse released the fields it keeps", tc.name, got)
				}
				if got := strings.Count(inside, "call __fn___sem_drop_Acc"); got != 2 {
					t.Errorf("%s: the release helper calls the children-drop helper %d times, want 2", tc.name, got)
				}
			}
			asmOff, _ := emit(t, proj, tc.src, "off", "FERN_SELFHOST_NO_REUSE=1")

			const witness = "call __fn___fern_alloc_reuse"
			if got := strings.Count(asmOn, witness); got != tc.firings {
				t.Errorf("%s: typed path emitted %d reuse calls, want %d", tc.name, got, tc.firings)
			}
			if got := strings.Count(asmOff, witness); got != 0 {
				t.Errorf("%s: FERN_SELFHOST_NO_REUSE=1 still emitted %d reuse calls — the switch does not reach the typed pairing", tc.name, got)
			}

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

func splitFunction(asm, label string) (inside, outside string) {
	at := strings.Index(asm, label)
	if at < 0 {
		return "", asm
	}
	end := strings.Index(asm[at:], ".cfi_endproc")
	if end < 0 {
		return asm[at:], asm[:at]
	}
	return asm[at : at+end], asm[:at] + asm[at+end:]
}
