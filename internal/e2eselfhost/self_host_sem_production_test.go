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
					if prog.nativeOnly && target == "wasm32-wasi" {
						t.Skip("the program reaches builtins the wasi profile does not grant")
					}
					if prog.want != "" {
						if prog.astAnswers == "" {
							semRefusedByAST(t, fernBin, stdlibRoot, src, target)
						} else if base, _, _ := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, false, "", prog.stdin); base != prog.astAnswers {
							t.Fatalf("the AST lowering answered %q, the case pins %q", base, prog.astAnswers)
						}
						got, report, leak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, "", prog.stdin)
						if got != prog.want {
							t.Fatalf("FERN_SEM_IR answered %q, want %q\nreport: %s", got, prog.want, report)
						}
						if n := semProducedCount(t, report); n < prog.atLeast {
							t.Fatalf("produced %d declarations, want at least %d:\n%s", n, prog.atLeast, report)
						}
						semNoLeak(t, prog.noLeak, target, leak)
						return
					}
					base, _, baseLeak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, false, "", prog.stdin)
					got, report, leak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, "", prog.stdin)
					if got != base {
						t.Fatalf("FERN_SEM_IR changed the answer:\n with = %q\nwithout = %q\nreport: %s",
							got, base, report)
					}
					if prog.skip == "" && prog.refuses != "" && !strings.Contains(report, prog.refuses) {
						t.Fatalf("FERN_SEM_IR did not report %q:\n%s", prog.refuses, report)
					}
					if prog.skip != "" {
						mixed, mixedReport, mixedLeak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, prog.skip, prog.stdin)
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
					semNoLeak(t, prog.noLeak, target, leak)
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

// semNoLeak pins that the produced bodies held nothing at exit. Only the
// sanitize target reports a leak figure at all, so the other three are the
// answer and tally checks alone.
func semNoLeak(t *testing.T, want bool, target string, leak int) {
	t.Helper()
	if !want || target != "x86-64-sanitize" {
		return
	}
	if leak != 0 {
		t.Fatalf("the produced bodies left %d bytes held at exit; this case claims the "+
			"semantic path reclaims the shape whole, which the relative pin cannot say", leak)
	}
}

// semRefusedByAST asserts the AST lowering refuses the program outright: with
// the semantic path off the compile fails and strict mode names the bail site.
// It is what makes a `want` case a claim about reach rather than agreement.
func semRefusedByAST(t *testing.T, fernBin, stdlibRoot, src, target string) {
	t.Helper()
	if target == "x86-64-sanitize" {
		target = "x86-64-linux"
	}
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(fernBin, "-target", target, "-emit", "asm", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=", "FERN_STRICT_IR=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("the AST lowering compiled a program the case says it refuses")
	}
	if !strings.Contains(stderr.String(), "did not lower") {
		t.Fatalf("the AST lowering failed for another reason:\n%s", stderr.String())
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
func semCompileRun(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, src, target string, sem bool, skip, stdin string) (string, string, int) {
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
	run.Stdin = strings.NewReader(stdin)
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
	// produced body the mixed module has to turn off, and why. Without a skip
	// leg it is a line the plain report must carry: the refusal that drops
	// the whole module to the AST lowering.
	refuses string
	// want, when set, is the answer ("<exit>|<stdout>") of a program the AST
	// lowering REFUSES and the semantic lowering produces whole, confirmed
	// against the native compiler; the AST leg is asserted to refuse it
	// rather than run.
	want string
	// astAnswers, with want, is the answer the AST lowering gives INSTEAD:
	// the program is one the native rc design gets wrong and the typed path
	// gets right, so `want` is the language's answer, confirmed by hand
	// against the contract the case names, and the AST answer is pinned so
	// the row is retired the day that lowering is fixed.
	astAnswers string
	// noLeak asks for an ABSOLUTE leak pin on the produced run rather than
	// only the relative one every entry gets: the sanitize leg must report
	// nothing still held at exit. The relative pin says the produced bodies
	// free no LESS than the AST lowering, which is green at any leak below
	// what the AST lowering already leaks — so a claim that the semantic path
	// reclaims a shape WHOLE needs this instead.
	noLeak bool
	// stdin is fed to every run of this program, so a body that reads it
	// answers the same thing on each leg.
	stdin string
	// nativeOnly leaves the wasm leg out: the program reaches builtins the
	// wasi profile does not grant (the user and process ids, the permission
	// bits, subprocess), which E066 refuses at compile time on both legs.
	nativeOnly bool
	src        string
}{
	// A lambda the SOURCE wrote, with an explicit callable return annotation.
	// parse_type_name coarsens that annotation to the tag "fn" and the lambda
	// parse discarded the contract, so e_lambda_at built every source lambda
	// with an empty pair — the same empty-spelling refusal the nested-decl
	// desugar hit, reached without any `function` keyword. The annotation is now
	// re-read verbatim by the reader that produced the tag, which is what
	// parse_decl_type already did for a declaration. Produces 0 of 3 without it.
	{name: "source-lambda-returning-callable", atLeast: 3, src: `
function main(): i32 {
    var mk: () => ((i32) => i32) = ((): ((i32) => i32) => { var g: (i32) => i32 = ((y: i32) => y); return g; });
    var f: (i32) => i32 = mk();
    return f(42);
}
`},
	// The same annotation written WITHOUT the outer parentheses. The two
	// spellings are read by different halves of the ambiguity dance
	// parse_arrow_lambda does (#8743) — one read swallows the lambda's own arrow
	// and one does not — and the contract is recovered on both paths, so both
	// are gated. Also produces 0 of 3 without the fix.
	{name: "source-lambda-returning-callable-bare", atLeast: 3, src: `
function main(): i32 {
    var mk: () => ((i32) => i32) = ((): (i32) => i32 => { var g: (i32) => i32 = ((y: i32) => y); return g; });
    var f: (i32) => i32 = mk();
    return f(42);
}
`},
	// An if-expression desugars to an IIFE whose ret_type if_expr_rt reads off
	// the then-branch, and for a boolean branch it tagged it "bool" — a spelling
	// the language does not have. The checker rejects `bool` as a type name (it
	// answers "did you mean `boolean`?"), so scope.ret_type came back unresolved
	// and semsource refused the IIFE for an empty result type, taking the module
	// with it. i32 and string branches were unaffected, which is why this
	// survived: only the boolean arm of if_expr_rt spelled its own tag wrong.
	// Produces 0 of 2 without the fix; the comparison case covers the binary arm,
	// which spelled it the same way.
	{name: "if-expr-boolean-branches", atLeast: 2, src: `
function main(): i32 {
    var v: boolean = if (true) { false } else { true };
    var w: boolean = if (v) { 1 < 2 } else { 2 < 1 };
    return if (v) { 0 } else { if (w) { 42 } else { 1 } };
}
`},
	// An if-expression whose ARMS are lambdas. The IIFE it desugars to is built
	// by e_lambda_origin, which writes the #5986 sidecar pair empty, and
	// irlower.hoist_value_iife declares the hoisted function with the coarse "fn"
	// tag on purpose (it IS a higher-order factory). Tag without contract is an
	// unresolved result type, so the module went to the AST lowering. The arms
	// carry the contract, so the hoist reads it off the returned lambda.
	//
	// The arms are ANNOTATED here: the contract is read straight off them, with
	// no result to infer. The unannotated case below covers the other half, which
	// reaches semsource with the tag and nothing to resolve.
	// Produces 0 of 4 without the fix.
	{name: "if-expr-lambda-arms-annotated", atLeast: 4, src: `
function main(): i32 {
    var f: (i32) => i32 = if (true) { ((x: i32): i32 => x) } else { ((y: i32): i32 => y + 1) };
    return f(42);
}
`},
	// The same shape in a MATCH expression. Its IIFE body is a StmtMatch, which
	// the contract walk has to enter for the same reason the if-expression's
	// StmtIf does — the hoist gate (iife_arms_have_lambda) already reaches both.
	// Produces 0 of 4 without the match arm.
	{name: "match-expr-lambda-arms-annotated", atLeast: 4, src: `
enum Pick { A, B }
function main(): i32 {
    var p: Pick = Pick.A;
    var f: (i32) => i32 = match (p) { A => ((x: i32): i32 => x), B => ((y: i32): i32 => y + 1) };
    return f(42);
}
`},
	// The UNANNOTATED arm — what the previous change deliberately left refusing.
	// An arm lambda's parameter spellings are always written but its result only
	// when the author annotates it, so irlower yields no contract rather than half
	// of one, and the hoisted IIFE reaches semsource with the coarse "fn" tag and
	// nothing to resolve. The body still says what it returns, so the result is
	// inferred the same way an unannotated declaration's already is. Produces
	// 0 of 4 without the fix.
	{name: "if-expr-lambda-arms-unannotated", atLeast: 4, src: `
function main(): i32 {
    var f: (i32) => i32 = if (true) { ((x: i32) => x) } else { ((y: i32) => y + 1) };
    return f(42);
}
`},
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
	// A column snapshot, at both element kinds the contract admits. Nothing
	// else pins that these PRODUCE: a contract that stopped would drop the
	// module to the AST lowering, which answers identically, so the corpus and
	// the leak census would stay green on two AST-lowered runs. The tally
	// below is what makes this a claim about the semantic path.
	//
	// Both arrays are read after the map that owned the column is gone, which
	// is the property the string column's per-element retain buys: an alias
	// would be reading freed bytes by then, and the sanitizer leg would say so.
	{name: "map-column-snapshot", atLeast: 4, src: `
function str_keys(n: i32): i32 {
    var ks: string[] = [];
    {
        var m: Map[string, i32] = map_new(4);
        m = m.insert("alpha", 1);
        m = m.insert("beta", 2);
        ks = m.keys();
    }
    var t: i32 = 0;
    for k in ks { t = t + k.len(); }
    return t;
}
function str_values(n: i32): i32 {
    var vs: string[] = [];
    {
        var m: Map[i32, string] = map_new(4);
        m = m.insert(1, "one");
        vs = m.values();
    }
    var t: i32 = 0;
    for v in vs { t = t + v.len(); }
    return t;
}
function i32_keys(n: i32): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(7, 1);
    m = m.insert(9, 2);
    var t: i32 = 0;
    for k in m.keys() { t = t + k; }
    return t + m.len();
}
function main(): i32 { return str_keys(0) + str_values(0) + i32_keys(0); }
`},
	// `without` hands the map back inside a fresh tuple and `cleared` builds an
	// empty one without reading the receiver at all. The AST lowering never
	// releases the delete's tuple, and through it loses the map it holds, so
	// the leak legs read the produced bodies freeing strictly more.
	// A record holding a capturing closure, built in one function and dropped
	// in another that never names the function type itself. The drop helper
	// for the record is emitted by every function that mentions it, and the
	// environment chain it walks came from each frame's OWN type list, taken
	// before the schema walk reached the field: the dropping frame emitted an
	// empty chain, the building frame a full one, and merge_helpers refused
	// the two under one symbol -- at emit time, after the module had produced
	// whole, so the compile FAILED rather than falling back (#9804). The rows
	// now close over the schema table, so the chain is a property of the type.
	{name: "closure-field-built-and-dropped-apart", atLeast: 4, noLeak: true, src: `
struct Holder { f: (i32) => i32 }

function make(n: i32): Holder {
    var xs: i32[] = [n, n + 1, n + 2];
    return Holder { f: (x: i32): i32 => { return x + xs[0] + xs[2]; } };
}

function apply(h: Holder): i32 {
    return h.f(1);
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var h: Holder = make(i);
        t = t + apply(h) % 3;
        i = i + 1;
    }
    return t % 7;
}`},
	// A value-position if/match is hoisted to a `__lam_N` body whose result
	// tag is if_expr_rt's reading of the first arm's SYNTAX: a call, a record
	// literal or a nested match all read "i32". The checker never verified the
	// tag, so the semantic source refused every such body whose arm was not an
	// i32 ("return type: declared i32, returns boolean") and the caller with
	// it ("holds a semantic value of i32"). A synthesised declaration's body
	// is the authority for its result now. Produced 0 of 4 before.
	{name: "value-if-arm-is-a-call", atLeast: 4, src: `
struct Xyz { n: i32, valid: boolean }
function gen(): boolean { return true; }
function mk(n: i32): Xyz { return Xyz { n: n, valid: true }; }
function main(): i32 {
    var v: boolean = (if (true) { gen() } else { false });
    var x: Xyz = (if (v) { mk(3) } else { Xyz { n: 4, valid: false } });
    return x.n;
}`},
	// A match-expression whose FIRST arm is itself a match-expression, both
	// yielding unannotated lambdas. Each hoists to a `$iife` body declaring
	// the coarse `fn` tag with no contract, so the result is read off the
	// body: the inner one returns a lambda, whose result the checker now
	// infers from its body, and the outer one returns the CALL of the inner,
	// which nothing but the contract table can type — so the table is built
	// to a fixpoint. Refused 4 of 8 before, `unresolved result type: declared
	// fn`, and the caller with it.
	{name: "value-match-first-arm-is-a-match-of-lambdas", atLeast: 8, noLeak: true, src: `
enum Status { Active, Inactive, Pending }
function main(): i32 {
    var v0: Status = Pending;
    var f: (i32) => i32 = (match (v0) {
        Active => (match (v0) { Active => ((d: i32) => d), Inactive => ((e: i32) => e - 1), Pending => ((g: i32) => g + 1) }),
        Inactive => ((c: i32) => c * 2),
        Pending => ((h: i32) => h + 40)
    });
    return f(2);
}`},
	// A view local rebound in a loop, starting from a view that stays live:
	// the phi merges the live view with a fresh one, and supplying the phi on
	// the entry edge means RETAINING the live view, which on a view's immortal
	// box is a no-op against a release that frees it. Produced, this read
	// `s` after the loop had released it (a sanitizer use-after-free); the
	// planner now refuses any plan that retains a view (#9802), and the AST
	// lowering, which leaks the boxes but answers, stands.
	{name: "view-loop-rebinds-a-live-view", atLeast: 0, refuses: "a view is lent, never retained", src: `
function main(): i32 {
    var t: string = "abcde" + "fghij";
    var s: str = slice_unchecked(t, 0, 5);
    var v: str = s;
    var i: i32 = 0;
    while (i < 3) { v = slice_unchecked(t, 5, 10); i = i + 1; }
    var u: string = "xyz" + "w";
    return v.len() + s.len() + u.len();
}`},
	// Two levels of value-position if, the inner arm a boolean call. The
	// outer IIFE returns the CALL of the inner one, which the checker types
	// from the inner declaration's tag — `if_expr_rt`'s concrete `i32` guess —
	// so reading the checker first kept the guess and the outer body refused
	// `declared i32, returns boolean`. A synthesised callee's contract is read
	// before the checker now. Refused 3 of 7 before.
	{name: "value-if-arm-is-a-value-if-of-a-call", atLeast: 7, src: `
function gen(): boolean { return true; }
function pick(n: i32): boolean { return n > 2; }
function main(): i32 {
    var v: boolean = (if (pick(3)) { (if (pick(1)) { gen() } else { pick(5) }) } else { pick(0) });
    var w: boolean = (if (pick(3)) { (if (pick(4)) { gen() } else { false }) } else { false });
    return (if (v) { 3 } else { 4 }) + (if (w) { 10 } else { 20 });
}`},
	// A hoisted lambda whose author annotation a bare literal adapts to: the
	// checker verified `: u8` over `5`, and the annotation stands. It is the
	// one shape where a synthesised-looking declaration's tag disagrees with
	// its body's own type without being a guess, so the body must not win.
	{name: "annotated-lambda-literal-body", atLeast: 3, src: `
function apply(f: (i32) => u8, n: i32): u8 { return f(n); }
function main(): i32 {
    var r: u8 = apply(((x: i32): u8 => 5), 1);
    return (r as i32) + 2;
}`},
	// A capturing closure held in a TUPLE and called through the element.
	// `ssasem.nests_func` refused a function value as a tuple element and
	// `semsource.method_call` had no arm for `p.0(1)`, so the whole function
	// went to the AST lowering — which leaks the closure's box and its
	// captures every round (16000 bytes at 200 rounds under the sanitizer).
	// The release walk already reached a tuple's elements through
	// `drop_tuple_fields`; produced, the shape is reclaimed whole.
	{name: "closure-in-a-tuple", atLeast: 2, noLeak: true, src: `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var xs: i32[] = [i, i + 1, i + 2];
        var p: ((i32) => i32, i32) = (((x: i32) => x + xs[0] + xs[2]), i);
        t = t + (p.0)(1) % 3 + p.1 % 2;
        i = i + 1;
    }
    return t % 7;
}`},
	// `for (k, v) in m` walks the map's two columns in step: each is
	// snapshotted into a fresh array the frame owns, and the value read
	// beside each key is the value column's element at the same index. The
	// deleted key must not come back, and a string key column is walked
	// through the string dec. The AST lowering leaks its snapshots (168 bytes
	// here); produced, the loop is reclaimed whole. Refused as
	// `unsupported iterable: Map[i32, i32]` before.
	{name: "map-iteration-both-columns", atLeast: 3, noLeak: true, src: `
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    m = m.insert(4, 40);
    var (m2, had) = m.without(2);
    var t: i32 = 0;
    for (k, v) in m2 { t = t + k * v; }
    var names: Map[string, i32] = Map { "a": 1, "bb": 2 };
    var n: i32 = 0;
    for (k2, v2) in names { n = n + k2.len() * v2; }
    return (t % 100) + n;
}`},
	// A value block's IIFE carries `if_expr_rt`'s reading of its arms, and a
	// unary arm read as the i32 default: `(!x)` labelled the block i32, the
	// checker typed the enclosing array from that label, and the outer block's
	// body inference came back untyped, so the module was refused for an
	// array element of the wrong type. These three blocks sit inside another
	// block's arm, where no binding annotation reaches them, so the arm's own
	// reading is what types them: `!` is a boolean, a cast is its target, a
	// negation is its operand.
	{name: "value-if-arm-is-a-unary", atLeast: 1, noLeak: true, src: `
function main(): i32 {
    var big: i64 = 5000000000;
    var flags: boolean[] = (if (big > 0) { [true, (if (big > 1) { (!false) } else { true })] } else { [false] });
    var wide: u64[] = (if (flags[1]) { [(if (flags[0]) { (big as u64) } else { (7 as u64) })] } else { [1] });
    var negs: i64[] = (if (flags[0]) { [(if (flags[1]) { -big } else { -(big + 1) })] } else { [2] });
    return ((wide[0] % 1000) as i32) + ((negs[0] % 7) as i32) + (if (flags[1]) { 3 } else { 0 });
}`},
	// The binding's annotation types the value block bound to it. A `None`
	// arm or a struct literal is as unguessable as a call, and the body
	// inference has nothing either (a bare `None` is an Option of no known
	// payload), so the block kept its i32 label and the module was refused.
	// The stamp used to apply to a 64-bit annotation alone.
	{name: "value-block-takes-its-bindings-type", atLeast: 1, noLeak: true, src: `
struct Pt { x: i32, y: i32 }
function main(): i32 {
    var a: Option[i32] = (if (true) { None } else { None });
    var b: Option[i32] = (if (false) { None } else { Some(5) });
    var p: Pt = (if (true) { Pt { x: 1, y: 2 } } else { Pt { x: 3, y: 4 } });
    var n: i32 = 0;
    match (a) { Some(v) => { n = n + v; }, None => { n = n + 7; } }
    match (b) { Some(v) => { n = n + v; }, None => { n = n + 70; } }
    return n + p.x + p.y;
}`},
	// The checker types `Some(k)` as `Option` with no payload, and a union
	// carrying no payloads counted as concrete, so a lambda annotated
	// `Option[i32]` had its annotation overridden by the family name and
	// its call site could not name a variant. A payload-less builtin generic
	// is a family, not a type.
	{name: "option-results-of-lambdas", atLeast: 3, noLeak: true, src: `
function id[T](x: T): T { return x; }
function main(): i32 {
    function some_of(k: i32): Option[i32] { return id(Some(k)); }
    var none_of: (i32) => Option[i32] = ((x: i32) => None);
    var n: i32 = 0;
    match (some_of(9)) { Some(v) => { n = n + v; }, None => { n = n + 1; } }
    match (none_of(9)) { Some(v) => { n = n + v; }, None => { n = n + 20; } }
    return n;
}`},
	// `return <lambda>` is desugared to a `$lamret$N` slot the lambda lift
	// then boxes; the slot carried no type, so a capturing lambda returned
	// from a local function left its binding unresolved. The slot is now
	// declared as the enclosing declaration's return signature, the way a
	// hand-written fn local is.
	{name: "local-function-returns-a-capturing-lambda", atLeast: 3, noLeak: true, src: `
function main(): i32 {
    var acc: i32 = 3;
    function mk(k: i32): (i32) => i32 { return ((x: i32) => acc + x + k); }
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var f: (i32) => i32 = mk(i);
        t = t + f(2) % 7;
        i = i + 1;
    }
    return t % 100;
}`},
	// A literal argument of a template has no type of its own: `id([])` is
	// produced at the bare variable T, so the array literal was refused for
	// an unresolved type and the map literal for naming no destination. The
	// call's destination names T through the result, so the binding it
	// implies types such an argument; an argument naming its own type still
	// binds the variable itself, left to right. The instance is `id[i32[]]`
	// and its result is reclaimed whole.
	{name: "literal-argument-typed-from-the-destination", atLeast: 2, noLeak: true, src: `
function id[T](x: T): T { return x; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var xs: i32[] = id([]);
        var ys: i32[] = id([i, i + 1]);
        var fs: ((i32) => i32)[] = id([((x: i32) => x + i)]);
        t = t + xs.len() + ys[1] + (fs[0])(2);
        i = i + 1;
    }
    return t % 97;
}`},
	// A value block whose arms are capturing lambdas is hoisted to a
	// `$iife` declaration tagged `fn`, each arm's lambda bound to a
	// `$lamret$N` slot the lift then fills with a closure constructor. The
	// checker types that constructor as nothing, so the slot and the
	// declaration's result were both unresolved. Both are typed off the
	// hoisted body's contract: what the closure hands out is its body's
	// promise minus the environment. The chosen closure is called every
	// round and its box reclaimed.
	{name: "value-block-of-capturing-lambdas", atLeast: 3, noLeak: true, src: `
function main(): i32 {
    var base: i32 = 5;
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 60) {
        var k: i32 = i;
        var f: (i32) => i32 = (if (i % 2 == 0) { ((x: i32) => x + base) } else { ((x: i32) => x * k) });
        var g: (i32) => i32 = (match (i % 3) { 0 => ((x: i32) => x - base), 1 => ((x: i32) => k), _ => ((x: i32) => x + 1) });
        t = t + f(2) % 11 + g(3) % 7;
        i = i + 1;
    }
    return t % 101;
}`},
	// A record literal's type is the struct it names. It was read off the
	// checker, which leaves a literal untyped when a field holds a value
	// block over a template call, and the literal was refused whole even
	// though every field is produced at its declared type. Here the literal
	// is a template argument too, so the destination binding types the
	// parameter and the field's block picks between two template calls.
	{name: "record-literal-is-the-struct-it-names", atLeast: 2, noLeak: true, src: `
struct Xyz { n: i32, valid: boolean, tag: string }
function pick[T](c: boolean, a: T, b: T): T { return if (c) { a } else { b }; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 40) {
        var v: Xyz = pick(i % 2 == 0, (Xyz { n: i, valid: (if (i % 3 == 0) { pick(true, false, true) } else { true }), tag: "a" + "b" }), (Xyz { n: 1, valid: false, tag: "c" }));
        var w: Xyz = Xyz { ...v, n: (if (v.valid) { pick(false, 1, 2) } else { 3 }) };
        t = t + w.n + (if (w.valid) { 10 } else { 0 }) + w.tag.len();
        i = i + 1;
    }
    return t % 97;
}`},
	{name: "map-delete-and-clear", atLeast: 4, noLeak: true, src: `
function survivors(n: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 10); i = i + 1; }
    var (m1, gone) = m.without(1);
    if (!gone) { return 0 - 1; }
    return m1.len() * 10 + m1.get_or(4, 0);
}
function emptied(n: i32): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 1);
    m = m.cleared();
    m = m.insert(7, 3);
    return m.len();
}
function absent(n: i32): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(5, 50);
    var (m1, missing) = m.without(99);
    if (missing) { return 0 - 1; }
    return m1.get_or(5, 0);
}
function main(): i32 { return survivors(5) + emptied(0) + absent(0); }
`},
	// An element read out of a tuple the frame is done with is TAKEN, not
	// borrowed (#9555): `chained` deletes twice, and the map coming back out of
	// the first delete's tuple is consumed by the second — which a borrow could
	// not pay for, since a map box carries no count to retain. `relayed` is the
	// same shape over a tuple literal, and reads BOTH elements, so the take has
	// to leave the one it did not null alone.
	{name: "tuple-element-take", atLeast: 3, noLeak: true, src: `
function chained(n: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 10); i = i + 1; }
    m = m.without(1).0;
    m = m.without(2).0;
    return m.len() * 100 + m.get_or(4, 0);
}
function relayed(n: i32): i32 {
    var t: (i32[], string) = ([n, n + 1], "ab");
    var b: i32[] = t.0;
    var s: string = t.1;
    b = b.append(n + 2);
    return b.len() * 10 + s.len() + b[2];
}
function main(): i32 { return (chained(6) + relayed(1)) & 255; }
`},
	// The CLOSED range form. `for i in LOW..=HIGH` parses to a synthetic
	// `__range_incl` for-iter, and the desugar that rewrites a range-for into a
	// counting while-loop matched only the half-open `__range` — so every
	// module using `..=` reached this boundary with a call it has no contract
	// for and fell to the AST lowering whole. The loop bodies here are the
	// shapes the break test decides: HIGH included, a single-element range that
	// runs ONCE where the half-open form runs not at all, and a reversed one
	// that still runs zero times.
	{name: "inclusive-range", atLeast: 3, src: `
function closed(n: i32): i32 {
    var s: i32 = 0;
    for i in 0..=n { s = s + i; }
    return s;
}
function single(n: i32): i32 {
    var c: i32 = 0;
    for i in n..=n { c = c + 1; }
    for j in (n + 4)..=n { c = c + 100; }
    return c;
}
function skipping(n: i32): i32 {
    var s: i32 = 0;
    for i in 0..=n {
        if (i == 4) { continue; }
        if (i == 9) { break; }
        s = s + i;
    }
    return s;
}
function main(): i32 { return (closed(5) + single(3) + skipping(12)) & 255; }
`},
	// A tuple read out of a CONTAINER is not a take, however dead the read
	// looks. `get_or` retains what it answers and the map's value column goes
	// on holding the same box, so nulling a slot here would show up in every
	// later read of that key — the #9555 corruption reached through the
	// container instead of a tuple slot. `shared` reads `.0` out of one
	// `get_or`, consumes it, and then reads the SAME key again: if the take
	// fired on `get_or` the second read would see the nulled slot.
	{name: "container-read-is-not-a-take", atLeast: 3, noLeak: true, src: `
function seeded(n: i32): Map[i32, (i32[], i32)] {
    var m: Map[i32, (i32[], i32)] = map_new(4);
    return m.insert(1, ([n, n + 1], n));
}
function shared(n: i32): i32 {
    var m: Map[i32, (i32[], i32)] = seeded(n);
    var fallback: (i32[], i32) = ([], 0);
    var first: i32[] = m.get_or(1, fallback).0;
    first = first.append(99);
    var again: (i32[], i32) = m.get_or(1, fallback);
    return first.len() * 100 + again.0.len() * 10 + again.1;
}
function main(): i32 { return shared(7) & 255; }
`},
	// Reach rather than agreement: the AST lowering reads the receiver's SLOT
	// to find a clear's key kind, so it declines a receiver that is not a plain
	// local. The contract reads the key kind from the result type instead and
	// never evaluates the receiver for anything else, so a call result works
	// the way a local does.
	{name: "map-clear-call-receiver", atLeast: 2, want: "1|", noLeak: true, src: `
function built(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i); i = i + 1; }
    return m;
}
function main(): i32 { return built(3).cleared().insert(7, 3).len(); }
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
	// A whole-module fallback: the capture the closure writes is refused, and
	// with it the module keeps the AST lowering whole rather than mixing.
	{name: "capture-write", atLeast: 0, refuses: "the AST lowering stands", src: `
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
	// Beyond the AST lowering: a match on a bare `Some(x)` scrutinee with an
	// array arm and an empty array literal as an arm's value are both
	// refused by the AST lowering ("immediately-invoked value block"), and
	// produced here, so the module compiles only through the semantic path.
	{name: "beyond-ast", atLeast: 3, want: "23|", src: `
function picked(k: i32): i32 {
    var rows: i32[] = [k, k];
    var some: i32[] = (match (Some(rows)) { Some(r) => r, None => [] });
    return some.len();
}
function widen(k: i32): i32[] {
    var o: Option[i32] = Some(k);
    return (match (o) { Some(v) => [v, v, v], None => [] });
}
function main(): i32 { return picked(4) * 10 + widen(2).len(); }
`},
	// Value blocks: a match-expression's arms carry a string, a record, an
	// array and a tuple to the join, and an empty array arm takes the type
	// the binding declares rather than the arms' syntactic guess.
	{name: "value-blocks", atLeast: 3, src: `
enum Shade { Dark, Light }
struct Tagged { n: i32, tag: string }
function vb_words(k: i32): i32 {
    var o: Option[i32] = Some(k);
    var words: string[] = (match (o) { Some(v) => ["a" + "b", "c"], None => [] });
    var sh: Shade = Dark;
    var tag: string = (match (sh) { Dark => "d" + "k", Light => "l" });
    var ot: Option[string] = Some(tag);
    var cell: Tagged = (match (ot) { Some(t) => Tagged { n: k, tag: t }, None => Tagged { n: 0, tag: "" } });
    var pair: (i32, string) = (if (k > 1) { (k, cell.tag) } else { (0, "z") });
    return words.len() * 100 + tag.len() * 10 + cell.tag.len() + pair.1.len();
}
function vb_rows(k: i32): i32 {
    var rows: i32[] = (if (k > 0) { [k, k] } else { [k] });
    var orows: Option[i32[]] = Some(rows);
    var picked: i32[] = (match (orows) { Some(r) => r, None => [0 - 1] });
    return rows.len() * 10 + picked.len();
}
function main(): i32 { return (vb_words(3) + vb_rows(2) + vb_rows(0)) & 255; }
`},
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
	{name: "type-param-only-in-a-lambda-parameter", atLeast: 10, src: `
import "core/iter" as iter;

function kept(xs: i32[]): i32 {
    var big = iter.filter(iter.of(xs), (x: i32): boolean => { return x > 3; });
    return big.len() * 10 + big[0] + big[1];
}

function doubled(xs: i32[]): i32 {
    var all = iter.map(iter.of(xs), (x: i32): i32 => { return x * 2; });
    return all.len() + all[2];
}

function main(): i32 { return kept([5, 2, 8, 1, 4]) + doubled([1, 2, 3]); }
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
	// A `defer` is replayed at the exits of the scope that registered it, and
	// a binding declared in a block INSIDE that scope had no slot there: each
	// function below refused with "unbound name is not a semantic value", and
	// with it the whole module. The desugar now lifts such a declaration to
	// the top of the scope and leaves its initialiser behind as an assignment,
	// so the replay reads what the block left — the replacement, not a capture
	// taken at registration.
	{name: "defer-binding-out-of-its-block", atLeast: 4, noLeak: true, src: `
function conditional(enabled: boolean): i32 {
    var seen: i32 = 1;
    loop {
        if (enabled) {
            var items: i32[] = [2];
            defer seen = items[0];
            items = [9];
        }
        break;
    }
    return seen;
}
function per_iteration(): i32 {
    var seen: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var k: i32[] = [i];
        defer seen = seen + k[0];
        i = i + 1;
    }
    return seen;
}
function from_value_block(): i32 {
    var seen: i32 = 0;
    loop {
        var yielded: i32 = { var items: i32[] = [2]; defer seen = items[0]; items = [4]; 1 };
        seen = seen + yielded;
        break;
    }
    return seen;
}
function main(): i32 {
    return conditional(true) + per_iteration() + from_value_block();
}
`},
	// The same expansion routes every `return` through one shared temp. It was
	// declared unannotated at a literal `0`, so a function returning an array
	// or a string assigned its result into an i32 slot — which the AST
	// lowering's untyped frame slots never noticed, and which the semantic
	// lowering refused as "replacement type does not match its binding".
	// The binding lift needs a value to start the declaration it moves, and
	// until ExprZero the only ones it could write were the ones source spells,
	// so a struct, a tuple, an enum or a cell kept its module on the AST
	// lowering. A zero now names its type and lowers to the zero word, which at
	// a reference type is the null the assignment left at the declaration site
	// overwrites before anything reads it.
	{name: "defer-binding-at-a-type-with-no-literal-zero", atLeast: 4, noLeak: true, src: `
struct P { a: i32 }
function via_record(on: boolean): i32 {
    var seen: i32 = 0;
    loop {
        if (on) { var v: P = P { a: 3 }; defer seen = v.a; v = P { a: 7 }; }
        break;
    }
    return seen;
}
function via_tuple(on: boolean): i32 {
    var seen: i32 = 0;
    loop {
        if (on) { var v: (i32, i32) = (1, 2); defer seen = v.0; v = (5, 6); }
        break;
    }
    return seen;
}
function via_variant(on: boolean): i32 {
    var seen: i32 = 0;
    loop {
        if (on) {
            var v: Option[i32] = Some(3);
            defer { match (v) { Some(n) => { seen = n; }, None => { seen = 1; } } }
            v = Some(9);
        }
        break;
    }
    return seen;
}
function main(): i32 { return via_record(true) + via_tuple(true) + via_variant(true); }
`},
	{name: "defer-typed-return-temp", atLeast: 3, noLeak: true, src: `
function snapshot(): i32[] {
    var items: i32[] = [7];
    defer items = [9];
    return items;
}
function labelled(): string {
    var s: string = "ok";
    defer s = "late" + "";
    return s;
}
function main(): i32 {
    return snapshot()[0] * 10 + labelled().len();
}
`},
	// A function value handed back across a call boundary: the frame that
	// receives one never built the box, so env_rows is what pairs the
	// environments it could carry with the type and lets the release walk the
	// captures. Churned in a loop so a missed capture shows
	// up as a leak rather than a constant. Each loop binds ONE returned
	// closure: two distinct ones in a single loop body is #9657, where the AST
	// lowering this case compares against answers wrongly.
	{name: "a-function-value-returned-from-a-declaration", atLeast: 9, noLeak: true, src: `
function scalar_capture(n: i32): (i32) => i32 {
    return (x: i32): i32 => { return x + n; };
}
function array_capture(n: i32): (i32) => i32 {
    var xs: i32[] = [n, n + 1, n + 2];
    return (x: i32): i32 => { return x + xs[0] + xs[2]; };
}
function two_captures(n: i32): (i32) => i32 {
    var xs: i32[] = [n, n];
    var ys: i32[] = [n, n];
    return (x: i32): i32 => { return x + xs[0] + ys[1]; };
}
function churn_one(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var f: (i32) => i32 = array_capture(i);
        t = t + f(0) % 3;
        i = i + 1;
    }
    return t;
}
function churn_two(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var g: (i32) => i32 = two_captures(i);
        t = t + g(0) % 3;
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    var s: (i32) => i32 = scalar_capture(2);
    return (churn_one() + churn_two()) % 7 + s(3);
}
`},
	// A function value held in a RECORD FIELD, and called through it. The
	// holder takes a unit of the field and its release walks it, so the
	// closure and its captures reclaim with the record; a bare named function
	// in the same field is the shape that must NOT be walked as a box. Churned
	// in a loop so a missed or doubled release shows as a leak or an
	// over-release rather than a constant.
	{name: "a-function-value-in-a-record-field", atLeast: 5, noLeak: true, src: `
struct Holder { f: (i32) => i32 }
function plain(x: i32): i32 { return x + 1; }
function make(n: i32): Holder {
    var xs: i32[] = [n, n + 1, n + 2];
    return Holder { f: (x: i32): i32 => { return x + xs[0] + xs[2]; } };
}
function main(): i32 {
    var a: Holder = Holder { f: plain };
    var t: i32 = a.f(1);
    var i: i32 = 0;
    while (i < 50) {
        var h: Holder = make(i);
        t = t + h.f(0) % 3;
        i = i + 1;
    }
    return t % 7;
}
`},
	// `s.as_bytes()`: the bytes as a fresh u8[], one slot per byte. Not a
	// window onto the string — the slot-array model has no zero-copy view — so
	// the array is an ordinary unit of the frame and the loop would leak one
	// per call if it were not. (`s.bytes()` is intercepted by the same shape
	// rule, but it is a std/string declaration rather than a builtin, so a
	// case for it would pull that whole module's refusals in with it.)
	{name: "the-bytes-of-a-string", atLeast: 3, noLeak: true, src: `
function total(s: string): i32 {
    var bs = s.as_bytes();
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < bs.len()) { t = t + (bs[i] as i32); i = i + 1; }
    return t;
}
function first(s: string): i32 {
    var bs = s.as_bytes();
    if (bs.len() == 0) { return 0; }
    return bs[0] as i32;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { t = t + total("abc") % 5 + first("z") % 3; i = i + 1; }
    return t % 7;
}
`},

	// The `@` whole-value binder. It names the scrutinee itself rather than a
	// projection of it, so the arm holds no reference of its own and the
	// scrutinee has to stay live past the payload name that would otherwise
	// end it — `whole_after_payload` reads `w` after `c`, and
	// `whole_as_value` carries `w` out of the match as the arm's value.
	{name: "the-at-binder-in-a-match-arm", atLeast: 4, noLeak: true, src: `
enum Shape { Circle(string), Square(string) }

function label(s: Shape): string {
    match (s) {
        Circle(c) => { return c; },
        Square(q) => { return q; }
    }
}

function whole_after_payload(s: Shape): i32 {
    match (s) {
        w @ Circle(c) => { return c.len() + label(w).len(); },
        _ => { return 0; }
    }
}

function whole_as_value(s: Shape): Shape {
    return match (s) {
        w @ Circle(v) => w,
        w2 @ Square(v2) => w2
    };
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        t = t + whole_after_payload(Shape.Circle("abc"));
        t = t + label(whole_as_value(Shape.Square("wxyz"))).len();
        i = i + 1;
    }
    return t % 7;
}
`},

	// A reference cast to `usize`. The address is the box's own slot, so the
	// cast converts nothing and owns nothing — and the array's LAST typed use
	// is the cast itself, so without the anchor the planner releases the box
	// there and __memcpy reads freed bytes. This is the shape std/string's
	// `bytes()` is written in.
	//
	// WHICH address the cast answers is #8799's open question — native gives
	// the data pointer and the self-host the box — so this case compares the
	// two lowerings of ONE compiler and settles nothing about that.
	{name: "a-reference-cast-to-an-address", atLeast: 2, noLeak: true, src: `
function copy_bytes(s: string): i32 {
    var n: i32 = s.len();
    var out: u8[] = __alloc_u8(n);
    if (n > 0) {
        __memcpy(out as usize, s.as_bytes() as usize, n);
    }
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < out.len()) { t = t + (out[i] as i32); i = i + 1; }
    return t;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { t = t + copy_bytes("abc"); i = i + 1; }
    return t % 7;
}
`},

	// The three helper-backed array builtins. Each borrows its operands and
	// hands back a fresh unit, so the loop would leak one joined string per
	// round if the result were not this frame's to release.
	// std/array is imported because the CHECKER keeps these three out of the
	// bare method table; every declaration it brings produces too, so the
	// tally below is the whole program's.
	{name: "the-array-reduce-and-join-builtins", atLeast: 70, noLeak: true, src: `
import "std/array";

function total(xs: i32[]): i32 { return xs.sum() + xs.product(); }

function names(sep: string): i32 {
    var parts: string[] = ["ab", "cde", "f"];
    return parts.join(sep).len();
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { t = t + total([2, 3, 4]) + names("--"); i = i + 1; }
    return t % 7;
}
`},

	// A driver that reads stdin. `io.read_all_stdin` binds `var r: Reader =
	// stdin()` and loops `r.read_chunk`, and the whole of std/io went to the
	// AST lowering because the checker resolved no type called `Reader` and
	// the producer held no contract for the handle builtins (#9781). Every
	// driver that reads stdin comes through here, so this is the production
	// shape rather than one entry's: 0 of 55 before, 55 of 55 after. The
	// stdin methods and the two standard Writers are all one family, so the
	// second half drives those too.
	{name: "stdin-and-the-stream-handles", atLeast: 55, stdin: "alpha\nbeta\n", src: `
import "std/io";

function main(): i32 {
    var text: string = io.read_all_stdin();
    var w: Writer = stdout();
    w.write(text);
    var e: Writer = stderr();
    e.write("");
    return text.len();
}
`},

	// A closure that captures a closure. A construction used to BORROW a
	// function-value operand, and the producer secured that borrow by
	// refusing any capture that was not a borrowed parameter — a rule that
	// cannot describe an escape. Here `inner` is a LOCAL of `wrap` and the
	// box holding it is RETURNED: `wrap` cannot free it, because the escaping
	// box still points at it, and the box never took it, so nobody did.
	// Native leaks three blocks an iteration on this program and the
	// self-host refused to produce `wrap` at all (#9637).
	//
	// noLeak, because the answer never depended on this: the program is
	// correct while leaking, and the leak is the whole subject. Measured 0 of
	// 150 blocks live on arm64-darwin under FERN_LEAKCHECK, against 150 of
	// 250 for the AST lowering.
	{name: "a-closure-that-captures-a-closure", atLeast: 5, noLeak: true, src: `
function make(n: i32): (i32) => i32 {
    var xs: i32[] = [n, n + 1, n + 2];
    return (x: i32): i32 => { return x + xs[0] + xs[2]; };
}

function wrap(n: i32): (i32) => i32 {
    var inner: (i32) => i32 = make(n);
    return (x: i32): i32 => { return inner(x) + 1; };
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var f: (i32) => i32 = wrap(i);
        t = t + f(0) % 3;
        i = i + 1;
    }
    return t % 7;
}
`},
	// Self-tail recursion. Tail-call optimisation reached a declaration
	// through `irlower.lower_func`, which a produced body never enters, so a
	// produced self-tail call grew the stack once per round and a deep enough
	// recursion took the program out — SIGSEGV on the default path, where the
	// AST leg answers (#9692). The depth is what makes this a gate rather than
	// a decoration: a million rounds is several times any stack here, and
	// before the rewrite the same program died at two hundred thousand on
	// every target. It costs about a sixth of a second natively.
	//
	// Scalars deliberately: the AST leg's own TCO fires for this shape, so the
	// two legs are comparable and the table's default assertion — same answer
	// as the AST lowering — is the gate. The reference-typed shapes are in
	// TestSelfHostSemanticTailRecursion, where the AST leg is not an oracle.
	{name: "self-tail-recursion", atLeast: 2, src: `
function count(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return count(n - 1, acc + 1);
}

function main(): i32 { return count(1000000, 0) % 7; }
`},
	// An array of function values. `ssasem.nests_func` refused one as an
	// element, so a program holding closures in an array put its whole
	// function on the AST lowering -- 52 of the 550 refusals across 512
	// fernsmith programs, and the last layer of a wall three checks deep.
	//
	// Both elements CAPTURE, because the release is what the refusal was
	// guarding: the array's drop walks each element through
	// `ssarc.drop_value`, which dispatches a function type to
	// `drop_captures`. `caps` and `other` are freed by that walk or not at
	// all, so noLeak is the assertion that carries this case.
	{name: "an-array-of-capturing-closures", atLeast: 4, noLeak: true, src: `
function build(n: i32): ((i32) => i32)[] {
    var caps: i32[] = [n, n + 1, n + 2];
    var other: i32[] = [n * 2, n * 3];
    return [((x: i32) => { return x + caps[0] + caps[2]; }),
            ((y: i32) => { return y + other[0] + other[1]; })];
}

function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var fs: ((i32) => i32)[] = build(i);
        total = total + fs[0](1) % 3 + fs[1](1) % 5;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 90; }
    return total % 7;
}
`},
	// A nested function naming a sibling nested function holds it in a
	// captured slot, so the sibling is a function VALUE whose signature has
	// an i64 parameter and an f64 result. Such a slot was refused ("function
	// signature slot") on the grounds that the untagged indirect call
	// describes every slot as one word; the call through the value now
	// carries the signature tag irlower's call sites carry, so wasm
	// dispatches it through the funcref type the body was declared with.
	{name: "sibling-function-with-a-wide-signature", atLeast: 3, noLeak: true, src: `
function main(): i32 {
    function scale(k: i64): f64 { return (k as f64) / 4.0; }
    function twice(n: i32): i32 { return (scale((n as i64) * 6i64) as i32) + (scale(9i64) as i32); }
    var half: (i64) => i64 = (x: i64) => x / 2i64;
    var via: (i32) => i32 = (n: i32) => (half((n as i64) * 5i64) as i32);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 30) {
        t = t + twice(i) + via(i);
        i = i + 1;
    }
    return t % 251;
}`},
	// A nested function returning an array of functions is bound with the
	// spelling its lambda carries, and that spelling flattened the result to
	// the coarse tag (` + "`(i32) => fn[]`" + `), which the checker resolves to nothing:
	// a sibling calling it, and the sibling's own slot, were left untyped.
	// The binding now spells the full signature, and the checker reads a
	// lambda's function-valued result through the same sidecars a
	// declaration's carries. The AST lowering refuses the shape: whether the
	// array a function value hands back holds boxes or bare addresses is the
	// callee's choice, and it dispatched by the declared tag alone.
	{name: "nested-function-returning-an-array-of-functions", atLeast: 3, want: "65|", noLeak: true, src: `
function main(): i32 {
    function make(k: i32): ((i32) => i32)[] { return [((a: i32) => a + k), ((b: i32) => b * k)]; }
    function pick(n: i32): ((i32) => i32)[] { return make(n + 1); }
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 40) {
        var fs: ((i32) => i32)[] = pick(i);
        t = t + fs[0](i) % 13 + fs[1](2) % 7;
        i = i + 1;
    }
    return t % 97;
}`},
	// A value block whose arm hands a capturing lambda to a template:
	// the arm's lambda is hoisted with no spelled result, so the checker's
	// signature table typed the closure the template forwards as nothing,
	// and the block's declaration had no result. The annotate pass now
	// stamps the result a lambda's body returns before the lift hoists it.
	{name: "value-block-arm-is-a-template-call-over-a-capturing-lambda", atLeast: 3, noLeak: true, src: `
function id[T](x: T): T { return x; }
function main(): i32 {
    var k: i32 = 3;
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 40) {
        var c: boolean = i % 3 == 0;
        var a: ((i32) => i32)[] = [(if (c) { id(((x: i32) => x + k)) } else { ((y: i32) => y - k) })];
        t = t + a[0](i) % 17;
        i = i + 1;
    }
    return t % 211;
}`},
	// A value block arm that hands a NESTED value block of lambda arrays to
	// a template: the lift boxes the arrays every arm yields, and reached
	// through a passthrough only an array literal, so the nested block's
	// lambdas stayed raw addresses. The passthrough chain now ends at a
	// value block as it ends at a literal, on the gate and the rewrite alike.
	{name: "passthrough-forwards-a-nested-value-block-of-lambda-arrays", atLeast: 3, noLeak: true, src: `
enum Color { Red, Green, Blue }
function id[T](x: T): T { return x; }
function choose(p: Color, k: i32): i32 {
    var v1: ((i32) => i32)[] = (match (p) {
        Red => [((a: i32) => a + k)],
        Green => [id(((b: i32) => b * k))],
        Blue => id((match (p) { Red => [((x: i32) => x)], Green => [((y: i32) => y - k)], Blue => [((z: i32) => 9 + k), ((w: i32) => w)] }))
    });
    return v1[0](4) + v1.len();
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 30) {
        var p: Color = if (i % 3 == 0) { Red } else if (i % 3 == 1) { Green } else { Blue };
        t = t + choose(p, i);
        i = i + 1;
    }
    return t % 223;
}`},
	// A map's unit is counted. The box carried no count on the register
	// backends, so a plan that retained one was refused ("map unit is not
	// shared"): a template handing its map parameter back, a map read from
	// two bindings, a map returned from a function that goes on reading it.
	// The box now takes the array header, so a retain is the ordinary
	// rc_inc and the free family releases one unit; the sanitizer leg reports
	// every box released.
	{name: "a-map-unit-is-shared", atLeast: 4, noLeak: true, src: `
import "core/map";
function id[T](x: T): T { return x; }
function same(m: Map[i32, i32]): Map[i32, i32] { return m; }
function total(m: Map[i32, i32], n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + m.get_or(i, 0); i = i + 1; }
    return t;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 40) {
        var m: Map[i32, i32] = map_new(8);
        var i: i32 = 0;
        while (i < 6) { m = m.insert(i, i * r); i = i + 1; }
        var a: Map[i32, i32] = id(m);
        var b: Map[i32, i32] = same(a);
        acc = acc + total(a, 6) % 17 + total(b, 6) % 13 + total(m, 6) % 7;
        r = r + 1;
    }
    return acc % 113;
}`},
	{name: "a-shared-string-map-releases-its-columns-once", atLeast: 2, noLeak: true, src: `
import "core/map";
function pick(c: boolean, a: Map[string, string], b: Map[string, string]): Map[string, string] { return if (c) { a } else { b }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 30) {
        var m: Map[string, string] = map_new(4);
        var key: string = if (r % 2 == 0) { "even" } else { "odd" };
        m = m.insert(key + "k", key + "v");
        m = m.insert("x", "y");
        var n: Map[string, string] = map_new(4);
        n = n.insert("z", "w");
        var p: Map[string, string] = pick(r % 2 == 0, m, n);
        acc = acc + p.len() + m.len() + n.len() + p.get_or("x", "").len();
        r = r + 1;
    }
    return acc % 101;
}`},
	// A consuming mutation of a SHARED map copies first. `snapshot = m` holds
	// a unit of m's box, so `m.insert` in place would show through it; the
	// frame takes a fresh box with the same entries when the count says the
	// box is shared, and a string key and a counted value each get the unit
	// the copy's entry owns.
	{name: "a-shared-map-is-copied-before-it-is-written", atLeast: 1, noLeak: true, src: `
import "core/map";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 30) {
        var m: Map[i32, i32] = map_new(8);
        m = m.insert(1, 10);
        var snapshot: Map[i32, i32] = m;
        m = m.insert(1, 99);
        m = m.insert(2, 20);
        acc = acc + snapshot.len() * 100 + snapshot.get_or(1, -1) + m.get_or(1, -1) + m.get_or(2, -1) + m.len();
        r = r + 1;
    }
    return acc % 127;
}`},
	// A map the frame still reads is not written through. Every collection
	// operation returns a new value (E055), so `var n = m.insert(k, v)` leaves
	// `m` as it was, a callee's insert through a lent parameter leaves the
	// caller's map as it was, and `without` leaves its receiver whole for the
	// bindings that still read it. The receiver's retain is what makes the
	// copy-on-write gate see a second holder. The AST lowering — with the
	// native compiler and the interpreter — borrows the receiver and writes
	// the sole-held box in place, so the write shows through `m` (#9834), and
	// its `without` writes an ALIASED receiver in place too (#9835).
	{name: "a-map-the-frame-still-reads-is-not-written", atLeast: 2, noLeak: true, want: "85|", astAnswers: "63|", src: `
import "core/map";
function grown(m: Map[i32, i32], k: i32): Map[i32, i32] { return m.insert(k, k * 3); }
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    var n: Map[i32, i32] = m.insert(2, 20);
    var g: Map[i32, i32] = grown(m, 7);
    var (rest, had) = n.without(2);
    var snapshot: Map[i32, i32] = n;
    var (rest2, had2) = n.without(1);
    if (g.get_or(7, -1) != 21 || !had || !had2) { return 99; }
    return m.len() + n.len() * 2 + g.len() * 4 + rest.len() * 8 + snapshot.len() * 16 + rest2.len() * 32;
}`},
	{name: "a-shared-string-map-is-copied-before-it-is-written", atLeast: 1, noLeak: true, src: `
import "core/map";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 30) {
        var word: string = if (r % 2 == 0) { "even" } else { "odd" };
        var m: Map[string, string] = map_new(4);
        m = m.insert(word + "k", word + "v");
        var snapshot: Map[string, string] = m;
        m = m.insert(word + "k", "changed");
        m = m.insert("x", word);
        acc = acc + snapshot.len() + snapshot.get_or(word + "k", "").len() + m.get_or(word + "k", "").len() + m.len();
        r = r + 1;
    }
    return acc % 101;
}`},
	// The OS floor: the process and host queries. Each is a stack IR op of
	// its own behind a contract (semsource.os_contracts), where the census
	// over the corpus found 773 call sites refusing for the missing contract,
	// most of them in the coreutils. A fresh string or array is the caller's
	// and a scalar owns nothing; the pin is that the produced bodies free no
	// less than the AST lowering, which leaks several of these results (#9832).
	{name: "os-floor-queries", atLeast: 2, nativeOnly: true, src: `
function queries(): i32 {
    var n: i32 = 0;
    if (getcwd().len() > 0) { n = n + 1; }
    if (hostname().len() > 0) { n = n + 2; }
    if (cpu_count() > 0) { n = n + 4; }
    if ((geteuid() as i64) >= 0) { n = n + 8; }
    if (uname_field(0).len() > 0) { n = n + 16; }
    if (isatty(1) || !isatty(1)) { n = n + 32; }
    var old: i32 = umask(18);
    if (umask(old) == 18) { n = n + 64; }
    if (environ().len() > 0) { n = n + 128; }
    return n;
}
function main(): i32 { return queries(); }
`},
	// The handle metadata surface, asked of the bare descriptor like close:
	// fstat, the open flags, lseek. Each hands back a fresh Result box the
	// caller owns. /dev/null is not preopened under wasmtime, so on that leg
	// both lowerings take the Err arm and still agree.
	{name: "os-floor-handles", atLeast: 2, src: `
function handles(path: string): i32 {
    var n: i32 = 0;
    match (open_reader(path)) {
        Ok(r) => {
            match (r.stat()) { Ok(st) => { n = n + 1; }, Err(e) => { n = n + 1000; } }
            match (r.flags()) { Ok(f) => { n = n + 2; }, Err(e) => { n = n + 2000; } }
            match (r.seek(0, 0)) { Ok(off) => { n = n + 4; }, Err(e) => { n = n + 4000; } }
            match (r.close()) { Some(e) => { n = n + 8000; }, None => { n = n + 8; } }
        },
        Err(e) => { n = n + 16000; },
    }
    return n;
}
function main(): i32 { return handles("/dev/null") % 256; }
`},
	// The directory, link and permission ops over a directory temp_dir hands
	// back, and statfs on it; every path is lent and every outcome a fresh
	// Result box. access and chmod are fsmode, which wasi does not grant.
	{name: "os-floor-files", atLeast: 2, nativeOnly: true, src: `
function files(dir: string): i32 {
    var n: i32 = 0;
    var sub: string = dir + "/d";
    match (create_dir(sub, 448)) { Ok(u) => { n = n + 1; }, Err(e) => { n = n + 1000; } }
    match (access(sub, 0)) { Ok(u) => { n = n + 2; }, Err(e) => { n = n + 2000; } }
    match (rename(sub, dir + "/e")) { Ok(u) => { n = n + 4; }, Err(e) => { n = n + 4000; } }
    match (create_symlink(dir + "/e", dir + "/l")) { Ok(u) => { n = n + 8; }, Err(e) => { n = n + 8000; } }
    match (read_link(dir + "/l")) { Ok(t) => { if (t.len() > 0) { n = n + 16; } }, Err(e) => { n = n + 16000; } }
    match (chmod(dir + "/e", 493)) { Ok(u) => { n = n + 32; }, Err(e) => { n = n + 32000; } }
    match (statfs(dir)) { Ok(fs) => { n = n + 64; }, Err(e) => { n = n + 64000; } }
    match (remove_file(dir + "/l")) { Ok(u) => { n = n + 128; }, Err(e) => { n = n + 128000; } }
    match (remove_dir(dir + "/e")) { Ok(u) => { n = n + 256; }, Err(e) => { n = n + 256000; } }
    return n;
}
function main(): i32 {
    var n: i32 = 0;
    match (temp_dir("fernsem")) {
        Ok(d) => { n = files(d); match (remove_dir(d)) { Ok(u) => { }, Err(e) => { n = n + 7; } } },
        Err(e) => { n = 5; },
    }
    return n % 256;
}
`},
	// subprocess hands back a record the caller owns, its two strings among
	// the parts a release walks; the arguments are lent.
	{name: "os-floor-subprocess", atLeast: 1, nativeOnly: true, src: `
function main(): i32 {
    var p: ProcessResult = subprocess("/bin/echo", ["hi"], "");
    print(p.stdout);
    return p.exit_code + p.stdout.len();
}
`},
	// A generic struct's method that redeclares the receiver's own variables
	// (`insert[K: cmp.Ord, V]` on `OrdMap[K, V]`) is cloned per receiver
	// instantiation by parser.monomorphize_structs. The clone is concrete, so
	// it is an ordinary declaration here, not a template nothing instantiates.
	{name: "generic-struct-method-redeclares-receiver-vars", atLeast: 2, src: `
struct Pair[T] { a: T, b: T }
function (p: Pair[T]) put[T](v: T): Pair[T] { return Pair { a: p.b, b: v }; }
function main(): i32 {
    var p: Pair[i32] = Pair { a: 1, b: 2 };
    p = p.put(7);
    return p.a * 10 + p.b;
}
`},
	// The same shape through the standard library: every ordmap method with a
	// bounded key is that clone, and the tree under it is produced with it.
	{name: "ordmap-bounded-method-clones", atLeast: 69, src: `
import "std/ordmap";
function main(): i32 {
    var m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
    m = m.insert(3, 4);
    m = m.insert(3, 5);
    m = m.insert(9, 1);
    return m.get_or(3, 0) * 10 + m.len();
}
`},
}
