package e2eselfhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The self-host `-backend ssa` (docs/SELFHOST-SSA-BACKEND.md): each function
// the production lift admits is emitted from SSA form with registers, the rest
// by the stack machine, in one module. These tests build the self-host CLI for
// this host once, compile each program both ways for an arm64 target the host
// can run, run both, and compare stdout and exit status. FERN_SSA_REPORT=1
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

// ssaBackendHost is the self-host CLI built for this host, the arm64 target
// whose output this host can run, and the runner prefix for that output.
type ssaBackendHost struct {
	cli    string
	target string
	runner []string
	stdlib string
}

var (
	ssaHostOnce sync.Once
	ssaHost     ssaBackendHost
	ssaHostSkip string
)

// selfHostCLIForHost builds examples/self_host/fern.fern once per test binary
// as a binary this host executes directly: an arm64-darwin Mach-O on Apple
// Silicon, an arm64-linux ELF on arm64 Linux, an x86-64 ELF on x86-64 Linux
// (whose arm64-linux output then runs under qemu-aarch64). The CLI takes host
// filesystem paths, so it is never run under an emulator itself.
func selfHostCLIForHost(t *testing.T) ssaBackendHost {
	t.Helper()
	ssaHostOnce.Do(func() {
		var cliTarget, outTarget string
		var runner []string
		switch {
		case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
			cliTarget, outTarget = "arm64-darwin", "arm64-darwin"
		case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
			cliTarget, outTarget = "arm64-linux", "arm64-linux"
		case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
			qemu, err := exec.LookPath("qemu-aarch64")
			if err != nil {
				ssaHostSkip = "qemu-aarch64 not on PATH; the arm64-linux output cannot run here"
				return
			}
			cliTarget, outTarget, runner = "x86-64-linux", "arm64-linux", []string{qemu}
		default:
			ssaHostSkip = runtime.GOOS + "/" + runtime.GOARCH + " cannot run the self-host CLI or its arm64 output"
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
			t.Fatalf("building the self-host CLI for %s: %v\n%s", cliTarget, err, out)
		}
		ssaHost = ssaBackendHost{cli: cli, target: outTarget, runner: runner, stdlib: stdlib}
	})
	if ssaHostSkip != "" {
		t.Skip(ssaHostSkip)
	}
	return ssaHost
}

// compileWith runs the CLI on src for the host's arm64 target, with the extra
// flags, and returns the CLI's stderr (the SSA report lives there).
func (h ssaBackendHost) compileWith(t *testing.T, src, out string, extra ...string) string {
	t.Helper()
	args := append([]string{"-target", h.target}, extra...)
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
func (h ssaBackendHost) runProduced(t *testing.T, bin string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if len(h.runner) == 0 {
		cmd = exec.CommandContext(ctx, bin)
	} else {
		cmd = exec.CommandContext(ctx, h.runner[0], append(h.runner[1:], bin)...)
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
	for _, p := range ssaBackendPrograms {
		p := p
		t.Run(p.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, p.name+".fern")
			if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
				t.Fatal(err)
			}
			flat := filepath.Join(dir, p.name+".flat")
			ssa := filepath.Join(dir, p.name+".ssa")
			h.compileWith(t, src, flat)
			report := h.compileWith(t, src, ssa, "-backend", "ssa")

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

			flatOut, flatExit := h.runProduced(t, flat)
			ssaOut, ssaExit := h.runProduced(t, ssa)
			if flatExit != ssaExit {
				t.Errorf("%s: exit %d through the stack machine, %d through the SSA backend", p.name, flatExit, ssaExit)
			}
			if flatOut != ssaOut {
				t.Errorf("%s: stdout differs\n--- stack machine\n%s\n--- SSA backend\n%s", p.name, flatOut, ssaOut)
			}
		})
	}
}

// The backend exists for arm64 only; another target is refused with the
// targets it is available for, as native's `-backend ssa` refuses wasm.
func TestSelfHostSSABackendRefusesOtherTargets(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-target", "x86-64-linux", "-backend", "ssa", "-o", filepath.Join(dir, "p"), src, h.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("-backend ssa for x86-64-linux was accepted")
	}
	if !strings.Contains(string(out), "-backend ssa is not available for -target x86-64-linux") {
		t.Errorf("refusal does not name the target: %s", out)
	}
	out, err = exec.Command(h.cli, "-target", h.target, "-backend", "flat", "-o", filepath.Join(dir, "p"), src, h.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("-backend flat was accepted")
	}
	if !strings.Contains(string(out), "unknown -backend: flat") {
		t.Errorf("unknown backend not reported: %s", out)
	}
}

// A second `-o` to the same path replaces the executable rather than
// rewriting it in place. macOS caches the code-signature verdict of an
// executable by inode, so an in-place rewrite is killed at exec with "Code
// Signature Invalid" while a byte-identical copy at a fresh path runs; the
// inode is the assertion every unix host can make, and Apple Silicon also runs
// the second program.
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
	out := filepath.Join(dir, "prog")
	h.compileWith(t, first, out)
	before := inodeOfBinary(t, out)
	if _, exit := h.runProduced(t, out); exit != 20 {
		t.Fatalf("first program exit %d, want 20", exit)
	}
	h.compileWith(t, second, out)
	if after := inodeOfBinary(t, out); after == before {
		t.Fatalf("the second compile kept inode %d; an executable must be replaced, not overwritten", after)
	}
	if _, exit := h.runProduced(t, out); exit != 55 {
		t.Fatalf("second program exit %d, want 55", exit)
	}
}

func inodeOfBinary(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no inode on this host")
	}
	return uint64(st.Ino)
}
