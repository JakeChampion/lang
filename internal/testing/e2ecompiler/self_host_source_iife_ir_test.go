package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

var sourceIifeCases = []struct {
	name string
	src  string
}{
	{"var_init_iife", `function main(): i32 {
    let out = 0;
    let v = ((): i32 => { defer { out = out + 5; } return 1; })();
    return out + v;
}`},
	{"iife_in_loop", `function main(): i32 {
    let out = 0;
    let i = 0;
    while (i < 3) {
        let v = ((): i32 => { defer { out = out + 5; } return 1; })();
        out = out + v;
        i = i + 1;
    }
    return out;
}`},
	{"nested_iifes", `function main(): i32 {
    let out = 0;
    let v = ((): i32 => {
        defer { out = out + 5; }
        let w = ((): i32 => { defer { out = out + 100; } return 2; })();
        return w;
    })();
    return out + v;
}`},
	{"iife_captures_local", `function main(): i32 {
    let base = 10;
    let out = 0;
    let v = ((): i32 => { defer { out = out + base; } return base + 1; })();
    return out + v;
}`},
	{"defer_nested_in_if", `function main(): i32 {
    let out = 0;
    let c = true;
    let v = ((): i32 => { if (c) { defer { out = out + 5; } } return 1; })();
    return out + v;
}`},
	{"iife_no_defer", `function main(): i32 {
    let v = ((): i32 => { return 42; })();
    return v;
}`},
	{"value_block_beside_iife", `function main(): i32 {
    let out = 0;
    let b = { defer { out = out + 1; } 3 };
    let v = ((): i32 => { defer { out = out + 5; } return 1; })();
    return out * 100 + b * 10 + v;
}`},
}

func TestSelfHostSourceIifeIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range sourceIifeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, cerr := cmd.Output()
			if cerr != nil {
				t.Fatalf("strict-IR compile: %v: %s", cerr, exitStderr(cerr))
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "srciife_"+tc.name, string(asm))
			run := runX86_64Bin(runner, progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
