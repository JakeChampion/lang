package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var ownershipCases = []struct {
	name      string
	src       string
	maxAllocs int64
}{
	{"append-into-element-takes-only-a-sole-owned-slot", `@noinline
function bucket(out: i32[][], n: i32, from: i32): i32[][] {
    let i: i32 = from;
    while (i < from + n) {
        let b: i32 = i % 2;
        out = out.with(b, out[b].append(i));
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let base: i32[][] = [[], []];
    let kept: i32[][] = base;
    let grown: i32[][] = bucket(base, 10, 0);
    if (kept[0].len() != 0 || kept[1].len() != 0) { return 1; }
    if (grown[0].len() != 5 || grown[1].len() != 5 || grown[1][4] != 9) { return 2; }
    let own: i32[][] = bucket([[], []], 10, 0);
    let again: i32[][] = bucket(own, 4, 10);
    if (again[0].len() != 7 || again[1].len() != 7 || again[0][6] != 12) { return 3; }
    if (kept[0].len() != 0) { return 4; }
    return 0;
}`, 13},
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
}`, 24},
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
	{"update-base-is-counted", `struct Hm { names: string[], values: string[] }
@noinline
function (h: Hm) add(n: string, v: string): Hm {
    return Hm { ...h, names: h.names.append(n), values: h.values.append(v) };
}
function main(): i32 {
    let h: Hm = Hm { names: [], values: [] };
    let i: i32 = 0;
    while (i < 30) { h = h.add("k", "v"); i = i + 1; }
    let kept: Hm = h;
    let more: Hm = kept.add("x", "y");
    if (h.names.len() != 30 || more.names.len() != 31 || kept.values.len() != 30) { return 1; }
    if (more.names[30] != "x" || h.values[29] != "v") { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 12},
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
	t.Run("modes-program-runs", func(t *testing.T) {
		stderr, exit := cli.exitOf(t, inferredModesProgram, "x86-64-linux",
			"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
		if exit != 0 {
			t.Fatalf("exit = %d, want 0\n%s", exit, stderr)
		}
		censusWithin(t, "modes-program", stderr, 22)
	})
}

const inferredModesProgram = `import "std/crypto/chacha20poly1305";
enum Node { Leaf(i32), Label(string), Empty }
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
function visit_depth(t: Tree, f: (Tree) => i32): i32 {
    match (t) { Tip(_) => { return f(t); }, Fork(l, r) => { return f(t) + visit_depth(l, f) + visit_depth(r, f); } }
}
function one_node(t: Tree): i32 { return 1; }
@noinline
function names_of(t: Tree, own acc: string[]): string[] {
    match (t) { Tip(_) => { return acc.append("tip"); }, Fork(l, r) => { return names_of(r, names_of(l, acc)); } }
}
@noinline
function hand_to(own t: Tree, f: (own Tree) => Tree): Tree { return f(t); }
function keep_tree(own t: Tree): Tree { return t; }
@noinline
function pick(a: Tree, b: Tree, k: i32): Tree {
    if (k > 0) { return a; }
    return b;
}
@noinline
function lends_to_builtin(r: Rec): i32 { return __count_byte(r.text, 97) + r.n; }
@noinline
function keep_or_new(r: Rec, k: i32): Rec {
    if (k > 0) { return r; }
    return Rec { text: "z" + "", n: k };
}
@noinline
function checked_keep(r: Rec, k: i32): Rec {
    if (lends_to_builtin(r) > k) { return r; }
    return Rec { text: "y" + "", n: k };
}
@noinline
function text_view(r: Rec): str {
    let v: str = r.text;
    return v;
}
@noinline
function view_keep(r: Rec, k: i32): Rec {
    if (text_view(r).len() > k) { return r; }
    return Rec { text: "x" + "", n: k };
}
struct Sealer { key: u8[], n: i32 }
@noinline
function sealed_len(s: Sealer, nonce: [u8]): i32 {
    match (chacha20poly1305.seal(s.key, nonce, nonce, nonce)) { Ok(x) => { return x.len() + s.n; }, Err(_) => { return s.n; } }
    return 0;
}
@noinline
function sealer_or_new(s: Sealer, k: i32): Sealer {
    if (k > 0) { return s; }
    return Sealer { key: __alloc_u8(32), n: k };
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
    let q: Rec = Rec { text: "aab" + "", n: 0 };
    q = checked_keep(q, 1);
    q = view_keep(q, 2);
    q = checked_keep(q, 5);
    if (q.text != "y" || q.n != 5) { return 7; }
    let t: Tree = Fork(Tip(1), Fork(Tip(2), Tip(3)));
    if (depth(t) != 5) { return 4; }
    t = pick(t, Tip(9), 1);
    if (depth(t) != 5) { return 5; }
    if (visit_depth(t, one_node) != 5) { return 8; }
    if (names_of(t, []).len() != 3) { return 10; }
    t = hand_to(t, keep_tree);
    if (depth(t) != 5) { return 9; }
    let c: Cursor = Cursor { toks: [Leaf(2), Label("xy" + "")], pos: 0 };
    let w: i32 = 0;
    while (c.pos < 3) { w = w + reads_only(c.peek()); c = c.advance(); }
    if (w != 6) { return 6; }
    let sl: Sealer = sealer_or_new(Sealer { key: __alloc_u8(32), n: 4 }, 1);
    if (sealed_len(sl, __alloc_u8(12)) != 32) { return 11; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`

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
		if strings.HasPrefix(lines[i], "__fn_") && strings.HasSuffix(lines[i], ":") && lines[i] != "__fn_"+name+".r:" && lines[i] != "__fn_"+name+".k:" {
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
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot)
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
	checked, ok := asmWholeFunc(string(asm), "checked_keep")
	if !ok {
		t.Fatal("no __fn_checked_keep in the emitted code")
	}
	if !strings.Contains(checked, recDrop) {
		t.Errorf("checked_keep does not call %s: a record read only through a user function whose result holds no reference was inferred BORROWED", recDrop)
	}
	viewed, ok := asmWholeFunc(string(asm), "view_keep")
	if !ok {
		t.Fatal("no __fn_view_keep in the emitted code")
	}
	if strings.Contains(viewed, recDrop) {
		t.Errorf("view_keep calls %s: a record lent to a user function returning a view of it was inferred COUNTED", recDrop)
	}
	const treeDrop = "__sem_release_Tree"
	picked, ok := asmWholeFunc(string(asm), "pick")
	if !ok {
		t.Fatal("no __fn_pick in the emitted code")
	}
	if !strings.Contains(picked, treeDrop) {
		t.Fatalf("pick does not call %s — the marker this reads is gone, so the traversal assertion below proves nothing", treeDrop)
	}
	walk, ok := asmWholeFunc(string(asm), "depth")
	if !ok {
		t.Fatal("no __fn_depth in the emitted code")
	}
	if strings.Contains(walk, treeDrop) {
		t.Errorf("depth calls %s: a recursive walk whose result holds no reference was inferred COUNTED through its own recursive call", treeDrop)
	}
	visit, ok := asmWholeFunc(string(asm), "visit_depth")
	if !ok {
		t.Fatal("no __fn_visit_depth in the emitted code")
	}
	if strings.Contains(visit, treeDrop) {
		t.Errorf("visit_depth calls %s: a parameter lent to a function value's borrowing slot was inferred COUNTED", treeDrop)
	}
	names, ok := asmWholeFunc(string(asm), "names_of")
	if !ok {
		t.Fatal("no __fn_names_of in the emitted code")
	}
	if strings.Contains(names, treeDrop) {
		t.Errorf("names_of calls %s: a recursive walk whose result cannot hold the tree was inferred COUNTED through its own recursive call", treeDrop)
	}
	const sealerDrop = "__sem_release_Sealer"
	fresh, ok := asmWholeFunc(string(asm), "sealer_or_new")
	if !ok {
		t.Fatal("no __fn_sealer_or_new in the emitted code")
	}
	if !strings.Contains(fresh, sealerDrop) {
		t.Fatalf("sealer_or_new does not call %s — the marker this reads is gone, so the view-parameter assertion below proves nothing", sealerDrop)
	}
	sealing, ok := asmWholeFunc(string(asm), "sealed_len")
	if !ok {
		t.Fatal("no __fn_sealed_len in the emitted code")
	}
	if strings.Contains(sealing, sealerDrop) {
		t.Errorf("sealed_len calls %s: a record whose field only reaches a callee's view parameter, which the callee never counts, was inferred COUNTED", sealerDrop)
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
