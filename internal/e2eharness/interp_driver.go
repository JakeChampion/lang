package e2eharness

import (
	"os"
	"path/filepath"
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
func InterpDriverMode() bool {
	v := os.Getenv("FERN_SELFHOST_INTERP")
	return v != "" && v != "0"
}

// writeInterpDriverShim writes an executable shim at dir/out that interprets the
// driver source dir/fernName, and returns its path.
func writeInterpDriverShim(t *testing.T, dir, fernName, out string) string {
	t.Helper()
	fern := BuildLangBinForInterp(t)
	path := filepath.Join(dir, out)
	script := "#!/bin/sh\nexec " + fern + " -interp " + filepath.Join(dir, fernName) + " -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write interp driver shim: %v", err)
	}
	return path
}
