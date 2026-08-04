package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Interpret-the-driver mode (FERN_SELFHOST_INTERP=1).
//
// A self-host driver test normally EMITS with a driver binary: the driver's Fern
// sources are compiled to x86-64 asm, linked, and executed (natively or under
// qemu-x86_64) with the test program on stdin. On a host with neither — an
// arm64 macOS dev machine, say — X86_64Tooling skips, and the whole
// wasm-emitter surface (~575 TestSelfHost* files) reports `ok 0.262s` while
// verifying nothing. Reading that as a pass is what let a wasm bridge change
// built against the box layout of the WRONG consumer look green (#5974): the
// tests that would have caught it never ran.
//
// The drivers whose output is all a test wants — an emitted .wat / .s on stdout —
// do not need to be MACHINE code to produce it. Interpreting them with the native
// `fern` interpreter gives the same output, because the driver is the same Fern
// program either way. So in this mode BuildSelfHostBin writes an executable SHIM
// in place of the linked binary:
//
//	#!/bin/sh
//	exec /path/to/fern -interp /path/to/driver.fern -- "$@"
//
// A shim rather than a sentinel string on purpose: the ~575 tests reach their
// driver in several ways (RunCapture, RunDriverStdinExits, a bare exec.Command),
// and a real executable serves all of them without each learning a new mode.
//
// It is off by default and changes nothing when unset: CI keeps building real
// driver binaries, which is also the only way to exercise the x86-64 emit itself.
// A test that runs an emitted x86 BINARY (rather than capturing a driver's
// stdout) is not served by this mode and fails on the absent runner instead of
// skipping — loudly, which is the point.
//
// Cost: interpreting a driver runs the whole self-host compiler under the
// interpreter, ~2 s for a small program — comparable to a warm driver-binary
// cache hit, and far cheaper than the cold multi-GB emit + link it replaces.

// InterpDriverMode reports whether FERN_SELFHOST_INTERP selects
// interpret-the-driver mode.
// CI-DARK: FERN_SELFHOST_INTERP — an alternative WAY to run the self-host
// suites (interpret the driver instead of building a binary), not extra
// coverage. Every test it affects runs in CI on the compiled path, which is
// also the only path that exercises the x86-64 emit itself. Wiring a lane
// would re-run the same assertions through a slower driver.
func InterpDriverMode() bool {
	v := os.Getenv("FERN_SELFHOST_INTERP")
	return v != "" && v != "0"
}

// Program harvesting (FERN_DUMP_PROGRAMS=<dir>), layered on the same shim.
//
// "Which programs actually reach the wasm emitter?" had no answer, and three
// attempts to guess one all failed: grepping Go test literals mixes in comment
// text and programs belonging to a DIFFERENT driver (self_host_x86_gas_test.go
// names wasm_run.fern while its programs are x86), rerouting to see what breaks
// turns a wide gap into a wide outage, and hand-probing finds only what you think
// to type. Attribution is not greppable — so capture it where it is unambiguous.
//
// With the dir set, the shim records each program on stdin under the name of the
// DRIVER it was fed to, and exits 0 without compiling. Tests fail (they get no
// output), which is fine: this is a harvesting run, not a test run. One pass over
// the wasm test families yields the exact corpus, per driver, in minutes — 5,026
// programs when this was written. Feed those to `wasm_run -decide` and the decline
// set is measured rather than estimated.

// writeInterpDriverShim writes an executable shim at dir/out that interprets the
// driver source dir/fernName, and returns its path.
func writeInterpDriverShim(t *testing.T, dir, fernName, out string) string {
	t.Helper()
	fern := BuildLangBinForInterp(t)
	path := filepath.Join(dir, out)
	script := "#!/bin/sh\n" +
		"if [ -n \"$FERN_DUMP_PROGRAMS\" ]; then\n" +
		"  f=$(mktemp \"$FERN_DUMP_PROGRAMS/" + strings.TrimSuffix(fernName, ".fern") + ".XXXXXX\")\n" +
		"  cat > \"$f.fern\"; rm -f \"$f\"; exit 0\n" +
		"fi\n" +
		"exec " + fern + " -interp " + filepath.Join(dir, fernName) + " -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write interp driver shim: %v", err)
	}
	return path
}
