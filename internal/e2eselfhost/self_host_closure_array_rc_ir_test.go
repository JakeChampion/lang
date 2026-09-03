package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Closure-ARRAY element reclaim on the self-host IR path (#4354). A closure
// array local (`var fns: (() => i32)[] = [() => n, …]`) is an is_arr slot whose
// elements are env boxes; the shallow buffer dec every array slot took leaked
// every box — measured 40 B per element per round on x86-64 against 0 on
// native. A "CLOARR:"-credited slot now releases the boxes with the buffer
// through __fern_arrarr_free at scope exit and at the loop rebind.
//
// Three instruments per case, because each is blind to something the others
// see: FERN_LEAKCHECK's live_bytes (the leak direction), the FERN_SANITIZE
// quarantine (an over-release that the census reads as a CLEANER run — the
// `[c, …]` literal's `for f in fns` loop var was an owned array whose exit
// dec double-released a box, at live_bytes 0), and the exit code against the
// native compiler's (a miscompile). The refusal cases assert only the last two:
// a shape the credit must decline leaks, and must not free.

// closureArrayRcCases: programs the credit ADMITS. `want` is the native
// compiler's exit code for the same source.
var closureArrayRcCases = []struct {
	name string
	src  string
	want int
}{
	// The literal shape, indexed and called through a loop variable.
	{"literal-loop-call", `function go(n: i32): i32 {
    var fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { s = s + fns[i](); i = i + 1; }
    return s;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 1},
	// A "CAC:" producer: the factory's local moves out, the caller's binding
	// is the sole owner and takes the walk; the array is also borrowed by a
	// callee that binds an element (`var d = fns[0]` — the retained element
	// bind, which before the fix freed the caller's box from under it).
	{"factory-and-param-bind", `function mkfns(n: i32): (() => i32)[] { var a: (() => i32)[] = [() => n, () => n + 1]; return a; }
function consume(fns: (() => i32)[]): i32 { var d = fns[0]; return d(); }
function go(n: i32): i32 {
    var fns = mkfns(n);
    var s: i32 = consume(fns);
    var i: i32 = 0;
    while (i < fns.len()) { s = s + fns[i](); i = i + 1; }
    return s;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 1},
	// Loop-local and block-local arrays: the rebind release frees the
	// previous iteration's boxes, the exit sweep the last.
	{"loop-local-and-block-local", `function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var fns: (() => i32)[] = [() => i, () => i + 1];
        if (i % 2 == 0) { var gs: (() => i32)[] = [() => i * 2]; t = t + gs[0](); }
        t = (t + fns[0]() + fns[1]()) % 251;
        i = i + 1;
    }
    return t % 7;
}`, 0},
	// Element binds in a loop: each `var d = fns[i]` retains its box and the
	// rebind releases the previous one, so the walk sees every count paid.
	{"element-bind-loop", `function go(n: i32): i32 {
    var fns: (() => i32)[] = [() => n, () => n + 1];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { var d = fns[i % 2]; s = s + d(); i = i + 1; }
    var e = fns[0];
    return s + e() + fns[1]();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 5},
	// Closure LOCALS as elements — at the literal, at a self-append, in a
	// producer — are retained by the container, so the local's own release
	// and the walk's dec meet at one free. `for f in fns` with f only called
	// is a borrow the credit admits.
	{"closure-local-elements", `function build(n: i32): (() => i32)[] {
    var c = () => n * 3;
    var out: (() => i32)[] = [c];
    out = out.append(() => n + 1);
    return out;
}
function go(n: i32): i32 {
    var c = () => n;
    var fns: (() => i32)[] = [c, () => n + 1];
    fns = fns.append(c);
    var s: i32 = 0;
    for f in fns { s = s + f(); }
    var d = fns[2];
    var hs = build(n);
    return s + d() + c() + hs[0]() + hs[1]();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 2},
	// The `[c, …]` literal read as an array-of-arrays before the fix, which
	// made the foreach var an OWNED array: its exit dec plus `var e = fns[1]`'s
	// released one box twice — invisible to the census (live_bytes 0), fatal
	// under the quarantine.
	{"closure-local-first-element-foreach", `function go(n: i32): i32 {
    var c = () => n;
    var fns: (() => i32)[] = [c, () => n + 1];
    var d = fns[0];
    var e = fns[1];
    var s: i32 = 0;
    for f in fns { s = s + f(); }
    return s + d() + e() + c() + fns.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 1},
	// An annotated EMPTY literal grown by `.append`: the is_closurearr flag
	// arrives at the append, after the bind, so the credit alone routes both
	// release sites.
	{"annotated-empty-append", `function go(n: i32): i32 {
    var fns: (() => i32)[] = [() => n];
    var c = () => n + 1;
    fns = fns.append(c);
    return fns[0]() + fns[1]() + c();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 3},
	// An EMPTY `fn[]` literal whose element READ is lowered BEFORE the first
	// append and reached at runtime from iteration 2. The is_closurearr flag
	// used to arrive only at the append, so this read bound a plain scalar and
	// called the box pointer as code: SIGSEGV, where native runs it. The
	// declaration now carries the flag ("CLOAPPEND:"), which also leaves no
	// state in which the CLOARR credit could forgive a bind the retain missed.
	{"empty-append-read-before-append", `function go(n: i32): i32 {
    var fns: (() => i32)[] = [];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        if (i > 0) { var g = fns[0]; s = s + g(); }
        fns = fns.append(() => n + i);
        i = i + 1;
    }
    return s;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 6},
}

// closureArrayRcRefusals: shapes the credit must DECLINE — an element escaping
// uncounted through a return, a whole-array alias, a loop var stored into
// another container, an element handed to a struct literal. Each is exercised
// after the array is dead, so a wrongly credited walk is a use-after-free the
// sanitizer leg traps, never a quiet leak. Exit 0 is native's.
const closureArrayRcRefusalsSrc = `struct H { f: () => i32 }
function pick(n: i32): () => i32 { var fns: (() => i32)[] = [() => n, () => n + 1]; return fns[1]; }
function alias(n: i32): i32 { var fns: (() => i32)[] = [() => n]; var g = fns; return g[0]() + fns[0](); }
function stash(n: i32): i32 {
    var fns: (() => i32)[] = [() => n, () => n * 2];
    var keep: (() => i32)[] = [];
    for f in fns { keep = keep.append(f); }
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < keep.len()) { s = s + keep[i](); i = i + 1; }
    return s;
}
function hold(n: i32): i32 { var fns: (() => i32)[] = [() => n + 3]; var h: H = H { f: fns[0] }; return h.f(); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { var p = pick(i); t = (t + p() + alias(i) + stash(i) + hold(i)) % 251; i = i + 1; }
    return t % 7;
}`

// closureArrayRcFixpointSrc is the bump-allocator fixpoint form the wasm and
// arm64 legs run (neither has the leak census): every admitted shape at once,
// a warm-up churn, then a second churn that must not move the high-water mark.
// 99 = the underflow detector ticked (an over-release), 98 = growth, 97 = the
// two churns disagree (a value corrupted).
const closureArrayRcFixpointSrc = `function mkfns(n: i32): (() => i32)[] { var a: (() => i32)[] = [() => n, () => n + 1]; return a; }
function early(n: i32): i32 {
    var fns: (() => i32)[] = [];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        if (i > 0) { var g = fns[0]; s = s + g(); }
        fns = fns.append(() => n + i);
        i = i + 1;
    }
    return s;
}
function go(n: i32): i32 {
    var fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { s = s + fns[i](); i = i + 1; }
    var d = fns[1];
    var more: (() => i32)[] = [];
    more = more.append(() => n + 5);
    for f in more { s = s + f(); }
    var hs = mkfns(n);
    return s + d() + hs[0]() + hs[1]() + more.len() + early(n);
}
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var w: i32 = churn(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    if (w != x) { return 97; }
    return 0;
}`

func TestSelfHostClosureArrayRcIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// census compiles under FERN_LEAKCHECK and returns the run's exit code and
	// live bytes; sanitize compiles under FERN_SANITIZE and returns the exit
	// code and whether the sanitizer reported anything.
	census := func(t *testing.T, name, src string) (int, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		bin := buildBin(t, gcc, dir, name+"-census", asm)
		stderr, code := hevRun(t, runner, bin)
		allocs, _, live := parseLeakcheck(t, name, stderr)
		if allocs == 0 {
			t.Fatalf("%s: allocated nothing — the probe is not exercising the path", name)
		}
		return code, live
	}
	sanitize := func(t *testing.T, name, src string) (int, string) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_SANITIZE=1"})
		bin := buildBin(t, gcc, dir, name+"-sanitize", asm)
		stderr, code := hevRun(t, runner, bin)
		finding := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "fern-sanitizer:") && !strings.Contains(line, "leak") {
				finding = line
			}
		}
		return code, finding
	}

	for _, tc := range closureArrayRcCases {
		t.Run(tc.name, func(t *testing.T) {
			code, live := census(t, tc.name, tc.src)
			if code != tc.want {
				t.Errorf("%s: exit %d, want %d (native's)", tc.name, code, tc.want)
			}
			if live != 0 {
				t.Errorf("%s: live_bytes=%d at exit, want 0 — the closure array's element boxes leaked", tc.name, live)
			}
			scode, finding := sanitize(t, tc.name, tc.src)
			if scode != tc.want || finding != "" {
				t.Errorf("%s under FERN_SANITIZE: exit %d (want %d), finding %q — an over-release the census cannot see", tc.name, scode, tc.want, finding)
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		code, live := census(t, "refusals", closureArrayRcRefusalsSrc)
		if code != 0 {
			t.Errorf("refusals: exit %d, want 0 (native's)", code)
		}
		if live == 0 {
			t.Errorf("refusals: live_bytes 0 — a shape the credit must decline was freed; check the sanitizer leg")
		}
		scode, finding := sanitize(t, "refusals", closureArrayRcRefusalsSrc)
		if scode != 0 || finding != "" {
			t.Errorf("refusals under FERN_SANITIZE: exit %d (want 0), finding %q — an escaping element box was freed", scode, finding)
		}
	})
	t.Run("fixpoint", func(t *testing.T) {
		asm := hevCompile(t, runner, driverBin, closureArrayRcFixpointSrc, []string{"PATH=/usr/bin:/bin"})
		bin := buildBin(t, gcc, dir, "fixpoint", asm)
		if _, code := hevRun(t, runner, bin); code != 0 {
			t.Errorf("fixpoint exited %d, want 0 (99 = over-release; 98 = the closure arrays leaked; 97 = value corrupted)", code)
		}
	})
}

// TestSelfHostClosureArrayRcWasmIR: the wasm sibling through the -ir driver.
func TestSelfHostClosureArrayRcWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping closure-array RC wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(closureArrayRcFixpointSrc))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "closure-array-rc.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != 0 {
		t.Errorf("closure-array RC wasm IR fixpoint = %d, want 0 (99 = over-release; 98 = the closure arrays leaked; 97 = value corrupted)", got)
	}
}

// TestSelfHostClosureArrayRcIRArm64: the arm64 sibling under qemu.
func TestSelfHostClosureArrayRcIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(closureArrayRcFixpointSrc), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "closure-array-rc-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("closure-array RC arm64 fixpoint exited %d, want 0 (99 = over-release; 98 = the closure arrays leaked; 97 = value corrupted)", code)
	}
}
