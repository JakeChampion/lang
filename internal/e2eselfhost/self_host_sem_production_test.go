package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostSemanticProduction drives the PRODUCTION consumer of the typed
// semantic pipeline: `fern -target … ` with FERN_SEM_IR=1, where
// semlower.lower_gated_sem produces every function semsource admits, lowers it
// through ssaunits + ssarc, and substitutes the result into the cache the
// backend emits from (docs/SELFHOST-SEMANTIC-SOURCE.md).
//
// The assertions are the two things the substitution has to keep true:
//
//   - the program answers the same as it does with the path off, on every
//     target — the AST lowering is the oracle here, and it is the same binary
//     producing both, so a divergence is the substitution's;
//   - every declaration produces. The report line is checked rather than
//     assumed, because a body that silently stops producing keeps the program
//     correct (the AST body stands) and would make the rest of this test pass
//     while testing nothing.
//
// TestSelfHostSemanticSourceRC covers the same pipeline under a bespoke driver
// with a leak check; this one covers the path the CLI actually takes.
func TestSelfHostSemanticProduction(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, prog := range semProductionPrograms {
		t.Run(prog.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(prog.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					base, _, baseLeak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, false, "")
					got, report, leak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, "")
					if got != base {
						t.Fatalf("FERN_SEM_IR changed the answer:\n with = %q\nwithout = %q\nreport: %s",
							got, base, report)
					}
					if prog.skip != "" {
						mixed, mixedReport, mixedLeak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, prog.skip)
						if mixed != base {
							t.Fatalf("FERN_SEM_IR with FERN_SEM_IR_SKIP=%s changed the answer:\n with = %q\nwithout = %q\nreport: %s",
								prog.skip, mixed, base, mixedReport)
						}
						if mixedLeak > baseLeak {
							t.Fatalf("FERN_SEM_IR with FERN_SEM_IR_SKIP=%s leaked %d bytes where the AST lowering leaks %d",
								prog.skip, mixedLeak, baseLeak)
						}
						if prog.refuses != "" && !strings.Contains(mixedReport, prog.refuses) {
							t.Fatalf("FERN_SEM_IR with FERN_SEM_IR_SKIP=%s did not report %q:\n%s",
								prog.skip, prog.refuses, mixedReport)
						}
					}
					// The sanitizer leg also reports what the run never
					// released. The AST lowering is the oracle for that too, so
					// the produced bodies may free more than it does and never
					// less.
					if leak > baseLeak {
						t.Fatalf("FERN_SEM_IR leaked %d bytes where the AST lowering leaks %d", leak, baseLeak)
					}
					// The tally is checked rather than assumed: a body that
					// silently stops producing keeps the program correct (the
					// AST body stands) and would leave the comparison above
					// between two AST-lowered runs. A floor rather than an
					// equality, so closing a refusal leaf does not red-light
					// the suite that measures it.
					if got := semProducedCount(t, report); got < prog.atLeast {
						t.Fatalf("produced %d declarations, want at least %d:\n%s",
							got, prog.atLeast, report)
					}
				})
			}
		})
	}
}

// semProducedCount reads the tally out of the FERN_SEM_IR_REPORT line.
func semProducedCount(t *testing.T, report string) int {
	t.Helper()
	const marker = "module: produced "
	at := strings.Index(report, marker)
	if at < 0 {
		t.Fatalf("no production tally in the report:\n%s", report)
	}
	rest := report[at+len(marker):]
	n, err := strconv.Atoi(rest[:strings.Index(rest, " ")])
	if err != nil {
		t.Fatalf("unreadable tally %q: %v", rest, err)
	}
	return n
}

// semCompileRun compiles src for target with the semantic path on or off, runs
// the result, and returns "<exit>|<stdout>", the compiler's stderr, and the
// bytes the sanitizer reports unreleased (0 on the legs that do not sanitize).
func semCompileRun(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, src, target string, sem bool, skip string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "prog")
	// The sanitizer leg is the one that sees a release the answer survives: a
	// box released while the program still reads it keeps answering correctly
	// on a quiet allocator, and turns into an abort under quarantine.
	sanitize := target == "x86-64-sanitize"
	if sanitize {
		target = "x86-64-linux"
	}
	args := []string{"-target", target, src, stdlibRoot, "-o", out}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", src, stdlibRoot, "-o", out}
	}
	cmd := exec.Command(fernBin, args...)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR_REPORT=1")
	if sanitize {
		cmd.Env = append(cmd.Env, "FERN_SANITIZE=1")
	}
	if sem {
		cmd.Env = append(cmd.Env, "FERN_SEM_IR=1")
		if skip != "" {
			cmd.Env = append(cmd.Env, "FERN_SEM_IR_SKIP="+skip)
		}
	} else {
		cmd.Env = append(cmd.Env, "FERN_SEM_IR=")
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile (sem=%v, %s): %v\n%s", sem, target, err, stderr.String())
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
	stdout, _ := run.Output()
	return fmt.Sprintf("%d|%s", run.ProcessState.ExitCode(), stdout), stderr.String(), sanitizerLeak(t, runErr.String())
}

// sanitizerLeak reads the byte count out of the sanitizer's leak line; a run
// that reports none released everything it allocated.
func sanitizerLeak(t *testing.T, out string) int {
	t.Helper()
	const marker = "fern-sanitizer: leak "
	at := strings.Index(out, marker)
	if at < 0 {
		return 0
	}
	rest := out[at+len(marker):]
	n, err := strconv.Atoi(rest[:strings.Index(rest, " ")])
	if err != nil {
		t.Fatalf("unreadable sanitizer leak line %q: %v", rest, err)
	}
	return n
}

// One program per kind of counted unit a produced body has to get right, and
// one — `capture-write` — the boundary deliberately refuses most of, so the
// AST fallback and the mixed module are covered too. `atLeast` is the measured
// tally at the time of writing.
var semProductionPrograms = []struct {
	name    string
	atLeast int
	// skip, when set, runs the program a third time with FERN_SEM_IR_SKIP
	// naming these declarations, so they keep the AST lowering while what
	// they call is produced: the mixed module in the direction the
	// registries rewritten by ssarc.caller_sigs and irlower.regrow_sigs
	// hold together.
	skip string
	// refuses, when set, is a line the skip leg's report must carry: a
	// produced body the mixed module has to turn off, and why.
	refuses string
	src     string
}{
	{name: "scalar-calls", atLeast: 3, src: `
function add(a: i32, b: i32): i32 { return a + b; }
function total(xs: i32[]): i32 {
    var t: i32 = 0;
    for x in xs { t = add(t, x); }
    return t;
}
function main(): i32 { return total([1, 2, 3, 4, 5]); }
`},
	{name: "owned-array-handback", atLeast: 3, src: `
function grown(own xs: i32[]): i32[] { return xs.append(9); }
function span(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    var ys: i32[] = grown([1, 2, 3]);
    return span(ys);
}
`},
	{name: "strings-and-records", atLeast: 3, src: `
struct Row { name: string, n: i32 }
function greet(r: Row): string { return "hi " + r.name; }
function mk(n: i32): Row { return Row { name: "row", n: n }; }
function main(): i32 {
    var r: Row = mk(7);
    print(greet(r) + "\n");
    return r.n;
}
`},
	{name: "enum-match-in-a-loop", atLeast: 3, src: `
enum Shape { Dot, Line(i32), Box(i32, i32) }
function area(s: Shape): i32 {
    match (s) { Dot => { return 0; }, Line(n) => { return n; }, Box(w, h) => { return w * h; } }
}
function total(ss: Shape[]): i32 {
    var t: i32 = 0;
    for s in ss { t = t + area(s); }
    return t;
}
function main(): i32 { return total([Dot, Line(3), Box(4, 5)]); }
`},
	{name: "map-and-string-keys", atLeast: 3, src: `
function tally(words: string[]): Map[string, i32] {
    var m: Map[string, i32] = map_new(8);
    for w in words { m = m.insert(w, 1); }
    return m;
}
function seen(words: string[], k: string): i32 {
    var m: Map[string, i32] = tally(words);
    if (m.has(k)) { return 1; }
    return 0;
}
function main(): i32 { return seen(["a", "b"], "b") + seen(["a", "b"], "z"); }
`},
	// A callee lent a string VIEW can hand that box straight back (#9328).
	// Both halves are here: `handed(v)` keeps what it is lent, so the produced
	// caller hands it a copy of the bytes, and `laundered` hands the result
	// ON, so the frame that sliced the view owns nothing of it by the time it
	// returns. The AST lowering leaks the escaping box, so the leak check
	// reads the produced bodies freeing more, never less.
	{name: "lent-view-handback", atLeast: 4, src: `
function handed(text: string): string { return text; }
function laundered(src: string): string {
    var v: str = slice_unchecked(src, 0, 3);
    return handed(v);
}
function fresh_of(text: string): string { return text + "!"; }
function main(): i32 {
    var s: string = "12345 abc";
    var v: str = slice_unchecked(s, 0, 4);
    return handed(v).len() + fresh_of(v).len() + laundered(s).len();
}
`},
	// A function value the AST lowering builds reaching a produced body. The
	// AST push never retains the element it appends (its buffer leaks
	// instead), so an array the AST-lowered ident_of hands back holds
	// identifiers it does not own, and a produced each would release them as
	// its own: the abort that stopped the produced compiler on checker.fern.
	// The skip leg keeps ident_of on the AST lowering; the trampoline that
	// names it follows through prune's direct-call rule, and each has to
	// follow through the value's type.
	{name: "ast-value-into-produced", atLeast: 3, skip: "ident_of",
		refuses: "each: calls a function value of 2 arguments, a type the AST lowering builds a value of", src: `
struct Id { name: string }
function ident_of(e: Id, own acc: string[]): string[] { return acc.append(e.name); }
function each(es: Id[], rounds: i32, reads: (Id, own string[]) => string[]): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < rounds) {
        for e in es { for n in reads(e, []) { t = t + n.len(); } }
        r = r + 1;
    }
    return t;
}
function main(): i32 {
    var es: Id[] = [Id { name: "ab" + "" }, Id { name: "cde" + "" }];
    var t: i32 = each(es, 5, ident_of);
    for e in es { t = t + e.name.len(); }
    return t;
}
`},
	// The other half of #9328: a callee lent a view need not RETURN the box to
	// keep it — `tok_of` stores it in the token it builds and `add_word` puts
	// it in the array it hands back, which is what every `*_tok` constructor in
	// the self-host lexer does. Both reach the caller through the callee's
	// result, so both callees are handed a copy the result may keep.
	{name: "lent-view-stored", atLeast: 5, src: `
struct Tok { text: string, line: i32 }
function tok_of(text: string, line: i32): Tok { return Tok { text: text, line: line }; }
function add_word(acc: string[], w: string): string[] { return acc.append(w); }
function words_of(src: string): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i + 2 <= src.len()) {
        var v: str = slice_unchecked(src, i, i + 2);
        out = add_word(out, v);
        i = i + 2;
    }
    return out;
}
function toks_of(src: string): Tok[] {
    var out: Tok[] = [];
    var i: i32 = 0;
    while (i + 2 <= src.len()) {
        var v: str = slice_unchecked(src, i, i + 2);
        out = out.append(tok_of(v, i));
        i = i + 2;
    }
    return out;
}
function main(): i32 {
    var n: i32 = 0;
    for w in words_of("abcdef") { n = n + w.len(); }
    for t in toks_of("abcdef") { n = n + t.text.len() + t.line; }
    return n;
}
`},
	// Appending through a BORROWED parameter, which is what the self-host x86
	// assembler does per instruction byte (`x86_emit_mem`). The callee pushes
	// through the non-consuming helper and takes its unit afterwards, so the
	// buffer grows in place instead of being copied per element (#9365); the
	// second half is `set0`, where the caller KEEPS its binding and the
	// caller-side bracket is what makes the update owe a copy. Both halves
	// have to answer the same as the AST lowering: 199 says `a` came through
	// the borrow untouched.
	{name: "borrowed-param-append", atLeast: 4, src: `
function push(buf: i32[], v: i32): i32[] { return buf.append(v); }
function build(n: i32): i32[] {
    var out: i32[] = [];
    var i: i32 = 0;
    while (i < n) { out = push(out, i); i = i + 1; }
    return out;
}
function set0(buf: i32[], v: i32): i32[] { return buf.with(0, v); }
function main(): i32 {
    var a: i32[] = build(64);
    var b: i32[] = set0(a, 99);
    if (a[0] != 0) { return 1; }
    if (b[0] != 99) { return 2; }
    if (a.len() != 64 || b.len() != 64) { return 3; }
    return 199;
}
`},
	// The same shape with COUNTED elements, which is what the grow path turns
	// on. `__fern_arr_push` copies element pointers into the fresh box without
	// retaining them and abandons the old buffer rather than freeing it, so
	// the two share one count per element; this frame's caller DOES release
	// that buffer, because the plan balances every unit, and the aliased
	// elements are counted for by `__fern_arr_inc_elems` over the receiver.
	// Without it the elements die under the new box and the freelist reissues
	// them.
	//
	// Every element is distinct and checked back, so a freed-and-reissued
	// element box shows as a wrong name rather than a crash.
	{name: "borrowed-param-append-counted-elems", atLeast: 4, src: `
struct Box { name: string }
function add(acc: Box[], n: string): Box[] { return acc.append(Box { name: n }); }
function tag(i: i32): string {
    var tbl: string[] = ["aa", "bb", "cc", "dd", "ee", "ff", "gg", "hh", "ii", "jj"];
    return "payload-" + tbl[i % 10] + "-longer-tail";
}
function build(n: i32): Box[] {
    var out: Box[] = [];
    var i: i32 = 0;
    while (i < n) { out = add(out, tag(i)); i = i + 1; }
    return out;
}
function main(): i32 {
    var all: Box[] = build(9);
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < all.len()) {
        if (all[i].name != tag(i)) { return 1 + i; }
        n = n + all[i].name.len();
        i = i + 1;
    }
    return n % 97;
}
`},
	// The NESTED borrow, which is what first caught #9388's corruption: the
	// inner appender's result lands in the outer one's argument position, so
	// the second push is handed a box the first grew. Elements are counted and
	// every one is distinct, so a box freed under the array that now points at
	// it reads back as the wrong name.
	{name: "borrowed-param-append-nested", atLeast: 4, src: `
struct D { name: string, fields: string[] }
function declared(ds: D[], name: string): boolean {
    var i: i32 = 0;
    while (i < ds.len()) {
        if (ds[i].name == name) { return true; }
        i = i + 1;
    }
    return false;
}
function add_one(acc: D[], name: string): D[] {
    if (declared(acc, name)) { return acc; }
    return acc.append(D { name: name, fields: [] });
}
function add_two(acc: D[], a: string, b: string): D[] { return add_one(add_one(acc, a), b); }
function main(): i32 {
    var all: D[] = [];
    all = add_one(all, "seed-value-one");
    all = add_two(all, "seed-value-two", "seed-value-three");
    all = add_two(all, "seed-value-four", "seed-value-five");
    all = add_one(all, "seed-value-one");
    var want: string[] = ["seed-value-one", "seed-value-two", "seed-value-three", "seed-value-four", "seed-value-five"];
    if (all.len() != want.len()) { return 1; }
    var i: i32 = 0;
    while (i < all.len()) {
        if (all[i].name != want[i]) { return 2 + i; }
        i = i + 1;
    }
    return 199;
}
`},
	// The paths a deferred receiver retain has to answer on: `maybe` does not
	// append at all when the flag is false; `ignore` never returns what it
	// appended to; `twice` appends through the same parameter at two sites, so
	// the second one is handed what the first produced; and the last `twice`
	// call has a caller that still reads `live` afterwards, where the bracket
	// makes the receiver shared and the push copies. Balanced allocs and frees
	// on both legs is what the leak-parity check makes of it.
	{name: "borrowed-param-obligations", atLeast: 5, src: `
struct B { n: string }
function tagx(i: i32): string { var t: string[] = ["aa", "bb", "cc", "dd"]; return "long-payload-" + t[i % 4]; }
function maybe(xs: B[], flag: boolean): B[] {
    if (flag) { return xs.append(B { n: tagx(1) }); }
    return [];
}
function ignore(xs: B[]): i32 { var ys: B[] = xs.append(B { n: tagx(2) }); return ys.len(); }
function twice(xs: B[]): B[] { xs = xs.append(B { n: tagx(3) }); xs = xs.append(B { n: tagx(0) }); return xs; }
function seed(n: i32): B[] { var o: B[] = []; var i: i32 = 0; while (i < n) { o = o.append(B { n: tagx(i) }); i = i + 1; } return o; }
function main(): i32 {
    var a: B[] = maybe(seed(3), true);
    var b: B[] = maybe(seed(3), false);
    var c: i32 = ignore(seed(3));
    var d: B[] = twice(seed(3));
    var live: B[] = seed(2);
    var e: B[] = twice(live);
    return a.len() * 1000 + b.len() * 100 + c * 10 + d.len() + e.len() + live.len();
}
`},
	// An append through a record FIELD, which is what the self-host x86
	// assembler does per instruction byte (`x86_gas_mem_op`): the callee
	// reads the record no further through that field, so the push grows the
	// buffer in place and moves it out of the field (#9365). `keeps` still
	// reads its record afterwards, so its bracket on the field makes the
	// push copy; the same call from an AST-lowered `keeps` — the skip leg —
	// brackets by the regrown registry. `boxes` appends counted elements,
	// where a buffer released under both holders reads back as a wrong name.
	{name: "field-append", atLeast: 9, skip: "keeps,dying,boxes,emit2,via", src: `
struct R { ops: i32[], n: i32 }
struct Box { name: string }
struct Q { items: Box[], tag: string }
function emitop(r: R, op: i32): R { return R { ...r, ops: r.ops.append(op) }; }
function addbox(q: Q, n: string): Q { return Q { ...q, items: q.items.append(Box { name: n }) }; }
function emit2(r: R, a: i32, b: i32): R { return emitop(emitop(r, a), b); }
function osz(code: i32[], size: i32): i32[] {
    if (size == 16) { return code.append(102); }
    return code;
}
function via(r: R, size: i32): R { return R { ...r, ops: osz(r.ops, size) }; }
function keeps(): i32 {
    var r: R = R { ops: [1, 2, 3], n: 7 };
    var s: R = emitop(r, 4);
    var u: R = emit2(r, 5, 6);
    var v: R = via(r, 16);
    return r.ops.len() * 1000 + s.ops.len() * 100 + u.ops.len() * 10 + v.ops.len();
}
function dying(n: i32): i32 {
    var r: R = R { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < n) { r = emitop(r, i); r = via(r, 16); i = i + 1; }
    return r.ops.len();
}
function boxes(n: i32): i32 {
    var q: Q = Q { items: [], tag: "t" };
    var i: i32 = 0;
    while (i < n) { q = addbox(q, "name-" + q.tag); i = i + 1; }
    var k: Q = addbox(q, "extra-longer-name-payload");
    var total: i32 = 0;
    for b in q.items { total = total + b.name.len(); }
    return total * 1000 + k.items.len() * 10 + q.items.len();
}
function main(): i32 {
    if (keeps() != 3454) { return 1; }
    if (dying(1000) != 2000) { return 2; }
    if (boxes(5) != 30065) { return 3; }
    return 199;
}
`},
	// An AST-lowered caller moving its accumulator into a produced callee's
	// OWN array position (#9407): the produced body consumes the buffer — the
	// push that supersedes it reclaims it — where an AST-lowered callee would
	// have left it for the caller's rebind to release. The skip leg keeps
	// `forward` and `census` on the AST lowering, so `census` moves its local
	// into `forward`, which passes it on to the produced instance; both read
	// the consuming position through irlower.own_consumed_positions and
	// transfer their reference instead of releasing it after. Under the
	// sanitizer leg the second release was a use-after-free.
	{name: "own-forwarded-into-produced", atLeast: 3, skip: "forward,census", src: `
function visit(x: string, own acc: string[]): string[] {
    if (x.len() > 2) { return acc.append(x); }
    return acc;
}
function fold[T](xs: string[], own acc: T, f: (string, own T) => T): T {
    for x in xs { acc = f(x, acc); }
    return acc;
}
function forward(xs: string[], own acc: string[]): string[] { return fold(xs, acc, visit); }
function census(rows: string[][], acc: string[]): string[] {
    var a: string[] = acc;
    for r in rows { a = forward(r, a); }
    return a;
}
function main(): i32 {
    var seed: string[] = ["seed-one", "seed-two"];
    var out: string[] = census([["alpha", "b", "gamma"], ["delta", "e"], ["epsilon"]], seed);
    return (out.len() * 10 + seed.len()) % 251;
}
`},
	// A write back through a capture is refused (#9320), so this module is
	// mixed: the produced bodies are emitted beside AST-lowered ones, with
	// ssarc.caller_sigs holding the two sides' release of a shared result
	// together. It answers the same either way, which is the whole point.
	{name: "capture-write", atLeast: 1, src: `
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var total: i32 = 0;
    var add = (x: i32): i32 => { total = total + x; return total; };
    var a: i32 = apply(add, 3);
    var b: i32 = apply(add, 4);
    return total;
}
`},
	// A generic the parser's monomorphiser CLONES rather than one this boundary
	// instantiates: `first` and `append_all` bind T through a container, which
	// promotes them to bounded templates, so the module the CLI lowers holds
	// `first__i32`, `append_all__string` and `fold__i32` with no template
	// left. Each clone is concrete, and every one of the five bodies produces;
	// a clone that still read as a template refused every caller of it as
	// "uninstantiated generic", and the fold's callback slot kept an `own T`
	// nothing substituted.
	{name: "generic-clones", atLeast: 6, src: `
function first[T](xs: T[]): T { return xs[0]; }
function append_all[T](into: T[], more: T[]): T[] {
    var out: T[] = into;
    for m in more { out = out.append(m); }
    return out;
}
function fold[T](xs: i32[], own acc: T, visit: (i32, own T) => T): T {
    for x in xs { acc = visit(x, acc); }
    return acc;
}
function add(x: i32, own acc: i32): i32 { return acc + x; }
function total(xs: i32[]): i32 { return fold(xs, 0, add); }
function main(): i32 {
    var names: string[] = append_all(["ab" + ""], ["cde" + "", "f" + ""]);
    return first([40]) + total([1, 2, 3]) + names.len() + names[2].len();
}
`},
	// A wrapper that lends its record parameter on to the function that
	// appends to a field of it. `helper` reads `r` no further after each call,
	// so it hands the whole record on and `emit` grows the field in place under
	// the record's own count; a bracket on the field there made every first
	// append in the callee copy the buffer, once per wrapper call, which is
	// the quadratic term that took the produced compiler's self-build past the
	// arena. The program reads the runtime's shared-append counter itself, so
	// a copy is a wrong answer (200 and up) rather than a slow one.
	{name: "record-handed-through-wrapper", atLeast: 4, src: `
struct R { ops: i32[], n: i32, name: string }
function emit(r: R, op: i32): R { return R { ...r, ops: r.ops.append(op) }; }
function helper(r: R, slot: i32): R {
    r = emit(r, slot);
    r = emit(r, slot + 1);
    return emit(r, slot + 2);
}
function build(n: i32): R {
    var r: R = R { ops: [], n: 0, name: "h" + "" };
    var i: i32 = 0;
    while (i < n) { r = helper(r, i); i = i + 1; }
    return r;
}
function main(): i32 {
    var r: R = build(501);
    if (__arr_push_shared_count() > 0) { return 200 + (__arr_push_shared_count() % 50); }
    return r.ops.len() % 100 + r.name.len();
}
`},
	// A `str` binding in a module that declares a generic struct. The parser
	// erases `str` to `string` at parse time and records the view-ness on the
	// declaration's `is_str`; the struct monomorphiser rewrote every `var`
	// annotation in every body and dropped that flag, so the checker typed
	// the binding `string` against a `str` value and every function holding
	// one refused. Produces 0 of 2 with the flag dropped.
	// The saturating and checked operators at every width the AST lowering
	// clamps at, matched on the exit code: each function is one shape the
	// shared op-list builders emit over the semantic lowering's own slots.
	{name: "overflow-operators", atLeast: 7, src: `
function s32(a: i32, b: i32): i32 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function u32s(a: u32, b: u32): u32 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function s64(a: i64, b: i64): i64 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function u8s(a: u8, b: u8): u8 { return (a +| b) + (a -| b) + (a *| b) + (a <<| b); }
function c32(a: i32, b: i32): i32 {
    var t: i32 = 0;
    match (a +? b) { Some(v) => { t = t + v; }, None => { t = t + 1; } }
    match (a -? b) { Some(v) => { t = t + v; }, None => { t = t + 2; } }
    match (a *? b) { Some(v) => { t = t + v; }, None => { t = t + 3; } }
    match (a /? b) { Some(v) => { t = t + v; }, None => { t = t + 4; } }
    match (a %? b) { Some(v) => { t = t + v; }, None => { t = t + 5; } }
    match (a <<? b) { Some(v) => { t = t + v; }, None => { t = t + 6; } }
    match (a >>? b) { Some(v) => { t = t + v; }, None => { t = t + 7; } }
    return t;
}
function c64(a: i64, b: i64): i64 {
    var t: i64 = 0;
    match (a +? b) { Some(v) => { t = t + v; }, None => { t = t + 1i64; } }
    match (a *? b) { Some(v) => { t = t + v; }, None => { t = t + 3i64; } }
    match (a /? b) { Some(v) => { t = t + v; }, None => { t = t + 4i64; } }
    match (a <<? b) { Some(v) => { t = t + v; }, None => { t = t + 6i64; } }
    return t;
}
function cu(a: u32, b: u32): u32 {
    var t: u32 = 0;
    match (a +? b) { Some(v) => { t = t + v; }, None => { t = t + 1u32; } }
    match (a -? b) { Some(v) => { t = t + v; }, None => { t = t + 2u32; } }
    match (a *? b) { Some(v) => { t = t + v; }, None => { t = t + 3u32; } }
    match (a %? b) { Some(v) => { t = t + v; }, None => { t = t + 5u32; } }
    match (a >>? b) { Some(v) => { t = t + v; }, None => { t = t + 7u32; } }
    return t;
}
function main(): i32 {
    var r: i32 = s32(2147483000, 1000) + s32(-5, 7) + (u32s(4000000000u32, 500000000u32) as i32) + ((s64(9223372036854775000i64, 1000i64) >> 40i64) as i32) + (u8s(200u8, 100u8) as i32);
    r = r + c32(2147483000, 1000) + c32(7, 0) + c32(-2147483648, -1) + ((c64(5i64, 3i64) & 1023i64) as i32) + (cu(4000000000u32, 500000000u32) as i32);
    return r & 255;
}
`},
	{name: "str-binding-beside-a-generic-struct", atLeast: 2, src: `
struct Box[T] { v: T }
function head(s: string): i32 {
    var v: str = slice_unchecked(s, 0, 3);
    var w: str = slice_unchecked(v, 1, 3);
    return v.len() * 10 + w.len();
}
function main(): i32 {
    var b: Box[i32] = Box { v: head("hello" + "") };
    return b.v;
}
`},
}
