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

	"github.com/jakechampion/lang/internal/envflag"
)

// These legs run the conformance corpus through the self-host compiler, the
// one users run, and hold each case to the sidecars (expected.stdout,
// expected.exit, expected.error) TestFernFixtures holds the oracle,
// internal/oracle/interp, to:
//
//   - TestFernFixturesSelfHostWasm, TestFernFixturesSelfHostX86_64 and
//     TestFernFixturesSelfHostArm64 compile with `fern -target` and run the
//     result under wasmtime, natively, and under qemu-aarch64;
//   - TestFernFixturesSelfHostX86_64Quarantine is the x86-64 leg under the
//     use-after-free quarantine;
//   - TestFernFixturesSelfHostInterp runs `fern -interp`;
//   - TestFernFixturesSelfHostCheck runs the compile-error cases through
//     `fern -check`.
//
// The driver is fern.fern, the real CLI, so a case loads its modules as a
// user's program does. Building it is a heavy self-host build, paid once per
// leg, so the legs are gated on FERN_SELFHOST_FIXTURES=1 and run as their own
// CI lanes.
//
// What a leg skips:
//
//   - compile-error cases (expected.error), on every leg but the check leg:
//     the front end is the same for every backend, so they run once.
//   - cases whose `backends` file omits the leg's selector.
//   - cases with stdin, on the wasm leg only.
//   - cases listed in the leg's known-divergences file, each with an issue and
//     a reason. A new divergence fails the leg, and so does a listed case that
//     starts passing, so the list can only shrink.
//
// NOTE `backend` is a fixture SELECTOR, matched against the `backends` file each
// conformance case carries on disk (interp / x86_64 / arm64 / wasm) — NOT a
// target name. Renaming it runs zero fixtures (the floor at the end of
// runSelfHostFixtureLeg is what makes that a failure). Since #6635 the
// self-host driver's -target values are cmd/fern's own `<isa>-<environment>`
// spellings, so only this selector keeps a vocabulary of its own.
func TestFernFixturesSelfHostWasm(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "wasm",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-wasm-known-divergences.txt",
		skipStdin: true,
		// A case whose expected.exit is >= 126 keeps its trap and rejection
		// checks but loses its value check: WASI refuses an exit status outside
		// [0..126), so wasmtime reports 1 and the program's answer is invisible.
		// A trap is still visible, since wasmtime names it in its cause chain:
		//
		//	exit with invalid exit status outside of [0..126)   → unmeasurable, fine
		//	wasm trap: wasm `unreachable` instruction executed  → a real failure
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			cmd := fixtureCompile(fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", watPath, f.mainPath, stdlibRoot)
			if out, err := cmd.CombinedOutput(); err != nil {
				failf("self-host compile failed: %v\n%s", err, out)
				return
			}
			run := exec.Command("wasmtime", "run", watPath)
			out, _ := run.CombinedOutput()
			exit := run.ProcessState.ExitCode()

			// The process exit code is main's return, so compare it directly,
			// but apart from "wasmtime refused the module", which a bare exit
			// code conflates with a program returning 1.
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

// TestFernFixturesSelfHostX86_64 runs the corpus through `fern -target
// x86-64-linux`, which assembles and links in-process (x86_native, elf.fern),
// and runs the binary natively.
func TestFernFixturesSelfHostX86_64(t *testing.T) {
	runSelfHostFixtureLegX86_64(t, nil)
}

// TestFernFixturesSelfHostX86_64Quarantine runs the same corpus compiled
// under FERN_RC_FREE_DEBUG=1: no block is recycled, and a release or retain
// that touches a freed block aborts with the sanitizer's report. A fixture
// that reads storage its own program already released fails here even when
// the stale bytes happen to still hold the right answer. A reclaim-observable
// case prints its free-off output under the quarantine, so only its exit code
// is held to the spec.
func TestFernFixturesSelfHostX86_64Quarantine(t *testing.T) {
	runSelfHostFixtureLegX86_64(t, []string{"FERN_RC_FREE_DEBUG=1"})
}

// runSelfHostFixtureLegX86_64 compiles each fixture with `env` added to the
// driver's environment.
func runSelfHostFixtureLegX86_64(t *testing.T, env []string) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "x86_64",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-x86_64-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath := filepath.Join(t.TempDir(), "prog")
			cmd := fixtureCompile(fernBin, "-target", "x86-64-linux", "-o", binPath, f.mainPath, stdlibRoot)
			if env != nil {
				cmd.Env = append(os.Environ(), env...)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Includes the in-process assembler's own refusal ("could not
				// encode: …"), which names the mnemonic or operand shape.
				failf("self-host compile failed: %v\n%s%s", err, out, strictIRBailSite(fernBin, "x86-64-linux", nil, nil, f.mainPath, stdlibRoot, out))
				return
			}
			// write_file does not set the exec bit (the Makefile chmods
			// bin/fern-selfhost for the same reason).
			if err := os.Chmod(binPath, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			// No runner prefix: runSelfHostFixtureLeg has already skipped the
			// hosts that need one (they cannot exec the driver either).
			r := runSelfHostBin(exec.Command(binPath), f.stdin)
			if strings.Contains(r.stderr, "fern-sanitizer:") {
				failf("the sanitizer reported a finding:\n%s", r.stderr)
				return
			}
			if env != nil && f.reclaimObservable {
				exitOnly := *f
				exitOnly.exact, exitOnly.contains = false, nil
				f = &exitOnly
			}
			checkSelfHostNativeRun(t, f, r, failf)
		},
	})
}

// TestFernFixturesSelfHostArm64 runs the corpus through `fern -target
// arm64-linux`, which assembles and links in-process (arm64_native, elf.fern),
// and runs the binary under qemu-aarch64. An in-process assembler gap is a
// compile failure naming the instruction.
func TestFernFixturesSelfHostArm64(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	// arm64Tooling is reused for its qemu discovery and its clean skip; the gcc
	// it returns is deliberately unused, because `-target arm64-linux` assembles and
	// links in-process. On a native arm64 Linux host qemu comes back "" and
	// runArm64Bin execs directly — though that host cannot run the x86-64
	// driver binary anyway, which the runner check in runSelfHostFixtureLeg
	// turns into a skip.
	_, qemu := arm64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "arm64",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-arm64-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath := filepath.Join(t.TempDir(), "prog")
			cmd := fixtureCompile(fernBin, "-target", "arm64-linux", "-o", binPath, f.mainPath, stdlibRoot)
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Includes the in-process assembler's own refusal ("hit an
				// instruction it does not yet support: …"), which names the
				// mnemonic — a different and more actionable failure than the
				// x86 leg's link error.
				failf("self-host compile failed: %v\n%s%s", err, out, strictIRBailSite(fernBin, "arm64-linux", nil, nil, f.mainPath, stdlibRoot, out))
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

// TestFernFixturesSelfHostInterp runs the corpus through `fern -interp`, the
// shipped interpreter (compiler/interp.fern). The `interp` backend token
// selects the cases the Go interpreter runs in TestFernFixtures, so the two
// interpreters are held to the same corpus.
func TestFernFixturesSelfHostInterp(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "interp",
		gcc:       gcc,
		runner:    runner,
		knownFile: "selfhost-interp-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			cmd := exec.Command(fernBin, "-interp", f.mainPath, stdlibRoot)
			checkSelfHostNativeRun(t, f, runSelfHostBin(cmd, f.stdin), failf)
		},
	})
}

// TestFernFixturesSelfHostCheck runs the compile-error cases through
// `fern -check` and holds the shipped front end to each case's whole
// expected.error, as TestFernFixtures holds the Go one: every line reported,
// and no diagnostic code the sidecar does not name.
func TestFernFixturesSelfHostCheck(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:    "check",
		gcc:        gcc,
		runner:     runner,
		knownFile:  "selfhost-check-known-divergences.txt",
		errorCases: true,
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			out, err := exec.Command(fernBin, "-check", f.mainPath, stdlibRoot).CombinedOutput()
			if err == nil {
				failf("the self-host front end accepted it; expected an error containing %q\n%s", f.wantErrors[0], out)
				return
			}
			reportDiagnosticMismatches("self-host -check", f.wantErrors, string(out), failf)
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
	gcc       string   // links the self-host DRIVER (always x86-64 asm), whatever the leg's target
	runner    []string // non-empty when the host cannot exec x86-64 binaries directly
	knownFile string   // testdata/<file> listing this leg's known divergences
	skipStdin bool
	// errorCases selects the compile-error cases instead of the runnable ones.
	errorCases bool
	check      func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc)
}

func requireSelfHostFixtureLeg(t *testing.T) {
	t.Helper()
	if !envflag.On("FERN_SELFHOST_FIXTURES") {
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
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
		selected := !f.rejectionCase() && f.backends[leg.backend] && !(leg.skipStdin && f.stdin != "")
		if leg.errorCases {
			selected = f.compileError
		}
		if !selected {
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
	// A leg that selected nothing reports PASS with no sub-tests at all, which
	// is indistinguishable from a clean corpus run. It has already happened
	// once: renaming the `backend` selector (a fixture-file token, not a target
	// name — see the note on TestFernFixturesSelfHostWasm) silently ran zero.
	if ran == 0 {
		t.Fatalf("the self-host %s leg selected 0 of %d fixtures — the `backend` selector matches "+
			"no fixture's `backends` file, so this leg asserted nothing", leg.backend, len(names))
	}
	t.Logf("self-host %s leg: %d fixtures run (%d of them known divergences), %d skipped (%s)",
		leg.backend, ran, expectedFail, skipped, skipReasons)
}

// fixtureCompile runs the self-host compiler, so a fixture it does not
// produce whole fails the compile.
func fixtureCompile(fernBin string, args ...string) *exec.Cmd {
	return exec.Command(fernBin, args...)
}

// strictIRBailSite re-runs a FAILED compile under FERN_STRICT_IR=1 and returns
// the bail site it names, or "" when the failure was not an eligibility bail.
// The driver's own message says to set the variable and re-run; doing it here
// means the CI log carries the answer instead of an instruction, which is the
// difference between triaging from a log and having to reproduce first. Costs one
// extra ~1.3s compile, and only on a fixture that already failed.
func strictIRBailSite(fernBin, target string, emit, env []string, mainPath, stdlibRoot string, firstOut []byte) string {
	if !strings.Contains(string(firstOut), "not IR-eligible") {
		return ""
	}
	args := append([]string{"-target", target}, emit...)
	cmd := exec.Command(fernBin, append(args, "-o", os.DevNull, mainPath, stdlibRoot)...)
	cmd.Env = append(append(os.Environ(), env...), "FERN_STRICT_IR=1")
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
// ~100x the slowest normal fixture) that a slow qemu start is never mistaken for
// a hang.
const selfHostRunTimeout = 20 * time.Second

func runSelfHostBin(cmd *exec.Cmd, stdin string) selfHostRun {
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	start := time.Now()
	timedOut, err := runBounded(cmd, selfHostRunTimeout)
	if cmd.ProcessState == nil && err != nil {
		return selfHostRun{stderr: err.Error(), exit: -1}
	}
	r := selfHostRun{stdout: so.String(), stderr: se.String(), exit: -1, elapsed: time.Since(start), timedOut: timedOut}
	if ps := cmd.ProcessState; ps != nil {
		r.exit, r.exited, r.state = ps.ExitCode(), ps.Exited(), ps.String()
	}
	return r
}

// checkSelfHostNativeRun holds a native leg to the contract TestFernFixtures
// holds the oracle to: exit code is main's return over
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
