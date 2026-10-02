package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ownershipCases pin the borrow inference: which reference parameters
// `semsource` hands to the callee as COUNTED, and which stay borrowed.
//
// The rule is `ownership.escaping` — a parameter is counted when it is
// returned, or when it or anything anchored to it is built into a container or
// box, stored into a cell or a map, or passed to a slot its callee counts.
// Returning a projection of it does not count it: the callee retains the
// projection instead. Everything else borrows, because the caller's retain and
// the callee's release would cancel.
//
// What each CONSUMING case measures is the allocation census, because that is
// what the two conventions differ on there. A borrowed parameter cannot be
// reclaimed by the frame that reads it, so a rebuild through one copies its
// buffer on every update; a counted one arrives uniquely held and the update
// lands in place. Correctness is the exit code, checked against the
// interpreter first. A reader allocates the same either way, so its mode is
// read off the emitted code instead (modes-in-the-emitted-code).
//
// The `NO own` in the consuming cases is the point. Every one of these shapes
// reclaimed only when the stdlib or the program spelled `own` before this;
// the annotation is what the inference replaces.
var ownershipCases = []struct {
	name      string
	src       string
	maxAllocs int64
}{
	// The `std/pvec` shape in miniature, and the case this inference exists
	// for: a trie rebuilt through a parameter nothing declares `own`. Without
	// the inference every `.with` on a payload clones the whole 1-slot buffer,
	// twice per level per update.
	{"rebuilt-tree-parameter-is-counted", `enum Tree { Node(Tree[]), Leaf(i32), Nil }
@noinline
function set_leaf(t: Tree, at: i32, v: i32): Tree {
    match (t) {
        Node(kids) => {
            let nil: Tree = Nil;
            let child: Tree = kids[at];
            let rest: Tree[] = kids.with(at, nil);
            child = set_leaf(child, 0, v);
            return Node(rest.with(at, child));
        },
        Leaf(_) => { return Leaf(v); },
        Nil => { return Nil; }
    }
}
function main(): i32 {
    let leaf: Tree = Leaf(0);
    let inner: Tree = Node([leaf]);
    let t: Tree = Node([inner]);
    let i: i32 = 0;
    while (i < 20) { t = set_leaf(t, 0, i); i = i + 1; }
    match (t) {
        Node(a) => {
            match (a[0]) {
                Node(b) => { match (b[0]) { Leaf(v) => { if (v != 19) { return 1; } }, Node(_) => { return 2; }, Nil => { return 3; } } },
                Leaf(_) => { return 4; }, Nil => { return 5; }
            }
        },
        Leaf(_) => { return 6; }, Nil => { return 7; }
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 105},
	// A RECEIVER is inferred too, which `decl_param_mode` borrowed
	// unconditionally before — and the whole chain has to line up for it to
	// pay, which is what this case pins. `keep` hands its parameter back, so
	// the parameter escapes and is counted; `b.c` is then supplied to a
	// counted slot, so the RECEIVER escapes and is counted; `steals` can take
	// the field's unit because the record is owned; `inner` therefore reaches
	// `keep` uniquely held and the payload take updates the buffer in place.
	// Break any link and the update copies, which is what the census reads.
	//
	// This is `std/pvec.with` in miniature. Note there is no `own` to be had:
	// E051 refuses a borrowed local as an owned argument, so a hand-annotated
	// version of this shape does not type-check at all.
	{"receiver-counted-through-an-escaping-callee", `enum Chain { Link(i32[]), Stop }
@noinline
function keep(c: Chain, at: i32, v: i32): Chain {
    match (c) {
        Link(xs) => { return Link(xs.with(at, v)); },
        Stop => { return c; }
    }
}
struct Carrier { tag: i32, c: Chain }
@noinline
function (b: Carrier) step(at: i32, v: i32): Carrier {
    let inner: Chain = b.c;
    let stop: Chain = Stop;
    b = Carrier { ...b, c: stop };
    return Carrier { ...b, c: keep(inner, at, v) };
}
function main(): i32 {
    let b: Carrier = Carrier { tag: 7, c: Link([0, 0, 0]) };
    let i: i32 = 0;
    while (i < 30) { b = b.step(i % 3, i); i = i + 1; }
    match (b.c) {
        Link(xs) => { if (xs[0] + xs[1] + xs[2] != 27 + 28 + 29) { return 1; } },
        Stop => { return 2; }
    }
    if (b.tag != 7) { return 3; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 93},
	// A READER borrows, and that is what keeps the convention from costing.
	// `weigh` never lets its argument leave the frame — the string payload is
	// only measured — so counting it would add a retain and a release per call
	// and reclaim nothing that the caller's own temp path does not already.
	//
	// What this case pins is that the caller still reclaims each fresh
	// argument: no leak, no over-release. It does NOT pin the mode, because
	// the two conventions allocate identically for a reader. The mode itself
	// is asserted from the emitted code, in modes-in-the-emitted-code below.
	{"reader-parameter-stays-borrowed", `enum Node { Leaf(i32), Label(string), Empty }
@noinline
function weigh(n: Node): i32 {
    match (n) {
        Leaf(v) => { return v * 2; },
        Label(s) => { return s.len(); },
        Empty => { return 0; }
    }
}
@noinline
function make(k: i32): Node {
    if (k % 3 == 0) { return Leaf(k); }
    if (k % 3 == 1) { return Label("a" + "bc"); }
    return Empty;
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 30) { total = total + weigh(make(i)); i = i + 1; }
    if (total != 300) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 30},
	// A reader that RETURNS a projection of its parameter keeps it borrowed:
	// `peek` retains the element it hands back instead of taking the whole
	// cursor, so a caller that goes on using the cursor retains and releases
	// nothing per call. The parser's `Par.peek` is this shape. The census pins
	// that the retained element is still reclaimed and the cursor is not
	// over-released.
	{"returned-projection-keeps-the-parameter-borrowed", `enum Tok { Word(string), Num(i32), End }
struct Cursor { toks: Tok[], pos: i32 }
@noinline
function (c: Cursor) peek(): Tok {
    if (c.pos < c.toks.len()) { return c.toks[c.pos]; }
    return End;
}
@noinline
function (c: Cursor) advance(): Cursor { return Cursor { toks: c.toks, pos: c.pos + 1 }; }
function main(): i32 {
    let c: Cursor = Cursor { toks: [Word("ab" + "c"), Num(4), Word("de" + "")], pos: 0 };
    let total: i32 = 0;
    while (c.pos < 4) {
        match (c.peek()) { Word(s) => { total = total + s.len(); }, Num(n) => { total = total + n; }, End => { total = total + 100; } }
        c = c.advance();
    }
    if (total != 109) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 4},
	// A parameter the frame hands BACK escapes, so it is counted even though
	// the body only reads it — the caller must not also reclaim what it
	// returned. The pin here is the exit code and a balanced census: an
	// over-release shows up as a wrong answer, a missed one as live bytes.
	{"returned-parameter-is-counted", `enum Boxed { One(i32[]), None2 }
@noinline
function pass(b: Boxed): Boxed { return b; }
function main(): i32 {
    let b: Boxed = One([4, 5, 6]);
    let i: i32 = 0;
    while (i < 20) { b = pass(b); i = i + 1; }
    match (b) { One(xs) => { if (xs[2] != 6) { return 1; } }, None2 => { return 2; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 6},
}

func TestSelfHostOwnershipInference(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range ownershipCases {
		t.Run(tc.name, func(t *testing.T) {
			if want := interpExit(t, interpBin, tc.src); want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, exit := cli.exitOf(t, tc.src, target,
						"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if exit != 0 {
						t.Fatalf("exit = %d, want 0 (interp oracle)\n%s", exit, stderr)
					}
					censusWithin(t, tc.name, stderr, tc.maxAllocs)
				})
			}
		})
	}
	t.Run("modes-in-the-emitted-code", func(t *testing.T) {
		assertInferredModes(t, cli.runner, cli.bin, cli.stdlib)
	})
}

// A reader's mode is not census-observable: a parameter the callee only reads
// costs a retain and a release per call and reclaims nothing, so counting it
// wrongly changes the instruction count and nothing the allocation census can
// see. (That cost is what the bench corpus reads — it is how the type rung was
// found, at 1.049x on `ascii_scan`.) The mode itself is legible in the emitted
// code instead: a COUNTED reference parameter carries the callee's release, so
// its drop function is called inside the callee; a borrowed one is not.
//
// Both directions are asserted from ONE compilation, so the marker is proven
// present before its absence is read as an answer: `hands_back` returns its
// parameter and must drop, `reads_only` must not. The same pair for a record:
// `keep_or_new` hands its parameter back on one arm and drops it on the other,
// and `lends_to_builtin` only lends a field to a builtin's lent slot, which
// takes no unit. And for a receiver: `advance` builds a new cursor out of its
// parameter and must drop it, `peek` returns an element of its token array and
// must not.
const inferredModesProgram = `enum Node { Leaf(i32), Label(string), Empty }
enum Tree { Tip(i32), Fork(Tree, Tree) }
struct Rec { text: string, n: i32 }
struct Cursor { toks: Node[], pos: i32 }
@noinline
function (c: Cursor) peek(): Node {
    if (c.pos < c.toks.len()) { return c.toks[c.pos]; }
    return Empty;
}
@noinline
function (c: Cursor) advance(): Cursor { return Cursor { toks: c.toks, pos: c.pos + 1 }; }
@noinline
function depth(t: Tree): i32 {
    match (t) { Tip(_) => { return 1; }, Fork(l, r) => { return 1 + depth(l) + depth(r); } }
}
@noinline
function bump(t: Tree): Tree {
    match (t) { Tip(v) => { return Tip(v + 1); }, Fork(l, r) => { return Fork(bump(l), bump(r)); } }
}
@noinline
function lends_to_builtin(r: Rec): i32 { return __count_byte(r.text, 97) + r.n; }
@noinline
function keep_or_new(r: Rec, k: i32): Rec {
    if (k > 0) { return r; }
    return Rec { text: "z" + "", n: k };
}
@noinline
function reads_only(n: Node): i32 {
    match (n) { Leaf(v) => { return v * 2; }, Label(s) => { return s.len(); }, Empty => { return 0; } }
}
@noinline
function hands_back(n: Node, v: i32): Node {
    match (n) { Leaf(_) => { return Leaf(v); }, Label(s) => { return Label(s); }, Empty => { return n; } }
}
@noinline
function make(k: i32): Node {
    if (k % 3 == 0) { return Leaf(k); }
    if (k % 3 == 1) { return Label("a" + "bc"); }
    return Empty;
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 6) { total = total + reads_only(make(i)); i = i + 1; }
    let n: Node = hands_back(make(1), 5);
    if (total != 12) { return 1; }
    if (reads_only(n) != 3) { return 2; }
    let r: Rec = Rec { text: "banana" + "", n: 1 };
    let j: i32 = 0;
    let seen: i32 = 0;
    while (j < 4) { seen = seen + lends_to_builtin(r); r = keep_or_new(r, j); j = j + 1; }
    if (seen != 4) { return 3; }
    let t: Tree = Fork(Tip(1), Fork(Tip(2), Tip(3)));
    if (depth(t) != 5) { return 4; }
    t = bump(t);
    if (depth(t) != 5) { return 5; }
    let c: Cursor = Cursor { toks: [Leaf(2), Label("xy" + "")], pos: 0 };
    let w: i32 = 0;
    while (c.pos < 3) { w = w + reads_only(c.peek()); c = c.advance(); }
    if (w != 6) { return 6; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`

// asmWholeFunc returns the emitted body of `__fn_<name>` in full, from its
// label to the next function label (its own register entry `.r:` is inside
// it). The package's asmFuncBody stops at the
// first `ret`, which is a window these multi-arm bodies leave through early.
func asmWholeFunc(asm, name string) (string, bool) {
	lines := strings.Split(asm, "\n")
	start := -1
	for i, l := range lines {
		if l == "__fn_"+name+":" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "__fn_") && strings.HasSuffix(lines[i], ":") && lines[i] != "__fn_"+name+".r:" {
			return strings.Join(lines[start:i], "\n"), true
		}
	}
	return strings.Join(lines[start:], "\n"), true
}

func assertInferredModes(t *testing.T, runner []string, fernBin, stdlibRoot string) {
	t.Helper()
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(inferredModesProgram), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	asmPath := filepath.Join(proj, "out.s")
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v (%s)", err, out)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	const drop = "__sem_release_Node"
	back, ok := asmWholeFunc(string(asm), "hands_back")
	if !ok {
		t.Fatal("no __fn_hands_back in the emitted code")
	}
	if !strings.Contains(back, drop) {
		t.Fatalf("hands_back does not call %s — the marker this reads is gone, so the reader assertion below proves nothing", drop)
	}
	reader, ok := asmWholeFunc(string(asm), "reads_only")
	if !ok {
		t.Fatal("no __fn_reads_only in the emitted code")
	}
	if strings.Contains(reader, drop) {
		t.Errorf("reads_only calls %s: a parameter the body only reads was inferred COUNTED, which costs a retain and a release per call and reclaims nothing", drop)
	}
	const recDrop = "__sem_release_Rec"
	keep, ok := asmWholeFunc(string(asm), "keep_or_new")
	if !ok {
		t.Fatal("no __fn_keep_or_new in the emitted code")
	}
	if !strings.Contains(keep, recDrop) {
		t.Fatalf("keep_or_new does not call %s — the marker this reads is gone, so the builtin assertion below proves nothing", recDrop)
	}
	lends, ok := asmWholeFunc(string(asm), "lends_to_builtin")
	if !ok {
		t.Fatal("no __fn_lends_to_builtin in the emitted code")
	}
	if strings.Contains(lends, recDrop) {
		t.Errorf("lends_to_builtin calls %s: a record whose field only reaches a builtin's lent slot was inferred COUNTED", recDrop)
	}
	const treeDrop = "__sem_release_Tree"
	rebuilt, ok := asmWholeFunc(string(asm), "bump")
	if !ok {
		t.Fatal("no __fn_bump in the emitted code")
	}
	if !strings.Contains(rebuilt, treeDrop) {
		t.Fatalf("bump does not call %s — the marker this reads is gone, so the traversal assertion below proves nothing", treeDrop)
	}
	walk, ok := asmWholeFunc(string(asm), "depth")
	if !ok {
		t.Fatal("no __fn_depth in the emitted code")
	}
	if strings.Contains(walk, treeDrop) {
		t.Errorf("depth calls %s: a recursive walk whose result holds no reference was inferred COUNTED through its own recursive call", treeDrop)
	}
	const cursorDrop = "__sem_release_Cursor"
	step, ok := asmWholeFunc(string(asm), "Cursor__advance")
	if !ok {
		t.Fatal("no __fn_Cursor__advance in the emitted code")
	}
	if !strings.Contains(step, cursorDrop) {
		t.Fatalf("Cursor.advance does not call %s — the marker this reads is gone, so the projection assertion below proves nothing", cursorDrop)
	}
	look, ok := asmWholeFunc(string(asm), "Cursor__peek")
	if !ok {
		t.Fatal("no __fn_Cursor__peek in the emitted code")
	}
	if strings.Contains(look, cursorDrop) {
		t.Errorf("Cursor.peek calls %s: a receiver whose projection is returned was inferred COUNTED, so every call retains and releases the whole cursor", cursorDrop)
	}
}
