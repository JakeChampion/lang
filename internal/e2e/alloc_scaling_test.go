package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// Allocation ASYMPTOTICS are ungated. `TestSelfHostAllocDifferentialX86_64`
// compares how much the two compilers allocate against each other, which
// catches them drifting apart — but a regression that lands in the shared
// frontend, or in a stdlib function both compile the same way, moves both
// sides equally and stays green. docs/TEST-GATES.md lists allocation volume
// under "what nothing gates"; the differential closed half of it. This closes
// the other half: does a shape still allocate in the COMPLEXITY CLASS it is
// supposed to?
//
// That is the regression that actually hurts. The two most expensive stdlib
// bugs found in the last pass were both asymptotic, not constant-factor: a
// naive substring search that went quadratic on repetitive input (2.655s ->
// 0.014s once fixed) and a merge sort that materialised a full copy of the
// array on every pass. Neither changes an answer, so no correctness gate can
// see them, and both look fine at the small n a unit test would use.
//
// WHY A RATIO AND NOT A BYTE BUDGET. The obvious design is to record
// `__heap_bump_bytes()` per shape and fail on exceeding it. That gate rots:
// every legitimate change to a header size, a growth schedule, or the SSO
// threshold moves every recorded number at once, so the budgets get re-recorded
// in bulk without being read, and a real regression rides in with the batch.
// Worse, the failure it reports ("42 KB, budget 39 KB") does not tell you
// whether anything is actually wrong.
//
// Measuring the same shape at n and 2n and bounding the RATIO is immune to all
// of that. Constant factors cancel — a change that makes every allocation 20%
// bigger does not move the ratio at all — while the asymptotic class comes
// through unmistakably. Measured on this corpus: a linear shape sits at
// 2.03-2.06x per doubling and a quadratic one at 3.79-3.89x. There is no
// tuning problem in that gap.
//
// WHAT `__heap_bump_bytes()` MEANS HERE, precisely: bytes the bump allocator
// handed out FRESH, i.e. total allocation minus whatever the freelist
// recycled. It is a high-water mark, not a traffic counter — a shape that
// allocates and frees the same block a million times reports one block. That
// makes it the right instrument for this gate (it tracks the working set the
// program actually forces the allocator to find) and the wrong one for
// counting churn; `__arr_push_shared_bytes()` is the traffic counter, and the
// differential gate already uses it.
//
// Never peak RSS: it varies 12x with transparent hugepages (43 MB local vs
// 552 MB on a CI runner, same binary and input), because the arena is a 16 GiB
// MAP_NORESERVE mapping whose first touch maps a 2 MB page under THP=always
// and a 4 KB page under madvise. `__heap_bump_bytes()` is exact, host-
// independent, and meaningful under qemu.
//
// Native x86-64 only, and deliberately cheap: a compile is ~18 ms, so the
// whole corpus runs in well under a second and can sit in a fast lane. The
// self-host side of the same question is the differential gate's job.

// allocScaleCase is one shape, measured at n and 2n in separate processes.
//
// decls must define `churn(n: i32): i32` — one self-contained unit of work
// returning something derived from the result, so nothing can be optimised
// away as dead. Each measurement runs churn ONCE from a cold heap, so the
// figure is that shape's peak fresh allocation with no freelist priming from
// a previous iteration.
type allocScaleCase struct {
	name  string
	decls string
	n     int

	// maxRatio bounds volume(2n) / volume(n), scaled by 100 to keep the
	// table integer-only. 220 admits linear growth with headroom; a
	// quadratic shape lands near 390 and cannot hide under it.
	//
	// A shape whose allocation is genuinely CONSTANT in n takes ~110: it
	// should not grow at all, and pinning that is the point of including it.
	maxRatio int

	// wantQuadratic marks a shape that is SUPPOSED to be superlinear, so the
	// gate asserts the ratio is ABOVE maxRatio instead of below it. Without
	// this, a shape like naive `s = s + x` in a loop would have to be left
	// out of the corpus, and the gate would lose its own calibration — these
	// entries are what prove the bound still separates the two classes.
	wantQuadratic bool
}

var allocScaleCases = []allocScaleCase{
	{
		// The accumulator every byte-emitter in the self-host compiler is
		// built from. If `.append` ever stops growing capacity
		// geometrically, or starts copying a buffer it could have extended,
		// this is where it shows.
		name: "array-append",
		decls: `function churn(n: i32): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		// The same accumulator threaded through a borrowed param and handed
		// back — the shape that developed OPPOSITE cliffs in the two
		// compilers (docs/TEST-GATES.md). Included here for its asymptotics
		// rather than the cross-compiler split the differential covers.
		name: "array-append-through-call",
		decls: `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function churn(n: i32): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = step(a, i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		// String building the RIGHT way: collect, then join once. This is the
		// documented alternative to the quadratic shape below, so if it ever
		// stops being linear the advice goes with it.
		name: "string-parts-join",
		decls: `import "std/string";
function churn(n: i32): i32 {
    var parts: string[] = [];
    var i: i32 = 0;
    while (i < n) { parts = parts.append("item"); i = i + 1; }
    return parts.join(",").len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		// Map insert. The table has to rehash as it grows; geometric
		// resizing keeps that linear in total.
		name: "map-insert",
		decls: `import "std/i32";
import "core/map";
function churn(n: i32): i32 {
    var m: Map[string, i32] = map_new(8);
    var i: i32 = 0;
    while (i < n) { m = m.insert(i.to_string(), i); i = i + 1; }
    return m.len();
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		// Substring search over a haystack that grows with n. Two-Way is
		// O(1) SPACE — it allocates no skip table — so the allocation here
		// is the haystack itself and nothing per-search. A regression to an
		// allocating search algorithm shows as growth beyond the input.
		name: "substring-search",
		decls: `import "std/string";
function churn(n: i32): i32 {
    var hay: string[] = [];
    var i: i32 = 0;
    while (i < n) { hay = hay.append("abcab"); i = i + 1; }
    var s: string = hay.join("");
    return s.index_of("abcabx") + s.index_of("bcab") + 2;
}`,
		n:        400,
		maxRatio: 220,
	},
	{
		// Replacing an element of an array held in a STRUCT FIELD. The loop
		// holds two elements and replaces one per round, so its allocation is
		// CONSTANT in n — every replaced element is reclaimed.
		//
		// It did not use to be. `emitFieldDropOnStack` released an array field
		// with the buffer-only `__fern_arr_dec`, which drops no elements, so
		// one element box leaked per call, unbounded (#6397). A bare local
		// array was always fine — its receiver is a reassign-to-self move, so
		// the CoW helper takes its in-place branch instead of the copy branch
		// that retains the elements — and only a struct-field read forces the
		// copy branch by leaving the buffer at rc >= 2. That is why this entry
		// reads the array out of a field rather than a local.
		name: "struct-field-array-with",
		decls: `struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function churn(n: i32): i32 {
    var b: Box = Box { items: [P { a: 0, b: 0 }, P { a: 0, b: 0 }], tag: 0 };
    var i: i32 = 0;
    while (i < n) {
        b = Box { ...b, items: b.items.with(i % 2, P { a: i, b: i + 1 }) };
        i = i + 1;
    }
    return b.items[0].a + b.items[1].b;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// The string-element half of the same family (#6407). `.with` skipped
		// every element retain/release for a `string[]` — strings were not in
		// arrElemIsRcTracked — so each round leaked the whole receiver: the
		// escape analysis has no counted store to key on, taints the receiver
		// through the `a[j]` projection, and drops it out of freeEligible. One
		// `.with` therefore cost N+1 blocks per round, not one element.
		//
		// Constant in n: each round builds, updates and discards one array.
		name: "string-array-with",
		decls: `import "std/i32";
function mks(): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 8) { out = out.append("kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var a: string[] = mks();
        a = a.with(3, a[5]);
        t = t + a.len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A closure stored in a struct FIELD (#6443) — a provider table, a
		// strategy record, a middleware chain, a callback registry. Nothing
		// released it: `appendChildDrop` had no FuncType arm, so a closure
		// field fell to the bare `__fern_rc_dec` fall-through, which zeroes
		// the pair's count and stops. The pair block, the env block and every
		// rc-tracked capture were stranded — three blocks per instance, and
		// per instance means it scales with the table.
		//
		// Constant in n: each round builds and discards one 8-provider table.
		name: "closure-in-struct-field",
		decls: `import "std/i32";
struct P { name: string, f: (i32) => i32 }
function mkP(n: i32): P { return P { name: "provider" + n.to_string(), f: (x: i32) => x + n }; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < n) {
        var ps: P[] = [];
        var i: i32 = 0;
        while (i < 8) { ps = ps.append(mkP(i)); i = i + 1; }
        var j: i32 = 0;
        while (j < ps.len()) { t = t + (ps[j].f)(1); j = j + 1; }
        r = r + 1;
    }
    return t % 7;
}`,
		n:        200,
		maxRatio: 130,
	},
	{
		// A fresh array handed to a CLOSURE call (#6460). The arg-temp
		// reclaim only ever ran for a call to a NAMED function, so the same
		// literal passed through a function-typed local or param was never
		// released at all — `frees=0`, not one short. Calling a callback with
		// a freshly built collection is the ordinary way to use one.
		//
		// Constant in n: each round builds and discards one 3-element array.
		name: "closure-call-array-arg",
		decls: `import "std/i32";
function each(f: (i32[]) => i32, n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + f([1, 2, i]); i = i + 1; }
    return t;
}
function churn(n: i32): i32 {
    var h: (i32[]) => i32 = (xs: i32[]) => xs.len();
    return each(h, n) % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// Reading a POINTER field off a call result (#6401) — `mk().items`,
		// `config().hosts`, `parse(s).body`. The container is a temporary
		// nobody holds, so nothing released it: 96 B a round, unbounded,
		// while `var b = mk(); b.items` was flat. The scalar-field form of
		// the same read was already reclaimed; the pointer form was left out
		// because the loaded value aliases the box, which is exactly what
		// makes it need a retain before the container's deep drop rather
		// than no drop at all.
		//
		// Constant in n: each round builds one container, keeps one field
		// and discards the rest.
		name: "call-result-field-read",
		decls: `import "std/i32";
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function mk_box(i: i32): Box { return Box { items: [P { a: i, b: i }, P { a: i, b: i }], tag: i }; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var a: P[] = mk_box(i).items;
        t = t + a[0].a;
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A struct-update spread whose base is a FRESH value. `T { ...b, f: v }`
		// where `b` is a LOCAL borrows the base — the local releases at its own
		// scope exit, so the construction must not — but that reasoning does
		// not cover a call result or a nested literal, which nobody else holds.
		// The base box leaked, one per evaluation, unbounded, while the
		// local-base spelling of the same thing was flat.
		//
		// Constant in n: each round builds and discards one record.
		name: "struct-update-fresh-base",
		decls: `struct R { tag: string, note: string, n: i32 }
function mk(): R { return R { tag: "base", note: "", n: 0 }; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var a: R = R { ...mk(), n: i };
        var b: R = R { ...R { tag: "inner", note: "x", n: 0 }, n: i };
        var c: R = R { ...R { ...mk(), note: "mid" }, n: i };
        t = t + a.n + b.note.len() + c.note.len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A pair-form Option return carries `(tag, payload)` in registers with
		// no box, so the box-reclaim the heap-form match path performs has
		// nothing to free. The payload is still allowed to be POINTER-shaped
		// (array / slice / struct / tuple all pass isPairFormPayloadShape),
		// and for those the register was the ONLY reference to a value the
		// callee had just allocated: bound as a borrow, owned by nobody. Every
		// evaluation leaked the whole payload — 3200 / 6400 / 12800 bytes at
		// 100 / 200 / 400 rounds, exactly linear.
		//
		// `match (mk()) { Some(v) => … }` is how a lookup is written, which is
		// what made this expensive. Constant in n: one payload per round,
		// released when the arm ends.
		name: "pair-form-pointer-payload",
		decls: `struct P { a: i32, b: i32 }
function mkarr(k: i32): Option[i32[]] {
    if (k == 0) { return None; }
    return Some([k, k + 1, k + 2]);
}
function mkstruct(k: i32): Option[P] {
    if (k == 0) { return None; }
    return Some(P { a: k, b: k + 1 });
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        match (mkarr(1)) { Some(a) => { t = t + a[0]; }, None => { }, }
        match (mkstruct(1)) { Some(p) => { t = t + p.b; }, None => { }, }
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// The TUPLE payload the case above does not reach (#6409). Its
		// eligibility looked identical — a tuple is pointer-shaped, `x.0`
		// reads as confined, and emitOwnedSlotDrop has a TupleType branch —
		// so the leak was assumed to be a partial reclaim below the gate.
		// It was not: the payload never reached the release at all.
		//
		// `variantPayloads` holds the DECLARED payload types, so `Some`'s is
		// still `ParamType{T}` for a generic Option, and exprNoParamEscape's
		// TupleLit case matches on the slot BEING a TupleType. StructLit and
		// ArrayLit carry their own type and never consult the slot, which is
		// why the same shape in a struct or an array was flat while the tuple
		// leaked its whole box every round.
		//
		// Constant in n on all three backends. The four shapes are what
		// separated cause from symptom: the plain tuple and the tuple with a
		// pointer ELEMENT would both be explained by a partial reclaim, the
		// NESTED tuple would not, and the Result case proves the fix is on the
		// generic-substitution path rather than special to Option.
		name: "pair-form-tuple-payload",
		decls: `function mkt(k: i32): Option[(i32, i32)] {
    if (k == 0) { return None; }
    return Some((k, k + 1));
}
function mkta(k: i32): Option[(i32[], i32)] {
    if (k == 0) { return None; }
    return Some(([k, k + 1, k + 2], k));
}
function mktn(k: i32): Option[((i32, i32), i32)] {
    if (k == 0) { return None; }
    return Some(((k, k), k));
}
function mkr(k: i32): Result[(i32, i32), (i32, i32)] {
    if (k == 0) { return Err((0, 0)); }
    return Ok((k, k + 1));
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        match (mkt(1))  { Some(a) => { t = t + a.0; }, None => { }, }
        match (mkta(1)) { Some(a) => { t = t + a.1; }, None => { }, }
        match (mktn(1)) { Some(a) => { t = t + a.1; }, None => { }, }
        match (mkr(1))  { Ok(a) => { t = t + a.0; }, Err(e) => { t = t + e.0; }, }
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// Binding a tuple's STRUCT element to a local — `var q: P = p.1` —
		// incs at the binding site and was never credited with owning the
		// reference, so the element leaked once per extraction, unbounded.
		//
		// rhsTainted admits the same read out of a struct-typed local as a
		// counted alias and was one type short of admitting it out of a tuple,
		// though both halves of that rule's argument hold for tuples: the
		// binding incs, and the tuple deep-drops its elements at scope exit.
		// The leak grew with the ELEMENT's width (32 B at three fields, 80 B
		// at fifteen) and not the tuple's, which is what identified it.
		//
		// `(value, state)` threading is the shape that hits it. Reading the
		// field straight through the tuple was flat all along — but `p.1.a`
		// did not compile at all until the sibling fix in this change, so
		// there was no leak-free spelling.
		//
		// Constant in n: one tuple and one element per round.
		name: "tuple-struct-element-binding",
		decls: `struct P { a: i32, b: i32, c: i32 }
function pull(s: P): (i32, P) { return (s.a, P { a: s.a + 1, b: s.b, c: s.c }); }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var s: P = P { a: 0, b: 0, c: 0 };
    var i: i32 = 0;
    while (i < n) {
        var p: (i32, P) = pull(s);
        var q: P = p.1;
        t = t + q.a;
        s = q;
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// The array half of the family above — `var a: Entry = es[0]` on a
		// struct-of-strings array (#6499). The binding's exit sweep reclaims
		// the struct box inline rather than through the generated drop fn,
		// and its native single-word string arm released each field with a
		// bare rc-dec: that decrements and never frees, so the count went
		// 1 -> 0 and both buffers were stranded, 128 B a round.
		//
		// The two spellings measured apart is what identified it: the same
		// read used INLINE (`es.with(0, es[1])`) was flat, because that route
		// goes through __drop_struct_Entry, which calls __fern_str_dec. Only
		// the inline arm was short.
		//
		// The binding has to be in a callee, not the loop body: a
		// loop-scoped local takes a different reclaim path and was flat
		// throughout.
		//
		// Constant in n: one array, four entries and one binding per round.
		name: "array-struct-element-binding",
		decls: `import "std/i32";
struct Entry { key: string, value: string }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function mk(k: i32): Entry[] {
    var es: Entry[] = [];
    var i: i32 = 0;
    while (i < 4) { es = es.append(Entry { key: wide(k + i), value: wide(k + i + 100) }); i = i + 1; }
    return es;
}
function probe(k: i32): i32 {
    var es: Entry[] = mk(k);
    var a: Entry = es[0];
    return es.len() + a.key.len();
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A fresh array handed to a constructor that stores it (#6522) —
		// `node(name, no_deps(), k)`, i.e. how every tree, graph and AST gets
		// built. The stage-(b) arg-temp reclaim is gated per CALL, and one
		// pointer-shaped result disqualifies every argument of that call at
		// once, so the caller's reference to the fresh array was stranded
		// while the callee took its own: 32 B per constructed node on all
		// three backends. Writing the same struct literal INLINE was flat,
		// because then there is no argument and no second reference.
		//
		// Constant in n: two nodes, each with one array, per round.
		name: "fresh-array-into-constructor",
		decls: `struct Node { name: string, deps: string[], mtime: i32, exists: boolean }
function no_deps(): string[] { var e: string[] = []; return e; }
function node(name: string, deps: string[], mtime: i32): Node {
    return Node { name: name, deps: deps, mtime: mtime, exists: true };
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var out: Node[] = [];
        out = out.append(node("alpha", no_deps(), i));
        out = out.append(node("beta", no_deps(), i + 1));
        t = t + out.len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// `param.append(x)` CONSUMED — passed straight to a borrowing call
		// rather than assigned back (#6501). Threading an immutable trail
		// down a recursion is the shape: backtracking, DFS with a path,
		// subset enumeration.
		//
		// The arg-temp recognizer for an append result only admitted the
		// #4827 FORCED-COPY path, whose test is syntactic last-use — and a
		// name that occurs once in the source reads as a last use even
		// inside a loop that re-reads it every iteration, so the loop's
		// appends were classified as in-place and left unreclaimed. Both
		// grow outcomes are accounted for, so the classification was not
		// carrying the safety it looked like it was.
		//
		// Constant in n: one seed and eight appends per round.
		name: "param-append-consumed",
		decls: `function sink(xs: i32[]): i32 { return xs.len(); }
function walk(path: i32[]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) { t = t + sink(path.append(i)); i = i + 1; }
    return t;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var seed: i32[] = [0];
        t = t + walk(seed) + sink(seed.append(i));
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// The element-receiver spelling of the same consumed append —
		// `sink(xs[i].append(9))`. An Index receiver is not an Ident, so it
		// could never satisfy the forced-copy test at all, and the row reads
		// `xs[0]` back after both appends so a reclaim that freed the
		// container's element instead of the fresh copy shows as a wrong
		// answer here rather than only in the rc corpus.
		//
		// Constant in n: one array-of-arrays and two appends per round.
		name: "array-element-append-consumed",
		decls: `function sink(xs: i32[]): i32 { return xs.len(); }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[][] = [[0], [1]];
        t = t + sink(xs[0].append(9)) + sink(xs[1].append(9)) + sink(xs[0]);
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// `.with` straight off a fresh container read. The read owns the
		// array it produced, so the pre-call retain that makes a BORROWED
		// projection copy is wrong here twice over: it buys the copy, and
		// it strands the original where no slot release can reach it — 64 B
		// a round, unbounded, while `mk().xs.len()` was flat.
		//
		// Constant in n: one struct, one array and one copy-free `.with`.
		name: "with-on-fresh-container-read",
		decls: `struct S { xs: i32[], tag: i32 }
function mk(): S { return S { xs: [1, 2], tag: 0 }; }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var b: i32[] = mk().xs.with(0, i);
        t = t + b[0];
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A struct literal consuming an owned STRING local at that local's
		// last use. Every other rc-tracked field type moves into the
		// container there — the field-init retain and the local's exit-sweep
		// release cancel — but a string field was excluded from the move
		// while the construction still suppressed the release, so the retain
		// stood alone and one buffer per round was stranded.
		//
		// Constant in n: one string per struct, one struct per round.
		name: "struct-string-field-move",
		decls: `import "std/i32";
struct W { name: string, n: i32 }
struct Acc { last: string, total: i32 }
function mk(k: i32): W {
    var s: string = "payload-string-" + k.to_string();
    return W { name: s, n: k };
}
function step(a: Acc, k: i32): Acc {
    var s: string = "acc-payload-" + k.to_string();
    return Acc { last: s, total: a.total + k };
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var a: Acc = Acc { last: "", total: 0 };
    var i: i32 = 0;
    while (i < n) {
        var w: W = mk(i % 8);
        a = step(a, i % 8);
        t = t + w.name.len() + a.last.len();
        i = i + 1;
    }
    return (t + a.total) % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A source-declared receiver method whose result feeds another call.
		// The caller owns that result, but the reclaim gate rejected any
		// `__`-prefixed callee not proven to return a fresh value — and a
		// source method mangles to `__method_<Type>_<name>`, so one identity
		// return in the body (`if (n <= 0) { return s; }`) disqualified it
		// and every materialised result was stranded. The same body as a
		// free function was reclaimed.
		//
		// Constant in n: two materialised results per round.
		name: "method-identity-return-result",
		decls: `import "std/i32";
import "std/string";
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var base: string = "long-enough-payload-" + (i % 8).to_string();
        t = t + base.tail(2).len() + base.tail(3).to_owned().len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// `string[][]`: the outer drop reclaims each inner `string[]` buffer
		// but reached it through the pointer-element helper on the
		// single-word string ABI, whose per-element `__fern_rc_dec` never
		// returns a heap string's buffer to the freelist. One buffer per
		// element per round, x86-64 only — the two-word backends already
		// selected the string-aware walk, so no cross-compiler gate could
		// see it.
		//
		// Constant in n: one nested array per round, dropped at scope exit.
		name: "nested-string-array-drop",
		decls: `import "std/i32";
function mk(k: i32): string[][] {
    var inner: string[] = ["alpha-payload-" + k.to_string(), "beta-payload-" + k.to_string()];
    return [inner, ["gamma-payload-" + k.to_string()]];
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var a: string[][] = mk(i % 8);
        t = t + a[0][0].len() + a[1][0].len();
        i = i + 1;
    }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// A heap-boxed `Result` matched with arms that RETURN. The box
		// release the match owes sits at the post-match join, which an arm
		// ending in a return branches straight past — so the box leaked once
		// per round while the identical match with fall-through arms was
		// flat (#6417). The map-get sibling below reaches the same join.
		//
		// Constant in n: one box per round, all of it reclaimed.
		name: "match-returning-arms-boxed-result",
		decls: `function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function pick(i: i64): i32 {
    match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => { return 0; } }
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + pick(i as i64); i = i + 1; }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// The `m.get(k)` rebox reaches the same join through
		// reclaimableMapGetScrutinee, and had the same hole: 16 B a lookup,
		// unbounded, whenever the arm returned rather than fell through.
		name: "match-returning-arms-map-get",
		decls: `import "core/map";
function pick(m: Map[string, i32], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v + 1; }, None => { return 0; } }
}
function churn(n: i32): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + pick(m, "a"); i = i + 1; }
    return t % 7;
}`,
		n:        400,
		maxRatio: 130,
	},
	{
		// CALIBRATION: naive left-fold string concatenation, which is
		// inherently quadratic — every `+` copies the whole accumulated
		// prefix. This is not a bug to fix; it is the control that proves the
		// bound above still discriminates. If this ever drops BELOW the
		// bound, either the gate stopped measuring or `+` grew a rope/builder
		// representation — both of which must be noticed deliberately.
		name: "string-concat-fold",
		decls: `import "std/string";
function churn(n: i32): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < n) { s = s + "x"; i = i + 1; }
    return s.len();
}`,
		n:             400,
		maxRatio:      220,
		wantQuadratic: true,
	},
}

// volumeSrc returns a program that prints the bytes one churn(n) hands out
// fresh, measured from a cold heap.
//
// b0 is read BEFORE the churn so process startup and any stdlib module
// initialisation are excluded — otherwise a fixed startup cost would inflate
// the n measurement, depress the ratio, and make the gate quietly lenient.
func (c allocScaleCase) volumeSrc(n int) string {
	return fmt.Sprintf(`import "std/i64";
%s
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var w: i32 = churn(%d);
    var b1: i64 = __heap_bump_bytes();
    if (w < 0) { return 251; }
    print("VOLUME " + (b1 - b0).to_string());
    return 0;
}
`, c.decls, n)
}

// parseVolume pulls the byte count out of the probe's stdout.
//
// The value is printed rather than returned as an exit code on purpose: an
// exit code caps at 255, which would force the figure into KB and lose the
// resolution the ratio is computed from.
func parseVolume(t *testing.T, label, out string, exit int) int64 {
	t.Helper()
	if exit == 251 {
		t.Fatalf("%s: churn returned a negative value — the probe is not measuring the work it thinks it is", label)
	}
	if exit != 0 {
		t.Fatalf("%s: probe exited %d, want 0\noutput:\n%s", label, exit, out)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "VOLUME ") {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimPrefix(line, "VOLUME "), 10, 64)
		if err != nil {
			t.Fatalf("%s: unparseable VOLUME line %q: %v", label, line, err)
		}
		return v
	}
	t.Fatalf("%s: no VOLUME line in output:\n%s", label, out)
	return 0
}

func TestX86_64AllocScaling(t *testing.T) {
	for _, tc := range allocScaleCases {
		t.Run(tc.name, func(t *testing.T) {
			out1, exit1 := compileAndRunX86_64(t, tc.volumeSrc(tc.n))
			v1 := parseVolume(t, tc.name+"@n", out1, exit1)
			out2, exit2 := compileAndRunX86_64(t, tc.volumeSrc(2*tc.n))
			v2 := parseVolume(t, tc.name+"@2n", out2, exit2)

			if v1 <= 0 {
				t.Fatalf("churn(%d) allocated %d bytes — a shape that allocates "+
					"nothing cannot show a scaling regression, so this case is not "+
					"measuring anything", tc.n, v1)
			}

			ratio := int(v2 * 100 / v1)

			// Log on every case, pass or fail. The ratio drifting toward the
			// bound is the early warning, and it is invisible if only
			// failures print.
			t.Logf("n=%d: %d B   2n=%d: %d B   ratio %d.%02dx (bound %d.%02dx)",
				tc.n, v1, 2*tc.n, v2, ratio/100, ratio%100, tc.maxRatio/100, tc.maxRatio%100)

			if tc.wantQuadratic {
				if ratio <= tc.maxRatio {
					t.Errorf("%s is the CALIBRATION case: it is inherently quadratic and "+
						"must measure ABOVE %d.%02dx, but came in at %d.%02dx (%d B -> %d B). "+
						"Either this shape stopped being quadratic — which would be a real "+
						"improvement worth recording by removing wantQuadratic — or the gate "+
						"has stopped discriminating and every other case in this corpus is "+
						"now passing vacuously",
						tc.name, tc.maxRatio/100, tc.maxRatio%100, ratio/100, ratio%100, v1, v2)
				}
				return
			}

			if ratio > tc.maxRatio {
				t.Errorf("%s allocation grew %d.%02dx when n doubled (%d B at n=%d -> %d B "+
					"at n=%d), over the %d.%02dx bound. Doubling the input should at most "+
					"double the memory; this is superlinear, which is the O(n) -> O(n^2) "+
					"class of regression that leaves every correctness test green while "+
					"making the shape unusable at real sizes",
					tc.name, ratio/100, ratio%100, v1, tc.n, v2, 2*tc.n,
					tc.maxRatio/100, tc.maxRatio%100)
			}
		})
	}
}
