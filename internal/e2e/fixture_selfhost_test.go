package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestFernFixturesSelfHostWasm runs the fixture corpus through the SELF-HOST
// compiler, which nothing else does.
//
// # Why this exists
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
// # Driver choice
//
// fern.fern — the real CLI — rather than wasm_ir_run, because it LOADS MODULES.
// Roughly a third of the corpus imports std/ or core/, and a driver without a
// loader silently ignores the import and then reports a broken program's verdict
// (see parser.warn_unresolved_imports, #6004). Using the loading driver is also
// what makes the leg a faithful test of what a user runs.
//
// # Cost, and why it is opt-in
//
// Building fern.fern is a heavy self-host driver build (~4.3 GB reserved by the
// harness's memory limiter). It is paid ONCE and amortised over the whole
// corpus; each fixture is then a native-binary invocation plus a wasmtime run.
// Still, that is minutes rather than the 15s the native fixture run takes, so it
// is gated on FERN_SELFHOST_FIXTURES=1 and run as its own CI lane rather than
// slowing every local `go test ./internal/e2e`.
//
// # What it skips, and why each skip is principled
//
//   - compile-error fixtures (expected.error): front-end only, backend-agnostic,
//     already covered once by TestFernFixtures.
//   - fixtures whose `backends` file omits `wasm`: if a program cannot run on
//     the native wasm backend it will not run on the self-host one either, and
//     a shared opt-out beats inventing a second exclusion mechanism.
//   - fixtures with stdin: the self-host CLI compiles to a .wat that wasmtime
//     runs, and threading stdin through that adds a variable this leg is not
//     trying to test. Four fixtures.
//
// A fixture whose expected.exit is >= 126 keeps its trap/rejection check but
// loses its VALUE check: WASI refuses an exit status outside [0..126), so
// wasmtime reports 1 and the program's answer is invisible. Measured: return 125
// exits 125; return 126, and 255, both exit 1 with "exit with invalid exit
// status outside of [0..126)". The native wasm leg dodges this by folding main's
// result into stdout (wasmbin's PrintMainResult); the self-host emit has no
// equivalent.
//
// This distinction is load-bearing, and getting it wrong in both directions is
// easy. The first run of this leg reported 26 mismatches; 14 were only the clamp
// — including nine regex fixtures whose expected 255 is a feature bitmask, which
// read as a broken regex engine and was not. But blanket-skipping those
// fixtures then discarded four REAL findings, because wasmtime distinguishes the
// two cases in its cause chain and a trap is detectable no matter what the
// program meant to return:
//
//		exit with invalid exit status outside of [0..126)   → unmeasurable, fine
//		wasm trap: wasm `unreachable` instruction executed  → a real failure
//	  - fixtures in testdata/selfhost-wasm-known-divergences.txt: programs where
//	    the self-host wasm path is KNOWN to disagree with the native compiler
//	    today. Following the deadcode-gate pattern, they are listed rather than
//	    silently skipped, each with the reason, so the lane is green and a NEW
//	    divergence fails. A listed fixture that starts PASSING also fails — an
//	    allowlist nobody prunes becomes a place bugs go to be forgotten.
func TestFernFixturesSelfHostWasm(t *testing.T) {
	if os.Getenv("FERN_SELFHOST_FIXTURES") == "" {
		t.Skip("set FERN_SELFHOST_FIXTURES=1 to run the fixture corpus through the self-host compiler")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	root := "testdata/cases"
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

	known := loadKnownDivergences(t)
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
		if f.compileError || !f.backends["wasm"] || f.stdin != "" {
			skipped++
			continue
		}
		ran++
		reason, isKnown := known[name]
		if isKnown {
			expectedFail++
		}
		t.Run(name, func(t *testing.T) {
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			// A known divergence reports through `diverged` instead of failing,
			// so the same assertions describe both directions and cannot drift
			// apart.
			diverged := false
			failf := func(format string, args ...any) {
				if isKnown {
					diverged = true
					t.Logf("known divergence (%s): "+format, append([]any{reason}, args...)...)
					return
				}
				t.Errorf(format, args...)
			}

			// The self-host CLI takes `<entry> [stdlib-root]` positionally.
			// Absolute paths throughout: a relative one was unopenable from an
			// arm64-darwin binary until #6002, and absolute is what every other
			// harness does anyway.
			cmd := exec.Command(fernBin, "-target", "wasm", f.mainPath, stdlibRoot, "-o", watPath)
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
			if isKnown && !diverged {
				t.Errorf("listed in selfhost-wasm-known-divergences.txt (%s) but it PASSES now — delete the entry", reason)
			}
		})
	}
	t.Logf("self-host wasm leg: %d fixtures run (%d of them known divergences), %d skipped (compile-error / wasm-excluded / stdin / exit>=126)", ran, expectedFail, skipped)
}

// loadKnownDivergences reads testdata/selfhost-wasm-known-divergences.txt:
// blank/`#` lines ignored, otherwise `<fixture-name>  <reason>`.
func loadKnownDivergences(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join("testdata", "selfhost-wasm-known-divergences.txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("read known-divergences: %v", err)
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
