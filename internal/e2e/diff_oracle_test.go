// Differential-execution oracle. Generates Lang programs with
// `fernsmith.GenMain` and runs each through every available
// backend, asserting they all agree on `main()`'s byte return
// value.
//
// The byte-mutation FuzzParse / FuzzCheck fuzzers and the
// fernsmith parse-roundtrip fuzzer all stop at the front end;
// they catch parser / checker bugs but say nothing about IR
// lowering or codegen. This harness is the cross-backend
// counterpart: same source, four backends, one expected result.
// Any disagreement points at a real codegen bug.
//
// The interpreter is the source of truth and is always exercised
// (no toolchain). Each native / wasm backend runs in its own
// sub-test so it can skip individually when its toolchain is
// missing — the rest of the comparison still runs.
package e2e

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/fernsmith"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// diffOracleSeedCount keeps `go test ./...` fast by default; the
// native FuzzGenerate_ExecutionAgrees harness in the same package
// expands the search space on demand.
//
// Bumped 8 → 32 once the wasmbin path stopped tripping on string-
// slot drops, 32 → 64 once the differential oracle started running
// cleanly, then 64 → 122 once the FlattenBranches stack-balance
// guard freed the seeds that broke the WAT validator.
// The 122 cap was specifically gated by seed 122's remaining
// WAT-only closure-table emission bug; with the WAT sub-test now
// retired from the oracle (the wasmbin path covers the same wasm
// surface and is the long-term replacement — see the WAT-retirement
// PR thread), the cap goes 122 → 1024.
//
// Bumped 1024 → 4096 once the interp's `?` propagation gap closed:
// the corpus is fully interp-runnable up to that count (a sweep
// confirmed 0 skips, where the previous gap was ~1 seed per
// thousand from `?` propagation alone). The wasmbin path stayed
// 0 emit-skips / 0 mismatches across the same range.
const diffOracleSeedCount = 2048

// diffOracleSeeds returns the seed count for the differential sweep,
// dropping to 1/8th (256) under `testing.Short()` so dev-loop
// `go test -short ./internal/e2e` finishes promptly without sacrificing
// the full 2048-seed coverage CI keeps.
func diffOracleSeeds(t *testing.T) uint64 {
	t.Helper()
	if testing.Short() {
		return diffOracleSeedCount / 8
	}
	return diffOracleSeedCount
}

// diffOracleArtifactDir returns the directory where the
// differential oracle stashes asm + binary artifacts on
// failure. The CI workflow uploads this path via
// actions/upload-artifact; locally it just accumulates on
// the filesystem (tests SetUp / TearDown don't touch it).
// Defaults to /tmp/lang-diff-failures; override with
// DIFF_ORACLE_ARTIFACT_DIR for sandboxed environments.
func diffOracleArtifactDir() string {
	if d := os.Getenv("DIFF_ORACLE_ARTIFACT_DIR"); d != "" {
		return d
	}
	return "/tmp/lang-diff-failures"
}

// diagInfo is the bundle of post-mortem details a diff-oracle
// failure needs: the captured stdout+stderr, the exit code,
// a human-readable signal name (empty when the process exited
// normally), and the path to the asm artifact for later
// inspection. Helpers below fill it out.
type diagInfo struct {
	out     string
	code    int
	signal  string // e.g. "SIGSEGV" — empty if exited normally
	asmPath string
	binPath string
}

// describeSignal turns a Go ExitError's WaitStatus into a
// short signal description (e.g. "signal 11 / segmentation
// fault"). Returns the empty string when the process wasn't
// signal-killed.
func describeSignal(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok {
		return ""
	}
	if !ws.Signaled() {
		return ""
	}
	sig := ws.Signal()
	return fmt.Sprintf("signal %d / %s", int(sig), sig.String())
}

// runArm64Diag is the diagnostic-aware sibling of
// compileAndRunArm64. Returns enough post-mortem info to
// recover from a CI failure: the captured combined output,
// the exit code, the signal name (when the binary was killed
// by a signal, e.g. SIGSEGV on a bad pointer deref), and the
// paths to the asm + binary so a follow-up `objdump -d` or
// `qemu-aarch64 -d ...` can pin down the failing instruction.
//
// Skips the test (via the shared `arm64Tooling` helper) when
// no aarch64 toolchain is available.
func runArm64Diag(t *testing.T, src string) diagInfo {
	t.Helper()
	gcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := runArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return diagInfo{
		out:     string(out),
		code:    cmd.ProcessState.ExitCode(),
		signal:  describeSignal(cmd.ProcessState),
		asmPath: asmPath,
		binPath: binPath,
	}
}

// runX86_64Diag mirrors runArm64Diag for the x86_64 backend.
// Same Go pipeline (modload → checker → monomorph → emit →
// gcc → run), same post-mortem fields. x86_64 prefers native
// exec on amd64 hosts and falls back to qemu-x86_64 elsewhere
// — `x86_64Tooling` already encodes that policy.
func runX86_64Diag(t *testing.T, src string) diagInfo {
	t.Helper()
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	out, _ := cmd.CombinedOutput()
	return diagInfo{
		out:     string(out),
		code:    cmd.ProcessState.ExitCode(),
		signal:  describeSignal(cmd.ProcessState),
		asmPath: asmPath,
		binPath: binPath,
	}
}

// preserveDiagArtifacts copies the asm + binary out of the
// per-test t.TempDir (which is rm-rf'd on test exit) into the
// stable artifact directory so CI can upload them and a
// developer can post-mortem locally. Source is also dumped so
// the whole crash is reproducible from artifacts alone.
//
// Best-effort: errors from the copy aren't propagated. The
// in-message `t.Errorf` text is the primary failure surface;
// the artifact path is a bonus.
func preserveDiagArtifacts(t *testing.T, label string, src string, d diagInfo) string {
	t.Helper()
	dest := filepath.Join(diffOracleArtifactDir(), label)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return ""
	}
	_ = os.WriteFile(filepath.Join(dest, "main.fern"), []byte(src), 0o644)
	if d.asmPath != "" {
		if b, err := os.ReadFile(d.asmPath); err == nil {
			_ = os.WriteFile(filepath.Join(dest, "prog.s"), b, 0o644)
		}
	}
	if d.binPath != "" {
		if b, err := os.ReadFile(d.binPath); err == nil {
			_ = os.WriteFile(filepath.Join(dest, "prog"), b, 0o755)
		}
	}
	return dest
}

// asmExcerpt reads the asm file at path and returns a head +
// tail slice suitable for inlining in a test failure log. The
// excerpt covers main's prologue (first N lines) and main's
// epilogue / .data sections (last N lines). Empty string on
// any read error — the failure message degrades gracefully.
func asmExcerpt(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const headTail = 80
	lines := strings.Split(string(b), "\n")
	if len(lines) <= 2*headTail {
		return string(b)
	}
	var sb strings.Builder
	for _, l := range lines[:headTail] {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString(fmt.Sprintf("...<%d more lines>...\n", len(lines)-2*headTail))
	for _, l := range lines[len(lines)-headTail:] {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// TestDifferential_LangsmithMain runs each backend on the same
// generator-emitted main() and asserts the byte return value
// agrees across all available backends. Per-seed parent test
// gathers the interp baseline; per-backend child tests skip when
// the relevant toolchain is missing.
//
// Shard the 2048-seed sweep across N parallel CI jobs by setting
// `DIFF_ORACLE_SHARD=I/N` (e.g. "2/4" = the third of four shards;
// 0-indexed). Each shard claims `seed % N == I`. Unset → run
// every seed, preserving local `go test ./internal/e2e/` behaviour
// and the pre-shard CI semantics.
//
// Per-seed subtests are parallel (`t.Parallel()`). Each seed
// generates its own source, runs through interp + compile-and-
// exec helpers that use `t.TempDir()` exclusively, so there's no
// shared filesystem or in-memory state across seeds — they can be
// driven up to GOMAXPROCS at a time. Halves wall-clock on the
// CI shards (1 shard × 4 cores ≈ 4 seeds in flight) without
// touching coverage.
// divergence names one (seed, backend) pair. Scoped to the backend
// rather than the seed so a seed that traps on one backend is still
// compared on the other three — the fact that interp, arm64 and x86-64
// agree on seed 17 is most of the evidence that its wasmbin trap is a
// backend bug and not an invalid program.
type divergence struct {
	seed    uint64
	backend string
}

// knownDivergences are the (seed, backend) pairs this oracle skips,
// each against the issue tracking the bug. The self-host fixture legs
// carry the same idea as `testdata/selfhost-<target>-known-divergences.txt`;
// an in-code table is used here because the skip message can then name
// the issue directly.
//
// A row here is a KNOWN COMPILER BUG being tolerated so the rest of the
// corpus keeps running — not a fixture that is allowed to be wrong. The
// skip is loud (never a silent pass) and every row must cite an open
// issue, so an untracked row is visible as such.
//
// Empty is the desired state: a row earns its place only while its issue
// is open, and #6142 (seed 17, wasmbin — the static closure-cell pool
// overlapping the allocator's freelist heads table) left with the fix.
var knownDivergences = map[divergence]string{}

func TestDifferential_LangsmithMain(t *testing.T) {
	shardIdx, shardCount := diffOracleShard(t)
	seedCount := diffOracleSeeds(t)
	for seed := uint64(0); seed < seedCount; seed++ {
		if seed%shardCount != shardIdx {
			continue
		}
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			expected := runInterpByteOrSkip(t, src)

			t.Run("arm64-linux", func(t *testing.T) {
				d := runArm64Diag(t, src)
				if d.code != expected {
					art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/arm64", seed), src, d)
					sig := d.signal
					if sig == "" {
						sig = "<normal exit>"
					}
					t.Errorf("arm64 exit=%d (signal=%s), interp=%d\nbinary output (stdout+stderr):\n%s\nartifact dir: %s\nasm (head+tail):\n%s\nsrc:\n%s",
						d.code, sig, expected, d.out, art, asmExcerpt(d.asmPath), src)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				d := runX86_64Diag(t, src)
				if d.code != expected {
					art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/x86_64", seed), src, d)
					sig := d.signal
					if sig == "" {
						sig = "<normal exit>"
					}
					t.Errorf("x86_64 exit=%d (signal=%s), interp=%d\nbinary output (stdout+stderr):\n%s\nartifact dir: %s\nasm (head+tail):\n%s\nsrc:\n%s",
						d.code, sig, expected, d.out, art, asmExcerpt(d.asmPath), src)
				}
			})
			// wasm sub-test (WAT-text backend) retired here as
			// the first step of the WAT-backend wind-down. wasmbin
			// covers the same wasm surface, and the dedicated
			// wasm_e2e_test.go suite still exercises the WAT path
			// on hand-picked programs for the other CLI consumers
			// (`-target wasm32-wasi` / `-target wasi-http`). Dropping
			// WAT here unblocks the bigger seed-count bump and
			// stops false-positive WAT codegen bugs from gating
			// fernsmith-corpus expansion.
			t.Run("wasmbin", func(t *testing.T) {
				if issue, known := knownDivergences[divergence{seed, "wasmbin"}]; known {
					t.Skipf("known divergence, see %s — remove this entry when it is fixed", issue)
				}
				got := compileAndRunWasmbinMain(t, src)
				if got != expected {
					t.Errorf("wasmbin result=%d, interp=%d\nsrc:\n%s", got, expected, src)
				}
			})
		})
	}
}

// diffOracleShard reads the optional `DIFF_ORACLE_SHARD` env var,
// expected as "I/N" with 0 <= I < N (e.g. "0/4", "3/4"). Returns
// (I, N) on success and (0, 1) — the full-sweep identity — when
// the var is unset. Malformed values t.Fatal so a CI misconfig
// surfaces immediately rather than silently running one shard's
// worth of seeds across every job.
func diffOracleShard(t *testing.T) (uint64, uint64) {
	t.Helper()
	raw := os.Getenv("DIFF_ORACLE_SHARD")
	if raw == "" {
		return 0, 1
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: want I/N (e.g. 0/4)", raw)
	}
	idx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: index parse: %v", raw, err)
	}
	count, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: count parse: %v", raw, err)
	}
	if count == 0 || idx >= count {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: require 0 <= I < N and N > 0", raw)
	}
	return idx, count
}

// FuzzGenerate_ExecutionAgrees is the same oracle as the table
// test, but for `go test -fuzz=...`. Operates on a uint64 seed
// per execution. The interp baseline still runs; backend
// disagreement (or any interp / parser / checker error) is a
// hard failure. Note that backends with missing toolchains will
// SKIP rather than fail, so this fuzzer only really stresses
// codegen when run on a machine with the full toolchain
// installed (or in CI).
//
// Run with: go test -fuzz=FuzzGenerate_ExecutionAgrees -run=^$ ./internal/e2e
func FuzzGenerate_ExecutionAgrees(f *testing.F) {
	// Seed with a handful of deterministic uint64 seeds repackaged
	// as 8-byte slices so the corpus is non-empty on first run;
	// after that the mutator drives the byte stream directly.
	for seed := uint64(0); seed < 16; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		src := fernsmith.GenMainBytes(data)
		expected := runInterpByteOrSkip(t, src)
		t.Run("arm64-linux", func(t *testing.T) {
			_, code := compileAndRunArm64(t, src)
			if code != expected {
				t.Errorf("arm64 exit=%d, interp=%d\ndata=%x\nsrc:\n%s", code, expected, data, src)
			}
		})
		t.Run("x86_64", func(t *testing.T) {
			_, code := compileAndRunX86_64(t, src)
			if code != expected {
				t.Errorf("x86_64 exit=%d, interp=%d\ndata=%x\nsrc:\n%s", code, expected, data, src)
			}
		})
		t.Run("wasm32-wasi", func(t *testing.T) {
			componentPath := buildComponent(t, src)
			stdout, stderr, ec := runComponent(t, componentPath, runOpts{})
			if ec != 0 {
				t.Fatalf("wasmtime exit=%d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
			}
			got, err := strconv.Atoi(strings.TrimSpace(stdout))
			if err != nil {
				t.Fatalf("parse wasm stdout: %v", err)
			}
			if got != expected {
				t.Errorf("wasm result=%d, interp=%d\ndata=%x\nsrc:\n%s", got, expected, data, src)
			}
		})
		t.Run("wasmbin", func(t *testing.T) {
			got := compileAndRunWasmbinMain(t, src)
			if got != expected {
				t.Errorf("wasmbin result=%d, interp=%d\ndata=%x\nsrc:\n%s", got, expected, data, src)
			}
		})
	})
}

// TestInterpOracleRunsGenericBodies pins the oracle's reach over generic
// code. A body bound `T: cmp.Eq` compares with the bound's `eq` method, and
// without monomorph that reaches the interpreter as a field access on a
// number — which used to `t.Skipf`, so every generic-bodied case in the
// differentials passed vacuously (#6840).
//
// The assertion is written as an inner sub-test whose completion is recorded,
// so a reintroduced skip fails the parent instead of turning it green again.
func TestInterpOracleRunsGenericBodies(t *testing.T) {
	src := `import "core/cmp" as cmp;
function same[T: cmp.Eq](a: T, b: T): boolean { return a.eq(b); }
function main(): i32 {
    var r: i32 = 0;
    if (same(1, 1)) { r = r + 1; }
    if (!same(2, 3)) { r = r + 2; }
    if (same("a", "a")) { r = r + 4; }
    if (!same("a", "b")) { r = r + 8; }
    return r;
}`
	ran := false
	t.Run("Eq-bounded body", func(t *testing.T) {
		if got := runInterpByte(t, src); got != 15 {
			t.Errorf("interp: got exit %d, want 15", got)
		}
		ran = true
	})
	if !ran {
		t.Fatal("the interp oracle skipped a generic body: it is not running the differential suites' generic cases (#6840)")
	}
}

// runInterpByte parses + checks + monomorphises + runs `main()`
// under the in-process interpreter and returns the result masked
// to a byte. Sources from `fernsmith.GenMain` already mask to a
// byte; the extra `& 0xFF` here is defensive in case the harness
// is ever fed a program that doesn't.
//
// An interpreter error FAILS the test. The interpreter is the
// differential oracle's source of truth, so a program it cannot
// run has to be visible: a skip here takes the compiled backends
// down with it and leaves the parent reporting PASS with nothing
// asserted (#6840). Hand-written cases therefore use this;
// generator-driven corpora use runInterpByteOrSkip.
func runInterpByte(t *testing.T, src string) int {
	t.Helper()
	return interpByte(t, src, false)
}

// runInterpByteOrSkip is runInterpByte for the fernsmith corpora,
// where interp-side coverage gaps (closures, the `?` propagation
// operator, etc.) `t.Skipf` rather than fail — the interpreter
// isn't a feature-complete target, and the generator regularly
// emits programs it doesn't model.
func runInterpByteOrSkip(t *testing.T, src string) int {
	t.Helper()
	return interpByte(t, src, true)
}

// interpByte is the shared body. Parser / checker / monomorph
// errors always Fatal: for a generated program those would mean
// fernsmith produced something the front end shouldn't have
// accepted, and for a hand-written one they are the bug.
func interpByte(t *testing.T, src string, skipGaps bool) int {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v\nsrc:\n%s", err, src)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v\nsrc:\n%s", err, src)
	}
	// Without monomorph a generic body reaches the interpreter with an
	// unsubstituted bound, so a `T: cmp.Eq` call like `x.eq(y)` looks like a
	// field access on a number. Every compiled backend runs this pass.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v\nsrc:\n%s", err, src)
	}
	i := interp.New()
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	// `exit(code)` must be captured, not run as a real os.Exit (which would
	// kill the whole test binary). The interp expects a non-returning Exiter
	// substitute, so panic with the code and recover it here — the captured
	// code wins over main's return value, matching process semantics.
	type interpExit struct{ code int }
	i.Exiter = func(code int) { panic(interpExit{code}) }
	var v interp.Value
	exitCode := -1
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ie, ok := r.(interpExit); ok {
					exitCode = ie.code
					return
				}
				panic(r)
			}
		}()
		v, err = i.CallByName("main", nil)
	}()
	if exitCode >= 0 {
		return exitCode & 0xFF
	}
	if err != nil {
		if skipGaps {
			t.Skipf("interp coverage gap: %v", err)
		}
		t.Fatalf("interp: %v\nsrc:\n%s", err, src)
	}
	n, ok := v.(interp.Number)
	if !ok {
		if skipGaps {
			t.Skipf("interp main returned non-number %T (coverage gap)", v)
		}
		t.Fatalf("interp: main returned %T, want a number\nsrc:\n%s", v, src)
	}
	return int(int64(n) & 0xFF)
}
