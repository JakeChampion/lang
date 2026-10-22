package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// payloadTakeIRCases pin the enum payload TAKE: a variant read whose box this
// frame owns and reads no further moves the payload out of the box rather than
// borrowing it, so the consuming use downstream updates in place.
//
// Before, every payload read was a borrow, and a borrow owes a retain to
// whatever consumes it. The retain landed BEFORE the uniqueness test the
// update makes, so the test read a count of two on a box nobody else held and
// answered "shared" every time: `Full(xs.with(i, x))` cloned the whole buffer
// on every call, for the lifetime of the program. `std/pvec`'s write path made
// 476,160 of those copies over one bench run against native's none, which is
// the whole of its 3.69x (#9891).
//
// The take is native's shape (`emitOwnedConsumingArmDrop`): the box is asked
// `__fern_rc_is_unique` at the read, the payload slot is nulled when the answer
// is yes and the payload retained when it is no, and the box's own drop — which
// runs in the same step, since the read is its last use — walks past the null
// and releases the box. A shared box therefore still behaves exactly as the
// borrow did.
//
// The census is what these cases are for: a loop of N updates that allocated N
// buffers is the volume this removes. Each case is oracle-checked against the
// interpreter under FERN_STRICT_IR, and a wrong uniqueness answer shows up as a
// wrong exit code rather than a census reading. `maxAllocs` is the count AFTER
// the change; every case exceeds it before.
var payloadTakeIRCases = []struct {
	name      string
	src       string
	maxAllocs int64
}{
	// A buffer in a record held in a list, read through `list[i]`, joined onto,
	// and put back with `.with(i, ...)` after an update of the holder that
	// keeps the list — `std/quic/streams.write`'s shape. The element's take
	// survives the update, so the three rebuilds and the join run in place:
	// 200 updates cost a handful of boxes where they cost three each.
	{"element-take-survives-a-keeping-update", `import "std/array";
struct Send { buf: u8[], fin: boolean }
struct St { id: i64, send: Send }
struct Streams { list: St[], n: i32, refused: string }
function refuse(own s: Streams, why: string): Streams {
    return Streams { ...s, refused: why };
}
function put(own s: Streams, i: i32, st: St): Streams {
    return Streams { ...s, list: s.list.with(i, st) };
}
@noinline
function write_at(own s: Streams, i: i32, data: u8[]): Streams {
    let st: St = s.list[i];
    let buf: u8[] = st.send.buf.concat(data);
    return put(refuse(s, ""), i, St { ...st, send: Send { ...st.send, buf: buf } });
}
function main(): i32 {
    let chunk: u8[] = [1 as u8, 2 as u8, 3 as u8, 4 as u8];
    let s: Streams = Streams { list: [St { id: 0, send: Send { buf: [], fin: false } }, St { id: 4, send: Send { buf: [], fin: false } }], n: 2, refused: "" };
    
    let i: i32 = 0;
    while (i < 200) { s = write_at(s, 1, chunk); i = i + 1; }
    if (s.list[1].send.buf.len() != 800 || s.list[0].send.buf.len() != 0 || s.list[1].id != 4) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 20},
	// The same with borrowed receivers, which the leaf inliner splices and
	// takes the intermediate record apart: the copies it leaves unread are
	// dropped, so the store-back is on the array the element was read out of.
	// `keep` shares the first holder, which the take leaves as it was.
	{"element-take-through-spliced-update", `import "std/array";
struct Send { buf: u8[], fin: boolean }
struct St { id: i64, send: Send }
struct Streams { list: St[], n: i32, refused: string }
function refuse(s: Streams, why: string): Streams {
    return Streams { ...s, refused: why };
}
function put(s: Streams, i: i32, st: St): Streams {
    return Streams { ...s, list: s.list.with(i, st) };
}
@noinline
function write_at(s: Streams, i: i32, data: u8[]): Streams {
    let st: St = s.list[i];
    let buf: u8[] = st.send.buf.concat(data);
    return put(refuse(s, ""), i, St { ...st, send: Send { ...st.send, buf: buf } });
}
function main(): i32 {
    let chunk: u8[] = [1 as u8, 2 as u8, 3 as u8, 4 as u8];
    let s: Streams = Streams { list: [St { id: 0, send: Send { buf: [], fin: false } }, St { id: 4, send: Send { buf: [], fin: false } }], n: 2, refused: "" };
    let keep: Streams = s;
    let i: i32 = 0;
    while (i < 200) { s = write_at(s, 1, chunk); i = i + 1; }
    if (s.list[1].send.buf.len() != 800 || s.list[0].send.buf.len() != 0 || s.list[1].id != 4 || keep.list[1].send.buf.len() != 0) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 20},
	// `std/http/h3.__on_request`'s shape: the element read, a branch between
	// the read and the store-back (`r.fin || fin`), and the store-back made by
	// an updater. The updater is spliced (seminline.updater), its read of the
	// list is the first read (semsink.reuse), and the join block does not read
	// the element again, since the join under it updates a buffer of it in
	// place (child_used_above).
	{"element-take-across-a-branch", `import "std/array";
struct Frames { buf: u8[], left: i64 }
struct Req { id: i64, frames: Frames, fin: boolean }
struct Sess { reqs: Req[], n: i32, tag: string }
function put(s: Sess, i: i32, r: Req): Sess {
    return Sess { ...s, reqs: s.reqs.with(i, r) };
}
@noinline
function on_bytes(s: Sess, i: i32, bytes: u8[], fin: boolean): Sess {
    let r: Req = s.reqs[i];
    r = Req { ...r, frames: Frames { ...r.frames, buf: r.frames.buf.concat(bytes) }, fin: r.fin || fin };
    return put(s, i, r);
}
function main(): i32 {
    let chunk: u8[] = [1 as u8, 2 as u8, 3 as u8, 4 as u8];
    let s: Sess = Sess { reqs: [Req { id: 0, frames: Frames { buf: [], left: 0 }, fin: false }, Req { id: 4, frames: Frames { buf: [], left: 0 }, fin: false }], n: 2, tag: "" };
    let keep: Sess = s;
    let i: i32 = 0;
    while (i < 200) { s = on_bytes(s, 1, chunk, i == 199); i = i + 1; }
    if (s.reqs[1].frames.buf.len() != 800 || !s.reqs[1].fin || s.reqs[0].fin || s.reqs[1].id != 4 || keep.reqs[1].frames.buf.len() != 0 || keep.reqs[1].fin) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 20},
	// The same across a branch with the list one record deeper: the join
	// block reads `h.st` again as the donor of the update that stores the
	// element back. That read is the first read (semsink.reuse), the donor
	// named through it, so the buffer joins in place.
	{"element-take-through-a-reused-donor", `import "std/array";
struct Req { buf: u8[], fin: boolean }
struct St { reqs: Req[], n: i32 }
struct H { st: St, tag: i32 }
@noinline
function feed(h: H, i: i32, data: u8[], fin: boolean): H {
    let r: Req = h.st.reqs[i];
    let buf: u8[] = r.buf.concat(data);
    let done: boolean = r.fin || fin;
    let st: St = St { ...h.st, reqs: h.st.reqs.with(i, Req { ...r, buf: buf, fin: done }) };
    return H { ...h, st: st };
}
function main(): i32 {
    let chunk: u8[] = [1 as u8, 2 as u8, 3 as u8];
    let h: H = H { st: St { reqs: [Req { buf: [], fin: false }, Req { buf: [], fin: false }], n: 2 }, tag: 7 };
    let keep: H = h;
    let i: i32 = 0;
    while (i < 100) { h = feed(h, 1, chunk, i == 99); i = i + 1; }
    if (h.st.reqs[1].buf.len() != 300 || !h.st.reqs[1].fin || h.st.reqs[0].fin || h.st.n != 2 || h.tag != 7 || keep.st.reqs[1].buf.len() != 0) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 20},
	// The headline: an owned box destructured and rebuilt every round. Thirty
	// updates used to clone thirty buffers.
	{"owned-box-updates-in-place", `enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) {
        Full(xs) => { return Full(xs.with(i, x)); },
        Empty => { return Empty; }
    }
}
function main(): i32 {
    let b: Box = Full([0, 0, 0]);
    let i: i32 = 0;
    while (i < 30) { b = put(b, i % 3, i); i = i + 1; }
    match (b) {
        Full(xs) => { if (xs[0] + xs[1] + xs[2] != 27 + 28 + 29) { return 1; } },
        Empty => { return 2; }
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 32},
	// `std/pvec.__pv_with_in`'s shape: an element read out of the payload that
	// outlives the update of the payload, over two levels of trie. This is the
	// case the ordering carries — the element takes a unit of its own only if
	// the array it came out of is already known to be owned, which is true only
	// once the payload take is folded in first.
	{"element-read-outlives-the-update", `enum Node { Branch(Node[]), Leaf(i32), Nil }
@noinline
function swap(own n: Node, at: i32, v: i32): Node {
    match (n) {
        Branch(kids) => {
            let nil: Node = Nil;
            let child: Node = kids[at];
            let rest: Node[] = kids.with(at, nil);
            child = swap(child, 0, v);
            return Branch(rest.with(at, child));
        },
        Leaf(_) => { return Leaf(v); },
        Nil => { return Nil; }
    }
}
function main(): i32 {
    let leaf: Node = Leaf(0);
    let inner: Node = Branch([leaf]);
    let n: Node = Branch([inner]);
    let i: i32 = 0;
    while (i < 20) { n = swap(n, 0, i); i = i + 1; }
    match (n) {
        Branch(a) => {
            match (a[0]) {
                Branch(b) => { match (b[0]) { Leaf(v) => { if (v != 19) { return 1; } }, Branch(_) => { return 2; }, Nil => { return 3; } } },
                Leaf(_) => { return 4; }, Nil => { return 5; }
            }
        },
        Leaf(_) => { return 6; }, Nil => { return 7; }
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 105},
	// Two payload slots, one taken and the other read after it: the take must
	// null only the slot it read. `b` coming back wrong, or at all, is the
	// assertion.
	{"sibling-slot-survives-the-take", `enum Pair { Both(i32[], i32[]), Neither }
@noinline
function left(own p: Pair, x: i32): Pair {
    match (p) {
        Both(a, b) => { return Both(a.with(0, x), b); },
        Neither => { return Neither; }
    }
}
function main(): i32 {
    let p: Pair = Both([1, 2], [5, 6]);
    let i: i32 = 0;
    while (i < 20) { p = left(p, i); i = i + 1; }
    match (p) {
        Both(a, b) => { if (a[0] != 19 || a[1] != 2 || b[0] != 5 || b[1] != 6) { return 1; } },
        Neither => { return 2; }
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 23},
	// A BORROWED box must not be taken from: its caller still names it and
	// reads it back. Nulling the slot here would hand the caller an empty
	// payload, so this case's census is deliberately unchanged by the take —
	// `bump` copies, as a borrow always did.
	{"borrowed-box-is-not-taken", `enum Box { Full(i32[]), Empty }
@noinline
function bump(b: Box, i: i32): Box {
    match (b) {
        Full(xs) => { return Full(xs.with(i, 7)); },
        Empty => { return Empty; }
    }
}
function main(): i32 {
    let b: Box = Full([1, 2, 3]);
    let c: Box = bump(b, 0);
    match (b) { Full(xs) => { if (xs[0] != 1) { return 1; } }, Empty => { return 2; } }
    match (c) { Full(ys) => { if (ys[0] != 7) { return 3; } }, Empty => { return 4; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 4},
	// A map field of an owned record, rebuilt through a spread (#11119): the
	// field read takes the map, so the insert finds it sole-held and updates
	// in place. A borrowed read held it at two counts and copied every entry
	// on every update.
	{"map-field-of-owned-record-updates-in-place", `import "core/map";
struct Db { entries: Map[i64, i64], n: i32 }
@noinline
function step(own db: Db, k: i64, v: i64): Db {
    return Db { ...db, entries: db.entries.insert(k, v), n: db.n + 1 };
}
function main(): i32 {
    let db: Db = Db { entries: Map {}, n: 0 };
    let i: i32 = 0;
    while (i < 200) { db = step(db, (i % 50) as i64, i as i64); i = i + 1; }
    if (db.entries.len() != 50 || db.n != 200 || db.entries.get_or(7i64, 0i64) != 157i64) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 450},
	// A BORROWED record keeps its map: the insert copies, and the caller reads
	// its own map back unchanged.
	{"map-field-of-borrowed-record-is-not-taken", `import "core/map";
struct Db { entries: Map[i64, i64], n: i32 }
@noinline
function with_key(db: Db, k: i64): Db {
    return Db { ...db, entries: db.entries.insert(k, 9i64), n: db.n + 1 };
}
function main(): i32 {
    let db: Db = Db { entries: Map {}, n: 0 };
    db = Db { ...db, entries: db.entries.insert(1i64, 1i64) };
    let other: Db = with_key(db, 2i64);
    if (db.entries.len() != 1 || db.entries.get_or(2i64, 0i64) != 0i64) { return 1; }
    if (other.entries.len() != 2 || other.entries.get_or(2i64, 0i64) != 9i64) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 16},
}

// censusWithin holds a run's leakcheck summary to a case's contract: balanced
// at live 0, and no more allocations than the bound.
func censusWithin(t *testing.T, name, stderr string, maxAllocs int64) {
	t.Helper()
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary on stderr: %q", name, stderr)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", name, summary, err)
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s: %s — every block the run takes must come back, and none twice", name, summary)
	}
	if allocs > maxAllocs {
		t.Errorf("%s: %s — allocs above %d: a buffer is being copied where it should be updated in place", name, summary, maxAllocs)
	}
}

// selfHostCLIRun compiles src for target through the production CLI with the
// leak census on, runs it, and hands back the exit code and the run's stderr.
func selfHostCLIRun(t *testing.T, fernBin, stdlibRoot, src, target string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	args := []string{"-target", target, "-o", out, in, stdlibRoot}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", "-o", out, in, stdlibRoot}
	}
	cmd := exec.Command(fernBin, args...)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
	var buildErr strings.Builder
	cmd.Stderr = &buildErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile (%s): %v\n%s", target, err, buildErr.String())
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatal(err)
		}
		run = exec.Command(out)
	case "arm64-linux":
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatal(err)
		}
		_, qemu := arm64Tooling(t)
		run = runArm64Bin(qemu, out)
	default:
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		run = exec.Command("wasmtime", "run", out)
	}
	var runErr strings.Builder
	run.Stderr = &runErr
	_ = run.Run()
	return run.ProcessState.ExitCode(), runErr.String()
}

func TestSelfHostPayloadTakeIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, tc := range payloadTakeIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if want := interpExit(t, interpBin, tc.src); want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					exit, stderr := selfHostCLIRun(t, fernBin, stdlibRoot, tc.src, target)
					if exit != 0 {
						t.Fatalf("exit = %d, want 0 (interp oracle)\n%s", exit, stderr)
					}
					censusWithin(t, tc.name, stderr, tc.maxAllocs)
				})
			}
		})
	}
}
