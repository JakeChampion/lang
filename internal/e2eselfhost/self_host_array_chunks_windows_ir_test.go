package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The receiver-method wrappers for `chunks` / `windows` (added to std/array
// alongside this test) lower on the self-host IR path. Both fold through the
// array-method monomorphisation (receiver-only type var `T`, slice 3) into an
// `__arrm_` clone that delegates to the free `chunks` / `windows` — which
// already lower on IR and return a fresh `T[][]`. The stdlib is resolved off
// disk by asm_load_run (the `root` arg), so the real wrappers are exercised
// end-to-end rather than inlined. Each case is oracle-checked against the
// interpreter and routing-pinned to "ir".
var arrayChunksWindowsIRCases = []struct {
	name string
	src  string
}{
	// chunks: non-overlapping groups, last one shorter.
	{"chunks", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; var cs: i32[][] = xs.chunks(2); return cs.len() * 10 + cs[0][1] + cs[2][0]; }`},
	// windows: overlapping sliding sub-slices.
	{"windows", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; var ws: i32[][] = xs.windows(2); return ws.len() * 10 + ws[3][0] + ws[0][1]; }`},
}

func TestSelfHostArrayChunksWindowsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "acw")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range arrayChunksWindowsIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "acw_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			if !strings.Contains(asm, "__arrm_") {
				t.Errorf("%s: no monomorphised __arrm_ clone in asm (method did not ride the IR array-method path)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "acw_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
