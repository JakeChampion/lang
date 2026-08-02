package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostAsyncCombinatorsModloadIRX86_64 is slice 6b of
// docs/ASYNC-SELFHOST-IR.md: race and with_deadline join gather (slice 6) on the
// self-host MODLOAD IR path. The blocker was monomorphization type-inference:
// with_deadline(ms: i32, fs: Future[T][]) -> Option[T][] has NO bare-T param, so
// T is recoverable ONLY from the generic-enum argument fs. flatten left a
// same-module bare enum-type reference UNMANGLED (`Future[T][]`) while a
// cross-module qualified use mangled (`async__Future[i32][]`), so bind_unify
// compared mismatched bases and never bound T — the call stayed generic and the
// program bailed to AST. (gather/race escaped because their on_incomplete /
// none_val params are a bare T that binds from a scalar arg.) Slice 6b adds enum
// names to flatten's collect_decl_names so the enum type mangles consistently.
//
// Each combinator program compiles via the asm_load_run modload driver, must
// route the IR path (-decide == "ir"), and must match the interpreter oracle.
// x86-64 only (loader driver takes argv paths — mirrors TestSelfHostStdlibModloadIR).
func TestSelfHostAsyncCombinatorsModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name string
		prog string
	}{
		// race: first ready wins (index 0, value 40) -> (0, 40) -> 0 + 40 = 40.
		{"race", `import "std/async";
function main(): i32 {
    var fs: async.Future[i32][] = [Ready(40), Ready(2)];
    var r: (i32, i32) = async.race(fs, -1);
    return r.0 + r.1;
}
`},
		// with_deadline: a ready future returns Some(7) within the deadline -> 7.
		{"with_deadline", `import "std/async";
function main(): i32 {
    var fs: async.Future[i32][] = [Ready(7)];
    var ds: Option[i32][] = async.with_deadline(100, fs);
    match (ds[0]) { Some(v) => { return v; }, None => { return 0; } }
    return 9;
}
`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.prog)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.prog), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			decide, err := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if got := strings.TrimSpace(string(decide)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (generic-enum type-param inference bailed)", tc.name, got)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil {
				t.Fatalf("loader compile: %v", err)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "async_"+tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
