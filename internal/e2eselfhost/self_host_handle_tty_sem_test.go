package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The four Reader terminal methods through the SEMANTIC lowering, on a real
// pseudo-terminal: `window_size`, `set_window_size`, `termios_get` and
// `termios_set` reached as methods rather than as the free ops.
//
// The IR legs beside this one (TestSelfHostHandleTtyIR) run the same source and
// pass whether or not the typed path produced it: a module the semantic
// boundary refuses falls back to the AST lowering silently, and the program
// still answers 0. So this leg reads the production tally and requires EVERY
// declaration, which is what makes it notice a missing contract.
//
// It also runs the binary, because a contract alone is not the lowering: a
// method whose contract exists but whose call reaches no op arrives at the
// backend as a call to a symbol no runtime defines.
func TestSelfHostSemanticHandleTty(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("handle-tty test runs only natively (drives a host terminal)")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostHandleTtySource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
	var report strings.Builder
	cmd.Stderr = &report
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile: %v\n%s", err, report.String())
	}
	if produced, total := semTally(t, report.String()); produced != total {
		t.Fatalf("the typed path produced %d of %d declarations:\n%s", produced, total, report.String())
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	handleTtyOnPath(t, exec.Command(out))
}
