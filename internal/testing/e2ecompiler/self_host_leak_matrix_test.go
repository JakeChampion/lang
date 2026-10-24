// CI-DARK: FERN_LEAK_MATRIX_DUMP — This flag regenerates the reference data.
package e2ecompiler

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

type leakKind struct {
	name      string
	decls     string
	init      string
	init2     string
	read      string
	match     string
	ptype     string
	fixedInit string
}

var leakKinds = []leakKind{
	{
		name: "arr_i32",
		init: "let x: i32[] = [i, i + 1];", init2: "x = [i + 2, i + 3, i + 4];",
		read: "x.len()", ptype: "i32[]", fixedInit: "let keep: i32[] = [7, 8, 9];",
	},
	{
		name:  "str",
		decls: "function mkstr(a: string): string { return a + \"!\"; }",
		init:  "let x: string = mkstr(\"x\");", init2: "x = mkstr(\"yz\");",
		read: "x.len()", ptype: "string", fixedInit: "let keep: string = mkstr(\"kk\");",
	},
	{
		name:  "str_arr",
		decls: "function mkstr(a: string): string { return a + \"!\"; }",
		init:  "let x: string[] = [mkstr(\"x\")];", init2: "x = [mkstr(\"y\"), mkstr(\"z\")];",
		read: "x.len()", ptype: "string[]", fixedInit: "let keep: string[] = [mkstr(\"k\")];",
	},
	{
		name:  "struct_arr_field",
		decls: "struct P { xs: i32[], k: i32 }",
		init:  "let x: P = P { xs: [i, i + 1], k: i };", init2: "x = P { xs: [i + 2], k: i + 1 };",
		read: "x.xs.len()", ptype: "P", fixedInit: "let keep: P = P { xs: [7, 8], k: 3 };",
	},
	{
		name:  "enum_rc_payload",
		decls: "enum E { Full(i32[]), None }",
		init:  "let x: E = E.Full([i, i + 1]);", init2: "x = E.Full([i + 2, i + 3, i + 4]);",
		match: "match (x) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }",
	},
	{
		name:  "enum_str_payload",
		decls: "enum G { Full(string), None }\nfunction mkstr(a: string): string { return a + \"!\"; }",
		init:  "let x: G = G.Full(mkstr(\"x\"));", init2: "x = G.Full(mkstr(\"yz\"));",
		match: "match (x) { G.Full(s) => { t = t + s.len(); }, G.None => {} }",
	},
	{
		name:  "enum_scalar",
		decls: "enum S { V(i32), W }",
		init:  "let x: S = S.V(i);", init2: "x = S.V(i + 1);",
		match: "match (x) { S.V(k) => { t = t + k; }, S.W => {} }",
	},
	{
		name: "tuple_mixed",
		init: "let x: (i32, i32[]) = (i, [i + 1, i + 2]);", init2: "x = (i + 1, [i + 3]);",
		read: "x.0 + x.1.len()", ptype: "(i32, i32[])", fixedInit: "let keep: (i32, i32[]) = (5, [6, 7]);",
	},
	{
		name: "tuple_nested",
		init: "let x: (i32, i32[][]) = (i, [[i + 1, i + 2], [i]]);", init2: "x = (i + 1, [[i + 3]]);",
		read: "x.0 + x.1.len() + x.1[0].len()", ptype: "(i32, i32[][])", fixedInit: "let keep: (i32, i32[][]) = (5, [[6, 7]]);",
	},
	{
		name: "opt_arr",
		init: "let x: Option[i32[]] = Some([i, i + 1]);", init2: "x = Some([i + 2, i + 3, i + 4]);",
		match: "match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }",
	},
}

type leakScope struct {
	name    string
	wrap    func(body string) string
	rebinds bool
}

var leakScopes = []leakScope{
	{
		name: "fnscope",
		wrap: func(b string) string { return b + "\n    t = t + 1;" },
	},
	{
		name: "if_block",
		wrap: func(b string) string {
			return "if (i % 2 == 0) {\n    " + strings.ReplaceAll(b, "\n", "\n    ") + "\n    t = t + 1;\n    }"
		},
	},
	{
		name: "loop_local",
		wrap: func(b string) string {
			return "let j: i32 = 0;\n    while (j < 2) {\n    " + strings.ReplaceAll(b, "\n", "\n    ") + "\n    j = j + 1;\n    }\n    t = t + 1;"
		},
	},
	{
		name:    "rebind",
		rebinds: true,
		wrap:    func(b string) string { return b + "\n    t = t + 1;" },
	},
	{
		name: "shadow_siblings",
		wrap: func(b string) string {
			ind := strings.ReplaceAll(b, "\n", "\n    ")
			return "if (i % 2 == 0) {\n    " + ind + "\n    t = t + 1;\n    }\n" +
				"    if (i % 2 == 1) {\n    " + ind + "\n    t = t + 2;\n    }"
		},
	},
}

type leakCell struct {
	name string
	src  string
}

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
				src += "function round(i: i32): i32 {\n    let t: i32 = 0;\n    " +
					strings.ReplaceAll(body, "\n", "\n    ") +
					"\n    return t;\n}\n" +
					"function main(): i32 {\n" +
					"    let acc: i32 = 0;\n    let i: i32 = 0;\n" +
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
	for _, k := range leakKinds {
		if k.ptype == "" || k.read == "" {
			continue
		}
		srcInit := strings.Replace(k.init, "let x:", "let src:", 1)
		readX := "t = (t + " + k.read + ") % 101;"
		readSrc := "t = (t + " + strings.ReplaceAll(k.read, "x.", "src.") + ") % 101;"
		readKeep := strings.ReplaceAll(k.read, "x.", "keep.")
		decls := ""
		if k.decls != "" {
			decls = k.decls + "\n"
		}
		mainTail := "function main(): i32 {\n" +
			"    let acc: i32 = 0;\n    let i: i32 = 0;\n" +
			"    while (i < 100) { acc = acc + round(i); i = i + 1; }\n" +
			"    if (__rc_underflow_count() != 0) { return 99; }\n" +
			"    return acc % 83;\n}\n"
		for _, sc := range []struct{ name, body string }{
			{"fnscope", "    let x: " + k.ptype + " = src;\n    " + readX},
			{"if_block", "    if (i % 2 == 0) {\n        let x: " + k.ptype + " = src;\n        " + readX + "\n        t = t + 1;\n    }"},
		} {
			src := decls +
				"function round(i: i32): i32 {\n" +
				"    " + srcInit + "\n" +
				"    let t: i32 = 0;\n" +
				sc.body + "\n" +
				"    " + readSrc + "\n" +
				"    return t;\n}\n" + mainTail
			cells = append(cells, leakCell{name: k.name + "__" + sc.name + "__alias_local", src: src})
		}
		for _, sc := range []struct{ name, body string }{
			{"fnscope", "    let x: " + k.ptype + " = src;\n    " + readX},
			{"if_block", "    if (i % 2 == 0) {\n        let x: " + k.ptype + " = src;\n        " + readX + "\n        t = t + 1;\n    }"},
		} {
			src := decls +
				"function round(src: " + k.ptype + ", i: i32): i32 {\n" +
				"    let t: i32 = 0;\n" +
				sc.body + "\n" +
				"    return t;\n}\n" +
				"function main(): i32 {\n" +
				"    " + k.fixedInit + "\n" +
				"    let acc: i32 = 0;\n    let i: i32 = 0;\n" +
				"    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }\n" +
				"    acc = (acc + " + readKeep + ") % 83;\n" +
				"    if (__rc_underflow_count() != 0) { return 99; }\n" +
				"    return acc % 83;\n}\n"
			cells = append(cells, leakCell{name: k.name + "__" + sc.name + "__alias_param", src: src})
		}
	}
	cells = append(cells,
		leakCell{name: "for_in_str_elem__loop__read", src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let names: string[] = [mkstr("a"), mkstr("b")];
    let t: i32 = 0;
    for s in names { t = (t + s.len()) % 101; }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "for_in_struct_elem__loop__read", src: `struct S { name: string, fields: string[] }
function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let xs: S[] = [S{ name: mkstr("a"), fields: [mkstr("f")] }, S{ name: mkstr("bb"), fields: [] }];
    let t: i32 = 0;
    for sd in xs { t = (t + sd.name.len() + sd.fields.len()) % 101; }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "for_in_struct_elem__loop__return_scalar", src: `struct S { name: string, fields: string[] }
function mkstr(a: string): string { return a + "!"; }
function count(xs: S[], k: i32): i32 {
    for sd in xs { if (sd.name.len() == k) { return sd.fields.len(); } }
    return 0 - 1;
}
function round(i: i32): i32 {
    let xs: S[] = [S{ name: mkstr("a"), fields: [mkstr("f")] }, S{ name: mkstr("bb"), fields: [] }];
    return (count(xs, 2) + count(xs, 3) + 2) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "field_read_arr__fnscope__read", src: `struct P { xs: i32[], k: i32 }
function round(i: i32): i32 {
    let src: P = P { xs: [i, i + 1], k: i };
    let x: i32[] = src.xs;
    let t: i32 = (x.len() + src.k) % 101;
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "field_read_arr__fnscope__alias_param", src: `struct P { xs: i32[], k: i32 }
function round(src: P, i: i32): i32 {
    let x: i32[] = src.xs;
    return (x.len() + i) % 101;
}
function main(): i32 {
    let keep: P = P { xs: [7, 8], k: 3 };
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.xs.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "enum_rc_payload__fnscope__alias_match", src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    let src: E = E.Full([i, i + 1]);
    let x: E = src;
    let t: i32 = 0;
    match (x) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "opt_arr__fnscope__alias_match", src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let t: i32 = 0;
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "str__loop_local__alias_local", src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s: string = "hi" + "!";
        let v: string = s;
        t = (t + v.len() + s.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "struct_arr_field__fnscope__alias_reuse", src: `struct P { xs: i32[], k: i32 }
function round(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], k: i };
    let v: P = p;
    let t: i32 = v.k + p.xs.len();
    let q: P = P { xs: [i, i + 2], k: t };
    return q.k + p.k;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "struct_arr_field__loop_local__alias_local", src: `struct P { xs: i32[], k: i32 }
function round(i: i32): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let p: P = P { xs: [i, j], k: j };
        let v: P = p;
        t = (t + v.k + p.xs.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "tuple_mixed__callarg__read", src: `function peek(t: (i32, i32[])): i32 { return t.0 + t.1.len(); }
function main(): i32 {
    let keep: (i32, i32[]) = (5, [6, 7]);
    let acc: i32 = peek(keep) + keep.0;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_nested__callarg__read", src: `function peek(t: (i32, i32[][])): i32 { return t.0 + t.1.len() + t.1[0][1]; }
function main(): i32 {
    let keep: (i32, i32[][]) = (5, [[6, 7], [8]]);
    let acc: i32 = peek(keep) + keep.0;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_mixed__structfield__local_store", src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    let k: (i32, i32[]) = (i, [i, i + 1]);
    let h: Hold = Hold { t: k, n: i };
    return h.n + k.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_mixed__callarg__stored_struct", src: `struct Hold { t: (i32, i32[]), n: i32 }
function keepit(t: (i32, i32[])): Hold { return Hold { t: t, n: 1 }; }
function main(): i32 {
    let keep: (i32, i32[]) = (5, [6, 7]);
    let h: Hold = keepit(keep);
    let acc: i32 = h.n + keep.0;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_mixed__elemret__payload_refused", src: `function get(src: (i32, i32[])): i32[] { return src.1; }
function make(): i32[] {
    let keep: (i32, i32[]) = (5, [6, 7]);
    return get(keep);
}
function main(): i32 {
    let r: i32[] = make();
    let acc: i32 = r.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_mixed__elemret__box_tier_only", src: `function maketup(): (i32, i32[]) { return (5, [6, 7]); }
function get(src: (i32, i32[])): i32[] { return src.1; }
function make(): i32[] {
    let t: (i32, i32[]) = maketup();
    return get(t);
}
function main(): i32 {
    let r: i32[] = make();
    let acc: i32 = r.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_mixed__fnscope__borrowed_arg", src: `function round(src: (i32, i32[]), i: i32): i32 {
    let t: i32 = 0;
    t = (t + src.0 + src.1.len()) % 101;
    return t;
}
function main(): i32 {
    let keep: (i32, i32[]) = (5, [6, 7]);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "tuple_mixed__callprod__alias_local", src: `function mk(i: i32): (i32, i32[]) { return (i, [i, i + 1]); }
function round(i: i32): i32 {
    let t: (i32, i32[]) = mk(i);
    let v: (i32, i32[]) = t;
    return v.0 + t.1.len();
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "tuple_mixed__ownedret_alias__bind_local", src: `function mk(i: i32): (i32, i32[]) {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let a: (i32, i32[]) = t;
    if (a.0 < 0) { return (0, [0]); }
    return t;
}
function round(i: i32): i32 {
    let r: (i32, i32[]) = mk(i);
    return r.0 + r.1.len();
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "tuple_mixed__loop_local__alias_local", src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let p: (i32, i32[]) = (j, [i, j]);
        let v: (i32, i32[]) = p;
        t = (t + v.0 + p.1.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "arr_i32__loop_local__alias_local", src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s: i32[] = [i, i + 1];
        let v: i32[] = s;
        t = (t + v.len() + s.len()) % 101;
        j = j + 1;
    }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`},
		leakCell{name: "enum_scalar__callarg__read", src: `enum E { A(i32), B(i32) }
function get(e: E, i: i32): i32 {
    match (e) { A(x) => { return x + i; }, B(y) => { return y; } }
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let e: E = A(i);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let e: E = A(i);
        let h: H = wrap(e, i);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let e: E = A(i);
        let xs: E[] = box(e);
        match (xs[0]) { A(x) => { acc = acc + x; }, B(y) => { acc = acc + y; } }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}
`},
		leakCell{name: "opt_arr__callarg__read", src: `function peek(o: Option[i32[]], i: i32): i32 {
    match (o) { Some(xs) => { return xs.len() + i; }, None => { return 0; } }
}
function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    return peek(o, i) % 101;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		leakCell{name: "opt_str__callarg__read", src: `function mk(a: string): string { return a + "!"; }
function peek(o: Option[string]): i32 {
    match (o) { Some(s) => { return s.len(); }, None => { return 0; } }
}
function round(i: i32): i32 {
    let o: Option[string] = Some(mk("abc"));
    return peek(o) + i % 3;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		leakCell{name: "opt_aarr__callarg__read", src: `function count(xs: Option[i32[]][]) : i32 { return xs.len(); }
function round(i: i32): i32 {
    let xs: Option[i32[]][] = [Some([i, i + 1]), None];
    return count(xs) + i % 3;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		leakCell{name: "map_struct_strfield__insert__match", src: mapStructColumnInsertMatchSrc},
		leakCell{name: "map_struct_strfield__literal__len", src: mapStructColumnLiteralLenSrc},
		leakCell{name: "map_struct_arrfield__insert__len", src: mapStructColumnArrFieldInsertSrc},
		leakCell{name: "arrtup_str__literal__len", src: arrTupStrLiteralLenSrc},
		leakCell{name: "arrtup_str__callarg__erased_generic", src: arrTupStrErasedGenericSrc},
		leakCell{name: "arrtup_arr__callarg__erased_generic", src: arrTupArrErasedGenericSrc},
		leakCell{name: "arrtup_mixed__callarg__erased_generic", src: arrTupMixedErasedGenericSrc},
		leakCell{name: "map_strarr__insert__get_or", src: mapStrArrColumnInsertGetOrSrc},
		leakCell{name: "map_strarr__literal__len", src: mapStrArrColumnLiteralLenSrc},
		leakCell{name: "res_strarr__callscrut__match", src: callScrutResultStrArrSrc},
		leakCell{name: "optopt_strarr__callscrut__match", src: callScrutOptOptStrArrSrc},
		leakCell{name: "opt_str__callscrut__match", src: callScrutOptStrSrc},
		leakCell{name: "opt_strarr__callbound__match", src: callBoundOptStrArrSrc},
		leakCell{name: "rcenum_mixed__callscrut__match", src: callScrutUserEnumSrc})
	return cells
}

type leakVerdict string

const (
	verdictClean leakVerdict = "clean"
	verdictLeak  leakVerdict = "leak"
	verdictError leakVerdict = "error"
	verdictCrash leakVerdict = "crash"
)

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

func selfHostLeakVerdict(t *testing.T, gcc string, runner []string, driverBin, dir, name, src string) (leakVerdict, int) {
	t.Helper()
	asm, stderr, err := loadCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	if err != nil {
		t.Logf("%s: self-host compile refused: %v\n%s", name, err, stderr)
		return verdictError, -1
	}
	bin := buildBin(t, gcc, dir, "leakmx_"+name, asm)
	stderr, exit := hevRun(t, runner, bin)
	if exit == -1 || exit == 139 || exit == 134 || exit == 137 {
		return verdictCrash, exit
	}
	return verdictFromLeakcheck(t, name+" (self-host)", stderr), exit
}

func selfHostSanitizeCell(t *testing.T, gcc string, runner []string, driverBin, dir, name, src string) (int, string) {
	t.Helper()
	asm, stderr, err := loadCompile(t, runner, driverBin, src, []string{"FERN_SANITIZE=1"})
	if err != nil {
		t.Fatalf("%s: sanitize compile refused what the census leg compiled: %v\n%s", name, err, stderr)
	}
	bin := buildBin(t, gcc, dir, "leakmxsan_"+name, asm)
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

func TestSelfHostLeakMatrixX86_64(t *testing.T) {
	dump := envflag.On("FERN_LEAK_MATRIX_DUMP")
	var known map[string][2]leakVerdict
	if !dump {
		known = loadLeakMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

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
