package e2eselfhost

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- The leak matrix ---------------------------------------------------------
//
// Goal 2's RECLAIM gaps have been found one at a time, each as a side effect of
// working on the previous one — #7364 surfaced while building #7360's controls,
// #7281/#7282 alongside a tuple fix, and the #6127 sweep that found seven at
// once was a hand-run session recorded in an issue, not a gate. This test is
// that sweep made standing: it GENERATES one small program per cell of
//
//	value kind × scope × consumption
//
// compiles each under BOTH compilers with FERN_LEAKCHECK=1, runs both, and
// classifies each side CLEAN (live_bytes == 0) or LEAK. The verdict pair per
// cell is pinned in testdata/selfhost-leak-matrix.txt, so the file IS the gap
// list: goal-2 progress flips recorded leak cells to clean deliberately, a
// regression flips a clean cell to leak loudly, and — the #7357 point — the
// generator reaches classes (shadowed siblings, conditional blocks) that no
// hand-curated corpus stays honest about.
//
// WHAT IT DOES NOT ASSERT: byte counts. Layout, capacity schedules and the
// #7351 per-string alloc split legitimately move totals; zero-vs-nonzero live
// bytes is the layout-free classification (same reasoning as the alloc
// differential's cliff counter). Exit codes must MATCH between compilers on
// every cell — a mismatch is a miscompile, never a matrix update — and exit 99
// (the underflow guard) fails hard on either side: an over-release is a bug in
// any cell, listed or not.
//
// Native compiles through the fern CLI as a child process (FERN_LEAKCHECK is
// read at init by internal/ast, so the in-process harness path cannot
// instrument per-case), which is also the pipeline that CONST-FOLDS. Every
// generated construction therefore embeds the loop variable, so no payload is
// foldable and the two pipelines measure the same allocations — the trap the
// #7364 log entry records.
//
// x86-64 only, deliberately: the comparison is between compilers, not targets
// (the alloc differential states the same rule).
//
// THE SANITIZE LEG (#7409's second companion instrument): each cell whose
// census leg compiled is compiled AGAIN under FERN_SANITIZE — the quarantine
// plus the over-release trap — and must exit IDENTICALLY with no
// `fern-sanitizer:` finding. The census sees only the leak direction; of the
// three defect shapes (fault / latent / denial) the LATENT class — a stray
// dec into an unclaimed box, a retain on a non-box, a premature free whose
// block is then read — makes the census read the SAME or BETTER. Under the
// quarantine nothing is recycled and a touched freed block is fatal, so that
// class goes red at the cell instead of after its leak is fixed. An exit that
// merely CHANGES under quarantine is the same signal: the un-quarantined run
// was reading recycled bytes.

// A leakKind is one value shape: decls it needs, an initializer for `var x`,
// an optional second initializer (the rebind scope), a borrow-read expression
// over x contributing to the checksum, and whether a consuming match exists.
type leakKind struct {
	name  string
	decls string
	init  string // uses `i`
	init2 string // uses `i`; distinct shape guts, same type
	read  string // expression over x yielding i32; "" = kind has no bare read
	match string // full match statement consuming x into t; "" = no match form
	// Origin-axis metadata (#7253's probe-audit requirement): ptype is the
	// type spelling for a param of this kind, fixedInit a `var keep: T = …;`
	// main can build ONCE and keep live across every call — the two
	// conditions an origin probe must meet (the source outlives the callee
	// and is genuinely released elsewhere). A kind whose fixedInit cannot
	// allocate on a pipeline (str on native x86: const-fold + SSO) has a
	// vacuous column there — see that kind's caveat. Kinds without a bare
	// read leave these empty and get hand cells instead.
	ptype     string
	fixedInit string
}

var leakKinds = []leakKind{
	{
		name: "arr_i32",
		init: "var x: i32[] = [i, i + 1];", init2: "x = [i + 2, i + 3, i + 4];",
		read: "x.len()", ptype: "i32[]", fixedInit: "var keep: i32[] = [7, 8, 9];",
	},
	{
		// mkstr is the importless fresh producer both pipelines accept: string
		// METHODS are stdlib-gated in native (E043 without `import
		// "std/string"`) and the self-host driver resolves no stdlib, while a
		// user concat fn compiles in both. CAVEAT (measured 2026-08-24): on
		// native x86 a constant-seeded fixedInit string never allocates —
		// const-fold collapses the call (an ident seed does not help; the
		// fold propagates through const locals) and SSO keeps the short
		// result inline — so the NATIVE column of this kind's origin cells is
		// vacuous: it measures the absence of an allocation, not a sweep.
		// The str alias_param rows' notes carry the real native verdict.
		name:  "str",
		decls: "function mkstr(a: string): string { return a + \"!\"; }",
		init:  "var x: string = mkstr(\"x\");", init2: "x = mkstr(\"yz\");",
		read: "x.len()", ptype: "string", fixedInit: "var keep: string = mkstr(\"kk\");",
	},
	{
		name:  "str_arr",
		decls: "function mkstr(a: string): string { return a + \"!\"; }",
		init:  "var x: string[] = [mkstr(\"x\")];", init2: "x = [mkstr(\"y\"), mkstr(\"z\")];",
		read: "x.len()", ptype: "string[]", fixedInit: "var keep: string[] = [mkstr(\"k\")];",
	},
	{
		name:  "struct_arr_field",
		decls: "struct P { xs: i32[], k: i32 }",
		init:  "var x: P = P { xs: [i, i + 1], k: i };", init2: "x = P { xs: [i + 2], k: i + 1 };",
		read: "x.xs.len()", ptype: "P", fixedInit: "var keep: P = P { xs: [7, 8], k: 3 };",
	},
	{
		name:  "enum_rc_payload",
		decls: "enum E { Full(i32[]), None }",
		init:  "var x: E = E.Full([i, i + 1]);", init2: "x = E.Full([i + 2, i + 3, i + 4]);",
		match: "match (x) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }",
	},
	{
		name:  "enum_str_payload",
		decls: "enum G { Full(string), None }\nfunction mkstr(a: string): string { return a + \"!\"; }",
		init:  "var x: G = G.Full(mkstr(\"x\"));", init2: "x = G.Full(mkstr(\"yz\"));",
		match: "match (x) { G.Full(s) => { t = t + s.len(); }, G.None => {} }",
	},
	{
		name:  "enum_scalar",
		decls: "enum S { V(i32), W }",
		init:  "var x: S = S.V(i);", init2: "x = S.V(i + 1);",
		match: "match (x) { S.V(k) => { t = t + k; }, S.W => {} }",
	},
	{
		name: "tuple_mixed",
		init: "var x: (i32, i32[]) = (i, [i + 1, i + 2]);", init2: "x = (i + 1, [i + 3]);",
		read: "x.0 + x.1.len()", ptype: "(i32, i32[])", fixedInit: "var keep: (i32, i32[]) = (5, [6, 7]);",
	},
	{
		name: "opt_arr",
		init: "var x: Option[i32[]] = Some([i, i + 1]);", init2: "x = Some([i + 2, i + 3, i + 4]);",
		match: "match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }",
	},
}

// A leakScope wraps a cell's body (declaration + consumption) in a lexical
// position. `body` receives the statements and returns the round-function
// body; every scope keeps the round's answer identical for a given
// consumption, so exit codes compare across scopes too.
type leakScope struct {
	name    string
	wrap    func(body string) string
	rebinds bool // scope injects init2 as a rebind; needs kind.init2
}

var leakScopes = []leakScope{
	{
		name: "fnscope",
		wrap: func(b string) string { return b + "\n    t = t + 1;" },
	},
	{
		// The #7360 class: entered on half the calls, slot entry-zeroed on
		// the other half.
		name: "if_block",
		wrap: func(b string) string {
			return "if (i % 2 == 0) {\n    " + strings.ReplaceAll(b, "\n", "\n    ") + "\n    t = t + 1;\n    }"
		},
	},
	{
		name: "loop_local",
		wrap: func(b string) string {
			return "var j: i32 = 0;\n    while (j < 2) {\n    " + strings.ReplaceAll(b, "\n", "\n    ") + "\n    j = j + 1;\n    }\n    t = t + 1;"
		},
	},
	{
		name:    "rebind",
		rebinds: true,
		wrap:    func(b string) string { return b + "\n    t = t + 1;" },
	},
	{
		// The #7357 class: two same-named siblings in disjoint blocks — the
		// shape name-keyed credits collide on and no emit-hash corpus reaches.
		name: "shadow_siblings",
		wrap: func(b string) string {
			ind := strings.ReplaceAll(b, "\n", "\n    ")
			return "if (i % 2 == 0) {\n    " + ind + "\n    t = t + 1;\n    }\n" +
				"    if (i % 2 == 1) {\n    " + ind + "\n    t = t + 2;\n    }"
		},
	},
}

// leakCell is one generated program.
type leakCell struct {
	name string
	src  string
}

// leakMatrixCells generates the valid cells. Consumptions: `read` (a borrow
// read feeding the checksum), `unused` (declared, never referenced), `match`
// (a consuming match, kinds that have one). A rebind scope pairs only with
// kinds carrying init2 (all of them today, kept explicit anyway).
func leakMatrixCells() []leakCell {
	var cells []leakCell
	for _, k := range leakKinds {
		type consumption struct{ name, stmts string }
		var cons []consumption
		if k.read != "" {
			cons = append(cons, consumption{"read", k.init + "\nt = (t + " + k.read + ") % 101;"})
		}
		cons = append(cons, consumption{"unused", k.init})
		if k.match != "" {
			cons = append(cons, consumption{"match", k.init + "\n" + k.match})
		}
		for _, sc := range leakScopes {
			for _, c := range cons {
				stmts := c.stmts
				if sc.rebinds {
					if k.init2 == "" {
						continue
					}
					// Rebind between declaration and consumption, so the
					// superseded chain needs the rebind release and the final
					// value the exit one.
					parts := strings.SplitN(stmts, "\n", 2)
					stmts = parts[0] + "\n" + k.init2
					if len(parts) == 2 {
						stmts += "\n" + parts[1]
					}
				}
				body := sc.wrap(stmts)
				src := ""
				if k.decls != "" {
					src = k.decls + "\n"
				}
				src += "function round(i: i32): i32 {\n    var t: i32 = 0;\n    " +
					strings.ReplaceAll(body, "\n", "\n    ") +
					"\n    return t;\n}\n" +
					"function main(): i32 {\n" +
					"    var acc: i32 = 0;\n    var i: i32 = 0;\n" +
					"    while (i < 100) { acc = acc + round(i); i = i + 1; }\n" +
					"    if (__rc_underflow_count() != 0) { return 99; }\n" +
					"    return acc % 83;\n}\n"
				cells = append(cells, leakCell{
					name: k.name + "__" + sc.name + "__" + c.name,
					src:  src,
				})
			}
		}
	}
	// --- The binding-origin axis (v2, #7253's probe-audit requirement) ------
	//
	// Every v1 cell binds x from a fresh construction — a local-var origin.
	// The 2026-08-22 defects sat on the ORIGIN axis (a parameter alias
	// reaching the stdlib, a for-in binder no collector credits), so these
	// cells bind the same kinds from an aliased LOCAL (the source read again
	// after the alias) and from a PARAMETER whose value main builds once,
	// keeps live across every call, and reads after the loop — the two probe
	// conditions the #7253 thread establishes. The underflow guard and the
	// exit-match are what let an over-crediting collision go RED here rather
	// than measure as a smaller leak.
	for _, k := range leakKinds {
		if k.ptype == "" || k.read == "" {
			continue
		}
		srcInit := strings.Replace(k.init, "var x:", "var src:", 1)
		readX := "t = (t + " + k.read + ") % 101;"
		readSrc := "t = (t + " + strings.ReplaceAll(k.read, "x.", "src.") + ") % 101;"
		readKeep := strings.ReplaceAll(k.read, "x.", "keep.")
		decls := ""
		if k.decls != "" {
			decls = k.decls + "\n"
		}
		mainTail := "function main(): i32 {\n" +
			"    var acc: i32 = 0;\n    var i: i32 = 0;\n" +
			"    while (i < 100) { acc = acc + round(i); i = i + 1; }\n" +
			"    if (__rc_underflow_count() != 0) { return 99; }\n" +
			"    return acc % 83;\n}\n"
		for _, sc := range []struct{ name, body string }{
			{"fnscope", "    var x: " + k.ptype + " = src;\n    " + readX},
			{"if_block", "    if (i % 2 == 0) {\n        var x: " + k.ptype + " = src;\n        " + readX + "\n        t = t + 1;\n    }"},
		} {
			src := decls +
				"function round(i: i32): i32 {\n" +
				"    " + srcInit + "\n" +
				"    var t: i32 = 0;\n" +
				sc.body + "\n" +
				"    " + readSrc + "\n" +
				"    return t;\n}\n" + mainTail
			cells = append(cells, leakCell{name: k.name + "__" + sc.name + "__alias_local", src: src})
		}
		for _, sc := range []struct{ name, body string }{
			{"fnscope", "    var x: " + k.ptype + " = src;\n    " + readX},
			{"if_block", "    if (i % 2 == 0) {\n        var x: " + k.ptype + " = src;\n        " + readX + "\n        t = t + 1;\n    }"},
		} {
			src := decls +
				"function round(src: " + k.ptype + ", i: i32): i32 {\n" +
				"    var t: i32 = 0;\n" +
				sc.body + "\n" +
				"    return t;\n}\n" +
				"function main(): i32 {\n" +
				"    " + k.fixedInit + "\n" +
				"    var acc: i32 = 0;\n    var i: i32 = 0;\n" +
				"    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }\n" +
				"    acc = (acc + " + readKeep + ") % 83;\n" +
				"    if (__rc_underflow_count() != 0) { return 99; }\n" +
				"    return acc % 83;\n}\n"
			cells = append(cells, leakCell{name: k.name + "__" + sc.name + "__alias_param", src: src})
		}
	}
	// Hand cells for origins the kind table cannot express uniformly.
	cells = append(cells,
		// The for-in element binder — #7356's finding: not a StmtVar, so no
		// collector ever credited it; each iteration aliases a live element.
		leakCell{name: "for_in_str_elem__loop__read", src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var names: string[] = [mkstr("a"), mkstr("b")];
    var t: i32 = 0;
    for s in names { t = (t + s.len()) % 101; }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// A FIELD READ as the origin — the #7343 "stolen" shape: a second
		// reference to a buffer the owner's deep drop also releases.
		leakCell{name: "field_read_arr__fnscope__read", src: `struct P { xs: i32[], k: i32 }
function round(i: i32): i32 {
    var src: P = P { xs: [i, i + 1], k: i };
    var x: i32[] = src.xs;
    var t: i32 = (x.len() + src.k) % 101;
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// A field read from a PARAM the caller keeps — over-release direction.
		leakCell{name: "field_read_arr__fnscope__alias_param", src: `struct P { xs: i32[], k: i32 }
function round(src: P, i: i32): i32 {
    var x: i32[] = src.xs;
    return (x.len() + i) % 101;
}
function main(): i32 {
    var keep: P = P { xs: [7, 8], k: 3 };
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.xs.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		// An enum ALIAS consumed by a match while the source lives on — the
		// #7253 thread's collision shape, in its admissible form.
		leakCell{name: "enum_rc_payload__fnscope__alias_match", src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    var src: E = E.Full([i, i + 1]);
    var x: E = src;
    var t: i32 = 0;
    match (x) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// The Option sibling of the same alias-then-consume shape.
		leakCell{name: "opt_arr__fnscope__alias_match", src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var t: i32 = 0;
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// A LOOP-scoped dead-alias-cancelled pair (#4402 opt 1): both slots
		// rebind each iteration, so the alias bind must store WITHOUT the
		// dec-on-overwrite — the source's own rebind already freed the prior
		// box at rc 1, and a second cow-guarded free is a use-after-free the
		// census cannot see (the double free balances it); the sanitize leg
		// is the instrument that catches it.
		leakCell{name: "str__loop_local__alias_local", src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s: string = "hi" + "!";
        var v: string = s;
        t = (t + v.len() + s.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// A cancelled pair with a LATER same-type construction while the
		// alias is live: the reuse donor gate must refuse the source (its
		// bare-ident alias mention is an escape), else the in-place-reuse
		// arm frees the box the alias still reads — the sanitize leg is the
		// witness that the donor vetting holds under cancellation (rc==1).
		leakCell{name: "struct_arr_field__fnscope__alias_reuse", src: `struct P { xs: i32[], k: i32 }
function round(i: i32): i32 {
    var p: P = P { xs: [i, i + 1], k: i };
    var v: P = p;
    var t: i32 = v.k + p.xs.len();
    var q: P = P { xs: [i, i + 2], k: t };
    return q.k + p.k;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// The struct flavor of the same loop-scoped cancelled pair — the
		// alias's box-only "NODEEP:" release is what the cancellation
		// elides; the rebind store must not fire the box dec either.
		leakCell{name: "struct_arr_field__loop_local__alias_local", src: `struct P { xs: i32[], k: i32 }
function round(i: i32): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var p: P = P { xs: [i, j], k: j };
        var v: P = p;
        t = (t + v.k + p.xs.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// THE PAYLOAD-TIER REFUSAL, literal-init flavor: get's param is
		// box-borrowable (a field read walks as a borrow in the shared gate)
		// but its "TUPB:" payload flag is 0 — it hands src.1 out — so the
		// caller's TUPRCS deep reclaim is refused and keep leaks (the safe
		// direction; granting it was a sanitizer-confirmed UAF, freeing the
		// element under the callee's returned reference). Native is clean via
		// its dup-at-extract convention — a recorded floor, not a hazard.
		leakCell{name: "tuple_mixed__elemret__payload_refused", src: `function get(src: (i32, i32[])): i32[] { return src.1; }
function make(): i32[] {
    var keep: (i32, i32[]) = (5, [6, 7]);
    return get(keep);
}
function main(): i32 {
    var r: i32[] = make();
    var acc: i32 = r.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		// The same element-returning callee with a CALL-producer tuple: the
		// payload tier refuses the ELEMENT kinds while the box tier still
		// grants the shallow "TUP:" box free — box freed once by the caller,
		// element ownership rides out to main's is_arr sweep. Clean on both
		// sides, and the layering (box flag licenses box-only, TUPB licenses
		// deep) is exactly what this cell pins.
		leakCell{name: "tuple_mixed__elemret__box_tier_only", src: `function maketup(): (i32, i32[]) { return (5, [6, 7]); }
function get(src: (i32, i32[])): i32[] { return src.1; }
function make(): i32[] {
    var t: (i32, i32[]) = maketup();
    return get(t);
}
function main(): i32 {
    var r: i32[] = make();
    var acc: i32 = r.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		// An rc-tuple passed to a BORROWING callee (reads src directly, so
		// its param is payload-borrowable): the rc-tuple escape scan's call
		// arm applies the "TUPB:" payload-tier borrow rule, so keep's TUPRCS
		// deep reclaim survives the call — the alias-param audit's "tuple
		// control leaks identically" finding, closed. The rcplan tables agree
		// either way, so this cell is the instrument.
		leakCell{name: "tuple_mixed__fnscope__borrowed_arg", src: `function round(src: (i32, i32[]), i: i32): i32 {
    var t: i32 = 0;
    t = (t + src.0 + src.1.len()) % 101;
    return t;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		// A CALL-producer tuple source: the owned-return admission credits
		// the bound result ("TUP:" + the ARRF-flagged element kinds behind
		// "TUPELEMOK:"), so box and element both release and the alias pair
		// cancels — clean on both sides. The rcplan tables agree here either
		// way (the credit table is not dumped), so this cell is the one
		// instrument pinning the release.
		leakCell{name: "tuple_mixed__callprod__alias_local", src: `function mk(i: i32): (i32, i32[]) { return (i, [i, i + 1]); }
function round(i: i32): i32 {
    var t: (i32, i32[]) = mk(i);
    var v: (i32, i32[]) = t;
    return v.0 + t.1.len();
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// An ALIASED producer local: the owned-return admission walks with
		// empty alias lists, so `var a = t` refuses the whole callee — no
		// registry entry, no caller credit, the leak floor. Pinned because
		// this is the admission boundary most tempting to widen next, and a
		// careless widening (forgiving aliases) flips it to over-release,
		// not just a leak.
		leakCell{name: "tuple_mixed__ownedret_alias__bind_local", src: `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var a: (i32, i32[]) = t;
    if (a.0 < 0) { return (0, [0]); }
    return t;
}
function round(i: i32): i32 {
    var r: (i32, i32[]) = mk(i);
    return r.0 + r.1.len();
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// The tuple flavor of the same loop-scoped cancelled pair — the
		// alias's shallow "TUP:" box dec is what the cancellation elides;
		// the rebind store must not fire it either.
		leakCell{name: "tuple_mixed__loop_local__alias_local", src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var p: (i32, i32[]) = (j, [i, j]);
        var v: (i32, i32[]) = p;
        t = (t + v.0 + p.1.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// The array flavor of the same loop-scoped cancelled pair — the
		// #7455 limb's instance of the identical rebind double free.
		leakCell{name: "arr_i32__loop_local__alias_local", src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s: i32[] = [i, i + 1];
        var v: i32[] = s;
        t = (t + v.len() + s.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		// The three SCENUMS plan-routing witnesses (promotion step 2): the
		// escape gate for all-scalar enums is the plan's free_eligible_of
		// verdict, which — like native's computeFreeEligible — does not taint
		// a plain call arg. A callee that KEEPS its arg retains it through
		// its own counted construction store, so the caller's sweep is
		// balanced; a botched routing shows here as exit 99 / a sanitizer
		// trap, never as a changed leak count.
		leakCell{name: "enum_scalar__callarg__read", src: `enum E { A(i32), B(i32) }
function get(e: E, i: i32): i32 {
    match (e) { A(x) => { return x + i; }, B(y) => { return y; } }
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var e: E = A(i);
        acc = acc + get(e, i);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "enum_scalar__callarg__stored_struct", src: `enum E { A(i32), B(i32) }
struct H { e: E, n: i32 }
function wrap(e: E, i: i32): H { return H { e: e, n: i, }; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var e: E = A(i);
        var h: H = wrap(e, i);
        match (h.e) { A(x) => { acc = acc + x; }, B(y) => { acc = acc + y; } }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "enum_scalar__callarg__stored_arr", src: `enum E { A(i32), B(i32) }
function box(e: E): E[] { return [e]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var e: E = A(i);
        var xs: E[] = box(e);
        match (xs[0]) { A(x) => { acc = acc + x; }, B(y) => { acc = acc + y; } }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`})
	return cells
}

// A leakVerdict classifies one compiler's behaviour on one cell.
type leakVerdict string

const (
	verdictClean leakVerdict = "clean" // exit ok, live_bytes == 0
	verdictLeak  leakVerdict = "leak"  // exit ok, live_bytes > 0
	verdictError leakVerdict = "error" // did not compile
	verdictCrash leakVerdict = "crash" // compiled, died on a signal
)

// loadLeakMatrix parses the pinned matrix. Line format:
//
//	<cell> <native-verdict> <selfhost-verdict> <note...>
//
// The note names the issue or reason for a leak cell; it is for the reader,
// not the comparison.
func loadLeakMatrix(t *testing.T) map[string][2]leakVerdict {
	t.Helper()
	path := filepath.Join("testdata", "selfhost-leak-matrix.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string][2]leakVerdict{}
	sc := bufio.NewScanner(f)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("%s:%d: want `<cell> <native> <selfhost> <note>`, got %q", path, ln, line)
		}
		out[fields[0]] = [2]leakVerdict{leakVerdict(fields[1]), leakVerdict(fields[2])}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

// nativeLeakVerdict compiles src with the fern CLI (a child process, so
// FERN_LEAKCHECK instruments the emitted program) and runs it.
func nativeLeakVerdict(t *testing.T, cli, dir, name, src string) (leakVerdict, int) {
	t.Helper()
	srcPath := filepath.Join(dir, name+".fern")
	binPath := filepath.Join(dir, name+".nat")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	compile := exec.Command(cli, "-target", "x86-64-linux", "-o", binPath, srcPath)
	compile.Env = childEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Logf("%s: native compile failed:\n%s", name, out)
		return verdictError, -1
	}
	cmd := exec.Command(binPath)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	exit := cmd.ProcessState.ExitCode()
	if exit == -1 || !cmd.ProcessState.Exited() {
		return verdictCrash, exit
	}
	return verdictFromLeakcheck(t, name+" (native)", errBuf.String()), exit
}

// selfHostLeakVerdict compiles src with the self-host x86-64 driver and runs
// the linked binary. Unlike runCaptureEnv this tolerates a driver refusal —
// a cell outside the compilable subset is an `error` verdict, not a t.Fatal,
// so the matrix records frontend gaps alongside reclaim ones.
func selfHostLeakVerdict(t *testing.T, gcc string, runner []string, driverBin, dir, name, src string) (leakVerdict, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = strings.NewReader(src)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Logf("%s: self-host compile refused: %v", name, err)
		return verdictError, -1
	}
	bin := buildBin(t, gcc, dir, "leakmx_"+name, string(asm))
	stderr, exit := hevRun(t, runner, bin)
	if exit == -1 || exit == 139 || exit == 134 || exit == 137 {
		return verdictCrash, exit
	}
	return verdictFromLeakcheck(t, name+" (self-host)", stderr), exit
}

// selfHostSanitizeCell compiles src through the same driver under
// FERN_SANITIZE and runs the result. The census leg already proved this cell
// compiles, so a refusal here is a flag-dependent frontend divergence and
// fails hard rather than downgrading to an `error` verdict.
func selfHostSanitizeCell(t *testing.T, gcc string, runner []string, driverBin, dir, name, src string) (int, string) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = strings.NewReader(src)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "FERN_SANITIZE=1"}
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("%s: sanitize compile refused what the census leg compiled: %v", name, err)
	}
	bin := buildBin(t, gcc, dir, "leakmxsan_"+name, string(asm))
	stderr, exit := hevRun(t, runner, bin)
	return exit, stderr
}

func verdictFromLeakcheck(t *testing.T, label, stderr string) leakVerdict {
	t.Helper()
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary on stderr", label)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", label, summary, err)
	}
	if live == 0 {
		return verdictClean
	}
	return verdictLeak
}

// TestSelfHostLeakMatrixX86_64 is the gate. FERN_LEAK_MATRIX_DUMP=1 prints
// every cell's measured line in matrix-file format instead of comparing, for
// (re)generating the testdata pin after a deliberate change.
func TestSelfHostLeakMatrixX86_64(t *testing.T) {
	// CI-DARK: FERN_LEAK_MATRIX_DUMP — a regeneration tool, not coverage: it
	// prints measured matrix-file lines INSTEAD of comparing, so a lane setting
	// it would disable this gate. The compare path below is the CI behaviour.
	dump := os.Getenv("FERN_LEAK_MATRIX_DUMP") == "1"
	var known map[string][2]leakVerdict
	if !dump {
		known = loadLeakMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cells := leakMatrixCells()
	seen := map[string]bool{}
	for _, cell := range cells {
		seen[cell.name] = true
		t.Run(cell.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdict(t, cli, dir, cell.name, cell.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, cell.name, cell.src)

			if dump {
				fmt.Printf("%-45s %-6s %-6s (exit native=%d selfhost=%d)\n",
					cell.name, natV, shV, natExit, shExit)
				return
			}

			// Hard failures first — never matrix updates.
			if natExit == 99 || shExit == 99 {
				t.Fatalf("underflow guard tripped (native=%d self-host=%d): an "+
					"over-release, which no matrix entry may pin", natExit, shExit)
			}
			if shV == verdictCrash {
				t.Errorf("self-host binary crashed (exit %d) — the #7360 class; "+
					"file it, do not pin it:\n%s", shExit, cell.src)
				return
			}
			if natV != verdictError && shV != verdictError && natExit != shExit {
				t.Errorf("exit codes disagree: native=%d self-host=%d — a wrong-code "+
					"divergence, not a leak-matrix update:\n%s", natExit, shExit, cell.src)
				return
			}

			rec, listed := known[cell.name]
			if !listed {
				t.Errorf("cell not in testdata/selfhost-leak-matrix.txt (measured "+
					"native=%s selfhost=%s). Rerun with FERN_LEAK_MATRIX_DUMP=1 and add "+
					"the line with a note naming the issue or reason", natV, shV)
				return
			}
			if rec[0] != natV || rec[1] != shV {
				t.Errorf("verdict moved: recorded native=%s selfhost=%s, measured "+
					"native=%s selfhost=%s. A leak→clean move is progress — update the "+
					"row (and its note) in the same change that caused it; clean→leak "+
					"is a regression", rec[0], rec[1], natV, shV)
			}

			// The sanitize leg: same cell under the quarantine + trap. The
			// exit must not move — 124 is a finding (the message below says
			// which), any other change means the census run was reading
			// recycled bytes — and no `fern-sanitizer:` line but the leak
			// verdict may appear.
			if shV == verdictError || shV == verdictCrash {
				return
			}
			sanExit, sanStderr := selfHostSanitizeCell(t, gcc, runner, driverBin, dir, cell.name, cell.src)
			for _, finding := range []string{
				"fern-sanitizer: rc over-release (double free)",
				"fern-sanitizer: use-after-free (touched a quarantined block)",
			} {
				if strings.Contains(sanStderr, finding) {
					t.Errorf("sanitize leg raised %q — a latent defect the census "+
						"could not see; fix it, never pin it", finding)
				}
			}
			if sanExit != shExit {
				t.Errorf("sanitize leg exited %d where the census leg exited %d — "+
					"with no recycling the answers may not move; the un-quarantined "+
					"run was reading freed memory (stderr: %q)", sanExit, shExit, sanStderr)
			}
		})
	}

	if !dump {
		for name := range known {
			if !seen[name] {
				t.Errorf("testdata pins %q but the generator emits no such cell — "+
					"rename or remove the row", name)
			}
		}
	}
}
