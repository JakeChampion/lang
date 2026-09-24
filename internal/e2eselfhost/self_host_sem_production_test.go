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
					if prog.reportLacks != "" && strings.Contains(report, prog.reportLacks) {
						t.Fatalf("FERN_SEM_IR reported %q, which this case pins as cleared:\n%s", prog.reportLacks, report)
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
	// reportLacks, when set, is a line the report must NOT carry: a refusal a
	// fix CLEARED on a module that still refuses for another reason, so the
	// production tally cannot observe it. `refuses` names what still stands;
	// this names what may not come back.
	reportLacks string
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
	// An element read into a local that stays live across a `.with` on its
	// array: the insertion sort's shape. The read takes a unit of its own, so
	// the array moves into the write and is written in place rather than
	// copied (#9849); TestSelfHostSemanticAllocationParity pins the count.
	{name: "element-read-outlives-the-array-write", atLeast: 2, noLeak: true, src: semHeldElementSource},
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
	// A generic struct named INSIDE a callable spelling. mg_ty understood an
	// `own` prefix, an array suffix, a tuple and a bracketed `Base[args]` — and
	// not a callable, so `(i32) => Slot[i32]` was read as the base
	// `(i32) => Slot` and handed back with the instantiation unmangled. The
	// tuple element is what carries a whole signature to mg_ty (a var or
	// parameter annotation is coarsened to the "fn" tag plus sidecars, which
	// were mangled already), and the callable's parameter and result positions
	// are separate arms of the rebuild, so both are here. Produces 0 of 6
	// without it: `main` refuses on the tuple element type, `take` and `unwrap`
	// on a binding whose declared `Slot__i32` meets a semantic value of `Slot`.
	{name: "generic-struct-inside-a-callable-spelling", atLeast: 6, src: `
struct Slot[T] { v: T }

function slot_of(n: i32): Slot[i32] { return Slot[i32] { v: n }; }

function take(p: ((i32) => Slot[i32], i32)): i32 {
    var f: (i32) => Slot[i32] = p.0;
    var s: Slot[i32] = f(p.1);
    return s.v;
}

function unwrap(q: ((Slot[i32]) => i32, Slot[i32])): i32 {
    var g: (Slot[i32]) => i32 = q.0;
    return g(q.1);
}

function main(): i32 {
    var p: ((i32) => Slot[i32], i32) = (((b: i32): Slot[i32] => slot_of(b)), 4);
    var q: ((Slot[i32]) => i32, Slot[i32]) = (((s: Slot[i32]): i32 => s.v), Slot[i32] { v: 7 });
    return take(p) + unwrap(q);
}
`},
	// The lambda half of the same fix, which the row above cannot observe:
	// both its lambdas return a plain struct, so ExprLambda's callable-result
	// sidecar stays empty. #9954's own repro is the shape that fills it — a
	// lambda whose declared result is `(i32) => Slot[i32]` — and it produces
	// whole since a capturing returned lambda takes the `$lamret$N` slot
	// (#10025). What the mangling clears is the refusal the module used to
	// carry: `outer: return type: declared ((i32) => ((i32) => Slot__i32)),
	// returns ((i32) => ((i32) => Slot))`, the sidecar the `...lm` spread
	// copied verbatim meeting the signature ms_func had already mangled, which
	// is pinned so it cannot come back.
	{
		name:        "a-lambda-declaring-a-callable-result-over-a-generic-struct",
		atLeast:     5,
		reportLacks: "outer: return type:",
		src: `
struct Slot[T] { v: T }

function slot_of(n: i32): Slot[i32] {
    return Slot[i32] { v: n };
}

function outer(): (i32) => (i32) => Slot[i32] {
    return (a: i32): (i32) => Slot[i32] => ((b: i32): Slot[i32] => slot_of(a + b));
}

function main(): i32 {
    var f: (i32) => (i32) => Slot[i32] = outer();
    var g: (i32) => Slot[i32] = f(10);
    var s: Slot[i32] = g(5);
    return s.v;
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
import "core/map";
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
import "core/map";
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
	// A generic enum that declares a method. monomorphize_enums skipped any
	// enum with one, so the enum stayed generic and enum_entry refused its
	// every use ("variant field type"). The methods clone per instantiation
	// now, the derived ones included, and a method with a type parameter of
	// its own folds into a free generic the way a struct's does, with the
	// variant argument's payload settling that parameter.
	{name: "generic-enum-methods-clone-per-instantiation", atLeast: 6, noLeak: true, src: `
import "core/cmp";

@derive(cmp.Eq)
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) or_else(d: T): T { match (o) { Sm(x) => { return x; }, Nn => { return d; } } }
function (o: Opt[T]) swap[U](other: Opt[U]): Opt[U] { match (o) { Sm(x) => { return other; }, Nn => { return Nn; } } }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var s: Opt[string] = Sm("round " + i.to_string());
        var n: Opt[i32] = s.swap(Sm(i));
        var e: Opt[string] = Nn;
        if (s == e) { t = t + 100; }
        if (s == s.swap(Sm("round " + i.to_string()))) { t = t + 1; }
        t = t + n.or_else(1) + s.or_else("").len();
        i = i + 1;
    }
    return t % 97;
}`},
	// A builtin union's literal takes its type arguments from the destination,
	// and `and[U](other: Result[U, E])` binds U from this very argument: the
	// destination `Result[U, string]` named Ok without saying what it held, so
	// the literal was refused ("unsupported variant literal"). The payload
	// settles it now, for Ok and Err as it already did for Some. The payload is
	// an i32 because the AST lowering, the oracle here, misreads a string one
	// through the erased U and answers differently on each leg (#10014).
	{name: "builtin-union-payload-settles-the-literal", atLeast: 1, noLeak: true, src: `
import "std/result";

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var r: Result[i32, string] = Ok(i);
        var s: Result[i32, string] = r.and(Ok(i + 1));
        var e: Result[i32, string] = Err("no");
        var f: Result[i32, string] = e.and(Ok(i + 2));
        t = t + s.unwrap_or(0) + f.unwrap_or(9);
        i = i + 1;
    }
    return t % 7;
}`},
	// A generic enum with a method that is only ever used at a composite key.
	// A tuple key has no clone name, so the enum is left out of the pass with
	// its method beside it. Nothing produces; the row pins that the module
	// still compiles and answers on every leg.
	{name: "generic-enum-method-at-a-composite-key", atLeast: 0, src: `
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) get_or(d: T): T {
    var t: Opt[T] = o;
    match (t) { Sm(x) => { return x; }, Nn => { return d; } }
}

function main(): i32 {
    var o: Opt[(i32, i32)] = Sm((3, 4));
    var n: Opt[(i32, i32)] = Nn;
    var p: (i32, i32) = o.get_or((0, 0));
    var q: (i32, i32) = n.get_or((1, 1));
    return p.0 + p.1 + q.0 + q.1;
}`},
	// The same enum used at a simple key AND a composite key in one module. A
	// clone beside the generic original is unsound on the AST lowering, which
	// dispatches an enum's methods by name (`a.get_or(9)` answered 0 with both
	// in the module), so one unkeyable use keeps the whole enum out of the pass.
	// Before, the pass dropped the generic `Opt` for the `Opt[i32]` use and the
	// `Opt[(i32, i32)]` annotation dangled, with or without a method.
	{name: "generic-enum-at-a-simple-and-a-composite-key", atLeast: 0, src: `
enum Opt[T] { Sm(T), Nn }

function (o: Opt[T]) get_or(d: T): T {
    match (o) { Sm(x) => { return x; }, Nn => { return d; } }
}

function main(): i32 {
    var a: Opt[i32] = Sm(3);
    var b: Opt[(i32, i32)] = Sm((1, 2));
    var n: Opt[i32] = Nn;
    var p: (i32, i32) = b.get_or((5, 5));
    return a.get_or(9) * 10 + n.get_or(4) + p.0 + p.1;
}`},
	// The method-less form of the mix, which dangled on main: the pass dropped
	// the generic `Opt` for the `Opt[i32]` use and `Sm((1, 2))` then named a
	// variant no declaration held (E001 from the checker).
	{name: "generic-enum-at-a-simple-and-a-composite-key-without-methods", atLeast: 0, src: `
enum Opt[T] { Sm(T), Nn }

function main(): i32 {
    var a: Opt[i32] = Sm(3);
    var b: Opt[(i32, i32)] = Sm((1, 2));
    var x: i32 = 0;
    match (a) { Sm(n) => { x = n; }, Nn => { x = 0; } }
    match (b) { Sm(p) => { x = x + p.0 + p.1; }, Nn => { } }
    return x;
}`},
	// `use v <- f(args)` writes its continuation with an untyped parameter,
	// and the trampoline the lift built from it declared none, so the typed
	// producer refused it ("unresolved result type"). checker.pretype_module
	// stamps the callee's callback parameter type on the binding ahead of
	// both checking and annotation. One continuation captures nothing (a `$wrap` trampoline), one captures
	// its caller's parameter (a `$clo` body), and the string binding is
	// released. A callee that is a function-typed LOCAL is stamped the same
	// way, but such a local's type nests a function type and the producer
	// refuses that slot outright, so no row can reach it yet.
	{name: "use-binding-takes-the-callee-parameter-type", atLeast: 5, noLeak: true, src: `
import "core/cmp";

function with_name(n: i32, k: (string) => i32): i32 { return k("name-" + n.to_string()); }

function plain(i: i32): i32 {
    use s <- with_name(i);
    return s.len() * 2;
}

function capturing(i: i32): i32 {
    use s <- with_name(i);
    return s.len() + i % 3;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        t = t + plain(i) + capturing(i);
        i = i + 1;
    }
    return t % 97;
}`},
	// A lambda returned from inside a lambda. The lift hoists the outer body
	// to a declaration of its own, and the pre-worklist rewrite that turns a
	// source function's ` + "`return <lambda>`" + ` into a boxed slot never
	// reached it, so the inner lambda stayed a bare function address, which the
	// producer refuses as a closure value. The worklist rewrites a lifted
	// body's capture-free tail lambda the same way now; a capturing one stays
	// with the escaping-closure hoist (#5281). The boxes each call builds are
	// released.
	{name: "lambda-returns-a-lambda-from-a-lambda", atLeast: 5, noLeak: true, src: `
function main(): i32 {
    var mk = (): ((i32) => i32) => { return (x: i32): i32 => x * 2; };
    var mk2 = (): (i32) => i32 => { return (x: i32): i32 => x + 3; };
    var f = mk();
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        t = t + f(i) + mk2()(i);
        i = i + 1;
    }
    return t % 97;
}`},
	// A lifted body whose capture-free tail takes the slot rewrite now has
	// every ` + "`return <lambda>`" + ` rewritten, a CAPTURING one in a branch
	// included; that is the corridor #5281 miscompiled in silently, so the
	// branch return is pinned here on every leg.
	{name: "capturing-lambda-returned-from-a-branch-of-a-lambda", atLeast: 4, noLeak: true, src: `
function main(): i32 {
    var pick = (flag: boolean, n: i32): (i32) => i32 => {
        if (flag) { return (x: i32): i32 => x + n; }
        return (x: i32): i32 => x * 2;
    };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        t = t + pick(i % 2 == 0, i)(3);
        i = i + 1;
    }
    return t % 97;
}
`},
	// A block-bodied lambda with no result annotation whose body binds through
	// ` + "`use`" + `, and one whose block yields a tail value.
	{name: "block-bodied-lambda-with-a-use-binding", atLeast: 4, noLeak: true, src: `
function give(x: i32, cb: (i32) => i32): i32 { return cb(x); }

function main(): i32 {
    var bound = (): i32 => {
        use n <- give(41);
        return n + 1;
    };
    var tail = (x: i32) => { var y: i32 = x + 1; y * 2 };
    return bound() + tail(3);
}`},
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
	// The same phi as the row above, with a LITERAL on the entry edge rather
	// than a live view — the `var spec: str = ""; … spec = slice_unchecked(…)`
	// that `std/format`'s two refused functions are both built on. Produced as
	// a `string` and retagged by `widen`, the literal is a borrow, and the
	// phi could only be supplied on that edge by retaining a view; a literal
	// carries the same immortal rc a view's box does, so producing it at the
	// destination's own type makes the edge a move. 0 of 4 before, and the AST
	// lowering strands every view box it makes (3240 bytes in 135 blocks).
	{name: "a-string-literal-is-already-a-view", atLeast: 4, noLeak: true, src: `
function span(fmt: string, take: boolean): i32 {
    var spec: str = "";
    if (take) { spec = slice_unchecked(fmt, 1, 4); }
    var sum: i32 = spec.len();
    var k: i32 = 0;
    while (k < spec.len()) { sum = sum + (spec[k] as i32); k = k + 1; }
    return sum;
}

function rounds(fmt: string, n: i32): i32 {
    var out: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var spec: str = "";
        if (i % 3 != 0) { spec = slice_unchecked(fmt, 0, i % 5); }
        out = out + spec.len();
        i = i + 1;
    }
    return out;
}

function width(s: str): i32 { return s.len(); }

function main(): i32 {
    var fmt: string = "abcdefgh" + "ijkl";
    var many: str[] = ["", "ab", "cde"];
    var total: i32 = span(fmt, true) + span(fmt, false) + rounds(fmt, 200);
    total = total + width("wxyz");
    for m in many { total = total + m.len(); }
    var last: str = "";
    if (total > 0) { last = slice_unchecked(fmt, 2, 6); }
    return total + last.len();
}`},
	// The map cursor: `m.iter()` and its four methods. `map_iter` is the only
	// allocation of the five and its block carries no rc header, so the frame
	// counts nothing and frees nothing; `key` and `value` read the map's own
	// columns at the cursor and take no unit of what they answer. Refused
	// whole before (`unsupported call target: Map[string, i32].iter`), which
	// held `examples/tests/json_roundtrip_test` and
	// `conformance/cases/audit_std_json` entirely to the AST lowering.
	//
	// Both key-column layouts are covered: `mapiter_key` loads a string key
	// through the counted column and a narrow-integer key through the raw one,
	// and only the second exercises the load `is_supported_map` admits beside
	// the string case. Nothing in the moving corpus rows is integer-keyed.
	//
	// No noLeak: both legs hold one 16-byte block per cursor and the typed leg
	// holds exactly the same bytes the AST leg does, which is what the
	// comparison against the AST oracle pins.
	{name: "a-map-cursor-reads-the-columns-it-points-at", atLeast: 6, src: `
import "core/map";

function sum_values(m: Map[string, i32]): i32 {
    var total: i32 = 0;
    var it: MapIter[string, i32] = m.iter();
    while (it.has_next()) {
        total = total + it.value() + it.key().len();
        it.advance();
    }
    return total;
}

function longest(m: Map[string, string]): i32 {
    var best: i32 = 0;
    var it: MapIter[string, string] = m.iter();
    while (it.has_next()) {
        var v: string = it.value();
        if (v.len() > best) { best = v.len(); }
        it.advance();
    }
    return best;
}

function rounds(m: Map[string, i32], n: i32): i32 {
    var out: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var it: MapIter[string, i32] = m.iter();
        while (it.has_next()) { out = out + it.value(); it.advance(); }
        i = i + 1;
    }
    return out;
}

function narrow_keys(m: Map[i32, i32]): i32 {
    var total: i32 = 0;
    var it: MapIter[i32, i32] = m.iter();
    while (it.has_next()) { total = total + it.key() * it.value(); it.advance(); }
    return total;
}

function narrow_key_str_value(m: Map[i32, string]): i32 {
    var total: i32 = 0;
    var it: MapIter[i32, string] = m.iter();
    while (it.has_next()) { total = total + it.key() + it.value().len(); it.advance(); }
    return total;
}

function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("aa", 1);
    m = m.insert("bbb", 2);
    m = m.insert("cccc", 4);
    var t: Map[string, string] = map_new(4);
    t = t.insert("k", "vvvvv");
    t = t.insert("kk", "vv");
    var empty: Map[string, i32] = map_new(4);
    var a: Map[i32, i32] = map_new(4);
    a = a.insert(2, 5);
    a = a.insert(3, 7);
    var b: Map[i32, string] = map_new(4);
    b = b.insert(10, "xyz");
    b = b.insert(20, "pq");
    return sum_values(m) + longest(t) + rounds(m, 50) + sum_values(empty)
        + narrow_keys(a) + narrow_key_str_value(b);
}`},
	// A cursor RETURNED over a map the frame owns. The anchor keeps the map
	// live for as long as a read through the cursor can happen inside the
	// frame, and a return takes the cursor out of it, so the columns it points
	// at are the ones this frame is about to release. Refused, and the AST
	// lowering — which sidesteps the class by never reclaiming a frame map
	// that has an explicit `iter()` — stands and answers.
	//
	// Before the refusal this crashed the compiler outright ("array index out
	// of range"), so the row pins a diagnostic where there was a backtrace.
	// Native answers 0 on this shape where the AST leg answers 7, filed
	// separately; the case here is the refusal, not the divergence.
	//
	{name: "a-cursor-result-escapes-its-map", atLeast: 0, refuses: "cursor result escapes its map", src: `
import "core/map";

function make_cursor(): MapIter[string, i32] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 7);
    return m.iter();
}

function main(): i32 {
    var it: MapIter[string, i32] = make_cursor();
    var total: i32 = 0;
    while (it.has_next()) { total = total + it.value(); it.advance(); }
    return total;
}`},
	// A CONTAINER carries the cursor exactly as far, and this is its OWN row on
	// purpose. Put beside the bare form, the container proves nothing: the bare
	// refusal alone satisfies the `refuses` substring, `atLeast: 0` sets no
	// floor, and the container shape ANSWERS when it is wrongly produced, so
	// the differential stays green too — a row holding both would pass with the
	// container walk deleted. Alone, the refusal line appears only while
	// `semtypes.holds_map_iter` recurses, so losing the walk turns this red.
	//
	// The container is the worse of the two shapes: `array_new` is no
	// projection, so the array has no anchor edge to the map at all. A TUPLE
	// holding a cursor is refused identically and is in no row, because the AST
	// leg declines that shape outright ("module is not IR-eligible") and a
	// differential row needs an oracle that runs.
	{name: "a-container-of-cursors-escapes-too", atLeast: 0, refuses: "cursor result escapes its map", src: `
import "core/map";

function make_cursors(): MapIter[string, i32][] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("b", 5);
    return [m.iter()];
}

function main(): i32 {
    var cs: MapIter[string, i32][] = make_cursors();
    var it: MapIter[string, i32] = cs[0];
    var total: i32 = 0;
    while (it.has_next()) { total = total + it.value(); it.advance(); }
    return total;
}`},
	// A type variable that only a CONSTRAINT mentions. `nth[T, I: Iterator[T]]`
	// and `last[T, I: Iterator[T]]` return `Option[T]`, and `to_array` returns
	// `T[]`, but no parameter spells T — it is the impl the bound resolved to
	// that says what it is (`impl Iterator[i32] for Range`,
	// `impl[T] Iterator[T] for ArrayIter[T]`). The parser dropped a bound's
	// type arguments outside `-fmt`, so the monomorphiser built the instance
	// with T still free and every reader downstream saw `Option[unknown]`:
	// `unbound type variable`, 0 of 13. It is the root of the whole cascade in
	// `examples/tests/iter_test` (175 declarations) and
	// `iter_combinators_test` (151), both of which produce whole with it.
	//
	// Both impl shapes are covered deliberately: `Range` writes its element
	// concretely, while `ArrayIter[T]` writes it in the impl's OWN parameter,
	// so only the second exercises matching the impl's `for` type against the
	// concrete one. `words` pins a string element, so a wrong binding cannot
	// pass as i32.
	{name: "a-type-variable-only-a-constraint-mentions", atLeast: 13, noLeak: true, src: `
import "core/iter" as iter;

function tail(xs: i32[]): i32 {
    match (iter.last(iter.of(xs))) {
        Some(v) => { return v; },
        None => { return 0 - 1; },
    }
}

function pick(n: i32): i32 {
    match (iter.nth(iter.range(0, 20), n)) {
        Some(v) => { return v; },
        None => { return 0 - 1; },
    }
}

function words(ws: string[]): i32 {
    var out: string[] = iter.to_array(iter.of(ws));
    var sum: i32 = 0;
    for w in out { sum = sum + w.len(); }
    return sum;
}

function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        total = total + pick(i % 20);
        total = total + tail([i, i + 1, i + 2]);
        total = total + words(["ab", "cde", "f"]);
        i = i + 1;
    }
    return total % 1000;
}`},
	// A generic-struct method whose receiver is a CALL RESULT. `map[T, U]`
	// declares type parameters of its own, so it is folded into the free
	// generic `__smm_<Base>_map` and the method is DROPPED; `mono_expr`
	// rewrites `recv.map(f)` onto the fold, gated on the receiver's inferred
	// spelling being a bracketed instantiation. `recv_method_ret_of` answered
	// with the chained method's DECLARED return — `NdArray[T]`, the receiver's
	// own `[i32]` never substituted in — so the gate failed, the call kept a
	// method spelling whose declaration had been dropped, and the module did
	// not build on EITHER leg: `call to unknown symbol
	// ndarray__NdArray__i32.map`. Binding the receiver to a name first was
	// enough to compile it, which is what pinned the cause (#9927).
	{name: "a-chained-receiver-keeps-its-instantiation", atLeast: 20, noLeak: true, src: `
import "std/ndarray" as ndarray;

function main(): i32 {
    var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4], [2, 2]);
    var m: ndarray.NdArray[i32] = a.transpose().map((x: i32): i32 => x * 2);
    return m.get([1, 1]);
}`},
	// The sibling root, one layer in. `map_rank[T, U](k: i32, f: (NdArray[T])
	// => NdArray[U])` pins U only inside the callback's BRACKETED return, and
	// `infer_inst` gated its fn-return recovery on the return BEING a bare
	// type parameter — so U never bound and this module did not build either.
	// `bind_unify` already descends into a bracketed spelling; only the gate
	// was wrong.
	//
	// Clearing that left the module compiling but refusing all 28 of its
	// declarations, behind the callable-slot spelling the struct
	// monomorphiser did not mangle. With both closed it produces whole and
	// reclaims whole, `live_bytes=0` against the 1232 bytes the AST leg
	// strands.
	{name: "a-callbacks-return-pins-the-methods-own-variable", atLeast: 28, noLeak: true, src: `
import "std/ndarray" as ndarray;

function sum_cell(c: ndarray.NdArray[i32]): ndarray.NdArray[i32] {
    return ndarray.from_flat([c.fold_all(0, (acc: i32, x: i32): i32 => acc + x)], []);
}

function main(): i32 {
    var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
    var sums: ndarray.NdArray[i32] = a.map_rank(1, sum_cell);
    return sums.get([1]);
}`},
	// The struct monomorphiser mangled every type spelling on a declaration —
	// parameters, return, receiver, struct fields — but not the two a CALLABLE
	// parameter carries separately. `ms_func` rebuilt each ParamDecl with
	// `fn_ret` and `fn_param_types` copied through verbatim, so a clone of
	// `through[T, U](f: (Slot[T]) => Slot[U])` kept `Slot[i32]` where every
	// other position said `Slot__i32`. Resolving that spelling dropped the
	// argument, the callable's parameter typed as the bare `Slot`, and the
	// call of `f` refused `call argument type` — taking the whole module with
	// it. `mg_ty` now covers both, at every site the pass rewrites a
	// declaration, a lambda, a `var`, or a struct field.
	//
	// Both callback shapes are here deliberately: a named function and a
	// lambda reach the slot by different routes, and `U = string` over
	// `T = i32` means a spelling that lost its argument cannot pass as the
	// receiver's own.
	{name: "a-callback-slot-keeps-its-instantiation", atLeast: 5, noLeak: true, src: `
struct Slot[T] { v: T }

function (s: Slot[T]) through[T, U](f: (Slot[T]) => Slot[U]): Slot[U] {
    return f(s);
}

function label(s: Slot[i32]): Slot[string] {
    if (s.v > 5) { return Slot[string] { v: "big" }; }
    return Slot[string] { v: "small" };
}

function main(): i32 {
    var a: Slot[i32] = Slot[i32] { v: 7 };
    var b: Slot[string] = a.through(label);
    var c: Slot[string] = a.through((s: Slot[i32]): Slot[string] => Slot[string] { v: "lam" });
    print(b.v + "/" + c.v);
    return b.v.len();
}`},
	// `e as T` reaches `semsource.cast` as a unary naming T, and the operand
	// was evaluated with no destination at all — so `[] as i32[]` asked an
	// empty literal to name its own type and refused `unresolved array literal
	// type`, although T was in hand two lines down. Supplying it was not
	// enough on its own: `cast_admits` covers the usize/reference
	// reinterpretation and the numeric conversions and nothing else, so the
	// identity `i32[] as i32[]` refused on the cast contract instead.
	//
	// One rule answers both. A target the conversion vocabulary does not
	// convert TO is an ASCRIPTION — T is the operand's own type written down —
	// so T is the operand's destination, and a value already at T passes
	// through unchanged. A numeric target stays a conversion, which is what
	// `widen` pins: 260 as u8 is still 4, not 260.
	//
	// One such line held `examples/tests/ndarray_test` at 0 of 254: the
	// refusal left an AST-built function value behind, and
	// `semlower.ast_value_call` then refused the runner's `it` for calling a
	// value of matching arity, taking every test in the file with it (#9940).
	{name: "an-ascription-names-a-destination", atLeast: 3, noLeak: true, src: `
function total(xs: i32[]): i32 {
    var s: i32 = 0;
    for x in xs { s = s + x; }
    return s;
}

function widen(n: i32): i32 {
    return (n as u8) as i32;
}

function main(): i32 {
    var empty: i32[] = [] as i32[];
    var held: i32[] = [4, 5] as i32[];
    var opt: Option[i32] = None as Option[i32];
    var seen: i32 = 0;
    match (opt) { Some(v) => { seen = v; }, None => { seen = 1; } }
    return total(empty) + total(held) + total([6, 7] as i32[]) + widen(260) + seen;
}`},
	// std/array writes its surface as FREE functions named
	// `__method_Array_<field>` taking the receiver first, which the checker
	// resolves for `xs.<field>(...)` and the bundler prefixes with the module.
	// `folded_method` keyed only the OTHER spelling of the same surface — the
	// `(xs: T[]) <field>` receiver form the registration pass folds to
	// `__arrm_<field>` — so every one of std/array's helpers refused
	// `unsupported call target`, taking its whole module down.
	//
	// The lookup is a suffix match for the same reason the checker's
	// `has_array_method` is one: the contract's name ends with the convention
	// name rather than being it.
	//
	// `examples/tests/array_combinators_test` went 0 of 211 to 211 of 211 on
	// this, on one call to `join_with_last`.
	{name: "an-array-helper-is-a-free-function", atLeast: 50, noLeak: true, src: `
import "std/array" as array;

function main(): i32 {
    var words: string[] = ["a", "b", "c"];
    var joined: string = words.join_with_last(", ", " and ");
    var ns: i32[] = [3, 1, 4, 1, 5];
    var sums: i32[] = ns.cumsum();
    var pos: boolean = ns.every_positive();
    print(joined);
    var last: i32 = sums[sums.len() - 1];
    if (!pos) { return 0; }
    return joined.len() + last;
}`},
	// A generic ENUM's type arguments were dropped where the generic STRUCT
	// arm right beside them kept theirs: `type_from_ref_names` answered
	// `t_union(r.base)` for `Box[i32]`, so the checker typed it as the bare
	// `Box`. Anything reading that spelling back names the GENERIC, which the
	// enum monomorphiser has dropped by then — here the return `inferred_
	// lambda_ret` stamps on a hoisted closure, which reached semsource as
	// `unresolved result type: declared Box` and took the whole module with
	// it through `call target has no semantic contract: <fn>$clo0`.
	//
	// The struct arm's own comment already gives the reason to carry them,
	// and the reserved-enum arm above carries them too; the user enum was the
	// one shape left name-only. Assignability still ignores union args, so it
	// is checker-behaviour-neutral in the same way those are.
	//
	// `examples/tests/sim_driver_test` went 0 of 180 to 180 of 180 on this —
	// std/sim's `__pend[T](tok, next: async.Future[T])` returns
	// `Pending(tok, (woken: i32) => next)`, which is this program with more
	// around it.
	{name: "a-generic-enum-keeps-its-arguments", atLeast: 3, noLeak: true, src: `
enum Box[T] {
    Now(T),
    Later(i32, (i32) => Box[T])
}

function hold[T](tok: i32, next: Box[T]): Box[T] {
    return Later(tok, (w: i32) => next);
}

function main(): i32 {
    var b: Box[i32] = hold(3, Now(7));
    match (b) {
        Now(v) => { return v; },
        Later(t, k) => { return t + 100; }
    }
    return 0;
}`},
	// `xs[lo:hi]` on an array whose elements are COUNTED was refused outright,
	// in semsource and again in the graph verifier. The self-host lowers a
	// slice to `op_arr_slice`, a window COPY that duplicates every element
	// pointer, and nothing retained them — so releasing the copy decremented
	// boxes it never held a unit on. `__fern_arr_inc_elems` is the retain,
	// and `sole_owned_base` already makes it over the same copy; the slice
	// was the one caller that did not.
	//
	// Written in the `[T]` slice-view spelling because that is what the
	// language has: `all[1:3]` is a borrowed window, not an owned `T[]`, and
	// native rejects both `var mid: string[] = all[1:3]` (E003) and returning
	// one out of the frame that owns its storage (E063). The answer here,
	// 19, is native's.
	//
	// A missing retain is not a leak: it frees a box the source still holds.
	// Verified by dropping the retain alone and leaving the refusals gone —
	// the sanitizer leg then aborts with `use-after-free (touched a
	// quarantined block)`. `[i32[]]` is in it too, because an element that is
	// itself a counted array takes the same retain at a different width.
	//
	// `conformance/cases/slice_views` went 0 of 111 to 111 of 111 on this.
	{name: "a-slice-retains-the-elements-it-copied", atLeast: 3, noLeak: true, src: `
function total(ws: [string]) : i32 {
    var n: i32 = 0;
    for w in ws { n = n + w.len(); }
    return n;
}

function widths(rs: [i32[]]): i32 {
    var n: i32 = 0;
    for r in rs { n = n + r.len(); }
    return n;
}

function main(): i32 {
    var all: string[] = ["alpha", "beta", "gamma", "delta"];
    var mid: [string] = all[1:3];
    print(mid[0] + "/" + mid[1]);
    var grid: i32[][] = [[1], [2, 2], [3, 3, 3]];
    var tail: [i32[]] = grid[1:3];
    return total(mid) + widths(tail) + all[3].len();
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
	// A struct- or enum-KEYED map (#9962): a column of boxes the map owns one
	// unit of per entry, probed through the key's derived hash and eq. The
	// AST lowering cannot read a field off a key it iterates straight out of
	// `keys()`, so the last row is a `want` row, its answer confirmed against
	// the interpreter.
	{name: "keyed-map-probes", atLeast: 3, noLeak: true, src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B, C(string) }
function names(): i32 {
    var m: Map[Name, i32] = map_new(8);
    m = m.insert(Name { first: "ada", rank: 1 }, 10);
    m = m.insert(Name { first: "bob", rank: 2 }, 20);
    if (m.get_or(Name { first: "a" + "da", rank: 1 }, 0 - 1) != 10) { return 1; }
    if (!m.has(Name { first: "ada", rank: 1 })) { return 2; }
    m = m.insert(Name { first: "ada", rank: 1 }, 99);
    if (m.len() != 2) { return 3; }
    return m.get_or(Name { first: "ada", rank: 1 }, 0 - 1) - 90;
}
function tags(): i32 {
    var em: Map[Tag, i32] = map_new(8);
    em = em.insert(A(1), 1);
    em = em.insert(B, 2);
    em = em.insert(C("x" + "y"), 3);
    if (em.get_or(C("xy"), 0) != 3) { return 1; }
    if (em.get_or(A(2), 0 - 1) != 0 - 1) { return 2; }
    var total: i32 = 0;
    for (k, v) in em { total = total + v; }
    match (em.get(A(1))) { Some(v) => { if (v != 1) { return 3; } }, None => { return 4; } }
    return total;
}
function main(): i32 { return names() * 10 + tags(); }
`},
	// A keyed map's key column read back through keys(): the retaining
	// snapshot, walked after the map is gone.
	{name: "keyed-map-keys-outlive-the-map", atLeast: 2, noLeak: true, src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
function ranks(): i32 {
    var ks: Coord[] = [];
    {
        var m: Map[Coord, i32] = map_new(2);
        var i: i32 = 0;
        while (i < 6) { m = m.insert(Coord { a: i, b: i * 2 }, i); i = i + 1; }
        ks = m.keys();
    }
    var t: i32 = 0;
    for k in ks { t = t + k.b; }
    return t;
}
function main(): i32 { return ranks() - 3; }
`},
	// A keyed map over a column of BOXES, shared and written through: the
	// insert finds the box aliased and rebuilds it, retaining every key and
	// value the copy names, so both maps release cleanly. `_kfvf` takes the
	// key's release and the value's.
	{name: "keyed-map-of-boxes-shared", atLeast: 2, want: "28|", noLeak: true, src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
struct Box { n: i32, tag: string }
function boxes(): i32 {
    var m: Map[Coord, Box] = map_new(4);
    var i: i32 = 0;
    while (i < 20) { m = m.insert(Coord { a: i, b: i * 2 }, Box { n: i * 10, tag: "v" }); i = i + 1; }
    m = m.insert(Coord { a: 7, b: 14 }, Box { n: 777, tag: "w" });
    if (m.len() != 20) { return 1; }
    var shared: Map[Coord, Box] = m;
    m = m.insert(Coord { a: 99, b: 0 }, Box { n: 1, tag: "z" });
    if (shared.len() != 20 || m.len() != 21) { return 2; }
    var t: i32 = 0;
    match (m.get(Coord { a: 7, b: 14 })) { Some(c) => { t = c.n + c.tag.len(); }, None => { return 3; } }
    for k in shared.keys() { t = t + k.b % 3; }
    return t + shared.get_or(Coord { a: 3, b: 6 }, Box { n: 0, tag: "" }).n;
}
function main(): i32 { return boxes() % 100; }
`},
	// `without` over a counted column releases the removed entry's key and
	// value (#9970): a string key, a string value, and a keyed column over a
	// column of boxes, each read back after the delete and re-inserted once.
	{name: "map-delete-releases-the-entry", atLeast: 3, noLeak: true, src: `
import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
struct Box { n: i32, tag: string }
function by_string(): i32 {
    var m: Map[string, i32] = map_new(2);
    m = m.insert("a" + "x", 1);
    m = m.insert("b" + "y", 2);
    var (m2, gone) = m.without("ax");
    if (!gone) { return 0 - 1; }
    m2 = m2.insert("a" + "x", 3);
    return m2.len() * 10 + m2.get_or("ax", 0);
}
function by_value(): i32 {
    var m: Map[i32, string] = map_new(2);
    m = m.insert(1, "one" + "!");
    m = m.insert(2, "two" + "!");
    var (m2, gone) = m.without(1);
    if (!gone) { return 0 - 1; }
    return m2.get_or(2, "").len();
}
function by_key(): i32 {
    var m: Map[Coord, Box] = map_new(2);
    var i: i32 = 0;
    while (i < 5) { m = m.insert(Coord { a: i, b: i }, Box { n: i, tag: "t" + "!" }); i = i + 1; }
    var (m2, gone) = m.without(Coord { a: 2, b: 2 });
    if (!gone) { return 0 - 1; }
    if (m2.has(Coord { a: 2, b: 2 })) { return 0 - 2; }
    return m2.len() + m2.get_or(Coord { a: 4, b: 4 }, Box { n: 0, tag: "" }).n;
}
function main(): i32 { return by_string() + by_value() + by_key(); }
`},
	{name: "map-delete-and-clear", atLeast: 4, noLeak: true, src: `
import "core/map";
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
import "core/map";
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
import "core/map";
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
import "core/map";
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
	// The capture the closure writes is a Cell[i32] the closure and its creator
	// share (#9320), so every body produces and the total is the same on both
	// lowerings.
	{name: "capture-write", atLeast: 3, src: `
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
	// The temp kept to the types with a literal zero, and a function with a
	// defer returning a record, a tuple, a variant or a Result stayed on the
	// AST lowering: declared at an untyped 0, the temp was an i32 slot, so
	// "replacement type does not match its binding", and a returned Err had
	// no expected type to name its variant through. The temp now starts at
	// the zero of the return type, like a lifted binding. The value a return
	// hands back is bound beside the local it came from until the cleanup
	// runs (two_defers replaces both), and the sanitizer leg holds the boxes
	// to zero over 200 rounds.
	{name: "defer-return-temp-at-a-type-with-no-literal-zero", atLeast: 7, noLeak: true, src: `
struct P { a: i32, s: string }
enum Shape { Dot, Box(i32) }
function record(n: i32): P {
    var p: P = P { a: n, s: "p" };
    defer p = P { a: 0, s: "" };
    return p;
}
function pair(n: i32): (i32, string) {
    var t: (i32, string) = (n, "one");
    defer t = (0, "");
    return t;
}
function variant(n: i32): Shape {
    var s: Shape = Shape.Dot;
    defer s = Shape.Box(n);
    if (n % 2 == 0) { return Shape.Box(n); }
    return s;
}
function maybe(n: i32): Option[P] {
    var log: i32 = 0;
    defer log = log + 1;
    if (n < 0) { return None; }
    return Some(P { a: n, s: "m" });
}
function risky(fail: boolean): Result[i32, i32] {
    var code: i32 = 0;
    errdefer { code = code + 7; }
    if (fail) { return Err(code + 1); }
    return Ok(50);
}
function two_defers(n: i32): P {
    var p: P = P { a: n, s: "x" };
    defer { p = P { a: 0, s: "" }; }
    var q: P = p;
    defer q = P { a: 5, s: "q" };
    return P { a: p.a + q.a, s: p.s + q.s };
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var pr: (i32, string) = pair(i);
        t = t + record(i).a + pr.0 + pr.1.len();
        match (variant(i)) { Shape.Dot => { t = t + 1; }, Shape.Box(v) => { t = t + v; } }
        match (maybe(i - 100)) { Some(p) => { t = t + p.a + p.s.len(); }, None => { t = t + 2; } }
        match (risky(i % 3 == 0)) { Ok(v) => { t = t + v; }, Err(e) => { t = t + e; } }
        t = t + two_defers(i).s.len();
        i = i + 1;
    }
    return t % 97;
}
`},
	// A function-typed return with a defer: the temp is a fn slot started at
	// its zero, and the lambda a return hands it is boxed at the assignment.
	// Both legs bind the temp a closure local from its annotation, so the
	// caller dispatches the box env-first; through_local returns a box a
	// declaration built, capturing and plain return one at the return.
	{name: "defer-in-a-closure-factory", atLeast: 7, noLeak: true, src: `
function capturing(k: i32): (i32) => i32 {
    var seen: i32 = 0;
    defer seen = seen + 1;
    return (x: i32): i32 => x + k;
}
function through_local(k: i32): (i32) => i32 {
    var f: (i32) => i32 = (x: i32): i32 => x * k;
    var seen: i32 = 0;
    defer seen = seen + 1;
    return f;
}
function plain(): (i32) => i32 {
    var n: i32 = 0;
    defer n = 1;
    return (x: i32): i32 => x + 4;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        t = t + capturing(i)(1) + plain()(i) + through_local(i)(2);
        i = i + 1;
    }
    return t % 97;
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
	{name: "stdin-and-the-stream-handles", atLeast: 51, stdin: "alpha\nbeta\n", src: `
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
	// A function value whose call yields nothing: the callback of a for_each.
	// Its result is the one word every void body hands back, so the type is
	// admitted and the call stands in statement position like any void call.
	// The value reaches the call every way a function value can: a parameter,
	// a local, a record field, a tuple element, a lambda and a bare name (the
	// lift's trampoline for a void target calls it as a statement).
	{name: "void-callback", atLeast: 7, src: `
function each(xs: string[], f: (string) => void): void {
    for x in xs { f(x); }
}
function show(x: string): void { print(x); }
struct Visitor { hit: (string) => void }
function main(): i32 {
    each(["a", "b"], show);
    each(["c"], (x: string): void => { print(x + "!"); });
    var v: Visitor = Visitor { hit: show };
    v.hit("d");
    var g: (string) => void = show;
    g("e");
    var pair: ((string) => void, i32) = (show, 1);
    pair.0("f");
    return pair.1;
}
`},
	// A function value as a VARIANT field, the shape of std/async's
	// `Future[T].Pending(i32, (i32) => Future[T])`: the variant takes a unit
	// of the value and its release walks the captures, the same walk a record
	// field takes (ssarc.drop_variant_fields drops a variant's fields as a
	// record's). Nested in an array or tuple it stays refused, as for a
	// record. `dropped` is a chain never run, so its closures are released by
	// the walk alone; the AST lowering releases none of them (#9841), so the
	// pin here is absolute.
	{name: "closure-in-a-variant-field", atLeast: 4, noLeak: true, src: `
enum Step {
    Done(i32),
    Next(i32, (i32) => Step),
}
function make(n: i32, k: i32): Step {
    if (n <= 0) { return Done(k); }
    return Next(n, (x: i32): Step => make(n - 1, k + x));
}
function run(s: Step): i32 {
    var cur: Step = s;
    var guard: i32 = 0;
    while (guard < 100) {
        match (cur) {
            Done(v) => { return v; },
            Next(w, f) => { cur = f(w); }
        }
        guard = guard + 1;
    }
    return -1;
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        total = total + run(make(4, i));
        i = i + 1;
    }
    var dropped: Step = make(2, 7);
    return total;
}
`},
	// The rest of the OS floor (semsource.os_floor_contracts): the signal,
	// priority, group and process-group builtins, chroot, poll over a
	// timerfd. Each arm counts whichever way the host answers, so the count
	// is the same in a container and on a developer machine, and the pin is
	// that both lowerings answer alike and the produced bodies free no less.
	{name: "os-floor-signals-and-process", atLeast: 2, nativeOnly: true, src: `
function signals(): i32 {
    var n: i32 = 0;
    if (signal_ignore(2) >= 0) { n = n + 1; }
    if (signal_disposition(2) >= 0) { n = n + 1; }
    if (signal_default(2) >= 0) { n = n + 1; }
    var was: i64 = signal_mask(0, 0i64);
    if (was >= 0i64) { n = n + 1; }
    if (priority() > -21) { n = n + 1; }
    var gs: i64[] = getgroups();
    if (gs.len() >= 0) { n = n + 1; }
    match (setgroups(gs)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (set_process_group(0, 0)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (chroot("/")) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    var fds: i32[] = [timer_fd(1)];
    if (poll(fds, 2000) > 0) { n = n + 1; }
    if (poll([], 0) == 0) { n = n + 1; }
    return n;
}
function main(): i32 { return signals(); }
`},
	// The sockets, `sync` and the set-id builtins, which no other row reaches:
	// a loopback listener on an ephemeral port (a fixed one sits in TIME_WAIT
	// for the leg that runs next), a connection, an accept, one send and its
	// receive as a fresh u8[], a connection's readiness token (the fd itself
	// on native), the three closes, `sync` as a statement, and
	// the set-id calls asking for root, which a host refuses or grants and
	// the row counts either way.
	{name: "os-floor-sockets-and-ids", atLeast: 2, nativeOnly: true, src: `
function sockets(): i32 {
    var n: i32 = 0;
    var listener: i32 = tcp_listen(0);
    if (listener >= 0) {
        var host_be: i32 = 127 | (1 << 24);
        var c: i32 = tcp_connect(host_be, tcp_local_port(listener));
        if (c >= 0) {
            var a: i32 = tcp_accept(listener);
            if (a >= 0) {
                if (tcp_send(c, "ping") == 4) { n = n + 1; }
                if (tcp_pollable(c) == c) { n = n + 1; }
                var got: u8[] = tcp_recv(a, 16);
                if (got.len() == 4 && got[0] as i32 == 112) { n = n + 1; }
                if (tcp_close(a) >= 0) { n = n + 1; }
            }
            if (tcp_close(c) >= 0) { n = n + 1; }
        }
        if (tcp_close(listener) >= 0) { n = n + 1; }
    }
    sync();
    match (setuid(0i64)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (setgid(0i64)) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    match (set_priority(priority())) {
        Ok(_) => { n = n + 1; },
        Err(_) => { n = n + 1; }
    }
    return n;
}
function main(): i32 { return sockets(); }
`},
	// The file-time, node, permission and directory ops over a temp_dir, and
	// the terminal questions asked of a descriptor and of a handle. mknod
	// makes a FIFO, the one node an unprivileged caller may create.
	{name: "os-floor-times-nodes-terminal", atLeast: 3, nativeOnly: true, src: `
function files(dir: string): i32 {
    var n: i32 = 0;
    var f: string = dir + "/f";
    match (write_file(f, "x")) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (chmod_at(f, 420, false)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (set_file_times(f, 1000000i64, 0i64, 2000000i64, 0i64, 0)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (stat(f)) {
        Ok(st) => { if (st.mtime == 2000000i64) { n = n + 1; } },
        Err(_) => {}
    }
    match (mknod(dir + "/fifo", 4096 + 420, 0, 0)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (chdir(dir)) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (remove_file("fifo")) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    match (remove_file("f")) {
        Ok(_) => { n = n + 1; },
        Err(_) => {}
    }
    return n;
}
function terminal(): i32 {
    var n: i32 = 0;
    match (window_size(1)) {
        Ok(w) => { if (w.rows >= 0i64) { n = n + 1; } },
        Err(_) => { n = n + 1; }
    }
    var r: Reader = stdin();
    if (!r.isatty()) { n = n + 1; }
    match (r.window_size()) {
        Ok(w) => { if (w.cols >= 0i64) { n = n + 1; } },
        Err(_) => { n = n + 1; }
    }
    match (open_reader("/dev/null")) {
        Ok(h) => {
            match (h.dup_onto(19)) {
                Some(_) => {},
                None => { n = n + 1; }
            }
        },
        Err(_) => {}
    }
    return n;
}
function main(): i32 {
    var n: i32 = 0;
    match (temp_dir("fernsem")) {
        Ok(d) => {
            n = n + files(d);
            match (remove_dir(d)) {
                Ok(_) => {},
                Err(_) => {}
            }
        },
        Err(_) => {}
    }
    return n + terminal();
}
`},
	// `xs[lo:hi]` on an array of scalars: the checker types it `[T]`, the
	// runtime copies the window into a fresh array (arr_slice), and the typed
	// lowering produces it as an owned value of the source's type, bounds
	// left to the runtime as the AST lowering leaves them. An open end reads
	// the source's length. Every element width the copy distinguishes is
	// here: u8 and i32 (4-byte on wasm), i64 and f64 (8-byte). The slices
	// handed straight to `sum` are the ones the AST lowering never releases
	// (#9843), so the pin is absolute.
	{name: "array-slice-of-scalars", atLeast: 2, noLeak: true, src: `
function sum(xs: [u8]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { t = t + (xs[i] as i32); i = i + 1; }
    return t;
}
function main(): i32 {
    var bytes: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
    var total: i32 = 0;
    var i: i32 = 0;
    while (i + 3 <= 10) { total = total + sum(bytes[i:i + 3]); i = i + 3; }
    total = total + sum(bytes[i:]);
    var words: i32[] = [10, 20, 30, 40];
    var mid: [i32] = words[1:3];
    var wide: i64[] = [100i64, 200i64, 300i64];
    var tail: [i64] = wide[1:];
    var fl: f64[] = [1.5, 2.5, 3.5];
    var head: [f64] = fl[0:2];
    return total + mid[0] + mid.len() + (tail[1] as i32) + tail.len() + (head[1] as i32);
}
`},
	// The OS-floor builtins that answer a fresh string, string array or
	// record: their runtime helpers used to keep the scratch block each call
	// filled (the 4 KiB path buffer, the 64 KiB drain buffers), so nothing
	// could pin these at zero (#9832). The helpers own their scratch now, so
	// the typed lowering, which releases the results, frees everything. The
	// AST lowering still never releases the fresh result, which the relative
	// pin tolerates and this row records.
	// A mutable capture is a Cell[T] on both lowerings (#9320): the scalars the
	// closure writes (every scalar kind, not only i32 and boolean, which was
	// all the box scan admitted and cost an i64 or f64 its writes), a string
	// the creator rebinds under a closure that reads it, and a Cell[i64]
	// parameter read in i64 arithmetic, which the AST lowering used to bail
	// on, and a string the creator rebinds under a closure that ESCAPES, which
	// the typed lowering used to read at its creation-time value. Both
	// lowerings are pinned to the native answer, since before the change they
	// agreed with each other on the wrong one.
	{name: "a-capture-the-closure-writes-is-a-cell", atLeast: 7, want: "42|", astAnswers: "42|", noLeak: true, src: `
function tally(): i32 {
    var n: i32 = 0;
    var wide: i64 = 100i64;
    var ratio: f64 = 1.5;
    var flag: boolean = false;
    var byte: u8 = 250u8;
    var bump = (k: i32): i32 => {
        n = n + k;
        wide = wide + (k as i64);
        ratio = ratio * 2.0;
        flag = !flag;
        byte = byte + 3u8;
        return n;
    };
    var a: i32 = bump(1);
    var b: i32 = bump(2);
    var t: i32 = 0;
    if (flag) { t = 1000; }
    return a + b + n + (wide as i32) + (ratio as i32) + t + (byte as i32);
}
function counter(c: Cell[i64]): i32 {
    c.set(c.get() + 1i64);
    return c.get() as i32;
}
function apply(f: () => i32): i32 { return f(); }
function rebound(): i32 {
    var s: string = "a";
    var read = (): i32 => { return s.len(); };
    var i: i32 = 0;
    var n: i32 = 0;
    while (i < 3) { s = s + "bb"; n = n + apply(read); i = i + 1; }
    return n;
}
function main(): i32 {
    var c: Cell[i64] = cell_new(4i64);
    var k: i32 = counter(c) + counter(c);
    return (tally() + k + rebound()) % 100;
}
`},
	{name: "os-floor-fresh-results-are-freed", atLeast: 3, noLeak: true, nativeOnly: true, src: `
function leaves(dir: string, p: string): i32 {
    var n: i32 = 0;
    match (create_dir(p + ".d", 493)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (remove_dir(p + ".d")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (create_link(p, p + ".h")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (rename(p + ".h", p + ".r")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (remove_file(p + ".r")) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chmod(p, 420)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chmod_at(p, 420, true)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chown_at(p, 0 - 1, 0 - 1, true)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (set_file_times(p, 1000i64, 0i64, 1000i64, 0i64, 0)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    match (chdir(dir)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 2; } }
    return n;
}
function each(dir: string): i32 {
    var n: i32 = 0;
    var cwd: string = getcwd();
    n = n + cwd.len() % 7;
    var host: string = hostname();
    n = n + host.len() % 7;
    var sys: string = uname_field(0);
    n = n + sys.len() % 7;
    var env: string[] = environ();
    n = n + env.len() % 7;
    match (create_symlink("target", dir + "/lnk")) {
        Ok(_) => {
            match (read_link(dir + "/lnk")) {
                Ok(t) => { n = n + t.len(); },
                Err(_) => {}
            }
        },
        Err(_) => { n = n + 1; }
    }
    match (create_symlink("target", dir + "/missing/lnk")) {
        Ok(_) => {},
        Err(_) => { n = n + 2; }
    }
    match (write_file(dir + "/f", "hello")) {
        Ok(_) => {
            match (truncate(dir + "/f", 3i64)) {
                Ok(_) => { n = n + 3; },
                Err(_) => {}
            }
        },
        Err(_) => {}
    }
    match (termios_get(0)) {
        Ok(words) => {
            match (termios_set(0, 0, words)) {
                Ok(_) => { n = n + 4; },
                Err(_) => { n = n + 5; }
            }
        },
        Err(_) => { n = n + 6; }
    }
    n = n + leaves(dir, dir + "/f") + leaves(dir, dir + "/missing/f");
    var rb: u8[] = random_bytes(24);
    n = n + rb.len();
    if (cpu_count() >= 0) { n = n + 1; }
    var gs: i64[] = getgroups();
    if (gs.len() >= 0) { n = n + 1; }
    var r: ProcessResult = subprocess("echo", ["hi", "there"], "");
    n = n + r.exit_code + r.stdout.len() + r.stderr.len();
    return n;
}
function main(): i32 {
    var n: i32 = 0;
    match (temp_dir("fernfresh")) {
        Ok(d) => {
            var i: i32 = 0;
            while (i < 3) { n = n + each(d); i = i + 1; }
            match (remove_dir_all(d)) {
                Ok(_) => {},
                Err(_) => {}
            }
        },
        Err(_) => {}
    }
    return n % 100;
}
`},
	// A map READ's key is hashed and compared and no column takes a unit of
	// it, so a `str` view is an ordinary lend there and takes the retag a
	// borrowed string parameter offers. Only an INSERT's key joins the key
	// column, where the map may outlive the bytes the view borrows, so only
	// that one is refused.
	{name: "a-view-is-a-map-read-key", atLeast: 46, noLeak: true, src: `
import "core/map";
import "std/string";
function tally(text: string, m: Map[string, i32]): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i + 2 <= text.len()) {
        var w: str = slice_unchecked(text, i, i + 2);
        n = n + m.get_or(w, 0);
        i = i + 2;
    }
    return n;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var m: Map[string, i32] = map_new(8);
        m = m.insert("ab", 3);
        m = m.insert("cd", 5);
        var text: string = "ab" + "cd" + "ef";
        acc = acc + tally(text, m);
        r = r + 1;
    }
    return acc % 113;
}`},
	// #9874: `replace` used to hand back the haystack's own box on both its
	// early paths, so the caller released a unit it never took. The argument
	// has to be a temporary nobody else names — a second name on the box
	// absorbs the extra release and hides the fault. Both early paths are
	// here: `blank` takes the empty-needle one, `sq` the no-match one.
	//
	// The over-release COUNT is printed rather than left to the leak pins,
	// which engage only on the sanitize leg. wasm keeps its own hand-written
	// WAT body for this builtin, so the register legs' fix says nothing about
	// it: before the wasm half of the fix this program printed 25 over-releases
	// on the typed leg against its own AST leg's 0.
	{name: "a-builtin-string-result-is-never-its-argument", atLeast: 111, noLeak: true, src: `
import "std/string";
import "std/io";
function sq(s: string): string { return s.replace("Q", "Z"); }
function blank(s: string): string { return s.replace("", "Z"); }
function words(n: i32): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) { out = out + " x"; i = i + 1; }
    return out;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        acc = acc + sq(words(r % 4)).len() + sq(words(r % 3) + "Q").len() + blank(words(r % 2)).len();
        r = r + 1;
    }
    print("over-releases " + __rc_underflow_count().to_string());
    return acc % 109;
}`},
	// An unannotated binding of a CALL takes its type from the call, not from
	// the checker: `var m = s.map(f)` on a generic receiver is a shape the
	// checker leaves `not yet checked`, and the instance the call resolves is
	// what settles it. Every declaration here refused before, through the
	// binding, so `std/result`'s whole combinator surface stood on the AST
	// lowering.
	{name: "an-unannotated-binding-takes-its-call-s-type", atLeast: 57, noLeak: true, src: `
import "std/option";
import "std/result";
function mapped(): i32 {
    var s: Option[i32] = Some(5);
    var m = s.map((x: i32): i32 => { return x * 2; });
    return m.unwrap_or(0);
}
function chained(): i32 {
    var r: Result[i32, string] = Ok(7);
    var d = r.map((x: i32): i32 => { return x + 1; });
    var e = d.map_err((m: string): string => { return m + "!"; });
    return e.unwrap_or(0);
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 20) { acc = acc + mapped() + chained(); i = i + 1; }
    return acc % 113;
}`},
	// A destination that names the union but settles nothing in it is no more
	// use than none at all. `Option.and` is `and[U](other: Option[U])`, so the
	// parameter binds U from this very argument: `Option[U]` names `Some`
	// without saying what `Some` holds, and only the payload can say. The
	// checker does not settle it either, since it infers the literal from the
	// same parameter.
	{name: "a-variant-literal-types-itself-where-the-parameter-cannot", atLeast: 52, noLeak: true, src: `
import "std/option";
function both(): i32 {
    var s: Option[i32] = Some(5);
    var other: Option[i32] = Some(9);
    return s.and(Some(9)).unwrap_or(0) + s.and(other).unwrap_or(0);
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 20) { acc = acc + both(); i = i + 1; }
    return acc % 107;
}`},
	// A VARIANT names a value, not a type. `Shape.Circle(1)` is a qualified
	// path and `Empty.to_string()` is a method call on the payloadless
	// literal, and the two are the same shape in the tree — `<ident>.<name>`
	// — so only the enum owner tells them apart. Reading the variant as a
	// type head sent the method call down the qualified-path arm, which asked
	// the contract table for `Empty.to_string` and found nothing. Every other
	// spelling of the same call already worked: a binding of the variant, an
	// annotated binding, and a payloaded `Circle(1).to_string()`.
	{name: "a-variant-is-a-value-not-a-type-head", atLeast: 108, noLeak: true, src: `
import "core/cmp";
@derive(cmp.Eq, cmp.Display, cmp.Ord)
enum Shape { Circle(i32), Square(i32), Empty }
function direct(): string { return Empty.to_string(); }
function qualified(): string { return Shape.Circle(1).to_string(); }
function bound(): string { var e: Shape = Empty; return e.to_string(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 20) { acc = acc + direct().len() + qualified().len() + bound().len(); i = i + 1; }
    return acc % 101;
}`},
	{name: "a-method-reads-its-receiver-by-name", atLeast: 107, noLeak: true, src: `
import "std/json";
@derive(json.Json)
struct Bag { items: i32[], names: string[] }
struct Held { xs: f64[] }
function (self: Held) render(): string { return self.xs.to_json(); }
function loose(b: Bag): string { return b.items.to_json(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var bag: Bag = Bag { items: [1, 2, 3], names: ["a", "b"] };
        var h: Held = Held { xs: [1.5, 2.5] };
        var nums: i32[] = [10, 20];
        acc = acc + bag.to_json().len() + h.render().len() + loose(bag).len() + nums.to_json().len();
        i = i + 1;
    }
    if (Bag { items: [1, 2, 3], names: ["a", "b"] }.to_json() != "{\"items\":[1,2,3],\"names\":[\"a\",\"b\"]}") { return 1; }
    if (Held { xs: [1.5, 2.5] }.render() != "[1.5,2.5]") { return 2; }
    return acc % 101;
}`},
	{name: "a-composite-compares-through-its-own-method", atLeast: 55, noLeak: true, src: `
import "core/cmp";
@derive(cmp.Eq, cmp.Ord)
struct Point { x: i32, y: string }
@derive(cmp.Eq, cmp.Ord)
enum Shape { Dot, Line(i32), Box(i32, string) }
@derive(cmp.Eq, cmp.Ord)
struct Holder[T] { v: T }
function same[T](a: Holder[T], b: Holder[T]): boolean { return a == b; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var a: Point = Point { x: 2, y: "hi" };
        var b: Point = Point { x: 2, y: "hi" };
        var c: Point = Point { x: 2, y: "hj" };
        if (!(a == b)) { return 1; }
        if (a == c) { return 2; }
        if (!(a != c)) { return 3; }
        if (!(a < c)) { return 4; }
        if (a < b) { return 5; }
        if (!(a <= b)) { return 6; }
        if (!(c > a)) { return 7; }
        if (!(a >= b)) { return 8; }
        if (!(Box(2, "a") == Box(2, "a"))) { return 9; }
        if (Box(2, "a") == Box(2, "b")) { return 10; }
        if (!(Box(2, "a") < Box(2, "b"))) { return 11; }
        if (!(Line(1) < Box(0, ""))) { return 12; }
        if (!(Dot == Dot)) { return 13; }
        if (Dot == Line(0)) { return 14; }
        var p: Holder[i32] = Holder { v: 1 };
        var q: Holder[i32] = Holder { v: 2 };
        var r: Holder[string] = Holder { v: "a" };
        if (!same(p, Holder { v: 1 })) { return 15; }
        if (same(p, q)) { return 16; }
        if (!(p < q)) { return 17; }
        if (!same(r, Holder { v: "a" })) { return 18; }
        acc = acc + 1;
        i = i + 1;
    }
    return acc + 22;
}`},
	{name: "a-u8-converts-to-and-from-a-float", atLeast: 1, noLeak: true, src: `
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 256) {
        var b: u8 = i as u8;
        var f: f64 = b as f64;
        if ((f as u8) != b) { return 1; }
        if ((f as i32) != i) { return 2; }
        acc = acc + ((f as i32) & 1);
        i = i + 1;
    }
    if ((300.7 as u8) != 44u8) { return 3; }
    if ((255.9 as u8) != 255u8) { return 4; }
    if ((0.5 as u8) != 0u8) { return 5; }
    var w: u8 = 200u8;
    if ((w as f64) * 2.0 != 400.0) { return 6; }
    if ((w as f32) != 200.0) { return 7; }
    return acc;
}`},
	// `use NAME <- CALL;` with no annotation on NAME. The parser appends the
	// rest of the block as a callback lambda whose parameter carries no type,
	// and the lift kept it that way, so the `$wrapN` body returned a value of
	// no type and the declaration was refused. checker.pretype_module now
	// stamps the parameter from the callee's callback slot (native
	// inferUseParam), before anything is typed against it. Produces 0 of 3
	// without the stamp.
	{name: "use-binding-typed-from-its-callee", atLeast: 3, noLeak: true, src: `
function with_doubled(n: i32, k: (i32) => i32): i32 { return k(n * 2); }

function main(): i32 {
    use v <- with_doubled(21);
    return v;
}
`},
	// The binding is a string, read through a method: the stamp carries the
	// callee's spelling, not a scalar guess.
	{name: "use-binding-is-a-string", atLeast: 3, noLeak: true, src: `
function taker(f: (string) => i32): i32 { return f("hi"); }

function main(): i32 {
    use x <- taker();
    return x.len() + 23;
}
`},
	// The `use` sits inside an arrow lambda's block body (the shape
	// conformance/cases/arrow_lambda_block_body pins), so the stamp runs in
	// the lambda's own scope and the lifted body is typed twice over.
	{name: "use-binding-inside-a-lambda", atLeast: 4, noLeak: true, src: `
function give(x: i32, cb: (i32) => i32): i32 { return cb(x); }

function main(): i32 {
    var bound = (): i32 => {
        use n <- give(41);
        return n + 1;
    };
    return bound();
}
`},
	// keys() and values() over narrow scalar columns. The runtime snapshot
	// bit-copies a column of i32-shaped cells into a fresh array, which is as
	// true of a `u8`, `u32` or `boolean` column as of the `i32` one
	// ssasem.retained_column admitted alone; the rest were refused as an
	// alias of the map's own elements, which is the refusal that kept
	// conformance/cases/map_narrow_int_keys on the AST lowering (#9550). The
	// AST lowering is the oracle here: native materialises a `u8` column at
	// the wrong stride (#10000), so its answer is not the one to pin.
	{name: "narrow-scalar-columns-snapshot", atLeast: 1, noLeak: true, src: `
import "core/map";

function main(): i32 {
    var m: Map[u8, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 12) { m = m.insert(i as u8, i * 2); i = i + 1; }
    var ks: u8[] = m.keys();
    var s: i32 = 0;
    for k in ks { s = s + (k as i32); }
    var w: Map[i32, u8] = map_new(2);
    i = 0;
    while (i < 12) { w = w.insert(i, (i * 2) as u8); i = i + 1; }
    for v in w.values() { s = s + (v as i32); }
    var b: Map[i32, boolean] = map_new(2);
    i = 0;
    while (i < 12) { b = b.insert(i, i % 3 == 0); i = i + 1; }
    for f in b.values() { if (f) { s = s + 10; } }
    return s - 200;
}
`},
	// Explicit type arguments at a call. The parser erases them from the
	// argument list and keeps only their count (`type_argc`, which E040 checks
	// against the declaration), and the instantiation is inferred from the
	// arguments and the destination exactly as it is without them; the three
	// call paths refused any count above zero as a `call arity`, which is what
	// kept conformance/cases/trailing_commas on the AST lowering (#9550). A
	// variable the arguments and destination leave unbound is still refused,
	// as `unbound type variable`.
	{name: "explicit-type-arguments-at-a-call", atLeast: 2, noLeak: true, src: `
function pick[T](xs: T[], i: i32): T { return xs[i]; }

function main(): i32 {
    var xs: i64[] = [1, 2, 3];
    var ys: i32[] = [4, 5, 6];
    return (pick[i64](xs, 1) as i32) + pick[i32](ys, 2,) + 34;
}
`},
	// A labelled range loop. `for i in LOW..HIGH` is desugared to a counting
	// while ahead of every consumer, and the while was built without the
	// source label, so a `continue outer` or `break scan` inside it named a
	// loop the semantic source could not find (`loop exit names no enclosing
	// loop`; conformance/cases/labeled_loops, #9550).
	{name: "labelled-range-loop", atLeast: 1, noLeak: true, src: `
function main(): i32 {
    var sum: i32 = 0;
    outer: for i in 0..4 {
        for j in 0..4 {
            if (j == 2) { continue outer; }
            sum = sum + 1;
        }
        sum = sum + 100;
    }
    var k: i32 = 0;
    scan: for a in 0..10 {
        while (true) {
            k = k + 1;
            if (k == 5) { break scan; }
        }
    }
    return sum + k;
}
`},
	// Character literals. The lexer tags one with the type name `char` where
	// an integer literal carries a numeric suffix, and semsource's literal_type
	// knew no such suffix, so every module with a `'x'` was refused as an
	// `unsupported literal width`; ssasem's verifier then refused the constant
	// as an integer of no integer type (conformance/cases/char_byte_literals,
	// #9550). A char is a 32-bit cell carrying a code point; it converts to and
	// from every integer width, and compares as one.
	{name: "char-literals", atLeast: 3, noLeak: true, src: `
const NEWLINE: char = '\n';

function upper_ascii(c: char): char {
    var n: i32 = c as i32;
    if (n >= 97 && n <= 122) { return (n - 32) as char; }
    return c;
}

function main(): i32 {
    var c: char = 'x';
    if (upper_ascii(c) != 'X') { return 1; }
    if (upper_ascii('Q') != 'Q') { return 2; }
    if ((NEWLINE as i32) != 10) { return 3; }
    if (('\u{1F600}' as i32) != 128512) { return 4; }
    var back: char = 65 as char;
    if (back != 'A') { return 5; }
    return (c as i32) - 78;
}
`},
	// Operator overloading on a struct: `a + b` is `a.add(b)` and `-a` is
	// `a.neg()`, the desugar the native checker applies and the AST lowering
	// mirrors (#2706). The semantic source dispatched only the comparisons
	// that way and refused every arithmetic operator on a nominal operand
	// with `operator contract: V + V` (conformance/cases/op_overload_nested,
	// #9550). The nested form leaves two intermediate records live across the
	// outer call, which the leak pin covers.
	{name: "arithmetic-operators-on-a-struct", atLeast: 5, noLeak: true, src: `
struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
function (a: V) sub(b: V): V { return V { x: a.x - b.x }; }
function (a: V) mul(b: V): V { return V { x: a.x * b.x }; }
function (a: V) neg(): V { return V { x: 0 - a.x }; }

function main(): i32 {
    var a: V = V { x: 5 };
    var b: V = V { x: 3 };
    var d: V = (a + b) * (a - b);
    var e: V = -(a - b);
    return d.x + e.x + 28;
}
`},
	// A lambda bound to a local that returns a lambda, with no result written.
	// The checker left a function-valued result unspelled, so the call of the
	// call (`mk()(3)`) had no function type to dispatch through and main was
	// refused ("callee is neither a name nor a field"). The result is stamped
	// `fn` with its signature sidecars now (#10025, first half). The returned
	// lambda captures nothing; a capturing one is the issue's second half.
	{name: "a-lambda-returning-a-lambda-is-called-through-its-result", atLeast: 3, noLeak: true, src: `
function main(): i32 {
    var mk = () => { return (b: i32): i32 => b * 2; };
    var f = mk();
    var add = (n: i32) => { return (a: i32, b: i32): i32 => a + b; };
    return f(4) + mk()(3) + add(0)(20, 8);
}
`},
	// A view result reads the parameter it is anchored to, so the caller keeps
	// that argument alive while the result lives: a temporary receiver is
	// released after the last read of the view rather than after the call,
	// and the anchor passes through `pick`, whose result is `tail`'s. Each
	// trip allocates the next string where the released one was, so a
	// dangling view prints it (conformance/cases/alloc_flat_method_identity_return).
	{name: "a-view-result-is-anchored-to-its-argument", atLeast: 3, noLeak: true, src: `
import "std/i32";
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function pick(a: string, n: i32): str { return a.tail(n); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var r: str = ("abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string()).tail(i);
        var junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZYYYY" + i.to_string();
        var local: string = "0123456789012345678901234567890ab" + i.to_string();
        var q: str = pick(local, 2);
        var junk2: string = "WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWVVVV" + i.to_string();
        print(r);
        print(q);
        t = t + r.len() + q.len() + junk.len() + junk2.len();
        i = i + 1;
    }
    return t - 194;
}
`},
	// A checked slice wraps its view in a fresh Option, and the Option reads the
	// source's bytes as the view did: the local stays alive until the last read
	// of the payload rather than dying at its own last use, the slice. Each trip
	// allocates the next string where a released one was, so a dangling view
	// prints it.
	{name: "a-slice-view-keeps-its-local-alive", atLeast: 1, noLeak: true, src: `
import "std/i32";
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string();
        match (s[0:30]) {
            Some(v) => {
                var junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ" + i.to_string();
                print(v);
                t = t + v.len() + junk.len();
            },
            None => { t = t + 1; }
        }
        i = i + 1;
    }
    return t - 204;
}
`},
	// `?` on the Option a slice built reads a view out of a box nothing else
	// holds, so it takes the payload with no count to test and releases the box
	// alone; the Option the function returns is anchored to the parameter its
	// view reads (conformance/cases/string_slice_option).
	{name: "an-option-view-result-takes-its-payload", atLeast: 2, noLeak: true, src: `
import "std/i32";
function first_three(s: string): Option[str] {
    var v: str = s[0:3]?;
    return Some(v);
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        match (first_three("abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string())) {
            Some(v) => {
                var junk: string = "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ" + i.to_string();
                print(v);
                t = t + v.len() + junk.len();
            },
            None => { t = t + 100; }
        }
        match (first_three("ab")) {
            Some(v) => { t = t + 100; },
            None => { t = t + 1; }
        }
        i = i + 1;
    }
    return t - 126;
}
`},
	// A str loop variable that starts as a view of a parameter and is then
	// reassigned from an owned string, std/string's drop, merges two sources
	// at its phi. Each operand is a counted string's own box, so the phi's unit
	// carries the bytes and anchors nothing (examples/cli/fold.fern).
	{name: "a-view-of-counted-strings-carries-its-own-bytes", atLeast: 2, noLeak: true, src: `
import "std/string";
function fold_line(line: string, width: i32): i32 {
    var n: i32 = 0;
    var rest: str = line;
    while (rest.len() > width) {
        print(rest.take(width));
        rest = rest.drop(width);
        n = n + 1;
    }
    print(rest);
    return n;
}
function main(): i32 {
    return fold_line("the quick brown fox jumps over the lazy dog", 10) - 4;
}
`},
	// A view of either of two parameters has no one argument its caller could
	// keep alive for it. (A view of a local is the checker's E065.)
	{name: "a-view-result-of-either-parameter-is-refused", atLeast: 0, refuses: "view result escapes its source", src: `
import "std/i32";
function either(a: string, b: string, first: boolean): str {
    if (first) { return a; }
    return b;
}
function main(): i32 {
    var v: str = either(7.to_string(), 42.to_string(), false);
    return v.len() - 2;
}
`},
	// A checked string slice is an Option[str], and binds and returns as one.
	// The driver's pre-lowering check typed it as its source string and
	// refused both with E003 and E002, where native accepts them.
	{name: "a-checked-slice-is-an-option", atLeast: 2, noLeak: true, src: `
function f(t: string): Option[str] { return t[0:2]; }
function main(): i32 {
    var s: string = "abcdef";
    var o: Option[str] = s[1:3];
    var n: i32 = 0;
    match (o) { Some(v) => { print(v); n = n + v.len(); }, None => { n = n + 10; } }
    match (f(s)) { Some(v) => { print(v); n = n + v.len(); }, None => { n = n + 10; } }
    match (s[5:9]) { Some(v) => { n = n + 10; }, None => { n = n + 1; } }
    return n - 5;
}
`},
	// A record holding a function value reaches, through the field, the
	// records the function takes and hands back, so a body that names only
	// the record still carries their schemas (examples/proposals/pipeline.fern).
	{name: "a-function-field-names-its-signature-records", atLeast: 3, noLeak: true, src: `
struct Ctx { value: i32 }
struct Fault { why: string }
struct Stage { name: string, run: (Ctx) => Result[Ctx, Fault] }
function names(a: Stage[]): i32 { return a.len(); }
function main(): i32 {
    var xs: Stage[] = [Stage { name: "a", run: (c: Ctx): Result[Ctx, Fault] => Ok(c) }];
    return names(xs) - 1;
}
`},
	// An unsuffixed literal beside an operand the checker gave no width is
	// read at that operand's width, on either side: the i64 deadline
	// arithmetic in std/tcp's request reader was refused as `i64 / i32`.
	{name: "a-literal-takes-its-operands-width", atLeast: 2, noLeak: true, src: `
function ms(recv_deadline_ms: i32): i32 {
    var read_start_ns: i64 = monotonic_ns();
    var deadline_ns: i64 = (recv_deadline_ms as i64) * 1000000;
    var remaining_ms: i32 = ((deadline_ns - (monotonic_ns() - read_start_ns)) / 1000000) as i32;
    var back: i64 = 7000000000 - (monotonic_ns() - read_start_ns);
    if (back < 6000000000) { return 0 - 1; }
    return remaining_ms;
}
function main(): i32 { if (ms(5000) > 4000) { return 0; } return 1; }
`},
	// A method call on a `dyn Trait` receiver: the widening borrows the
	// record, and the call dispatches on its shape to the implementation
	// (conformance/cases/dyn_trait_dispatch). Two implementations behind one
	// dyn type reach different bodies, and the second method proves the
	// dispatch reads the method rather than landing on the first.
	{name: "dyn-dispatch", atLeast: 6, noLeak: true, src: `
trait Shape {
    function area(self: Self): i32;
    function sides(self: Self): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function sides(self: Self): i32 { return 4; }
}
struct Triangle { base: i32, height: i32 }
impl Shape for Triangle {
    function area(self: Self): i32 { return (self.base * self.height) / 2; }
    function sides(self: Self): i32 { return 3; }
}
function describe(s: dyn Shape): i32 {
    return s.area() * 10 + s.sides();
}

function main(): i32 {
    var sq: dyn Shape = Square { side: 3 };
    var tr: dyn Shape = Triangle { base: 4, height: 5 };
    return describe(sq) + describe(tr) - 155;
}
`},
	// A dyn value holding a record that owns a string, built on every trip
	// round a loop. The AST lowering leaks the record each trip (199 blocks
	// over 100 trips); the typed path owns the record, lends the dyn value to
	// the call, and releases the record when the trip ends.
	{name: "dyn-over-a-counted-record", atLeast: 3, noLeak: true, src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct Named { label: string, side: i32 }
impl Shape for Named {
    function area(self: Self): i32 { return self.side * self.side + self.label.len(); }
}
function describe(s: dyn Shape): i32 { return s.area(); }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var n: dyn Shape = Named { label: "sq" + i.to_string(), side: 3 };
        t = t + describe(n);
        i = i + 1;
    }
    return t % 97;
}
`},
	// Operands past the receiver, of a counted type, a 64-bit float and an
	// integer, and results of a counted type and a float, dispatched to a
	// record and to an enum implementation. The operands reach the dispatch
	// in consecutive frame slots, which wasm types by value: a float operand
	// in an i32 slot is a module the validator rejects. An enum temporary
	// widened at the argument is released after the call that borrowed it.
	{name: "dyn-method-operands-and-results", atLeast: 6, noLeak: true, src: `
import "std/i32";

trait Label {
    function tag(self: Self, prefix: string, n: i32): string;
    function weight(self: Self, k: f64): f64;
}
struct Box { name: string, size: i32 }
impl Label for Box {
    function tag(self: Self, prefix: string, n: i32): string { return prefix + self.name + n.to_string(); }
    function weight(self: Self, k: f64): f64 { return k * 2.0; }
}
enum Shape { Dot, Line(i32) }
impl Label for Shape {
    function tag(self: Self, prefix: string, n: i32): string {
        match (self) {
            Dot => { return prefix + "dot"; },
            Line(len) => { return prefix + "line" + (len + n).to_string(); }
        }
    }
    function weight(self: Self, k: f64): f64 { return k + 1.0; }
}
function show(l: dyn Label, i: i32): i32 {
    var s: string = l.tag("<", i);
    var w: f64 = l.weight(1.5);
    return s.len() + (w * 2.0) as i32;
}

function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var b: dyn Label = Box { name: "b" + i.to_string(), size: i };
        total = total + show(b, i);
        total = total + show(Shape.Line(i), i);
        total = total + show(Shape.Dot, i);
        i = i + 1;
    }
    print(total.to_string());
    return total % 200;
}
`},
	// The dispatch crosses the mixed module in both directions. With the
	// implementations left to the AST lowering, the produced caller would
	// reach bodies whose modes it cannot read, so it is turned off; with the
	// caller left there instead, the AST dispatch reaches produced bodies,
	// which keep the modes they declared.
	{name: "dyn-dispatch-into-ast-implementations", atLeast: 1, skip: "area,sides",
		refuses: "describe: calls Square.area, which the AST lowering defines", src: `
trait Shape {
    function area(self: Self): i32;
    function sides(self: Self): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function sides(self: Self): i32 { return 4; }
}
struct Triangle { base: i32, height: i32 }
impl Shape for Triangle {
    function area(self: Self): i32 { return (self.base * self.height) / 2; }
    function sides(self: Self): i32 { return 3; }
}
function describe(s: dyn Shape): i32 { return s.area() * 10 + s.sides(); }

function main(): i32 {
    var sq: dyn Shape = Square { side: 3 };
    return describe(sq) - 94;
}
`},
	{name: "ast-dispatch-into-produced-implementations", atLeast: 4, skip: "describe", src: `
trait Shape {
    function area(self: Self): i32;
    function sides(self: Self): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function sides(self: Self): i32 { return 4; }
}
struct Triangle { base: i32, height: i32 }
impl Shape for Triangle {
    function area(self: Self): i32 { return (self.base * self.height) / 2; }
    function sides(self: Self): i32 { return 3; }
}
function describe(s: dyn Shape): i32 { return s.area() * 10 + s.sides(); }

function main(): i32 {
    var sq: dyn Shape = Square { side: 3 };
    var tr: dyn Shape = Triangle { base: 4, height: 5 };
    return describe(sq) + describe(tr) - 155;
}
`},
	// A dyn value is counted: a holder owns the box's unit, and its release
	// dispatches on the box's shape to the concrete's drop. The concretes are
	// a record and an enum that each own a string, so a release that missed
	// the children would leak. Each shape builds six values round a loop:
	// rebound at a phi, returned, held in a field, held in an array, and
	// passed through a function that hands its parameter back. A skip leg
	// leaves the dyn's producer or its pass-through to the AST lowering.
	{name: "a-rebound-dyn-value-is-owned", atLeast: 3, noLeak: true, src: semDynShapes + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var s: dyn Shape = Square { side: 1, tag: "a" + i.to_string() };
        if (i % 2 == 1) { s = Tri.Right(i, "b" + i.to_string()); }
        t = t + s.area();
        i = i + 1;
    }
    return t - 24;
}
`},
	{name: "a-returned-dyn-value-is-owned", atLeast: 4, noLeak: true, skip: "make", src: semDynShapes + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var s: dyn Shape = make(i);
        t = t + s.area();
        i = i + 1;
    }
    return t - 50;
}
`},
	{name: "a-dyn-field-is-owned", atLeast: 4, noLeak: true, src: semDynShapes + `
struct Holder { s: dyn Shape }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var h: Holder = Holder { s: make(i) };
        t = t + h.s.area();
        i = i + 1;
    }
    return t - 50;
}
`},
	{name: "a-dyn-array-owns-its-elements", atLeast: 4, noLeak: true, src: semDynShapes + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var xs: dyn Shape[] = [make(i), make(i + 1)];
        t = t + xs[0].area() + xs[1].area();
        i = i + 1;
    }
    return t - 136;
}
`},
	{name: "a-dyn-option-payload-is-owned", atLeast: 4, noLeak: true, src: semDynShapes + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var o: Option[dyn Shape] = Some(make(i));
        if let Some(s) = o { t = t + s.area(); }
        i = i + 1;
    }
    return t - 50;
}
`},
	// A superseded entry is released through the map's value column, which
	// dispatches the same way.
	{name: "a-dyn-map-value-is-owned", atLeast: 4, noLeak: true, src: `import "core/map";` + semDynShapes + `
function main(): i32 {
    var t: i32 = 0;
    var m: Map[i32, dyn Shape] = map_new(8);
    var i: i32 = 0;
    while (i < 6) { m = m.insert(i % 3, make(i)); i = i + 1; }
    i = 0;
    while (i < 3) { if let Some(s) = m.get(i) { t = t + s.area(); } i = i + 1; }
    return t - 35;
}
`},
	{name: "a-captured-dyn-value-is-owned", atLeast: 5, noLeak: true, src: semDynShapes + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var s: dyn Shape = make(i);
        var f = (): i32 => { return s.area(); };
        t = t + f() + s.area();
        i = i + 1;
    }
    return t - 100;
}
`},
	{name: "a-dyn-parameter-handed-back-is-retained", atLeast: 4, noLeak: true, skip: "pass", src: semDynShapes + `
function pass(s: dyn Shape): dyn Shape { return s; }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var q: Square = Square { side: 2, tag: "k" + i.to_string() };
        var s: dyn Shape = pass(q);
        t = t + s.area() + q.tag.len();
        i = i + 1;
    }
    return t - 48;
}
`},
	// A dyn array local an AST-lowered frame builds and lends to a produced
	// callee. The AST frame frees the elements at exit only when the callee's
	// row promises it keeps no reference, which the produced callee now gives
	// for a borrowed parameter nothing derived from is retained or handed on.
	{name: "a-dyn-array-lent-to-a-produced-callee", atLeast: 4, noLeak: true, skip: "main", src: `
trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self * 2; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
function total(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; }

function main(): i32 {
    var xs: dyn Show[] = [1, 2];
    return total(xs) - 6;
}
`},
	// A scalar or string widened to dyn is boxed into a cell whose shape is the
	// primitive's name; the release dispatches on it and frees a boxed
	// string. The widenings reach a return, a local and an array element, and
	// the skip leg leaves the producer to the AST lowering.
	{name: "a-primitive-dyn-value-is-boxed", atLeast: 5, noLeak: true, skip: "make", src: semPrimitiveShows + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var d: dyn Show = make(i);
        var e: dyn Show = i + 1;
        t = t + d.show() + e.show();
        i = i + 1;
    }
    return t - 63;
}
`},
	{name: "a-boxed-string-dyn-held-in-an-array", atLeast: 5, noLeak: true, src: semPrimitiveShows + `
struct Holder { d: dyn Show }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var xs: dyn Show[] = [make(i), make(i + 1)];
        var h: Holder = Holder { d: "h" + i.to_string() };
        t = t + xs[0].show() + xs[1].show() + h.d.show();
        i = i + 1;
    }
    return t - 66;
}
`},
	// A 64-bit integer and a float are boxed at their own width, which wasm's
	// box stores and unboxes by the primitive's name (#10098): an i64 above
	// 2^32 keeps its high half, and an f64 its fraction. The AST lowering
	// refuses the module.
	{name: "a-wide-scalar-dyn-value-is-boxed-at-its-width", atLeast: 5, noLeak: true, want: "0|", src: `
trait Show { function show(self: Self): i32; }
impl Show for i64 { function show(self: Self): i32 { return (self / 1000000000) as i32; } }
impl Show for f64 { function show(self: Self): i32 { return (self * 4.0) as i32; } }
impl Show for boolean { function show(self: Self): i32 { if (self) { return 1; } return 0; } }
function pick(i: i32): dyn Show {
    if (i % 3 == 0) { return 5000000000 as i64; }
    if (i % 3 == 1) { return 2.5; }
    return i > 3;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var d: dyn Show = pick(i);
        t = t + d.show();
        i = i + 1;
    }
    return t - 31;
}
`},
	// A literal mixing a scalar and a string, at each position that declares a
	// `dyn Show[]`: a binding, a field, a return and an argument. The checker
	// once refused all four with the first-element E034 (#10097); the skip
	// leg leaves the returning function to the AST lowering.
	{name: "a-mixed-dyn-array-literal-at-each-destination", atLeast: 5, noLeak: true, skip: "mk", src: `
trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self * 2; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
struct H { xs: dyn Show[] }
function total(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; }
function mk(): dyn Show[] { return [3, "abc"]; }

function main(): i32 {
    var xs: dyn Show[] = [1, "ab"];
    var h: H = H { xs: [2, "x"] };
    return total(xs) + total(h.xs) + total(mk()) + total([5, "q"]) - 29;
}
`},
	// A generic implementation's instances are not enumerated, so a release
	// could not find their children: owning a dyn value of a type one
	// implements is refused, and borrowing one is not.
	{name: "a-dyn-value-over-a-generic-implementation-is-refused", atLeast: 0,
		refuses: "a dyn value over a generic implementation is lent, never owned", src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct W[T] { v: T, tag: string }
impl[T] Shape for W[T] { function area(self: Self): i32 { return self.tag.len(); } }
function make(i: i32): dyn Shape { return W { v: "v" + i.to_string(), tag: "w" + i.to_string() }; }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var s: dyn Shape = make(i);
        t = t + s.area();
        i = i + 1;
    }
    return t - 12;
}
`},
	{name: "a-borrowed-dyn-value-over-a-generic-implementation", atLeast: 2, noLeak: true, src: `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct W[T] { v: T, tag: string }
impl[T] Shape for W[T] { function area(self: Self): i32 { return self.tag.len(); } }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var s: dyn Shape = W { v: "v" + i.to_string(), tag: "w" + i.to_string() };
        t = t + s.area();
        i = i + 1;
    }
    return t - 12;
}
`},
	// A function VALUE whose type nests a function type: a parameter that is
	// itself callable, or a result that is. ssasem.signature_slot refused every
	// function type inside another ("function signature slot"), so a
	// higher-order function could be called by name but not through a local or
	// a parameter (#10024). A function type is one word, the environment box,
	// whatever it nests, and the call through the value dispatches by the same
	// signature tag. Covered: a local bound to a function taking a callback, a
	// `use` through such a local, a parameter whose type takes a callback, and
	// a local bound to a function returning a function value.
	{name: "a-function-value-whose-type-nests-a-function-type", atLeast: 9, noLeak: true, src: `
function with_name(n: i32, k: (string) => i32): i32 { return k("x") + n; }
function through_local(i: i32): i32 {
    var f: (i32, (string) => i32) => i32 = with_name;
    return f(i, (s: string): i32 => s.len());
}
function taker(f: (string) => i32): i32 { return f("hi"); }
function use_through_local(): i32 {
    var t = taker;
    use x <- t();
    if (x == "hi") { return 5; }
    return 0;
}
function runner(k: (i32) => i32, v: i32): i32 { return k(v) + 1; }
function apply2(h: ((i32) => i32, i32) => i32, v: i32): i32 { return h((z: i32): i32 => z * 3, v); }
function adder(n: i32): (i32) => i32 { return (b: i32): i32 => b + n; }
function through_result(): i32 {
    var mk: (i32) => ((i32) => i32) = adder;
    var add3 = mk(3);
    return add3(4) + mk(10)(0);
}
function main(): i32 {
    return through_local(3) + use_through_local() + apply2(runner, 5) + through_result() - 1;
}
`},
	// A call of a call, where the inner callee is a function VALUE: the AST
	// lowering, the control leg, dispatched the returned function as a bare
	// code pointer unless it could name the inner callee as a closure factory,
	// so every shape here but the last segfaulted there (#10057). Every function
	// value is an env box, so the outer call always dispatches env-first.
	// Covered: a capturing and a capture-free result through a local, a
	// parameter, a generic passthrough of a lambda and of a named function,
	// three levels through a local, and a method's result. The f64 and i64
	// arguments pin the outer call's funcref signature: with only the arity
	// known, the AST lowering called an all-i32 funcref and wasm refused the
	// module.
	{name: "a-call-of-a-call-through-a-function-value", atLeast: 9, noLeak: true, src: `
struct M { k: i32 }
function (m: M) make(): (i32) => i32 { var k = m.k; return (b: i32): i32 => b + k; }
function adder(n: i32): (i32) => i32 { return (b: i32): i32 => b + n; }
function doubler(n: i32): (i32) => i32 { return (b: i32): i32 => b * 2; }
function id[T](x: T): T { return x; }
function inc(b: i32): i32 { return b + 1; }
function c3(a: i32): (i32) => ((i32) => i32) { return (b: i32): (i32) => i32 => (c: i32): i32 => a * 100 + b * 10 + c; }
function call_through(mk: (i32) => ((i32) => i32)): i32 { return mk(1)(2); }
struct F { k: f64 }
function (f: F) scale(): (f64) => f64 { var k = f.k; return (x: f64): f64 => x * k; }
function mkf(k: f64): (f64) => f64 { return (x: f64): f64 => x * k; }
function mkw(k: i64): (i64) => i64 { return (x: i64): i64 => x * k; }
function wide(mk: (i64) => ((i64) => i64)): i32 { if (mk(3i64)(5000000000i64) == 15000000000i64) { return 1; } return 0; }
function floats(): i32 {
    var f = F { k: 2.0 };
    var m: (f64) => ((f64) => f64) = mkf;
    var r = 0;
    if (f.scale()(1.5) == 3.0) { r = r + 1; }
    if (m(2.0)(1.5) == 3.0) { r = r + 1; }
    return r + wide(mkw);
}
function main(): i32 {
    var add: (i32) => ((i32) => i32) = adder;
    var dbl = doubler;
    var three = c3;
    var k = 3;
    var m = M { k: 4 };
    return add(10)(20) + dbl(0)(5) + call_through(adder) + id((b: i32): i32 => b + k)(4)
        + id(inc)(5) + three(1)(2)(3) - 100 + m.make()(5) - 33 + floats();
}
`},
	// A function array holds env boxes whatever built it (#10076). An array of
	// named functions held bare code pointers on the AST lowering, the control
	// leg, while every other function value was a box, so an element that left
	// the array (returned, reassigned, passed) reached a caller that dispatched
	// it env-first and segfaulted. A zero-parameter function names a function
	// value unless it is a `const`, so it boxes too.
	{name: "a-function-array-holds-boxes", atLeast: 15, want: "64|", astAnswers: "64|", noLeak: true, src: `
struct M { k: i32 }
enum E { Wrap(() => i32), No }
function a(b: i32): i32 { return b + 1; }
function bb(b: i32): i32 { return b + 2; }
function z(): i32 { return 7; }
function y(): i32 { return 8; }
function pickf(i: i32): (i32) => i32 { var fs = [a, bb]; return fs[i]; }
function pickz(i: i32): () => i32 { var fs = [z, y]; return fs[i]; }
function refill(xs: ((i32) => i32)[]): i32 { var fs: ((i32) => i32)[] = []; fs = xs; return fs[0](1) + fs[1](1); }
function (m: M) run(fs: ((i32) => i32)[]): i32 { return fs[0](m.k); }
function main(): i32 {
    var f = pickf(0);
    var t: ((() => i32), i32) = (z, 1);
    var o: Option[() => i32] = Some(y);
    var w = Wrap(z);
    var r = f(10) + pickf(1)(10) + pickz(1)() + refill([a, bb]) + M { k: 4 }.run([a]) + t.0() + t.1;
    match (o) { Some(g) => { r = r + g(); }, None => {} }
    match (w) { Wrap(h) => { r = r + h(); }, No => {} }
    return r;
}
`},
	// A CAPTURING lambda returned from a lambda. The lifted body's tail
	// `return <lambda>` kept an escaping-closure hoist that left the lambda at
	// the return site, which the typed path refused ("unsupported expression"),
	// because the AST lowering had once segfaulted given the `$lamret$N` slot
	// instead (#5281). That no longer reproduces, so every tail takes the slot
	// and the hoist is gone (#10025). Two levels, three levels, and a lambda
	// bound to a local that returns an expression-bodied lambda.
	{name: "a-lambda-returning-a-capturing-lambda", atLeast: 5, noLeak: true, src: `
function main(): i32 {
    var curry = (a: i32) => { return (b: i32): i32 => a + b; };
    var add5 = curry(5);
    var c3 = (a: i32) => { return (b: i32) => { return (c: i32): i32 => a + b + c; }; };
    var add = (x: i32) => (y: i32) => x + y;
    return add5(4) + curry(1)(2) + c3(1)(2)(3) + add(3)(4) + 17;
}
`},
	// A function declared to return a function returns an env box, whatever its
	// return statements spell (#9763). The AST lowering, the control leg,
	// registered a box-returning function by the shape of its returns, so a
	// lambda handed back through a generic call, a local bound from one, or a
	// match-arm payload was dispatched as a bare code pointer by a caller that
	// bound the result to a local, and segfaulted.
	// A field read stored into a container is a second owner of a box its
	// struct's __struct_drop_<T> releases: a scalar array, an array of structs,
	// a nested struct, an enum. The AST lowering, the control leg, retained only
	// an enum field read, so `xs.append(a.env)` in a callee left the element
	// uncounted, the caller's drop of the argument freed it, and the next
	// allocation reused the block under the container. That was the
	// out-of-bounds abort of a compiler built through the AST lowering (#9763).
	{name: "a-stored-field-read-is-retained", atLeast: 11, want: "24|", astAnswers: "24|", noLeak: true, src: `
struct In { x: i32 }
enum Tag { A(i32), B }
struct E { env: i32[], live: boolean }
struct I { ins: In[], live: boolean }
struct N { inner: In, live: boolean }
struct T { tag: Tag, live: boolean }
function mke(k: i32): E { return E { env: [k, k + 1], live: true }; }
function mki(k: i32): I { return I { ins: [In { x: k }], live: true }; }
function mkn(k: i32): N { return N { inner: In { x: k }, live: true }; }
function mkt(k: i32): T { return T { tag: A(k), live: true }; }
function put_env(xs: i32[][], a: E): i32[][] { return xs.append(a.env); }
function put_ins(xs: In[][], a: I): In[][] { return xs.append(a.ins); }
function put_inner(xs: In[], a: N): In[] { return xs.append(a.inner); }
function put_tag(xs: Tag[], a: T): Tag[] { return xs.append(a.tag); }
function lit_env(a: E): i32[][] { return [a.env]; }
function tag_of(t: Tag): i32 { match (t) { A(n) => { return n; }, B => { return 0; } } }
function main(): i32 {
    var es: i32[][] = [];
    var k: i32 = 1;
    while (k < 5) { es = put_env(es, mke(k)); k = k + 1; }
    var is: In[][] = [];
    k = 1;
    while (k < 5) { is = put_ins(is, mki(k)); k = k + 1; }
    var ns: In[] = [];
    k = 1;
    while (k < 5) { ns = put_inner(ns, mkn(k)); k = k + 1; }
    var ts: Tag[] = [];
    k = 1;
    while (k < 5) { ts = put_tag(ts, mkt(k)); k = k + 1; }
    var ls: i32[][] = [];
    k = 1;
    while (k < 5) { ls = ls.append(lit_env(mke(k))[0]); k = k + 1; }
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        t = t * 3 + es[i][0] * 1000 + is[i][0].x * 100 + ns[i].x * 10 + tag_of(ts[i]) + ls[i][1];
        i = i + 1;
    }
    return t % 256;
}
`},
	// An unsuffixed literal in an array literal takes a concrete sibling's
	// type, as native's settleNumeric does after joining the element type, and
	// a float literal with no sibling to take a width from is an f64 inside a
	// container as it is alone. The self-host checker rejected the mixed
	// literals with E034, and the typed path refused every binding here.
	{name: "an-array-literal-settles-to-its-concrete-sibling", atLeast: 4, want: "59|", noLeak: true, src: `
function half(x: f32): f32 { return x / 2.0; }
function big(): i64 { return 5000000000; }
function id[T](x: T): T { return x; }
function main(): i32 {
    var xs = id([4, half(1.0), 0.5]);
    var ys = [0.25, half(0.5), 3];
    var zs = [1, big(), 2];
    var ws = [1.5, 2.25];
    var tu = (1.5, 2);
    var s: f32 = 0.0;
    for x in xs { s = s + x; }
    for y in ys { s = s + y; }
    var t: i64 = 0;
    for z in zs { t = t + z; }
    var w: f64 = 0.0;
    for v in ws { w = w + v; }
    return (s * 4.0) as i32 + (t / 1000000000) as i32 + (w * 4.0) as i32 + (tu.0 * 2.0) as i32 + tu.1;
}
`},
	{name: "a-function-returning-a-function-returns-a-box", atLeast: 15, want: "53|", astAnswers: "53|", noLeak: true, src: `
enum Box { W((i32) => i32), No }
function id[T](x: T): T { return x; }
function inc(x: i32): i32 { return x + 1; }
function through(a: i32): (i32) => i32 { return id(((x: i32) => x + a)); }
function bound(a: i32): (i32) => i32 { var g: (i32) => i32 = id(((x: i32) => x * a)); return g; }
function either(k: i32): (i32) => i32 { if (k > 0) { return inc; } return (x: i32) => x + k; }
function unbox(b: Box): (i32) => i32 { match (b) { W(f) => { return f; }, No => { return inc; } } }
function main(): i32 {
    function mk(a: i32): (i32) => i32 { return id(((x: i32) => x)); }
    var f: (i32) => i32 = mk(1);
    var g = through(2);
    var h = bound(3);
    var e = either(0);
    var u = unbox(W((x: i32) => x - 1));
    return f(4) + g(5) + h(6) + e(7) + u(8) + either(1)(9);
}
`},
}

// semDynShapes is a trait with a record and an enum implementation, each
// owning a string, and a function widening either into the dyn type.
const semDynShapes = `
import "std/i32";

trait Shape { function area(self: Self): i32; }
struct Square { side: i32, tag: string }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side + self.tag.len(); }
}
enum Tri { Right(i32, string), Flat }
impl Shape for Tri {
    function area(self: Self): i32 {
        match (self) { Right(b, t) => { return b + t.len(); }, Flat => { return 0; } }
    }
}
function make(n: i32): dyn Shape {
    if (n % 2 == 0) { return Square { side: n, tag: "sq" + n.to_string() }; }
    return Tri.Right(n, "tri" + n.to_string());
}
`

// semPrimitiveShows implements a trait for i32, string and a record owning a
// string, and a function widening each of the three into the dyn type.
const semPrimitiveShows = `
import "std/i32";

trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return self * 2; } }
impl Show for string { function show(self: Self): i32 { return self.len(); } }
struct Sq { side: i32, tag: string }
impl Show for Sq { function show(self: Self): i32 { return self.side + self.tag.len(); } }
function make(i: i32): dyn Show {
    if (i % 3 == 0) { return i; }
    if (i % 3 == 1) { return "s" + i.to_string(); }
    return Sq { side: i, tag: "t" + i.to_string() };
}
`

// semHeldElementSource sorts by length with the insertion sort's body: the
// element read into `v` is live across the inner loop's `.with`.
const semHeldElementSource = `
import "std/string";

function ins(own a: string[], lo: i32, hi: i32): string[] {
    var i: i32 = lo + 1;
    while (i < hi) {
        var v: string = a[i];
        var j: i32 = i - 1;
        var moving: boolean = j >= lo;
        while (moving) {
            if (a[j].len() > v.len()) {
                a = a.with(j + 1, a[j]);
                j = j - 1;
                moving = j >= lo;
            } else {
                moving = false;
            }
        }
        a = a.with(j + 1, v);
        i = i + 1;
    }
    return a;
}

function main(): i32 {
    var xs: string[] = ["ccc", "a", "bb", "dddd", "", "ee", "ffffff", "g"];
    var n: i32 = xs.len();
    xs = ins(xs, 0, n);
    var out: string = "";
    for x in xs { out = out + x + "|"; }
    print(out);
    return xs[7].len();
}
`

// TestSelfHostSemanticAllocationParity pins the produced bodies' allocation
// count against the AST lowering's on shapes where a unit decision is what
// decides between writing a buffer in place and copying it. The answer cannot
// see a copy and the leak pins see only what is never released, so this is
// the check that a buffer moved rather than being retained and duplicated.
func TestSelfHostSemanticAllocationParity(t *testing.T) {
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
	for _, prog := range []struct{ name, src string }{
		{"element-read-outlives-the-array-write", semHeldElementSource},
	} {
		t.Run(prog.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(prog.src), 0o644); err != nil {
				t.Fatal(err)
			}
			base := semAllocations(t, fernBin, stdlibRoot, src, false)
			got := semAllocations(t, fernBin, stdlibRoot, src, true)
			if got > base {
				t.Fatalf("the produced bodies allocated %d times where the AST lowering allocates %d", got, base)
			}
		})
	}
}

// semAllocations compiles the program under FERN_LEAKCHECK on x86-64 with the
// semantic path on or off and answers the run's allocation count.
func semAllocations(t *testing.T, fernBin, stdlibRoot, src string, sem bool) int64 {
	t.Helper()
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if sem {
		cmd.Env = append(cmd.Env, "FERN_SEM_IR=1")
	} else {
		cmd.Env = append(cmd.Env, "FERN_SEM_IR=")
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile (sem=%v): %v\n%s", sem, err, output)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	output, _ := exec.Command(out).CombinedOutput()
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(string(output)), &allocs, &frees, &live); err != nil {
		t.Fatalf("no leakcheck summary (sem=%v): %v\n%s", sem, err, output)
	}
	return allocs
}
