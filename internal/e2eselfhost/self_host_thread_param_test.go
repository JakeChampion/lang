package e2eselfhost

import "testing"

// A pointer-element array rebound through a threader (#10420): `acc = walk(n,
// acc)` where every return of walk is its parameter, grown only by fresh
// elements. The credit that walks a struct array's or a string[]'s elements at
// exit admitted no such rebind, so the elements leaked on the AST lowering.
// Answers are the interpreter's.

const threadInst = "struct Inst { name: string, depth: i32 }\n"

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
`, true},
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
`, true},
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
`, true},
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
`, true},
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
`, true},
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
`, true},
	// Appended elements whose string fields come from an existing element and
	// from a caller's string: each must be counted, not shared.
	{"shared_fields", threadInst + `function walk(n: i32, tag: string, acc: Inst[]): Inst[] {
    if (n % 4 == 0 && acc.len() > 0) {
        var e: Inst = acc[0];
        return acc.append(Inst { name: e.name, depth: n }).append(Inst { name: acc[acc.len() - 1].name, depth: 1 }).append(Inst { name: tag, depth: 2 });
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
`, true},
	// The callee keeps the array in a struct, so it threads nothing and the
	// caller keeps the shallow release.
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
`, false},
	// The caller keeps the superseded array, so the credit is refused.
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
`, false},
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
