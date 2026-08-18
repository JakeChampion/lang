package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostAsmLoadRunStdlibRootVsFlags pins asm_load_run's argv parsing: the
// stdlib root is the first argument matching no flag, and a FLAG'S VALUE is not
// one.
//
// The driver's flag chain ends with `else if (root == "") { root = av[ag]; }`.
// That arm has to stay attached to the chain. When the `-target` validation
// block sat between them, the arm bound to the validation `if` instead, so it
// ran for every argument whose target was valid — including the value `-target`
// had just stepped over. `mmc prog.fern -target arm64-linux` therefore set the
// stdlib root to "arm64-linux" and resolved `core/…` under it:
//
//	asm_load_run: cannot read module: arm64-linux/core/map.fern
//
// Latent for as long as no compiler module imported a `core/…` module, which is
// why it surfaced only when irverify's NameIndex became a Map (#6993 slice
// four). The stage-2 arm64 fixpoint catches it, but at 214s behind qemu; this
// case is the same bug for the price of one emit, and it covers x86-64 too —
// the fault was never arm64-specific.
func TestSelfHostAsmLoadRunStdlibRootVsFlags(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the driver takes host filesystem paths as argv; native-only")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "asm_load_run")

	// A `core/map` import is what makes the bogus root reachable: without one,
	// nothing ever tries to open a file under it and the driver exits 0 with the
	// root still wrong.
	src := "import \"core/map\";\n" +
		"function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 1); return m.get_or(\"a\", 0); }\n"
	prog := filepath.Join(dir, "flagprog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		// No root: `core/…` imports are skipped and map_new lowers to the
		// builtin runtime helpers. The emit must succeed rather than try to
		// read core/map.fern under "x86-64-linux".
		{"target-flag-is-not-a-root", []string{prog, "-target", "x86-64-linux"}},
		{"target-flag-is-not-a-root-arm64", []string{prog, "-target", "arm64-linux"}},
		// A real root AFTER a flag is still the root — the same misbinding
		// swallowed it, leaving the genuine path ignored.
		{"root-after-a-flag-still-binds", []string{prog, "-target", "x86-64-linux", stdlibRoot}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, _ := cmd.Output()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("driver did not exit normally")
			}
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Fatalf("driver exited %d, want 0\nstderr: %s", code, stderr.String())
			}
			if len(out) == 0 {
				t.Fatal("driver emitted 0 bytes")
			}
			if got := stderr.String(); strings.Contains(got, "cannot read module") {
				t.Errorf("driver resolved a module under a flag value: %s", got)
			}
		})
	}
}
