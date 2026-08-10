package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Three legs run the fixture corpus through the SELF-HOST compiler:
// TestFernFixturesSelfHostWasm, TestFernFixturesSelfHostX86_64 and
// TestFernFixturesSelfHostArm64. Nothing else does.
//
// # Why these exist
//
// TestFernFixtures has four backends — interp, x86_64, arm64, wasm — and all
// four are the NATIVE compiler: arm64codegen.Emit, x86_64codegen.Emit,
// wasmbin.Build, internal/interp. So the declarative, reviewable, easy-to-extend
// corpus (335 directories) exercises the compiler that is meant to be FROZEN,
// while the compiler that is meant to become the product is covered by ~10,000
// `function main` string literals inlined across internal/e2eselfhost/*.go.
//
// That asymmetry is not academic. Every self-host bug found on 2026-08-02
// followed from it:
//
//   - #5992's three bugs (u32→f64 converting signed, u64→f64 emitting invalid
//     wasm, an undefined $__fern_u32_to_string) lived in the self-host wasm
//     emitter. Their fixture directories existed and PASSED, because the
//     fixture wasm leg is wasmbin.
//   - #5993 excluded `wasm` from three of those fixtures to record the
//     divergence — aiming the exclusion at the native backend, which was fine
//     all along (#5999 restored it).
//   - #5983's "the mode-0 construct decline set is empty" was true of the
//     harvested corpus and false of the language, because the corpus only
//     contained programs some test already fed to a wasm driver.
//
// One leg over the existing corpus would have caught all of them without anyone
// thinking to look, which is the point: coverage that has to be remembered is
// coverage that lapses.
//
// The wasm leg came first (#6005) and found twelve divergences on fixtures green
// for months. It covers ONE of the three targets the self-host compiler emits, so
// x86-64 parity was unmeasured at corpus level — an assumption of exactly the kind
// the wasm leg's first run disproved. The x86-64 leg closes that, and it is not
// merely the wasm leg with a flag changed; it reaches three things wasm
// structurally cannot:
//
//   - Full exit-code fidelity. A native binary propagates main's return over
//     0..255, so the VALUE check always applies — including the nine regex
//     fixtures whose expected 255 is a feature bitmask that WASI's [0..126)
//     cap makes permanently unmeasurable on the wasm leg (#6008's second half
//     asked for a stdout oracle to see them; this leg just sees them). Measured
//     on the first run: the wasm leg leaves SIXTEEN fixtures' values unchecked,
//     and six of those crash here.
//   - stdin. This leg feeds it, so the four stdin fixtures the wasm leg skips
//     are covered.
//   - The link step, which on wasm does not exist. `-target x86-64` emits GAS
//     text that gcc must accept, so the leg can catch the #5862/#6022 class (a
//     Fern function named after a register/mnemonic emitting asm the assembler
//     rejects).
//   - Crashes. A trap on wasm is a clean "wasm trap:" string; a native
//     miscompile is a SIGSEGV or an exit-137 arena abort, both distinguished
//     here from a wrong answer.
//
// # Driver choice
//
// fern.fern — the real CLI — rather than wasm_ir_run / asm_ir_run, because it
// LOADS MODULES. Roughly a third of the corpus imports std/ or core/, and a
// driver without a loader silently ignores the import and then reports a broken
// program's verdict (see parser.warn_unresolved_imports, #6004). Using the
// loading driver is also what makes the legs a faithful test of what a user runs.
//
// # Cost, and why they are opt-in
//
// Building fern.fern is a heavy self-host driver build (~4.3 GB reserved by the
// harness's memory limiter). It is paid ONCE per leg and amortised over the whole
// corpus; each fixture is then a native-binary invocation plus a run. Still, that
// is minutes rather than the 15s the native fixture run takes, so all three are
// gated on FERN_SELFHOST_FIXTURES=1 and run as their own CI lanes rather than
// slowing every local `go test ./internal/e2e`.
//
// # What they skip, and why each skip is principled
//
//   - compile-error fixtures (expected.error): front-end only, backend-agnostic,
//     already covered once by TestFernFixtures.
//   - fixtures whose `backends` file omits this leg's target: if a program cannot
//     run on the native backend for a target it will not run on the self-host one
//     either, and a shared opt-out beats inventing a second exclusion mechanism.
//   - fixtures with stdin, on the WASM leg only: the self-host CLI compiles to a
//     .wat that wasmtime runs, and threading stdin through that adds a variable
//     that leg is not trying to test. Four fixtures. The native legs feed stdin
//     normally.
//   - fixtures listed in the leg's known-divergences file: programs where the
//     self-host path is KNOWN to disagree with the native compiler today.
//     Following the deadcode-gate pattern, they are listed rather than silently
//     skipped, each with the reason, so the lane is green and a NEW divergence
//     fails. A listed fixture that starts PASSING also fails — an allowlist
//     nobody prunes becomes a place bugs go to be forgotten.
//
// # One harness asymmetry to know about before filing a divergence
//
// The native legs' front end (LoadCheckMono) INJECTS `import "core/int";` into
// the entry file so wasmbin's PrintMainResult can stringify main's result. The
// self-host CLI injects nothing. So a fixture that leans on a name that import
// happens to supply passes natively and fails here with a missing-name error.
// That is a harness artifact, not a compiler divergence: check a suspicious
// compile failure against `bin/fern -check` on the unmodified fixture before
// writing it down.
func TestFernFixturesSelfHostWasm(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "wasm32-wasi",
		target:    "wasm32-wasi",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-wasm-known-divergences.txt",
		skipStdin: true,
		// A fixture whose expected.exit is >= 126 keeps its trap/rejection check
		// but loses its VALUE check: WASI refuses an exit status outside
		// [0..126), so wasmtime reports 1 and the program's answer is invisible.
		// Measured: return 125 exits 125; return 126, and 255, both exit 1 with
		// "exit with invalid exit status outside of [0..126)". The native wasm
		// leg dodges this by folding main's result into stdout (wasmbin's
		// PrintMainResult); the self-host emit has no equivalent. The x86-64 leg has
		// no such blind spot, which is half of why it exists.
		//
		// This distinction is load-bearing, and getting it wrong in both
		// directions is easy. The first run of this leg reported 26 mismatches;
		// 14 were only the clamp — including nine regex fixtures whose expected
		// 255 is a feature bitmask, which read as a broken regex engine and was
		// not. But blanket-skipping those fixtures then discarded four REAL
		// findings, because wasmtime distinguishes the two cases in its cause
		// chain and a trap is detectable no matter what the program meant to
		// return:
		//
		//	exit with invalid exit status outside of [0..126)   → unmeasurable, fine
		//	wasm trap: wasm `unreachable` instruction executed  → a real failure
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			cmd := exec.Command(fernBin, "-target", "wasm32-wasi", f.mainPath, stdlibRoot, "-o", watPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				failf("self-host compile failed: %v\n%s", err, out)
				return
			}
			run := exec.Command("wasmtime", "run", watPath)
			out, _ := run.CombinedOutput()
			exit := run.ProcessState.ExitCode()

			// Unlike the native wasm leg, the self-host emit does NOT fold
			// main's result into stdout (wasmbin does that under
			// PrintMainResult). Here the process exit code IS the program's
			// return, so compare it directly — and separate "wasmtime refused
			// the module" from "the program returned N", which a bare exit code
			// conflates. That conflation is what made #5992's two conversion
			// bugs look like one: a validation failure and a program returning
			// 1 are both exit 1.
			rejected, why := wasmRejected(string(out))
			switch {
			case rejected:
				failf("wasmtime REJECTED the self-host module — not a wrong answer, the artifact is invalid or incomplete (%s):\n%s", why, out)
			case strings.Contains(string(out), "wasm trap:"):
				// A trap is a real failure whatever the program meant to
				// return, so it is checked even for the fixtures below whose
				// VALUE is unmeasurable. Four regex fixtures fail exactly here.
				failf("self-host wasm TRAPPED:\n%s", out)
			case f.wantExit >= 126:
				// Value unmeasurable (see the note above). Reaching here means
				// it neither trapped nor was rejected, which is all this leg
				// can honestly assert.
				t.Logf("ran clean; exit value unmeasurable (expected %d, WASI caps at 125)", f.wantExit)
			default:
				if exit != f.wantExit {
					failf("self-host wasm exit = %d, want %d\n%s", exit, f.wantExit, out)
				}
				if f.exact && f.wantOut != "" && !strings.Contains(string(out), f.wantOut) {
					failf("stdout missing expected output\n got: %q\nwant: %q", out, f.wantOut)
				}
			}
		},
	})
}

// TestFernFixturesSelfHostX86_64 runs the corpus through `fern -target x86-64`:
// the self-host x86 emitter, assembled + linked IN-PROCESS by x86_native +
// elf.fern (no `.s`, no gcc), executed natively.
//
// This leg used to stop at asm text and hand it to gcc, because the self-host
// x86-64 path had no in-process assembler. It has one now, so both Linux legs
// produce the finished binary by themselves and the corpus tests the
// self-hosted toolchain end to end on each. The external link is gone rather
// than kept alongside: it was the toolchain, and keeping a second path would
// mean the leg no longer tests what `-target x86-64` actually does.
//
// One thing went with it. A Fern function named after an x86 register or
// directive (`and`, `not` — the #5862 / #6022 class) used to fail here as a
// gcc link error; the in-process assembler has no such reserved-word clash, so
// those names are now simply legal on this path. They still break
// `-target x86-64-asm` piped to gcc, which is what that flag is for.
func TestFernFixturesSelfHostX86_64(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "x86_64",
		target:    "x86-64-linux",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-x86_64-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath := filepath.Join(t.TempDir(), "prog")
			cmd := exec.Command(fernBin, "-target", "x86-64-linux", f.mainPath, stdlibRoot, "-o", binPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				// Includes the in-process assembler's own refusal ("could not
				// encode: …"), which names the mnemonic or operand shape.
				failf("self-host compile failed: %v\n%s%s", err, out, strictIRBailSite(fernBin, "x86-64-linux", f.mainPath, stdlibRoot, out))
				return
			}
			// write_file does not set the exec bit (the Makefile chmods
			// bin/fern-selfhost for the same reason).
			if err := os.Chmod(binPath, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			// No runner prefix: runSelfHostFixtureLeg has already skipped the
			// hosts that need one (they cannot exec the driver either).
			checkSelfHostNativeRun(t, f, runSelfHostBin(exec.Command(binPath), f.stdin), failf)
		},
	})
}

// TestFernFixturesSelfHostArm64 runs the corpus through `fern -target arm64`:
// the self-host arm64 emitter, assembled + linked IN-PROCESS by arm64_native +
// elf.fern (no `.s`, no gcc — the production path since the ELF flip), executed
// under qemu-aarch64.
//
// This is the only leg where the self-host compiler produces the finished binary
// by itself, so it is the closest thing the corpus has to a test of the
// self-hosted toolchain end to end. It also means an in-process-assembler gap
// surfaces as a compile failure naming the instruction, not as a link error.
func TestFernFixturesSelfHostArm64(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	// arm64Tooling is reused for its qemu discovery and its clean skip; the gcc
	// it returns is deliberately unused, because `-target arm64` assembles and
	// links in-process. On a native arm64 Linux host qemu comes back "" and
	// runArm64Bin execs directly — though that host cannot run the x86-64
	// driver binary anyway, which the runner check in runSelfHostFixtureLeg
	// turns into a skip.
	_, qemu := arm64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "arm64-linux",
		target:    "arm64-linux",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-arm64-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath := filepath.Join(t.TempDir(), "prog")
			cmd := exec.Command(fernBin, "-target", "arm64-linux", f.mainPath, stdlibRoot, "-o", binPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				// Includes the in-process assembler's own refusal ("hit an
				// instruction it does not yet support: …"), which names the
				// mnemonic — a different and more actionable failure than the
				// x86 leg's link error.
				failf("self-host compile failed: %v\n%s%s", err, out, strictIRBailSite(fernBin, "arm64-linux", f.mainPath, stdlibRoot, out))
				return
			}
			// write_file does not set the exec bit (the Makefile chmods
			// bin/fern-selfhost for the same reason).
			if err := os.Chmod(binPath, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			checkSelfHostNativeRun(t, f, runSelfHostBin(runArm64Bin(qemu, binPath), f.stdin), failf)
		},
	})
}

// failFunc reports a divergence. It is t.Errorf for an unlisted fixture and a
// t.Logf for a listed one, so the same assertions describe both directions and
// cannot drift apart.
type failFunc func(format string, args ...any)

// selfHostLeg is one (target, artifact, runner) triple over the shared corpus
// walk. Everything target-specific lives in check; everything else — the driver
// build, the skip rules, the known-divergence bookkeeping — is shared, so a leg
// cannot quietly grow its own idea of what counts as covered.
type selfHostLeg struct {
	backend   string   // the `backends` sidecar token this leg gates on; also its label
	target    string   // -target value passed to the self-host CLI
	gcc       string   // links the self-host DRIVER (always x86-64 asm), whatever the leg's target
	runner    []string // non-empty when the host cannot exec x86-64 binaries directly
	knownFile string   // testdata/<file> listing this leg's known divergences
	skipStdin bool
	check     func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc)
}

func requireSelfHostFixtureLeg(t *testing.T) {
	t.Helper()
	if os.Getenv("FERN_SELFHOST_FIXTURES") == "" {
		t.Skip("set FERN_SELFHOST_FIXTURES=1 to run the fixture corpus through the self-host compiler")
	}
}

func runSelfHostFixtureLeg(t *testing.T, leg selfHostLeg) {
	t.Helper()
	if len(leg.runner) != 0 {
		// The driver takes host filesystem paths as argv, so a qemu runner
		// would not see the same paths. Native-only, like every other
		// fern.fern test (see TestSelfHostCLIX86_64).
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, leg.gcc, dir, "fern.fern", "fern")

	root := conformanceCases
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	known := loadKnownDivergences(t, leg.knownFile)
	var ran, skipped, expectedFail int
	for _, name := range names {
		abs, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("abs %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(abs, "main.fern")); err != nil {
			continue
		}
		f := loadFixture(t, abs)
		if f.rejectionCase() || !f.backends[leg.backend] || (leg.skipStdin && f.stdin != "") {
			skipped++
			continue
		}
		ran++
		reason, isKnown := known[name]
		if isKnown {
			expectedFail++
		}
		t.Run(name, func(t *testing.T) {
			diverged := false
			failf := func(format string, args ...any) {
				if isKnown {
					diverged = true
					t.Logf("known divergence (%s): "+format, append([]any{reason}, args...)...)
					return
				}
				t.Errorf(format, args...)
			}
			// Absolute paths throughout: a relative one was unopenable from an
			// arm64-darwin binary until #6002, and absolute is what every other
			// harness does anyway. The self-host CLI takes `<entry>
			// [stdlib-root]` positionally.
			leg.check(t, fernBin, stdlibRoot, f, failf)
			if isKnown && !diverged {
				t.Errorf("listed in %s (%s) but it PASSES now — delete the entry", leg.knownFile, reason)
			}
		})
	}
	skipReasons := "compile-error / " + leg.backend + "-excluded"
	if leg.skipStdin {
		skipReasons += " / stdin"
	}
	t.Logf("self-host %s leg: %d fixtures run (%d of them known divergences), %d skipped (%s)",
		leg.backend, ran, expectedFail, skipped, skipReasons)
}

// strictIRBailSite re-runs a FAILED compile under FERN_STRICT_IR=1 and returns
// the bail site it names, or "" when the failure was not an eligibility bail.
// The driver's own message says to set the variable and re-run; doing it here
// means the CI log carries the answer instead of an instruction, which is the
// difference between triaging from a log and having to reproduce first. Costs one
// extra ~1.3s compile, and only on a fixture that already failed.
func strictIRBailSite(fernBin, target, mainPath, stdlibRoot string, firstOut []byte) string {
	if !strings.Contains(string(firstOut), "not IR-eligible") {
		return ""
	}
	cmd := exec.Command(fernBin, "-target", target, mainPath, stdlibRoot, "-o", os.DevNull)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	out, _ := cmd.CombinedOutput()
	return "\n--- FERN_STRICT_IR=1 ---\n" + string(out)
}

// selfHostRun is a finished run of a self-host-compiled native binary. stderr is
// kept separately from stdout so an exact stdout comparison stays exact while
// diagnostics still get the runtime's own complaint (an rc abort, a bounds
// abort) instead of dropping it.
type selfHostRun struct {
	stdout   string
	stderr   string
	exit     int
	exited   bool // false → killed by a signal
	state    string
	elapsed  time.Duration
	timedOut bool // we killed it at selfHostRunTimeout
}

// selfHostRunTimeout bounds ONE fixture's run. Every fixture in the corpus
// finishes in well under a second natively; a self-host-compiled one that does
// not is diverging, and the leg should say so in seconds rather than let one
// program spend the lane's budget. Measured on the first run: six x86-64 regex
// fixtures burned ~83s each before dying, and the held-back arm64 leg (see the
// file header) hit its 43m lane wall two thirds through the corpus — a red lane
// that could not report what else was red. Generous enough (20s,
// ~100x the slowest honest fixture) that a slow qemu start is never mistaken for
// a hang.
const selfHostRunTimeout = 20 * time.Second

func runSelfHostBin(cmd *exec.Cmd, stdin string) selfHostRun {
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return selfHostRun{stderr: err.Error(), exit: -1}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	select {
	case <-done:
	case <-time.After(selfHostRunTimeout):
		timedOut = true
		_ = cmd.Process.Kill()
		<-done
	}
	r := selfHostRun{stdout: so.String(), stderr: se.String(), exit: -1, elapsed: time.Since(start), timedOut: timedOut}
	if ps := cmd.ProcessState; ps != nil {
		r.exit, r.exited, r.state = ps.ExitCode(), ps.Exited(), ps.String()
	}
	return r
}

// checkSelfHostNativeRun holds the x86-64 leg to the same contract
// TestFernFixtures holds the native backends to: exit code is main's return over
// the full 0..255, and stdout matches exactly (or contains each required line).
// The three failure modes are kept apart because they mean different things: a
// signal is a miscompile, exit 137 is the arena, a wrong number is a wrong
// answer.
func checkSelfHostNativeRun(t *testing.T, f *fixtureSpec, r selfHostRun, failf failFunc) {
	t.Helper()
	if r.timedOut {
		failf("the self-host-compiled binary HUNG — killed at %s (natively this fixture finishes in milliseconds)\nstdout so far: %q\nstderr: %s",
			selfHostRunTimeout, r.stdout, r.stderr)
		return
	}
	if !r.exited {
		// The signal is the whole diagnosis and it must be printed: SIGSEGV is a
		// miscompile in the emitted code, SIGKILL after a long run is the host's
		// OOM killer reaping a program that walked the 16 GiB MAP_NORESERVE arena,
		// SIGILL is FERN_RC_UNDERFLOW_TRAP. Reporting only "killed by a signal"
		// (as this did on its first run, over six regex fixtures at ~83s each)
		// leaves the reader unable to tell a wrong instruction from a runaway
		// allocation, which are opposite ends of the compiler.
		failf("the self-host-compiled binary CRASHED after %s — %s (not a wrong answer)\nstdout: %q\nstderr: %s",
			r.elapsed.Round(time.Millisecond), r.state, r.stdout, r.stderr)
		return
	}
	if r.exit != f.wantExit {
		// __fern_alloc's arena-exhaustion abort is exit 125 (ExitArenaExhausted).
		// 137 is NOT the arena abort — the two statuses were
		// split precisely so 137 could mean the host OOM-killed us and nothing
		// else, and a hint asserting the opposite sends the reader hunting a
		// leak when they should be lowering a memory budget.
		hint := ""
		switch r.exit {
		case 125:
			hint = " — 125 is __fern_alloc's arena-exhaustion abort, so this is a leak"
		case 137:
			hint = " — 137 is 128+SIGKILL, i.e. the HOST ran out of RAM, not the arena"
		}
		failf("exit = %d, want %d%s\nstdout: %q\nstderr: %s", r.exit, f.wantExit, hint, r.stdout, r.stderr)
	}
	if f.exact {
		if r.stdout != f.wantOut {
			failf("stdout mismatch\n got: %q\nwant: %q\nstderr: %s", r.stdout, f.wantOut, r.stderr)
		}
		return
	}
	for _, sub := range f.contains {
		if !strings.Contains(r.stdout, sub) {
			failf("stdout missing %q\nfull stdout:\n%s\nstderr: %s", sub, r.stdout, r.stderr)
		}
	}
}

// loadKnownDivergences reads testdata/<file>: blank/`#` lines ignored, otherwise
// `<fixture-name>  <reason>`.
func loadKnownDivergences(t *testing.T, file string) map[string]string {
	t.Helper()
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("read %s: %v", file, err)
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		name, reason, _ := strings.Cut(ln, " ")
		out[strings.TrimSpace(name)] = strings.TrimSpace(reason)
	}
	return out
}
