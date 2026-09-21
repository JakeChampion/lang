package e2eselfhost

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
    var b: Box = Full([0, 0, 0]);
    var i: i32 = 0;
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
            var nil: Node = Nil;
            var child: Node = kids[at];
            var rest: Node[] = kids.with(at, nil);
            child = swap(child, 0, v);
            return Branch(rest.with(at, child));
        },
        Leaf(_) => { return Leaf(v); },
        Nil => { return Nil; }
    }
}
function main(): i32 {
    var leaf: Node = Leaf(0);
    var inner: Node = Branch([leaf]);
    var n: Node = Branch([inner]);
    var i: i32 = 0;
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
    var p: Pair = Both([1, 2], [5, 6]);
    var i: i32 = 0;
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
    var b: Box = Full([1, 2, 3]);
    var c: Box = bump(b, 0);
    match (b) { Full(xs) => { if (xs[0] != 1) { return 1; } }, Empty => { return 2; } }
    match (c) { Full(ys) => { if (ys[0] != 7) { return 3; } }, Empty => { return 4; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 4},
}

// payloadTakeCensus holds a run's leakcheck summary to the case's contract:
// balanced at live 0, and no more allocations than the bound.
func payloadTakeCensus(t *testing.T, name, stderr string, maxAllocs int64) {
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
		t.Errorf("%s: %s — a nulled payload slot must be walked past, not released twice or leaked", name, summary)
	}
	if allocs > maxAllocs {
		t.Errorf("%s: %s — allocs above %d: the payload is being borrowed and copied rather than taken", name, summary, maxAllocs)
	}
}

// payloadTakeRun compiles src for target through the production CLI with the
// leak census on, runs it, and hands back the exit code and the run's stderr.
//
// The CLI rather than `asm_ir_run.fern`, because the take lives in the typed
// semantic pipeline (ssaunits + ssarc) and that driver has no leg through it:
// the same program compiled there keeps the AST lowering's allocation count
// whichever way this change goes.
func payloadTakeRun(t *testing.T, fernBin, stdlibRoot, src, target string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	args := []string{"-target", target, in, stdlibRoot, "-o", out}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", in, stdlibRoot, "-o", out}
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
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
					exit, stderr := payloadTakeRun(t, fernBin, stdlibRoot, tc.src, target)
					if exit != 0 {
						t.Fatalf("exit = %d, want 0 (interp oracle)\n%s", exit, stderr)
					}
					payloadTakeCensus(t, tc.name, stderr, tc.maxAllocs)
				})
			}
		})
	}
}
