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

// --- Tuple-valued container element (#5879 cause B) ----------------
//
// An array of tuples routed its per-element release through the flat
// __fern_drop_arr_ptr (per-element __fern_rc_dec), because
// arrElemStructDropName gated the generated per-element walk on a
// "does this tuple need a deep drop?" predicate — false for a tuple of plain
// scalars, whose drop body has no element to traverse. But the tuple BOX still has to be freed, and a flat
// rc_dec only decrements: freeing needs the size, which only the generated
// __drop_arr_tuple_<mangled> loop supplies. So `[t]` leaked exactly one tuple
// box per iteration (16 bytes for `(i32, i32)` — 8 rc header + 8 payload),
// linear and unbounded.
//
// The scalar tuple is the subject; a tuple carrying a string was already
// covered before the gate came out, so it is the control — if it ever
// regresses, the cause is the walk itself, not the gate.
const tupleElemArrayScalarSrc = `function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var t = (k, k + 1);
        var c = [t, (7, 8)];
        total = total + c[0].0 + c[1].1;
        k = k + 1;
    }
    return total % 251;
}`

const tupleElemArrayStringSrc = `function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var s: string = "ab" + "cd";
        var t = (s, k);
        var c = [t];
        total = total + c[0].0.len();
        k = k + 1;
    }
    return total % 251;
}`

func TestX86_64LeakCheckTupleElemArray(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"scalar-tuple-element", tupleElemArrayScalarSrc, 119},
		{"string-tuple-element", tupleElemArrayStringSrc, 200},
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

func TestArm64LeakCheckTupleElemArray(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"scalar-tuple-element", tupleElemArrayScalarSrc, 119},
		{"string-tuple-element", tupleElemArrayStringSrc, 200},
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

// --- Tuple-valued element of a tuple (#5879 cause B, nested half) ---
//
// The sibling #5899 left open. dropFnNameFor's TupleType arm carried the same
// "needs a deep drop?" bail as arrElemStructDropName did, so a tuple-valued
// element of a generated __drop_tuple_ fell through dropStructField to a flat
// decValueOnStack dec — decrementing the inner box's rc but never freeing it,
// since freeing needs the size only the generated body has. 16 bytes per
// iteration for `(i32, i32)`, linear and unbounded.
//
// Removing that bail leaves tupleNeedsDrop with no callers, so it is gone; a
// scalar tuple now reaches genTupleDropFn and emits an is_unique-gated
// box_free with no element drops, which is exactly the free that was missing.
const tupleInTupleScalarSrc = `function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var t = (k, k + 1);
        var o = (t, 99);
        total = total + o.0.1 + o.1;
        k = k + 1;
    }
    return total % 251;
}`

// A tuple-valued element nested in a STRUCT field, the third routing into the
// same per-element drop (dropStructField reaches it via __drop_struct_*).
const tupleInStructScalarSrc = `struct Holder { pair: (i32, i32), n: i32 }
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var t = (k, k + 1);
        var h = Holder { pair: t, n: 3 };
        total = total + h.pair.1 + h.n;
        k = k + 1;
    }
    return total % 251;
}`

func TestX86_64LeakCheckNestedTupleElem(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"tuple-in-tuple", tupleInTupleScalarSrc},
		{"tuple-in-struct", tupleInStructScalarSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift (exit %d)", code)
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

func TestArm64LeakCheckNestedTupleElem(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"tuple-in-tuple", tupleInTupleScalarSrc},
		{"tuple-in-struct", tupleInStructScalarSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations recorded — fixture drift (exit %d)", code)
			}
			if frees != allocs || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
			}
		})
	}
}

// --- Construction-retained loop source (#5879 cause A) --------------
//
// A loop-body local whose reference is RETAINED into a container while the
// local stays live (read after the construction) took a construction alias-inc
// per iteration against a single exit-sweep dec, so n-1 values leaked — linear
// and unbounded. It is not the move-side gap #5889 closed: the inc is CORRECT
// here, because the local and the container genuinely both hold the value, so a
// move is unavailable. What was missing is the per-iteration release, which
// emitVarReinitDropOld skipped along with every other !freeEligible local.
//
// The release is keyed on rc.ctorAliasInced — the locals that actually received
// a construction inc — not on !freeEligible, because ineligibility has several
// causes and only this one leaves a reference to give back. It is the FLAT dec
// the exit sweep already emits, never the deep drop: the container shares the
// value and reclaims it through its own drop.
//
// Both readings of the same construction are pinned. The container-read form
// was already clean (the construction is the source's last use, so the move
// fires) and is the control: if it regresses, the cause is the move analysis,
// not this release.
const ctorRetainedSourceReadSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    while (k < 40) {
        var xs: i32[] = [4, 5, 6];
        var o = (xs, 9);
        s = s + xs[1] + o.1;
        k = k + 1;
    }
    return s % 251;
}`

const ctorRetainedContainerReadSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    while (k < 40) {
        var xs: i32[] = [4, 5, 6];
        var o = (xs, 9);
        s = s + o.0[1] + o.1;
        k = k + 1;
    }
    return s % 251;
}`

// Same retain through an ENUM payload rather than a tuple — the
// EnumRcPayloads inc site, the fourth routing computeCtorAliasInced covers.
const ctorRetainedEnumPayloadSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    while (k < 40) {
        var xs: i32[] = [4, 5, 6];
        var o = Some(xs);
        s = s + xs[1];
        k = k + 1;
    }
    return s % 251;
}`

// The shape the release must NOT swallow: a1 aliases a loop-OUTER array. It
// takes no construction inc, so it is absent from ctorAliasInced and keeps its
// existing handling; releasing it per iteration would over-release a0's buffer.
// Pinned on exit code too, since an over-release corrupts the read.
const ctorOuterAliasSrc = `function main(): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    var a0: i32[] = [7, 8, 9];
    while (k < 40) {
        var a1: i32[] = a0;
        var o = (a1, 1);
        s = s + a1[2] + o.1;
        k = k + 1;
    }
    return s % 251;
}`

func TestX86_64LeakCheckCtorRetainedLoopSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"source-read-after", ctorRetainedSourceReadSrc, 58},
		{"container-read", ctorRetainedContainerReadSrc, 58},
		{"enum-payload", ctorRetainedEnumPayloadSrc, 200},
		{"outer-alias", ctorOuterAliasSrc, 149},
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

func TestArm64LeakCheckCtorRetainedLoopSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"source-read-after", ctorRetainedSourceReadSrc, 58},
		{"container-read", ctorRetainedContainerReadSrc, 58},
		{"enum-payload", ctorRetainedEnumPayloadSrc, 200},
		{"outer-alias", ctorOuterAliasSrc, 149},
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

// --- Boxed generic enum with a string payload (#5879) ---------------
//
// `Option[string]` is heap-boxed, but dropFnNameFor / emitEnumSlotDrop only
// adopted a substituted generic decl when enumHasPointerPayload said so — and
// that predicate is built on arrElemIsRcTracked, which deliberately EXCLUDES
// strings (their retain/release is two-word on wasm + arm64-TwoWordOverride).
// So an Option[string] read false, kept the generic decl, fell to the flat dec,
// and its box was never freed: 32 bytes per construction, linear and unbounded.
//
// enumHasBoxedPayload answers the narrower question the box_free actually needs
// ("is this instantiation boxed?") and is kept separate from
// enumHasPointerPayload, which also selects the drop SHAPE (uniform-branchless
// vs variant-plan). A scalar instantiation (Option[i32], pair-form, no box)
// still reads false, so box_free is never emitted for it.
//
// The constant-folded fixture is the subject: with the payload folded to a
// static string the box is the only allocation, so allocs==frees isolates the
// box exactly. The array fixture is the control — Option[i32[]] always had a
// pointer payload, so it was never affected.
const enumStringPayloadBoxSrc = `function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var st: string = "ab" + "cd";
        var o = Some(st);
        t = t + st.len();
        k = k + 1;
    }
    return t % 251;
}`

const enumArrayPayloadBoxSrc = `function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var xs: i32[] = [1, 2];
        var o = Some(xs);
        t = t + xs[1];
        k = k + 1;
    }
    return t % 251;
}`

// A scalar instantiation. This was pinned as UNCHANGED-and-leaking when the
// string-payload box fix landed (allocs=100 frees=0 live=1600), with the note
// that it flips to the ordinary assertion once #5917 lands. It has: adopting the
// substituted decl unconditionally in emitEnumSlotDrop gives the uniform drop
// path concrete payload types, so the box is freed here too.
//
// The reasoning that kept it leaking was that a scalar instantiation is
// "pair-form, no box". Pair-form is a per-FUNCTION return ABI (findPairFormFuncs,
// keyed by function name) describing how a callee hands an Option back, not how
// an Option LOCAL is represented — and a local is boxed in every measured shape,
// including one bound from a pair-form-eligible callee (the returned-from-callee
// fixture below).
const enumScalarPayloadNoBoxSrc = `function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var o = Some(k);
        t = t + 1;
        k = k + 1;
    }
    return t % 251;
}`

// A scalar Option bound from a callee whose every return is Some(EXPR)/None —
// i.e. a function findPairFormFuncs marks pair-form-ELIGIBLE. The caller's local
// is still boxed and must still be freed; this is the fixture that disproves
// "pair-form ⇒ no box" for the local (#5917).
const enumScalarFromCalleeSrc = `function mk(n: i32): Option[i32] { return Some(n); }
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var o = mk(k);
        t = t + 1;
        k = k + 1;
    }
    return t % 251;
}`

// A scalar Option NESTED in a container — a struct field and a tuple element.
// These go through dropFnNameFor rather than emitEnumSlotDrop, which carried the
// identical "pair-form, no box" gate, so they leaked 16 bytes per construction
// even once the direct-local case was fixed (#5917). Both gates are gone.
const enumScalarNestedStructSrc = `struct H { o: Option[i32], n: i32 }
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var h = H { o: Some(k), n: 1 };
        t = t + h.n;
        k = k + 1;
    }
    return t % 251;
}`

const enumScalarNestedTupleSrc = `function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var p = (Some(k), 1);
        t = t + p.1;
        k = k + 1;
    }
    return t % 251;
}`

func TestX86_64LeakCheckEnumStringPayloadBox(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"string-payload-boxed", enumStringPayloadBoxSrc, 149},
		{"array-payload-boxed", enumArrayPayloadBoxSrc, 200},
		{"scalar-payload-boxed", enumScalarPayloadNoBoxSrc, 100},
		{"scalar-from-pairform-callee", enumScalarFromCalleeSrc, 100},
		{"scalar-nested-in-struct", enumScalarNestedStructSrc, 100},
		{"scalar-nested-in-tuple", enumScalarNestedTupleSrc, 100},
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

func TestArm64LeakCheckEnumStringPayloadBox(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		exit int
	}{
		{"string-payload-boxed", enumStringPayloadBoxSrc, 149},
		{"array-payload-boxed", enumArrayPayloadBoxSrc, 200},
		{"scalar-payload-boxed", enumScalarPayloadNoBoxSrc, 100},
		{"scalar-from-pairform-callee", enumScalarFromCalleeSrc, 100},
		{"scalar-nested-in-struct", enumScalarNestedStructSrc, 100},
		{"scalar-nested-in-tuple", enumScalarNestedTupleSrc, 100},
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

// `n.to_string()` leaked one heap buffer per call (#5931) — pervasive, since
// `core/int.int_to_string` / `__int_to_string_u64` back the `std/i32` /
// `std/i64` / `std/u32` / `std/u64` method wrappers and
// `int_to_string_radix` backs `to_hex` / `to_binary` / `to_rgb_hex`.
//
// All three helpers pack their digits into a `__alloc_u8(n_bytes)` output
// buffer whose size is COMPUTED, and rhsTainted's generic any-arg-tainted
// rule read the tainted scalar byte count as evidence the buffer might alias
// something: `__alloc_u8(16)` (literal arg, untainted) was freeEligible while
// `__alloc_u8(n_bytes)` was not, so the buffer was never dropped.
// `string_from_bytes_unchecked` always COPIES its input — into an inline-
// tagged register value at <= 7 bytes, into a fresh rc1 heap buffer above —
// so the input buffer is dead at the return and must be reclaimed.
//
// Pinned by a FULL reclaim (allocs == frees, live_bytes == 0) plus the loop's
// value: before the fix `to_string` freed 346 of 545 blocks and the radix
// helper 177 of 295. The value checks are the over-release half of the
// contract — a radix buffer freed while its string still needed it would show
// as a wrong exit code, not as a leak. The third leg (to_rgb_hex) asserts the
// value only; see its own comment for the #5942 residual it composes in.
const toStringReclaimSrc = `import "std/i32";
import "std/i64";
import "std/u64";
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var s: string = k.to_string();
        var u: string = ((k as i64) * 1000000007).to_string();
        var v: string = ((k as u64) * (1234567891 as u64)).to_string();
        var w: string = (k * 1234567).to_binary();
        t = t + s.len() + u.len() + v.len() + w.len();
        k = k + 1;
    }
    if (t == 2378) { return 0; }
    return 1;
}`

// int_to_string_radix's own output buffer, both signs (the negative arm takes
// the `out_len = k + 1` path), read back byte by byte so an over-release
// corrupts the exit code.
const radixReclaimSrc = `import "std/i32";
import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var k: i32 = 1;
    while (k < 60) {
        var h: string = int.int_to_string_radix(k * 7919, 16);
        var b: string = int.int_to_string_radix(0 - (k * 7919), 2);
        var i: i32 = 0;
        while (i < h.len()) { acc = (acc * 31 + (h[i] as i32)) % 100003; i = i + 1; }
        i = 0;
        while (i < b.len()) { acc = (acc * 31 + (b[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

// to_rgb_hex — the shape the *ast.Binary case in rhsTainted still cites as
// its over-release witness. Untainting the ALLOCATOR rather than the scalar
// size expression keeps the VALUE right, which is all this leg asserts.
//
// It deliberately does NOT assert a full reclaim, because `to_rgb_hex`'s body
// chains its intermediates — `r.to_hex().pad_start(2, "0")`, three times, then
// CONCATENATED — and the concat half of that chain still leaks on arm64.
//
// The call half is now closed: a fresh string temp handed to a string-RETURNING
// call is reclaimed (#5942, pinned by the StringArgTempReclaim tests below), so
// arm64 moved 388 -> 425 of 542. What remains is the `"#" + a + b + c` chain
// itself. A concat operand is an *ast.Binary, not a Call, so it never reaches
// the stage-(b) arg-temp reclaim that fixed the call case; the ~117 blocks left
// here are 3 per iteration, one per concat step.
//
// x86-64 shows none of this: the hex pieces are <= 7 bytes, so the single-word
// ABI keeps them SSO-inline and there is no block to lose. That is also why the
// x86 leg happens to satisfy allocs == frees while the arm64 one does not —
// treat a clean x86 string-leak number as weak evidence unless the strings
// exceed the SSO window.
const rgbHexReclaimSrc = `import "std/i32";
function main(): i32 {
    var acc: i32 = 0;
    var k: i32 = 1;
    while (k < 40) {
        var s: string = (k * 66049).to_rgb_hex();
        var i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

var toStringReclaimCases = []struct {
	name string
	src  string
	exit int
	// fullReclaim: assert allocs == frees / live == 0. False only for
	// to_rgb_hex, whose body also trips the separate #5942 arg-temp leak; that
	// leg still pins the exit VALUE, which is the over-release half.
	fullReclaim bool
}{
	{"to_string-i32-i64-u64-binary", toStringReclaimSrc, 0, true},
	{"int_to_string_radix", radixReclaimSrc, 143, true},
	{"to_rgb_hex", rgbHexReclaimSrc, 29, false},
}

func checkToStringReclaim(t *testing.T, stderr string, code, wantExit int, fullReclaim bool) {
	t.Helper()
	if code != wantExit {
		t.Fatalf("exit=%d, want %d — the loop result is wrong, not just its accounting", code, wantExit)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift")
	}
	if fullReclaim && (frees != allocs || live != 0) {
		t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckToStringReclaim(t *testing.T) {
	for _, tc := range toStringReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			checkToStringReclaim(t, stderr, code, tc.exit, tc.fullReclaim)
		})
	}
}

func TestArm64LeakCheckToStringReclaim(t *testing.T) {
	for _, tc := range toStringReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			checkToStringReclaim(t, stderr, code, tc.exit, tc.fullReclaim)
		})
	}
}

// A fresh string temp handed straight to a string-RETURNING call used to be
// reclaimed by nobody (#5942) — one heap block per call, unbounded in a loop.
// Binding the intermediate to a `var` first reclaimed it, so the two shapes
// below differ only in whether the intermediate has a name, and had to differ
// only in that after the fix too.
//
// TWO gates had to open, and each alone leaves the leak in place:
//
//   - The stage-(b) arg-temp reclaim was gated on `resultCannotAliasArg`, which
//     rejects every pointer result. `pad_start` really can return its receiver
//     (`if (sl >= n) { return s; }`), so the gate was not wrong — but the alias
//     it fears is COUNTED: `return <param>` emits the return-transfer inc, and a
//     param is never an isOwnedRcLocal, so move-on-return cannot cancel that inc
//     away. rc is 2 on the pass-through path and 1 on the fresh path, and one
//     post-call dec nets both to a single owner (resultIsCountedStringAlias).
//   - `ownedCallResultType` refuses to reclaim a `__`-prefixed method result
//     unless the callee is PROVEN fresh-returning, and the whole int-to-string
//     family was unproven: they all end in `string_from_bytes_unchecked`, a
//     builtin absent from the fixpoint set, so `exprNoParamEscape` rejected it
//     and the verdict propagated up through to_string / to_hex / to_binary. That
//     call always copies, which is now stated where the fixpoint can use it.
//
// The pass-through leg is the one with teeth on the first gate: it pads to a
// width the receiver already exceeds, so `pad_start` returns its argument and
// the post-call dec lands on a buffer the RESULT still needs. If the counted-
// alias reasoning were wrong that is a use-after-free — a wrong exit code or a
// segfault, not a leak. Widening this gate to pointer results in general is
// what segfaulted the differential oracle before (see reclaimArgTemps), so the
// narrowing to concrete strings + user callees is load-bearing, not tidiness.
//
// Both legs use LONG (> 7 byte) intermediates deliberately. On the single-word
// x86-64 string ABI a <= 7-byte string is SSO-inline and allocates nothing, so
// short fixtures report a clean allocs == frees whether or not the leak exists —
// which is exactly how this hid from the x86-only suite while arm64's two-word
// strings heap-allocated the same values. The arm64 mirror is not optional here.
const argTempReclaimTempRecvSrc = `import "std/i32";
import "std/string";
function main(): i32 {
    var acc: i32 = 0;
    var k: i32 = 1;
    while (k < 40) {
        var s: string = (k * 66049).to_binary().pad_start(40, "0");
        var i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

// The control: identical, with the intermediate named. Balanced before and
// after — if this ever diverges from the leg above, the fix has become a
// double-free rather than a reclaim.
const argTempReclaimBoundRecvSrc = `import "std/i32";
import "std/string";
function main(): i32 {
    var acc: i32 = 0;
    var k: i32 = 1;
    while (k < 40) {
        var h: string = (k * 66049).to_binary();
        var s: string = h.pad_start(40, "0");
        var i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

// pad_start's PASS-THROUGH arm: width 2 against a >= 32-byte receiver, so
// `sl >= n` holds and the result IS the argument. Reads every byte back, so an
// over-release shows as a wrong exit code.
const argTempReclaimPassThroughSrc = `import "std/i32";
import "std/string";
function main(): i32 {
    var acc: i32 = 0;
    var k: i32 = 1;
    while (k < 40) {
        var s: string = (k * 66049).to_binary().pad_start(2, "0");
        var i: i32 = 0;
        while (i < s.len()) { acc = (acc * 31 + (s[i] as i32)) % 100003; i = i + 1; }
        k = k + 1;
    }
    return acc % 251;
}`

var argTempReclaimCases = []struct {
	name string
	src  string
	exit int
}{
	{"temp-receiver", argTempReclaimTempRecvSrc, 240},
	{"bound-receiver", argTempReclaimBoundRecvSrc, 240},
	{"pass-through-arm", argTempReclaimPassThroughSrc, 24},
}

func checkArgTempReclaim(t *testing.T, stderr string, code, wantExit int) {
	t.Helper()
	if code != wantExit {
		t.Fatalf("exit=%d, want %d — the string is wrong, which for the pass-through leg "+
			"means the post-call dec freed a buffer the result still referenced", code, wantExit)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations recorded — fixture drift (are the intermediates still > 7 bytes?)")
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want allocs==frees / live==0", allocs, frees, live)
	}
}

func TestX86_64LeakCheckStringArgTempReclaim(t *testing.T) {
	for _, tc := range argTempReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			checkArgTempReclaim(t, stderr, code, tc.exit)
		})
	}
}

func TestArm64LeakCheckStringArgTempReclaim(t *testing.T) {
	for _, tc := range argTempReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			checkArgTempReclaim(t, stderr, code, tc.exit)
		})
	}
}
