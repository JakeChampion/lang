package e2eselfhost

import "testing"

// A pointer-element array rebound through a threader (#10420): `acc = walk(n,
// acc)` where every return of walk is its parameter, grown only by fresh
// elements. The credit that walks a struct array's or a string[]'s elements at
// exit admitted no such rebind, so the elements leaked on the AST lowering.
// Answers are the interpreter's.

const threadInst = "struct Inst { name: string, depth: i32 }\n"

const threadHolder = threadInst + "struct Holder { x: Inst }\n"

const threadInstMain = `function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.depth + p.name.len(); }
    return (t + pending.len()) % 100;
}
`

const threadStrMain = `function main(): i32 {
    var pending: string[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.len(); }
    return (t + pending.len()) % 100;
}
`

var threadParamRows = []leakRow{
	{"struct_arr", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (n % 4 == 0) { return acc.append(Inst { name: "w" + "", depth: n }); }
    return acc;
}
function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() % 256;
}
`, true, [2]int64{}},
	{"string_arr", `import "std/i32";
function walk(n: i32, acc: string[]): string[] {
    if (n % 4 == 0) { return acc.append(n.to_string()); }
    return acc;
}
function main(): i32 {
    var pending: string[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() % 256;
}
`, true, [2]int64{}},
	// Enough appends to outgrow the buffer, read after the loop.
	{"grow", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (n % 3 == 0) { return acc; }
    return acc.append(Inst { name: "w" + "", depth: n });
}
function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    while (fd < 60) { pending = walk(fd, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.depth + p.name.len(); }
    return t % 100;
}
`, true, [2]int64{}},
	// A local alias rebuilt through threaders, a recursive threader, and a
	// literal seed.
	{"alias_recursive", threadInst + `function rec(n: i32, acc: Inst[]): Inst[] {
    if (n == 0) { return acc; }
    var next: Inst[] = acc.append(Inst { name: "r" + "", depth: n });
    return rec(n - 1, next);
}
function step(n: i32, acc: Inst[]): Inst[] {
    if (n % 2 == 0) { return rec(2, acc); }
    return acc;
}
function walk(stmts: i32[], acc: Inst[]): Inst[] {
    var out: Inst[] = acc;
    for st in stmts { out = step(st, out); }
    return out;
}
function main(): i32 {
    var pending: Inst[] = [Inst { name: "seed" + "", depth: 1 }];
    var fd: i32 = 0;
    while (fd < 10) { pending = walk([fd, fd + 1], pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.depth; }
    return (t + pending.len()) % 100;
}
`, true, [2]int64{}},
	{"string_arr_alias", `import "std/i32";
function step(n: i32, acc: string[]): string[] {
    if (n % 3 == 0) { return acc; }
    return acc.append(n.to_string());
}
function walk(n: i32, acc: string[]): string[] {
    var out: string[] = acc;
    var i: i32 = 0;
    while (i < 3) { out = step(n + i, out); i = i + 1; }
    return out;
}
function main(): i32 {
    var pending: string[] = ["s" + ""];
    var fd: i32 = 0;
    while (fd < 20) { pending = walk(fd, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.len(); }
    return (t + pending.len()) % 100;
}
`, true, [2]int64{}},
	// The element walk that also drops each element's array field.
	{"array_field_elem", `struct Node { name: string, kids: i32[] }
function walk(n: i32, acc: Node[]): Node[] {
    if (n % 2 == 0) { return acc.append(Node { name: "n" + "", kids: [n, n + 1] }); }
    return acc;
}
function main(): i32 {
    var pending: Node[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len();
}
`, true, [2]int64{}},
	// Appended elements whose string field comes from a caller's string and
	// whose scalar comes from an existing element: the string must be counted,
	// not shared.
	{"shared_fields", threadInst + `function walk(n: i32, tag: string, acc: Inst[]): Inst[] {
    if (n % 4 == 0 && acc.len() > 0) {
        return acc.append(Inst { name: tag, depth: acc[0].depth + acc[acc.len() - 1].depth + n });
    }
    if (n % 4 == 0) { return acc.append(Inst { name: "w" + "", depth: n }); }
    return acc;
}
function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    var tag: string = "t" + "";
    while (fd < 12) { pending = walk(fd, tag, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.name.len() + p.depth; }
    return (t + tag.len()) % 100;
}
`, true, [2]int64{}},
	// A parameter named like a strict-fresh producer: the call reaches the
	// caller's closure, so walk threads nothing.
	{"value_block_read", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (acc.len() > 0) { var d: i32 = { acc[0].depth }; return acc.append(Inst { name: "w" + "", depth: d + n }); }
    return acc.append(Inst { name: "w" + "", depth: n });
}
` + threadInstMain, true, [2]int64{}},
	{"producer_shadowed", threadInst + `function mk(n: i32): Inst { return Inst { name: "m" + "", depth: n }; }
function walk(n: i32, mk: (i32) => Inst, acc: Inst[]): Inst[] {
    return acc.append(mk(n));
}
function main(): i32 {
    var keep: Inst = Inst { name: "k" + "", depth: 3 };
    var f: (i32) => Inst = (d: i32) => keep;
    var pending: Inst[] = [];
    var fd: i32 = 0;
    while (fd < 4) { pending = walk(fd, f, pending); fd = fd + 1; }
    var t: i32 = keep.depth + keep.name.len();
    for p in pending { t = t + p.depth + p.name.len(); }
    return (t + pending.len() + mk(1).depth) % 256;
}
`, true, [2]int64{}},
	// The refused rows below hold the AST census to the shallow fallback: each
	// may leak, and must never free an element early.
	//
	// The callee keeps the array in a struct, so it threads nothing.
	{"callee_keeps", threadInst + `struct Holder { xs: Inst[] }
function walk(n: i32, acc: Inst[], h: Holder): Holder {
    if (n % 4 == 0) { return Holder { xs: acc.append(Inst { name: "w" + "", depth: n }) }; }
    return Holder { xs: acc };
}
function thread(n: i32, acc: Inst[]): Inst[] {
    var kept: Holder = walk(n, acc, Holder { xs: [] });
    return kept.xs;
}
function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = thread(fd, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in pending { t = t + p.depth; }
    return (t + pending.len()) % 256;
}
`, false, [2]int64{42, 0}},
	// The caller keeps the superseded array.
	{"caller_keeps", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (n % 4 == 0) { return acc.append(Inst { name: "w" + "", depth: n }); }
    return acc;
}
function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    var old: Inst[] = [];
    while (fd < 12) { old = pending; pending = walk(fd, pending); fd = fd + 1; }
    var t: i32 = 0;
    for p in old { t = t + p.depth; }
    for p in pending { t = t + p.depth; }
    return (t + pending.len() + old.len()) % 256;
}
`, false, [2]int64{8, 5}},
	// The callee hands an element to a keeping sink: a struct field, a
	// holder built by another function, another array, a loop variable kept
	// past its iteration, or an element's string field.
	{"elem_struct_field", threadHolder + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (acc.len() > 0) { var h: Holder = Holder { x: acc[0] }; return acc.append(Inst { name: "w" + "", depth: h.x.depth + n }); }
    return acc.append(Inst { name: "w" + "", depth: n });
}
` + threadInstMain, false, [2]int64{26, 14}},
	{"elem_value_block", threadHolder + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (acc.len() > 0) { var h: Holder = { Holder { x: acc[0] } }; return acc.append(Inst { name: "w" + "", depth: h.x.depth + n }); }
    return acc.append(Inst { name: "w" + "", depth: n });
}
` + threadInstMain, false, [2]int64{36, 13}},
	{"elem_holder", threadHolder + `function hold(e: Inst): Holder { return Holder { x: e }; }
function walk(n: i32, acc: Inst[]): Inst[] {
    if (acc.len() > 0) { var h: Holder = hold(acc[0]); return acc.append(Inst { name: "w" + "", depth: h.x.depth + n }); }
    return acc.append(Inst { name: "w" + "", depth: n });
}
` + threadInstMain, false, [2]int64{26, 14}},
	{"elem_other_array", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    var ys: Inst[] = [];
    if (acc.len() > 0) { ys = ys.append(acc[0]); }
    return acc.append(Inst { name: "w" + "", depth: n + ys.len() });
}
` + threadInstMain, false, [2]int64{27, 15}},
	{"elem_loop_var", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    var ys: Inst[] = [];
    for p in acc { ys = ys.append(p); }
    return acc.append(Inst { name: "w" + "", depth: n + ys.len() });
}
` + threadInstMain, false, [2]int64{37, 25}},
	{"elem_field_payload", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (acc.len() > 0) { return acc.append(Inst { name: acc[0].name, depth: n }); }
    return acc.append(Inst { name: "w" + "", depth: n });
}
` + threadInstMain, false, [2]int64{25, 13}},
	// An existing element handed back a second time.
	{"elem_returned", threadInst + `function walk(n: i32, acc: Inst[]): Inst[] {
    if (acc.len() > 0) { return acc.append(acc[acc.len() - 1]); }
    return acc.append(Inst { name: "w" + "", depth: n });
}
` + threadInstMain, false, [2]int64{14, 13}},
	{"string_elem_struct_field", `import "std/i32";
struct Hs { s: string }
function walk(n: i32, acc: string[]): string[] {
    if (acc.len() > 0) { var h: Hs = Hs { s: acc[0] }; return acc.append(h.s + "x"); }
    return acc.append("w" + "");
}
` + threadStrMain, false, [2]int64{25, 14}},
	{"string_elem_other_array", `import "std/i32";
function walk(n: i32, acc: string[]): string[] {
    var ys: string[] = [];
    if (acc.len() > 0) { ys = ys.append(acc[0]); }
    return acc.append("w" + ys.len().to_string());
}
` + threadStrMain, false, [2]int64{51, 39}},
	{"string_elem_loop_var", `import "std/i32";
function walk(n: i32, acc: string[]): string[] {
    var ys: string[] = [];
    for p in acc { ys = ys.append(p); }
    return acc.append("w" + ys.len().to_string());
}
` + threadStrMain, false, [2]int64{61, 49}},
	// A loop variable rebinding the threaded name returns another array's row.
	{"loop_var_shadows", threadInst + `function walk(qs: Inst[][], acc: Inst[]): Inst[] {
    for acc in qs { if (acc.len() > 1) { return acc; } }
    return acc;
}
function main(): i32 {
    var rows: Inst[][] = [[Inst { name: "a" + "", depth: 1 }, Inst { name: "b" + "", depth: 2 }]];
    var pending: Inst[] = [];
    pending = walk(rows, pending);
    var t: i32 = 0;
    for p in pending { t = t + p.depth + p.name.len(); }
    for r in rows { t = t + r.len(); }
    return (t + pending.len()) % 256;
}
`, false, [2]int64{5, 3}},
}

func TestSelfHostThreadParamX86_64(t *testing.T) {
	runLeakRowsX86_64(t, threadParamRows)
}

// A threader the semantic lowering produced takes every threader row with it
// (ssarc.caller_rows), so an AST caller of one keeps the shallow release: it
// may leak, and must not over-release.
func TestSelfHostThreadParamMixedX86_64(t *testing.T) {
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range threadParamRows {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		for _, skip := range []string{"main", "walk"} {
			t.Run(tc.name+"/ast_"+skip, func(t *testing.T) {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", "FERN_SEM_IR=1", "FERN_SEM_IR_SKIP="+skip), nil)
				if exit != want || forArrStructSanitizerFault(stderr, false) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, want, stderr)
				}
			})
		}
	}
}

func TestSelfHostThreadParamArm64(t *testing.T) {
	runLeakRowsArm64(t, threadParamRows)
}

func TestSelfHostThreadParamWasm(t *testing.T) {
	runLeakRowsWasm(t, threadParamRows)
}
