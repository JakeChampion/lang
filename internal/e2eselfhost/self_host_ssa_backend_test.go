package e2eselfhost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The self-host `-backend ssa` (docs/SELFHOST-SSA-BACKEND.md): each function
// the production lift admits is emitted from SSA form with registers, the rest
// by the stack machine, in one module. These tests build the self-host CLI for
// this host once, compile each program both ways for every target the host can
// run output for (its own ISA natively, the other under its qemu user emulator
// when present), run both, and compare stdout and exit status. FERN_SSA_REPORT=1
// gives the per-module tally and a line per declined function, so a program
// can also pin WHICH functions the backend emitted: a program the lift admits
// whole must report no declined function, or the coverage has silently
// narrowed and the differential is comparing the stack machine with itself.

// ssaBackendProgram is one differential case.
type ssaBackendProgram struct {
	name string
	src  string
	// allSSA requires every function of the module to go through the SSA
	// backend (the tally reads "N of N ... 0 declined").
	allSSA bool
	// viaSSA names functions that must not appear among the declined lines.
	viaSSA []string
}

var ssaBackendPrograms = []ssaBackendProgram{
	{name: "fact", allSSA: true, src: `
function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }
function main(): i32 { return fact(5) - 100; }
`},
	{name: "fib_swap", allSSA: true, src: `
function fib(n: i32): i32 {
    var a: i32 = 0;
    var b: i32 = 1;
    var i: i32 = 0;
    while (i < n) { var t: i32 = a; a = b; b = t + b; i = i + 1; }
    return a;
}
function main(): i32 { return fib(10) % 256; }
`},
	{name: "wide_and_casts", allSSA: true, src: `
function mix(x: i64, y: i64): i64 { return (x * y) / 7 + (x % 5) - (y << 3); }
function narrow(v: i64): i32 { return (v as i32) & 255; }
function shifts(a: i32, k: i32): i32 { return ((a << k) | (a >> 1)) ^ (((a as u32) >> 2) as i32); }
function unsigned(a: u32, b: u32): i32 { if (a < b) { return 1; } return 0; }
function main(): i32 {
    var m: i64 = mix(123456789, 987654321);
    var s: i32 = shifts(1000, 3) + shifts(7, 40);
    var u: i32 = unsigned(4000000000, 5) * 10 + unsigned(5, 4000000000);
    var r: i32 = narrow(m) + s + u;
    return r & 127;
}
`},
	{name: "control_flow", allSSA: true, src: `
function first_square_over(limit: i32): i32 {
    var i: i32 = 0;
    while (i < 1000) {
        if (i * i > limit) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}
function nested(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var j: i32 = 0;
        while (j < n) {
            if (j == 2) { j = j + 1; continue; }
            if (i + j > 7) { break; }
            t = t + i * j;
            j = j + 1;
        }
        i = i + 1;
    }
    return t;
}
function signs(a: i32): i32 {
    if (a < 0) { return 0 - 1; } else if (a == 0) { return 0; } else { return 1; }
}
function main(): i32 {
    var d: i32 = (0 - 2147483647 - 1) / (0 - 1);
    var e: i32 = (0 - 7) % 3;
    var k: i32 = first_square_over(50) + nested(5) + signs(0 - 9) + signs(0) + signs(4) + (d % 7) + e;
    if (!(k > 1000)) { k = k + 100; }
    return k & 255;
}
`},
	// A loop-invariant parameter carried through the header phi while the
	// body calls out: the phi's back-edge operand is the phi itself, and the
	// allocator once let a body temporary take its register, so the next
	// iteration compared against garbage (borrowed_forward_lifetime returned
	// 11, the bit sieve counted no primes).
	{name: "invariant_through_phi", allSSA: true, src: `
@noinline
function step(x: i32, i: i32): i32 { return x + i; }
function forward(x: i32, rounds: i32): i32 {
    var i: i32 = 0;
    var acc: i32 = x;
    while (i < rounds) { acc = step(acc, i); i = i + 1; }
    return acc;
}
@noinline
function bit(w: i32, i: i32): boolean { return ((w >> (i & 31)) & 1) == 1; }
function count_bits(w: i32, n: i32): i32 {
    var c: i32 = 0;
    for i in 0..(n + 1) { if (bit(w, i)) { c = c + 1; } }
    return c;
}
function main(): i32 { return (forward(3, 10) + count_bits(1431655765, 31) * 10) % 256; }
`},
	// The box layouts: records, arrays, tuples, Option boxes, enum variants,
	// string literals and bytes, each lifted onto the flat backend's layout
	// (ssa.fern kinds 40 to 48) rather than through a runtime call.
	{name: "boxes", allSSA: true, src: `
struct P { x: i32, y: i32 }
enum Shape { Dot(i32), Line(i32, i32), Empty }
function mk(a: i32, b: i32): P { return P { x: a, y: b }; }
function sum(p: P): i32 { return p.x + p.y; }
function third(xs: i32[]): i32 { if (xs.len() > 2) { return xs[2]; } return 0 - 1; }
function pair(a: i32): (i32, i32) { return (a, a * 2); }
function unwrap(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0; } } }
function area(s: Shape): i32 {
    match (s) {
        Dot(r) => { return r; },
        Line(a, b) => { return a * b; },
        Empty => { return 0; },
    }
}
function letters(): i32 { var s: string = "fern"; return s.len() * 100 + (s[1] as i32); }
function main(): i32 {
    var p: P = mk(3, 4);
    var xs: i32[] = [5, 6, 7];
    var t: (i32, i32) = pair(9);
    var total: i32 = sum(p) + third(xs) + t.0 + t.1 + unwrap(Some(11)) + unwrap(None)
        + area(Dot(2)) + area(Line(3, 4)) + area(Empty) + letters();
    return total % 256;
}
`},
	// The runtime calls the stack machine makes for string and array ops: the
	// Fern-compiled helpers on the stack ABI (concat, equality, ordering) and
	// the register-ABI routines (array push).
	{name: "runtime_calls", viaSSA: []string{"join", "same", "before", "grow", "mid", "main"}, src: `
function join(a: string, b: string): string { return a + b; }
function same(a: string, b: string): boolean { return a == b; }
function before(a: string, b: string): boolean { return a < b; }
function grow(xs: i32[], v: i32): i32[] { return xs.append(v); }
function mid(s: string, lo: i32, hi: i32): i32 {
    match (s[lo:hi]) {
        Some(t) => { return t.len() * 10 + (t[0] as i32); },
        None => { return 0 - 1; },
    }
}
function main(): i32 {
    var s: string = join("fe", "rn");
    var xs: i32[] = grow([1, 2], 3);
    var n: i32 = xs.len() * 10 + s.len() + xs[2] + mid(s, 1, 3) + mid("abcdef", 2, 5) + mid("ab", 1, 9);
    if (same(s, "fern")) { n = n + 100; }
    if (before("apple", s)) { n = n + 1; }
    return n % 256;
}
`},
	// A loop appending a constant through the owned push: the array and the
	// value both sit in registers at the runtime call, and on x86-64 the
	// argument registers are among the allocatable ones, so the call must
	// read both homes before it writes either.
	{name: "byte_sieve", viaSSA: []string{"sieve", "count", "main"}, src: `
function sieve(n: i32): boolean[] {
    var s: boolean[] = [];
    for i in 0..(n + 1) { s = s.append(true); }
    if (n >= 0) { s = s.with(0, false); }
    if (n >= 1) { s = s.with(1, false); }
    var i: i32 = 2;
    while (i * i <= n) {
        if (s[i]) {
            var j: i32 = i * i;
            while (j <= n) { s = s.with(j, false); j = j + i; }
        }
        i = i + 1;
    }
    return s;
}
function count(s: boolean[]): i32 {
    var n: i32 = 0;
    for b in s { if (b) { n = n + 1; } }
    return n;
}
function main(): i32 { return count(sieve(1000)); }
`},
	// std/json's array parser ends its loop body in a return, so the block
	// holding the return reaches the loop's end live and terminated; the lift
	// must append it, or the if before it branches to a label nothing defines.
	{name: "json_array", viaSSA: []string{"json____json_p_array", "json____json_p_value"}, src: `
import "core/cmp";
import "std/json";
function main(): i32 {
    match (json.json_parse_result("[1, [2, 3], [], {\"k\": [4]}]")) {
        Ok(v) => { return json.json_encode(v).len(); },
        Err(e) => { return 1; },
    }
}
`},
	// A mixed module: main and the string helpers keep the stack machine, the
	// integer functions go through the SSA backend, and both call each other
	// through the shared stack ABI.
	{name: "mixed_module", viaSSA: []string{"sum_to", "gcd", "eight"}, src: `
import "std/i32";
function sum_to(n: i32): i32 { var s: i32 = 0; var i: i32 = 1; while (i <= n) { s = s + i; i = i + 1; } return s; }
function gcd(a: i32, b: i32): i32 { while (b != 0) { var t: i32 = b; b = a % b; a = t; } return a; }
function eight(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a - b + c - d + e - f + g - h; }
function main(): i32 {
    print(sum_to(100).to_string());
    print(gcd(1071, 462).to_string());
    print(eight(1, 2, 3, 4, 5, 6, 7, 8).to_string());
    var xs: i32[] = [3, 1, 2];
    print((xs.len() + gcd(xs[0], xs[2])).to_string());
    return 3;
}
`},
}

// ssaBackendTarget is one target this host can run output for: the target
// name and the runner prefix for a binary built for it ("" when native).
type ssaBackendTarget struct {
	target string
	runner []string
}

// ssaBackendHost is the self-host CLI built for this host, the targets whose
// output this host can run, and the stdlib root.
type ssaBackendHost struct {
	cli     string
	targets []ssaBackendTarget
	stdlib  string
}

var (
	ssaHostOnce sync.Once
	ssaHost     ssaBackendHost
	ssaHostSkip string
	ssaHostErr  string
)

// hostTargets lists the targets this host can run output for: the native
// one, plus the other ISA through its qemu user emulator when that is on PATH.
// The CLI itself is built for the host and never run under an emulator, since
// it takes host filesystem paths.
func hostTargets() (cliTarget string, targets []ssaBackendTarget, skip string) {
	emulated := func(target, qemu string) {
		if q, err := exec.LookPath(qemu); err == nil {
			targets = append(targets, ssaBackendTarget{target: target, runner: []string{q}})
		}
	}
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		cliTarget = "arm64-darwin"
		targets = append(targets, ssaBackendTarget{target: "arm64-darwin"})
		emulated("x86-64-linux", "qemu-x86_64")
	case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
		cliTarget = "arm64-linux"
		targets = append(targets, ssaBackendTarget{target: "arm64-linux"})
		emulated("x86-64-linux", "qemu-x86_64")
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		cliTarget = "x86-64-linux"
		targets = append(targets, ssaBackendTarget{target: "x86-64-linux"})
		emulated("arm64-linux", "qemu-aarch64")
	default:
		skip = runtime.GOOS + "/" + runtime.GOARCH + " cannot run the self-host CLI or its output"
	}
	return
}

// selfHostCLIForHost builds examples/self_host/fern.fern once per test binary
// as a binary this host executes directly.
func selfHostCLIForHost(t *testing.T) ssaBackendHost {
	t.Helper()
	ssaHostOnce.Do(func() {
		cliTarget, targets, skip := hostTargets()
		if skip != "" {
			ssaHostSkip = skip
			return
		}
		fern := buildLangBinForInterp(t)
		src, err := filepath.Abs("../../examples/self_host/fern.fern")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		stdlib, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		dir, err := os.MkdirTemp("", "selfhost-ssa-cli-")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		cli := filepath.Join(dir, "fern")
		if out, err := exec.Command(fern, "-target", cliTarget, "-o", cli, src).CombinedOutput(); err != nil {
			// Recorded rather than fatal here: sync.Once runs this for the
			// FIRST test only, so failing inside it left every later test with
			// a zero-valued host and no skip reason — they indexed an empty
			// targets slice and panicked, which reads as a test bug rather
			// than as the build failure it is.
			ssaHostErr = fmt.Sprintf("building the self-host CLI for %s: %v\n%s", cliTarget, err, out)
			return
		}
		ssaHost = ssaBackendHost{cli: cli, targets: targets, stdlib: stdlib}
	})
	if ssaHostErr != "" {
		t.Fatal(ssaHostErr)
	}
	if ssaHostSkip != "" {
		t.Skip(ssaHostSkip)
	}
	return ssaHost
}

// compileWith runs the CLI on src for the target, with the extra flags, and
// returns the CLI's stderr (the SSA report lives there).
func (h ssaBackendHost) compileWith(t *testing.T, tg ssaBackendTarget, src, out string, extra ...string) string {
	t.Helper()
	args := append([]string{"-target", tg.target}, extra...)
	args = append(args, "-o", out, src, h.stdlib)
	cmd := exec.Command(h.cli, args...)
	cmd.Env = append(os.Environ(), "FERN_SSA_REPORT=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", args, err, stderr.String())
	}
	return stderr.String()
}

// runProduced runs a binary the CLI produced and returns its stdout and exit
// status. A program that does not exit normally within the timeout fails.
func (h ssaBackendHost) runProduced(t *testing.T, tg ssaBackendTarget, bin string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if len(tg.runner) == 0 {
		cmd = exec.CommandContext(ctx, bin)
	} else {
		cmd = exec.CommandContext(ctx, tg.runner[0], append(tg.runner[1:], bin)...)
	}
	var stdout strings.Builder
	cmd.Stdout = &stdout
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally: %v", bin, cmd.ProcessState)
	}
	return stdout.String(), cmd.ProcessState.ExitCode()
}

var ssaTallyRe = regexp.MustCompile(`FERN_SSA: module: (\d+) of (\d+) functions through the SSA backend, (\d+) declined`)

// ssaTally reads the module tally out of the CLI's report.
func ssaTally(t *testing.T, report string) (emitted, total, declined int) {
	t.Helper()
	m := ssaTallyRe.FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("no FERN_SSA module tally in the report:\n%s", report)
	}
	emitted, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	declined, _ = strconv.Atoi(m[3])
	return
}

func TestSelfHostSSABackendAgreesWithStackMachine(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, tg := range h.targets {
		tg := tg
		for _, p := range ssaBackendPrograms {
			p := p
			t.Run(tg.target+"/"+p.name, func(t *testing.T) {
				dir := t.TempDir()
				src := filepath.Join(dir, p.name+".fern")
				if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
					t.Fatal(err)
				}
				flat := filepath.Join(dir, p.name+".flat")
				ssa := filepath.Join(dir, p.name+".ssa")
				h.compileWith(t, tg, src, flat)
				report := h.compileWith(t, tg, src, ssa, "-backend", "ssa")

				emitted, total, declined := ssaTally(t, report)
				if emitted == 0 {
					t.Errorf("the SSA backend emitted no function of %s:\n%s", p.name, report)
				}
				if p.allSSA && (declined != 0 || emitted != total) {
					t.Errorf("%s: want every function through the SSA backend, got %d of %d with %d declined:\n%s", p.name, emitted, total, declined, report)
				}
				for _, fn := range p.viaSSA {
					if strings.Contains(report, "FERN_SSA: "+fn+": ") {
						t.Errorf("%s: %s was declined by the SSA backend:\n%s", p.name, fn, report)
					}
				}

				flatOut, flatExit := h.runProduced(t, tg, flat)
				ssaOut, ssaExit := h.runProduced(t, tg, ssa)
				if flatExit != ssaExit {
					t.Errorf("%s: exit %d through the stack machine, %d through the SSA backend", p.name, flatExit, ssaExit)
				}
				if flatOut != ssaOut {
					t.Errorf("%s: stdout differs\n--- stack machine\n%s\n--- SSA backend\n%s", p.name, flatOut, ssaOut)
				}
			})
		}
	}
}

// The backend exists for the two native ISAs; another target is refused with
// the targets it is available for, as native's `-backend ssa` refuses wasm.
// `-backend flat` names the stack machine, as it does on native, and a name
// nothing implements is an error rather than a fall-through.
func TestSelfHostSSABackendRefusesOtherTargets(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-target", "wasm32-wasi", "-backend", "ssa", "-o", filepath.Join(dir, "p"), src, h.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("-backend ssa for wasm32-wasi was accepted")
	}
	if !strings.Contains(string(out), "-backend ssa is not available for -target wasm32-wasi") {
		t.Errorf("refusal does not name the target: %s", out)
	}
	out, err = exec.Command(h.cli, "-target", h.targets[0].target, "-backend", "nope", "-o", filepath.Join(dir, "p"), src, h.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("-backend nope was accepted")
	}
	if !strings.Contains(string(out), "unknown -backend: nope") {
		t.Errorf("unknown backend not reported: %s", out)
	}
	tg := h.targets[0]
	h.compileWith(t, tg, src, filepath.Join(dir, "dflt"))
	h.compileWith(t, tg, src, filepath.Join(dir, "flat"), "-backend", "flat")
	dflt, err := os.ReadFile(filepath.Join(dir, "dflt"))
	if err != nil {
		t.Fatal(err)
	}
	flat, err := os.ReadFile(filepath.Join(dir, "flat"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dflt, flat) {
		t.Errorf("-backend flat and the default emitter produced different executables")
	}
}

// A second `-o` to the same path replaces the executable rather than
// rewriting it in place. macOS caches the code-signature verdict of an
// executable by inode, so an in-place rewrite is killed at exec with "Code
// Signature Invalid" while a byte-identical copy at a fresh path runs. The
// portable observation is a handle held open across the second compile: a
// replaced file leaves it reading the first program, an in-place rewrite
// shows it the second. Apple Silicon also runs the second program, which is
// the exec the cache would have killed.
// The lift gives every merge a phi per local slot and prune_dead removes
// almost all of them, so the id space of a large function is hundreds of
// times its live values. The frame is sized by the spilled values, not by
// the ids: a function with 200 locals and 200 merges reserves well under
// 16 KB on both ISAs, where a slot per id would be over 300 KB.
func TestSelfHostSSAFrameIsSizedBySpills(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("function wide(n: i32): i32 {\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    var v%d: i32 = n + %d;\n", i, i)
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    if (n > %d) { v%d = v%d + 1; }\n", i, i, (i+1)%200)
	}
	b.WriteString("    var s: i32 = 0;\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    s = s + v%d;\n", i)
	}
	b.WriteString("    return s;\n}\nfunction main(): i32 { return wide(3) % 256; }\n")
	src := filepath.Join(dir, "wide.fern")
	if err := os.WriteFile(src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		frame  *regexp.Regexp
	}{
		{"x86-64-linux", regexp.MustCompile(`__fn_wide:\n(?:.*\n){1,8}?\s+subq \$(\d+), %rsp`)},
		// Over 4,095 bytes the arm64 prologue builds the immediate in x17; a
		// movk after the movz would mean a frame over 64 KB.
		{"arm64-linux", regexp.MustCompile(`__fn_wide:\n(?:.*\n){1,10}?\s+(?:sub sp, sp, #|movz x17, #)(\d+)\n(\s+movk)?`)},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "wide-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", out, src, h.stdlib)
		cmd.Env = append(os.Environ(), "FERN_SSA_REPORT=1")
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		if strings.Contains(string(report), "FERN_SSA: wide:") {
			t.Fatalf("%s: wide was declined:\n%s", c.target, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		m := c.frame.FindSubmatch(asm)
		if m == nil {
			t.Fatalf("%s: no frame reservation found after __fn_wide", c.target)
		}
		n, _ := strconv.Atoi(string(m[1]))
		if len(m) > 2 && len(m[2]) > 0 {
			n += 64 * 1024
		}
		if n > 16*1024 {
			t.Errorf("%s: wide reserves %d bytes of frame; a slot per spilled value stays under 16 KB", c.target, n)
		}
	}
}

func TestSelfHostOutputReplacesExecutable(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.fern")
	second := filepath.Join(dir, "second.fern")
	if err := os.WriteFile(first, []byte("function main(): i32 { return 20; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("function main(): i32 { return 55; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tg := h.targets[0]
	out := filepath.Join(dir, "prog")
	h.compileWith(t, tg, first, out)
	firstBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, exit := h.runProduced(t, tg, out); exit != 20 {
		t.Fatalf("first program exit %d, want 20", exit)
	}
	held, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	h.compileWith(t, tg, second, out)
	stillFirst, err := io.ReadAll(held)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stillFirst, firstBytes) {
		t.Fatalf("the handle opened before the second compile no longer reads the first program; the executable was rewritten in place, not replaced")
	}
	if _, exit := h.runProduced(t, tg, out); exit != 55 {
		t.Fatalf("second program exit %d, want 55", exit)
	}
}

// TestSelfHostCLIBuildsForEveryNativeTarget builds the self-host compiler for
// both native targets on whatever host runs it.
//
// The build is a cross-compile, so the host does not decide what can be built —
// but every other gate here takes its target from the host (hostTargets), which
// means each machine tests one of the two and neither machine tests both. That
// is how #9525 reached main: the arm64 default flip left four runtime helpers
// with no emitter, and `fern -target arm64-linux examples/self_host/fern.fern`
// failed to link on every push while the x86-64 lanes stayed green.
//
// The whole compiler is the point: it is the largest program in the tree and
// the one that reaches the widest set of runtime helpers, so a helper missing
// from a backend shows up here and almost nowhere else. `-o` is passed so the
// link runs too; emit alone would miss a symbol the assembler resolves.
func TestSelfHostCLIBuildsForEveryNativeTarget(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src, err := filepath.Abs("../../examples/self_host/fern.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "fern")
			if o, err := exec.Command(fern, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("the self-host compiler does not build for %s: %v\n%s", target, err, o)
			}
			st, err := os.Stat(out)
			if err != nil {
				t.Fatalf("no binary written for %s: %v", target, err)
			}
			if st.Size() == 0 {
				t.Fatalf("the %s build wrote an empty binary", target)
			}
		})
	}
}
