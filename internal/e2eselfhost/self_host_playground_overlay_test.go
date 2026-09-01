package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The playground driver resolves `std/…` out of its own embedded stdlib, with
// no filesystem underneath (#6643).
//
// That is the last thing standing between the self-host compiler and the
// browser playground. `fern.fern` is a CLI — argv paths in, an executable out —
// and `wasm_ir_run.fern` has the right shape but resolves nothing. Native
// serves the stdlib from `go:embed`; the self-host CLI reads it from a root
// named on the command line, which a wasm-hosted compiler does not have.
// `playground_run.fern` carries it as `-embed` assets and hands them to the
// module loader as a sealed overlay.
//
// # Why these tests need no wasmtime
//
// The driver is run with its working directory set to an EMPTY temp dir. Its
// entry comes from stdin, so the entry directory is "": a disk resolution of
// `std/i64` would read `std/i64.fern` relative to the process's cwd, and there
// is nothing there. So a program that compiles proves the source came from the
// overlay and from nowhere else — the whole claim, in seconds, on every runner.
//
// The decoy case then proves the other half. A `std/i64.fern` on disk returning
// a wrong answer is not merely LOSING to the overlay, it is never read: sealed
// means a miss is a miss, so nothing below resolve_module can reach the host.
// Without that, a stdin-fed program's unresolved import falls through to a
// cwd-relative read and then to try_manifest, which opens `fern.toml` and can
// reach the user's package cache.

// playgroundProgram imports std/i64 and calls a function only that module
// defines, so the answer depends on the module's CONTENTS and not just on its
// presence. gcd(48, 36) is 12.
const playgroundProgram = `import "std/i64";
function main(): i32 {
    var a: i64 = 48;
    var b: i64 = 36;
    return a.gcd(b) as i32;
}
`

// buildPlaygroundDriver builds playground_run.fern for x86-64 with the stdlib
// embedded, and returns the binary's path.
//
// Built by shelling out to the native CLI rather than through
// buildSelfHostBin's cache: that path emits with `constfold.Fold(prog, nil)`
// (`internal/e2eharness/self_host_buildcache.go`), so it has no way to pass an
// asset bundle, and a driver whose whole point is the bundle cannot come out of
// it. Teaching the cache about `-embed` means keying it on the bundle's
// contents too, which is its own change.
func buildPlaygroundDriver(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "playground_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	bin := filepath.Join(dir, "playground_run")
	// No stdlib root argument: the native CLI serves `std/…` from go:embed, so
	// the driver's own `import "std/io"` resolves without one. The -embed
	// bundle is what the driver will carry at RUNTIME, which is a different
	// question from what resolves its own imports here.
	cmd := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux",
		"-embed", stdlib, "-o", bin, filepath.Join(dir, "playground_run.fern"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the playground driver failed: %v\n%s", err, out)
	}
	return bin
}

// runPlayground pipes `src` into the driver with the working directory set to
// `cwd`, and returns stdout, stderr and the exit code.
func runPlayground(t *testing.T, bin, cwd, src string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(src)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostPlaygroundOverlay(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	bin := buildPlaygroundDriver(t)
	t.Run("resolves-stdlib-from-the-overlay", func(t *testing.T) { playgroundResolves(t, bin) })
	t.Run("does-not-reach-the-disk", func(t *testing.T) { playgroundIgnoresDecoy(t, bin) })
	t.Run("reports-an-unresolvable-import", func(t *testing.T) { playgroundReportsMissing(t, bin) })
}

func playgroundResolves(t *testing.T, bin string) {
	// An empty directory: no std/ tree, no fern.toml, nothing to resolve
	// against. Every import has to come from the embedded bundle.
	empty := t.TempDir()
	wat, stderr, code := runPlayground(t, bin, empty, playgroundProgram)
	if code != 0 {
		t.Fatalf("driver exited %d in an empty directory, want 0\n%s", code, stderr)
	}
	if !strings.HasPrefix(wat, "(module") {
		t.Fatalf("driver did not emit a module:\n%s\n%s", wat, stderr)
	}
	assertWatRuns(t, wat, 12)
}

// The decoy is the sealed half. A `std/i64.fern` on disk whose gcd returns 999
// is what a disk read WOULD find; the answer stays 12, so the disk was never
// consulted — not preferred-against, not read.
func playgroundIgnoresDecoy(t *testing.T, bin string) {
	decoy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(decoy, "std"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "std", "i64.fern"),
		[]byte("pub function gcd(a: i64, b: i64): i64 { return 999; }\n"), 0o644); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	wat, stderr, code := runPlayground(t, bin, decoy, playgroundProgram)
	if code != 0 {
		t.Fatalf("driver exited %d, want 0\n%s", code, stderr)
	}
	assertWatRuns(t, wat, 12)
}

// An import the overlay does not carry is REPORTED, not skipped.
//
// load_imports drops an unresolvable import silently — deliberate in the CLI,
// where a compiler intrinsic looks exactly like one. In a playground it is a
// trap: the program compiles and then fails at a call nothing defines, with
// nothing pointing at the import the user actually got wrong.
func playgroundReportsMissing(t *testing.T, bin string) {
	_, stderr, code := runPlayground(t, bin, t.TempDir(),
		"import \"std/nosuchmodule\";\nfunction main(): i32 { return 0; }\n")
	if code == 0 {
		t.Fatalf("driver accepted a program importing a module it cannot resolve")
	}
	if !strings.Contains(stderr, "std/nosuchmodule") {
		t.Errorf("the diagnostic does not name the import:\n%s", stderr)
	}
}

// The wasm leg: the compiler running AS wasm, with no preopens at all, so
// every read_file below the loader is dead by construction rather than by
// argument. Its output must be byte-identical to the natively-hosted driver's
// — a wasm-hosted compiler is only interesting if it agrees with the native
// one, which is the same standard TestSelfHostWasmHostedCompilerMatchesNative-
// OnNestedArith holds.
//
// Skipped under -short: the wasm build of this driver is the expensive one.
func TestSelfHostPlaygroundHostedInWasm(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the playground driver to wasm; skipped under -short")
	}
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	for _, tool := range []string{"wasmtime", "wasm-tools"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "playground_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	mod := filepath.Join(dir, "playground.wasm")
	build := exec.Command(buildFernCLIBin(t), "-target", "wasm32-wasi", "-emit", "core-module",
		"-embed", stdlib, "-o", mod, filepath.Join(dir, "playground_run.fern"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the playground driver for wasm failed: %v\n%s", err, out)
	}

	// `--invoke main`, not a bare `wasmtime run`: a wasmbin core module carries
	// no `_start`, so a plain run exits 0 having called nothing — which reads
	// as an empty answer rather than as an error. No `--dir`, so the guest has
	// no preopens and path_open cannot succeed.
	run := exec.Command("wasmtime", "run", "--invoke", "main", mod)
	run.Dir = t.TempDir()
	run.Stdin = strings.NewReader(playgroundProgram)
	var out strings.Builder
	run.Stdout = &out
	if err := run.Run(); err != nil {
		t.Fatalf("wasm-hosted driver failed: %v", err)
	}
	// `--invoke` prints main's return value on stdout after whatever the
	// program wrote, so the trailing line is wasmtime's, not the compiler's.
	wat := strings.TrimSuffix(out.String(), "0\n")
	if !strings.HasPrefix(wat, "(module") {
		t.Fatalf("wasm-hosted driver emitted no module:\n%q", out.String())
	}
	assertWatRuns(t, wat, 12)

	// Agreement with the native build, byte for byte.
	nativeWat, _, code := runPlayground(t, buildPlaygroundDriver(t), t.TempDir(), playgroundProgram)
	if code != 0 {
		t.Fatalf("natively-hosted driver exited %d", code)
	}
	if wat != nativeWat {
		t.Errorf("wasm-hosted and natively-hosted output differ: %d vs %d bytes", len(wat), len(nativeWat))
	}
}

// assertWatRuns parses the emitted WAT and runs it, asserting the exit code.
// The exit code is the assertion rather than the WAT's length because an
// overlay handing back empty sources would still emit a module — one that
// either fails to parse or answers wrongly.
func assertWatRuns(t *testing.T, wat string, want int) {
	t.Helper()
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	watPath := filepath.Join(dir, "out.wat")
	binPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("emitted WAT does not parse: %v\n%s", err, out)
	}
	cmd := exec.Command("wasmtime", "run", binPath)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("compiled program exited %d, want %d\n%s", got, want, out)
	}
}
