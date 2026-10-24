package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostAsyncCombinatorsModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name string
		prog string
	}{
		{"race", `import "std/async";
function main(): i32 {
    let fs: async.Future[i32][] = [Ready(40), Ready(2)];
    let r: (i32, i32) = async.race(fs, -1);
    return r.0 + r.1;
}
`},
		{"with_deadline", `import "std/async";
import "std/time";
function main(): i32 {
    let fs: async.Future[i32][] = [Ready(7)];
    let ds: Option[i32][] = async.with_deadline(time.duration_millis(100), fs);
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
			decide, err := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").CombinedOutput()
			if err != nil {
				t.Fatalf("decide: %v\n%s", err, decide)
			}
			if got := strings.TrimSpace(string(decide)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (generic-enum type-param inference bailed)", tc.name, got)
			}
			asm, err := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).CombinedOutput()
			if err != nil {
				t.Fatalf("loader compile: %v\n%s", err, asm)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "async_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
