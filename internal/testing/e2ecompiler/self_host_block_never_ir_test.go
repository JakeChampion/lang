package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var blockNeverCases = []struct {
	name string
	src  string
}{
	{"arrow_lambda_block_return", `function main(): i32 {
    let f = (n: i32): i32 => { return n; };
    return f(4);
}`},
	{"var_init_all_paths_return", `function f(c: boolean): i32 {
    let x: i32 = { if (c) { return 1; } return 2; };
    return x + 100;
}
function main(): i32 {
    return f(true) * 10 + f(false);
}`},
	{"if_expression_arm_returns", `function pick(c: boolean): i32 {
    let x: i32 = if (c) { return 1; } else { 2 };
    return x + 100;
}
function main(): i32 {
    return pick(true) * 10 + pick(false);
}`},
	{"string_block_all_paths_return", `function label(c: boolean): string {
    let s: string = { if (c) { return "yes"; } return "no"; };
    return s + "!";
}
function main(): i32 {
    return label(true).len() * 10 + label(false).len();
}`},
}

func TestSelfHostBlockNeverIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range blockNeverCases {
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
			progBin := buildBin(t, gcc, dir, "blocknever_"+tc.name, string(asm))
			run := runX86_64Bin(runner, progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostBlockValuelessStillRejected(t *testing.T) {
	_, mmc, stdlibRoot, _, runner, _ := annotateF64ProjDir(t)
	const src = `function side(): i32 { return 1; }
function main(): i32 {
    let x: i32 = { side(); };
    return x;
}`
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Fatalf("a fall-through value-less block was accepted; driver emitted %d bytes of asm", len(out))
	}
	if !strings.Contains(string(out), "E061") {
		t.Errorf("refused, but not with native's E061; driver said: %s", out)
	}
}
