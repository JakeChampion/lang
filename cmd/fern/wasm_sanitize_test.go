package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The wasm leak census through the CLI (#7912).
//
// The e2e suite exercises the preview-2 component path; this covers the
// other one — `-emit command-module`, the WASI preview-1 shape
// `web/wasi-shim.js` runs — whose report goes out through `fd_write`
// rather than `wasi:cli/stderr`, and whose exit seam is the synthesised
// `_start` calling `__fern_exit`. Both writers have to produce the same
// line, and the seam has to produce exactly one of it.
//
// The flag reaches the compiler as an environment variable here because
// that is what it is: read at emit time, changing the module the backend
// produces.

var cliCensusRe = regexp.MustCompile(`leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)\n`)

// buildCommandModule compiles src to a preview-1 command module with the
// given FERN_* settings on the compiler process, and returns the path.
func buildCommandModule(t *testing.T, src string, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, src)
	out := filepath.Join(t.TempDir(), "cmd.wasm")
	cmd := exec.Command(bin, "-target", "wasm32-wasi", "-emit", "command-module", "-o", out, entry)
	cmd.Env = append(os.Environ(), env...)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("-emit command-module: %v\n%s", err, o)
	}
	return out
}

func runCommandModule(t *testing.T, path string) (stdout, stderr string, code int) {
	t.Helper()
	run := exec.Command("wasmtime", "run", path)
	var so, se strings.Builder
	run.Stdout = &so
	run.Stderr = &se
	_ = run.Run()
	return so.String(), se.String(), run.ProcessState.ExitCode()
}

// The catching leg: two never-freed 64-byte blocks read as 128 live
// bytes, on stderr, without disturbing stdout or the exit code that a
// command module exists to carry.
func TestCommandModuleLeakCensusReportsLeak(t *testing.T) {
	path := buildCommandModule(t, "function main(): i32 {\n"+
		"  var a: usize = __alloc(64);\n"+
		"  var b: usize = __alloc(64);\n"+
		"  print(\"ran\");\n"+
		"  if (a == 0 || b == 0) { return 1; }\n"+
		"  return 42;\n}\n", "FERN_LEAKCHECK=1")
	stdout, stderr, code := runCommandModule(t, path)
	if code != 42 {
		t.Errorf("exit %d, want 42 — the census must not take over the exit code", code)
	}
	if strings.TrimSpace(stdout) != "ran" {
		t.Errorf("stdout %q, want %q — the report goes to stderr only", stdout, "ran")
	}
	m := cliCensusRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no census line on stderr: %q", stderr)
	}
	if live, _ := strconv.Atoi(m[3]); live != 128 {
		t.Errorf("live_bytes=%s, want 128 (two unfreed 64-byte blocks)", m[3])
	}
	if strings.Count(stderr, "leakcheck: allocs=") != 1 {
		t.Errorf("census printed more than once: %q", stderr)
	}
}

// The clean leg: paired allocs and frees balance, and — flag off — the
// same program says nothing at all.
func TestCommandModuleLeakCensusBalancesAndIsOptIn(t *testing.T) {
	const src = "function main(): i32 {\n" +
		"  var i: i32 = 0;\n" +
		"  while (i < 100) {\n" +
		"    var a: usize = __alloc(64);\n" +
		"    __free(a, 64);\n" +
		"    i = i + 1;\n" +
		"  }\n  return 0;\n}\n"

	_, stderr, code := runCommandModule(t, buildCommandModule(t, src, "FERN_LEAKCHECK=1"))
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	m := cliCensusRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no census line on stderr: %q", stderr)
	}
	if m[1] != "100" || m[2] != "100" || m[3] != "0" {
		t.Errorf("census says allocs=%s frees=%s live_bytes=%s, want 100 / 100 / 0", m[1], m[2], m[3])
	}

	_, offErr, _ := runCommandModule(t, buildCommandModule(t, src))
	if strings.Contains(offErr, "leakcheck:") {
		t.Errorf("census-off build still reported: %q", offErr)
	}
}

// -sanitize on a wasm target must describe what that build actually
// carries. Claiming the whole mode would make a silent run read as "no
// findings" when two of the three checks were never emitted; claiming
// nothing at all would hide the census that IS there.
func TestSanitizeWarnsWasmCarriesTheCensusOnly(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 { return 0; }\n")
	out := filepath.Join(t.TempDir(), "prog.wasm")
	o, err := exec.Command(bin, "-target", "wasm32-wasi", "-sanitize", "-o", out, entry).CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}
	got := string(o)
	if !strings.Contains(got, "leak census") {
		t.Errorf("warning does not name what wasm carries:\n%s", got)
	}
	if strings.Contains(got, "carries no checks") {
		t.Errorf("warning still says wasm carries nothing:\n%s", got)
	}
}

// The SSA backends carry no instrumentation on any target, so selecting
// one drops back to the no-checks warning even where the target's
// default emitter is fully instrumented.
func TestSanitizeWarnsForSSABackend(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 { return 0; }\n")
	out := filepath.Join(t.TempDir(), "prog.wasm")
	o, err := exec.Command(bin, "-target", "wasm32-wasi", "-backend", "ssa", "-sanitize", "-o", out, entry).CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}
	if !strings.Contains(string(o), "carries no checks") {
		t.Errorf("-backend ssa should warn that nothing is instrumented:\n%s", o)
	}
}
