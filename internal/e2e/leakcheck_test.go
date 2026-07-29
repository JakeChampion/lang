package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Leak detector, slice 1 (#5362) -------------------------------
//
// ast.LeakCheckEnabled (FERN_LEAKCHECK=1) counts every __fern_alloc
// and __fern_free — count + (size+15)&-16-rounded bytes each, the same
// rounding on both sides so a block's alloc and free cancel exactly —
// and prints one line to stderr at BOTH exit seams (the _start
// epilogue and the exit() builtin's __fern_exit):
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// __fern_alloc_reuse's in-place path counts as neither an alloc nor a
// free. These tests pin the report's numbers on deterministic
// __alloc/__free shapes and on an rc-driven drop-everything loop (the
// heap-bump flat-heap shape, which rc_heap_bump_test.go already proves
// frees every iteration's buffer), pin exit-code and stdout
// preservation, and pin that a flag-off build emits no __fern_lc_
// symbol at all (the byte-identical guarantee's cheap proxy).

// emitLeakCheck compiles src with ast.LeakCheckEnabled (and the
// freelist) toggled per the flags, returning the asm text. Follows the
// compileX86_64FreeOn pipeline; monomorph is included so the arm64 leg
// can share it.
func emitLeakCheck(t *testing.T, backend, src string, leakCheck bool) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prevFree, prevLc := ast.RcFreeEnabled, ast.LeakCheckEnabled
	t.Cleanup(func() { ast.RcFreeEnabled, ast.LeakCheckEnabled = prevFree, prevLc })
	ast.RcFreeEnabled = true
	ast.LeakCheckEnabled = leakCheck
	var asm string
	var emitErr error
	if backend == "arm64" {
		asm, emitErr = arm64codegen.Emit(prog, info)
	} else {
		asm, emitErr = x86_64.Emit(prog, info)
	}
	ast.RcFreeEnabled, ast.LeakCheckEnabled = prevFree, prevLc
	if emitErr != nil {
		t.Fatalf("%s emit: %v", backend, emitErr)
	}
	return asm
}

// runLeakCheckX86_64 compiles src flag-on and runs it, returning
// stdout, stderr, and the exit code separately (the report contract is
// "stderr only, stdout untouched", so combined output won't do).
func runLeakCheckX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	asm := emitLeakCheck(t, "x86_64", src, true)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	return runSplit(t, cmd)
}

// runLeakCheckArm64 is the arm64 sibling (qemu; SKIPs without the
// aarch64 toolchain — rides CI).
func runLeakCheckArm64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	asm := emitLeakCheck(t, "arm64", src, true)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	return runSplit(t, runArm64Bin(qemu, binPath))
}

func runSplit(t *testing.T, cmd *exec.Cmd) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

var leakCheckLineRe = regexp.MustCompile(`^leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)\n$`)

// parseLeakCheckLine asserts stderr is exactly one well-formed report
// line and returns its three numbers.
func parseLeakCheckLine(t *testing.T, stderr string) (allocs, frees, live int64) {
	t.Helper()
	m := leakCheckLineRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("stderr is not a single leakcheck report line: %q", stderr)
	}
	allocs, _ = strconv.ParseInt(m[1], 10, 64)
	frees, _ = strconv.ParseInt(m[2], 10, 64)
	live, _ = strconv.ParseInt(m[3], 10, 64)
	return allocs, frees, live
}

// leakCheckBalancedSrc: 100 paired __alloc/__free of one class. Fully
// deterministic: allocs=100, frees=100, live_bytes=0.
const leakCheckBalancedSrc = `function main(): i32 {
    var i: i32 = 0;
    while (i < 100) {
        var a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    return 0;
}`

// leakCheckRcDropSrc is the rc-driven drop-everything loop — the
// heap-bump flat-heap shape (rc_heap_bump_test.go proves each
// iteration's row buffer is freed and reused, keeping the bump mark
// flat). Every alloc is precisely dropped, so the report must balance
// exactly: allocs == frees, live_bytes == 0. (Shapes that do NOT free
// under phase-1 precise drop — e.g. shared rc>1 buffers whose final dec
// isn't a free site — would show as live; this fixture deliberately has
// no sharing.)
const leakCheckRcDropSrc = `function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 50) {
        var row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0];
        i = i + 1;
    }
    if (sum == 1225) { return 0; }
    return 1;
}`

// leakCheckLeakySrc: three 60-byte allocations (rounded to 64), one
// freed, exit code 42 through the _start epilogue. Pins the leak
// numbers AND that the report doesn't clobber main's exit code:
// allocs=3, frees=1, live_bytes=2*64=128.
const leakCheckLeakySrc = `function main(): i32 {
    var a: usize = __alloc(60);
    var b: usize = __alloc(60);
    var c: usize = __alloc(60);
    __free(a, 60);
    if (b == c) { return 9; }
    return 42;
}`

// leakCheckExitBuiltinSrc: the exit() builtin bypasses the _start
// epilogue, so __fern_exit must report too — with the exit code (7)
// preserved and stdout (the print) clean of the report. alloc(100)
// rounds to 112 live bytes.
const leakCheckExitBuiltinSrc = `function main(): i32 {
    var a: usize = __alloc(100);
    print("hello");
    exit(7);
    return 0;
}`

// Flag-off, the emitted asm must not contain any leak-detector symbol
// at all — instrumentation, counters, report, and literals are all
// behind the flag (the flag-off byte-identical guarantee).
func TestLeakCheckOffEmitsNoSymbols(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64"} {
		asm := emitLeakCheck(t, backend, leakCheckLeakySrc, false)
		if strings.Contains(asm, "__fern_lc_") || strings.Contains(asm, ".Llc_") {
			t.Errorf("%s: flag-off asm contains leak-detector symbols", backend)
		}
	}
}

func TestX86_64LeakCheckBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckBalancedSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 100/100/0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckRcDropBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckRcDropSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Errorf("expected a non-zero alloc count (one row per iteration)")
	}
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0 (precise drop frees every row)", allocs, frees, live)
	}
}

func TestX86_64LeakCheckLeakReported(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckLeakySrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (report must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (report goes to stderr only)", stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 3 || frees != 1 || live != 128 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 3/1/128", allocs, frees, live)
	}
}

func TestX86_64LeakCheckExitBuiltinReports(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, leakCheckExitBuiltinSrc)
	if code != 7 {
		t.Errorf("exit=%d, want 7 (__fern_exit must preserve the code around the report)", code)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout=%q, want %q", stdout, "hello\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 1 || frees != 0 || live != 112 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/0/112", allocs, frees, live)
	}
}

// Arm64 mirrors (qemu; ride CI, SKIP without the toolchain).
func TestArm64LeakCheckBalanced(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckBalancedSrc)
	if code != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q, want 0 / empty", code, stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 100/100/0", allocs, frees, live)
	}
}

func TestArm64LeakCheckLeakReported(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckLeakySrc)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (report must not clobber main's exit code)", code)
	}
	if stdout != "" {
		t.Errorf("stdout=%q, want empty (report goes to stderr only)", stdout)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 3 || frees != 1 || live != 128 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 3/1/128", allocs, frees, live)
	}
}

func TestArm64LeakCheckExitBuiltinReports(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, leakCheckExitBuiltinSrc)
	if code != 7 {
		t.Errorf("exit=%d, want 7 (__fern_exit must preserve the code around the report)", code)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout=%q, want %q", stdout, "hello\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 1 || frees != 0 || live != 112 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/0/112", allocs, frees, live)
	}
}

// A callee that RETAINS a string parameter into the value it returns —
// `mkT(name, line) -> Tk { name: name, line: line }`, the shape every one of
// the lexer's eight `*_tok` helpers has — used to leak one reference per call.
// The field init is a counted store, so the returned struct owns a reference,
// but computeFreeEligible taints any string argument passed to a user function
// (it cannot see whether the callee retains it uncounted), so the CALLER's
// reference was never released and the rc sat one above its true owner count
// forever. inferParamCountedRetain lifts that taint for the counted case.
//
// Pinned by frees, not by a leak-free total: the shape has a second, unrelated
// leak (the inline-literal form leaks one block per round too), so the contract
// here is that routing through the retaining helper costs NOTHING over building
// the struct in place — equal on x86-64, and better than inline on arm64, whose
// two-word string ABI never took this taint (it is gated to single-word
// natives) and whose inline form reclaims less. `examples/probes/retained_param_leak.fern` is
// the standalone probe; on `parser.fern` this is worth +90% frees in the lexer.
const retainedParamSrc = `import "std/i32";
struct Tk { name: string, line: i32 }
function mkT(name: string, line: i32): Tk { return Tk { name: name, line: line }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var s: string = "id" + r.to_string();
        var t: Tk = mkT(s, r);
        acc = acc + t.name.len();
        r = r + 1;
    }
    return acc % 5;
}`

// The same program with the struct built in place — no retaining call at all.
const retainedParamInlineSrc = `import "std/i32";
struct Tk { name: string, line: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var s: string = "id" + r.to_string();
        var t: Tk = Tk { name: s, line: r };
        acc = acc + t.name.len();
        r = r + 1;
    }
    return acc % 5;
}`

func TestLeakCheckRetainedParamX86_64(t *testing.T) {
	_, viaHelperErr, viaHelperCode := runLeakCheckX86_64(t, retainedParamSrc)
	_, inlineErr, inlineCode := runLeakCheckX86_64(t, retainedParamInlineSrc)
	if viaHelperCode != inlineCode {
		t.Fatalf("exit codes differ: helper %d, inline %d", viaHelperCode, inlineCode)
	}
	ha, hf, _ := parseLeakCheckLine(t, viaHelperErr)
	ia, iff, _ := parseLeakCheckLine(t, inlineErr)
	if ha != ia {
		t.Fatalf("allocs differ: helper %d, inline %d — fixture drift", ha, ia)
	}
	if hf < iff {
		t.Errorf("retaining callee frees %d of %d, inline form frees %d: the call leaks the caller's reference", hf, ha, iff)
	}
}

func TestLeakCheckRetainedParamArm64(t *testing.T) {
	_, viaHelperErr, viaHelperCode := runLeakCheckArm64(t, retainedParamSrc)
	_, inlineErr, inlineCode := runLeakCheckArm64(t, retainedParamInlineSrc)
	if viaHelperCode != inlineCode {
		t.Fatalf("exit codes differ: helper %d, inline %d", viaHelperCode, inlineCode)
	}
	ha, hf, _ := parseLeakCheckLine(t, viaHelperErr)
	ia, iff, _ := parseLeakCheckLine(t, inlineErr)
	if ha != ia {
		t.Fatalf("allocs differ: helper %d, inline %d — fixture drift", ha, ia)
	}
	if hf < iff {
		t.Errorf("retaining callee frees %d of %d, inline form frees %d: the call leaks the caller's reference", hf, ha, iff)
	}
}

// The result-struct field-extraction threading that costs lexer.tokenize ~4
// blocks per token (docs/SELFHOST-AST-RETIREMENT.md): a scanner returns
// `Res { lex, tok }`, the caller extracts `l = r.lex` to thread the cursor and
// `out.append(r.tok)` to accumulate. `l = r.lex` — a FieldAccess read out of a
// struct LOCAL — used to taint `l` conservatively, stranding the whole `l`/`r`
// cluster (never freed) every iteration. It is a COUNTED alias (the bind inc's
// it and both `l` and `r` deep-drop), so it reclaims; rc_analysis.go un-taints
// a field read whose source is a struct local. Standalone probe:
// examples/probes/result_thread_leak.fern. Pinned by a FULL reclaim
// (allocs == frees, live_bytes == 0) — before the fix this shape freed 200 of
// 2400 (92% leaked).
const resultThreadReclaimSrc = `struct Lx { src: string, i: i32 }
struct TId { text: string }
struct TEof {}
type Tok = TId | TEof;
struct Res { lex: Lx, tok: Tok }
function scan(l: Lx): Res {
    var t: Tok = TId { text: l.src[l.i : l.i + 1] + "" };
    return Res { lex: Lx { src: l.src, i: l.i + 1 }, tok: t };
}
function run(src: string): i32 {
    var l: Lx = Lx { src: src, i: 0 };
    var out: Tok[] = [];
    while (l.i < 5) {
        var r = scan(l);
        l = r.lex;
        out = out.append(r.tok);
    }
    return out.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var n: i32 = 0;
    while (n < 100) {
        acc = acc + run("abcdefgh");
        n = n + 1;
    }
    return acc % 7;
}`

func TestLeakCheckResultThreadReclaimX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, resultThreadReclaimSrc)
	if code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed (allocs==frees, live==0): the l = r.lex result-struct threading strands its cluster", allocs, frees, live)
	}
}

func TestLeakCheckResultThreadReclaimArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, resultThreadReclaimSrc)
	if code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed (allocs==frees, live==0): the l = r.lex result-struct threading strands its cluster", allocs, frees, live)
	}
}

// The scalar-poisoned sibling of the result-struct threading: the scanner also
// takes a SCALAR argument (`start_line`) the caller taints by storing it into a
// token literal, and a tainted scalar argument re-taints the whole result
// struct — so the FieldAccess un-taint (result_thread) alone leaves it leaking.
// The interprocedural counted-retain fixpoint closes it: the struct cursor
// param is credited (its uses are projections, counted stores, pure-read method
// calls, and a returned-borrow), which enables the scalar-arg exemption. Probe:
// examples/probes/scalar_thread_leak.fern. Pinned by a FULL reclaim — before
// the fixpoint this freed 1000 of 3400 (70% leaked).
const scalarThreadReclaimSrc = `struct Lx { src: string, i: i32, line: i32 }
struct TId { text: string, line: i32 }
struct TPunct { text: string, line: i32 }
struct TEof { line: i32 }
type Tok = TId | TPunct | TEof;
struct Res { lex: Lx, tok: Tok }
function scan(l: Lx, start_line: i32): Res {
    var t: Tok = TId { text: l.src[l.i : l.i + 1] + "", line: start_line };
    return Res { lex: Lx { src: l.src, i: l.i + 1, line: l.line + 1 }, tok: t };
}
function run(src: string): i32 {
    var l: Lx = Lx { src: src, i: 0, line: 1 };
    var out: Tok[] = [];
    while (l.i < 8) {
        var start_line: i32 = l.line;
        if (l.src[l.i] == 46) {
            out = out.append(TPunct { text: ".", line: start_line });
            l = Lx { src: l.src, i: l.i + 1, line: l.line };
            continue;
        }
        var r = scan(l, start_line);
        l = r.lex;
        out = out.append(r.tok);
    }
    return out.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var n: i32 = 0;
    while (n < 100) {
        acc = acc + run("abc.def.g");
        n = n + 1;
    }
    return acc % 7;
}`

func TestLeakCheckScalarThreadReclaimX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, scalarThreadReclaimSrc)
	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed: the scalar-arg exemption must credit the projection-threaded cursor param", allocs, frees, live)
	}
}

func TestLeakCheckScalarThreadReclaimArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, scalarThreadReclaimSrc)
	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want fully reclaimed: the scalar-arg exemption must credit the projection-threaded cursor param", allocs, frees, live)
	}
}

// --- Move-on-construction in a loop body (#5879) -------------------
//
// A bare-ident heap value consumed by a container literal at its last use is
// MOVED into the construction: the alias-inc is skipped and the container's
// deep drop releases the element. markConstructionMoves used to walk only the
// function's top-level statements, so inside a loop body the move never fired
// — the inc was emitted with nothing releasing the source's own reference per
// iteration, leaking one array per iteration (linear, unbounded: 3.2 MB over
// 100k iterations).
//
// The three fixtures are the discriminator. They differ in one line each, and
// pinning all three together is what makes the gate meaningful: the fix must
// close the leak WITHOUT moving a var that outlives the iteration.
const loopConstructionMoveSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    while (k < 20) {
        var xs: i32[] = [1, 2, 3];
        var t = (xs, 99);
        s = s + t.0[2];
        k = k + 1;
    }
    return s % 7;
}`

// The same loop with a FRESH literal element: names no local, so it never took
// the construction inc and was already balanced. The control for the fixture
// above — if this one ever regresses, the cause is not the move analysis.
const loopConstructionFreshSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    while (k < 20) {
        var t = ([1, 2, 3], 99);
        s = s + t.0[2];
        k = k + 1;
    }
    return s % 7;
}`

// The hazard the move must NOT swallow: `a1` aliases a loop-OUTER array
// without an inc. Moving it would let the first iteration's release free a
// buffer later iterations still read — a use-after-free, not a leak. Pinned by
// exit code as well as balance, since an over-release corrupts the read.
const loopAliasNoIncSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    var a0: i32[] = [1, 2, 3];
    while (k < 20) {
        var a1: i32[] = a0;
        s = s + a1[2];
        k = k + 1;
    }
    return s % 7;
}`

func TestX86_64LeakCheckLoopConstructionMove(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"bare-ident-element", loopConstructionMoveSrc, 4},
		{"fresh-literal-element", loopConstructionFreshSrc, 4},
		{"alias-without-inc", loopAliasNoIncSrc, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckLoopConstructionMove(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"bare-ident-element", loopConstructionMoveSrc, 4},
		{"fresh-literal-element", loopConstructionFreshSrc, 4},
		{"alias-without-inc", loopAliasNoIncSrc, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			if code != tc.exit {
				t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, tc.exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift")
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

// A string param used in a construction slot AND read by a pure-read builtin
// (`s.len()`) must stay counted-retain — the `.len()` is a scalar read that
// retains nothing. This is the residual lexer strand: `tokenize`'s `src` param
// is `Lex { src: src, n: src.len() }`, and the `src.len()` occurrence used to
// disqualify it, so every `toks = tokenize(src)` result was tainted and its
// tokens stranded at the caller. Pinned differentially, like the retained-param
// pair: `mk` using `s.len()` must free as much as the same builder without it.
const pureReadLenSrc = `import "std/i32";
struct Box { s: string, n: i32 }
function mk(s: string): Box { return Box { s: s, n: s.len() }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var key: string = "k" + r.to_string();
        var b: Box = mk(key);
        acc = acc + b.s.len();
        r = r + 1;
    }
    return acc % 7;
}`

const pureReadNoLenSrc = `import "std/i32";
struct Box { s: string, n: i32 }
function mk(s: string): Box { return Box { s: s, n: 0 }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var key: string = "k" + r.to_string();
        var b: Box = mk(key);
        acc = acc + b.s.len();
        r = r + 1;
    }
    return acc % 7;
}`

func TestLeakCheckPureReadLenX86_64(t *testing.T) {
	_, lenErr, lenCode := runLeakCheckX86_64(t, pureReadLenSrc)
	_, noLenErr, noLenCode := runLeakCheckX86_64(t, pureReadNoLenSrc)
	if lenCode != noLenCode {
		t.Fatalf("exit codes differ: len %d, nolen %d", lenCode, noLenCode)
	}
	la, lf, _ := parseLeakCheckLine(t, lenErr)
	na, nf, _ := parseLeakCheckLine(t, noLenErr)
	if la != na {
		t.Fatalf("allocs differ: len %d, nolen %d — fixture drift", la, na)
	}
	if lf < nf {
		t.Errorf("with s.len() frees %d of %d, without it frees %d: the pure-read receiver must not disqualify the counted-retain param", lf, la, nf)
	}
}

func TestLeakCheckPureReadLenArm64(t *testing.T) {
	_, lenErr, lenCode := runLeakCheckArm64(t, pureReadLenSrc)
	_, noLenErr, noLenCode := runLeakCheckArm64(t, pureReadNoLenSrc)
	if lenCode != noLenCode {
		t.Fatalf("exit codes differ: len %d, nolen %d", lenCode, noLenCode)
	}
	la, lf, _ := parseLeakCheckLine(t, lenErr)
	na, nf, _ := parseLeakCheckLine(t, noLenErr)
	if la != na {
		t.Fatalf("allocs differ: len %d, nolen %d — fixture drift", la, na)
	}
	if lf < nf {
		t.Errorf("with s.len() frees %d of %d, without it frees %d: the pure-read receiver must not disqualify the counted-retain param", lf, la, nf)
	}
}
