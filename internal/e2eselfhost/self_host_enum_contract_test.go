package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func enumContractRuntimeCases() []arrayClaimCase {
	var cases []arrayClaimCase
	for _, body := range []struct{ name, body string }{
		{"direct", "let src: E = E.Full([i, i + 1]); match (src) { Full(xs) => { return xs; }, Empty => { return [0]; } }"},
		{"alias", "let src: E = E.Full([i, i + 1]); let x: E = src; let y: E = x; match (y) { Full(xs) => { return xs; }, Empty => { return [0]; } }"},
		{"payload-alias", "let src: E = E.Full([i, i + 1]); match (src) { Full(xs) => { let ys = xs; return ys; }, Empty => { return [0]; } }"},
		{"conditional", "let src: E = E.Full([i, i + 1]); match (src) { Full(xs) => { if (i % 2 == 0) { return xs; } }, Empty => {} } return [i, i + 1];"},
		{"shadowed-parameter", "if (i >= 0) { let i: E = E.Full([i, i + 1]); match (i) { Full(xs) => { return xs; }, Empty => { return [0]; } } } return [0, 1];"},
	} {
		cases = append(cases, arrayClaimCase{body.name, `enum E { Full(i32[]), Empty }
@noinline function produce(i: i32): i32[] { ` + body.body + ` }
@noinline function exercise(i: i32): i32 {
    let xs = produce(i); let churn = [91, 92];
    if (xs.len() != 2 || xs[0] != i || xs[1] != i + 1 || churn[0] != 91) { return 2; }
    return 0;
}
function main(): i32 {
    let i = 0; while (i < 32) { let r = exercise(i); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; } return 0;
}`, true})
	}
	cases = append(cases, arrayClaimCase{"shared-child", `enum E { Full(i32[]), Empty }
@noinline function take(e: E): i32[] {
    match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } }
}
@noinline function exercise(i: i32): i32 {
    let e = E.Full([i, i + 1]); let alias: E = e;
    let first = take(e); let second = take(alias); let churn = [91, 92];
    if (first[0] != i || second[1] != i + 1 || churn[0] != 91) { return 2; }
    match (e) { Full(xs) => { if (xs[0] != i || xs[1] != i + 1) { return 3; } }, Empty => { return 4; } }
    return 0;
}
function main(): i32 {
    let i = 0; while (i < 32) { let r = exercise(i); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; } return 0;
}`, true})
	cases = append(cases, arrayClaimCase{"empty-variant", `enum E { Full(i32[]), Empty }
@noinline function take(e: E, z: i32): i32[] {
    match (e) { Full(xs) => { return xs; }, Empty => { return [z]; } }
}
@noinline function exercise(i: i32): i32 {
    let e = E.Empty; let xs = take(e, i); let churn = [i + 91];
    if (xs.len() != 1 || xs[0] != i || churn[0] != i + 91) { return 2; } return 0;
}
function main(): i32 {
    let i = 0; while (i < 32) { let r = exercise(i); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; } return 0;
}`, true})
	return cases
}

func TestSelfHostEnumContractIRArm64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	armgcc, armrunner := arm64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	stage0 := buildSelfHostBin(t, gcc, dir, "fern.fern", "stage0")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	build := runX86_64Bin(runner, stage0, "-target", "arm64-linux", "-emit", "asm", filepath.Join(dir, "fern.fern"), root)
	var diagnostic bytes.Buffer
	build.Stderr = &diagnostic
	asm, err := build.Output()
	if err != nil {
		t.Fatalf("self-host compiler build: %v\n%s", err, diagnostic.String())
	}
	stage1 := buildBinArm64(t, armgcc, dir, "stage1", string(asm))
	for _, tc := range enumContractRuntimeCases() {
		for _, target := range []string{"arm64-linux", "x86-64-linux", "x86-64-sanitize", "wasm32-wasi"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				input := filepath.Join(dir, "input.fern")
				if err := os.WriteFile(input, []byte(tc.source), 0o644); err != nil {
					t.Fatal(err)
				}
				emitTarget, mode := target, "FERN_LEAKCHECK=1"
				if target == "x86-64-sanitize" {
					emitTarget, mode = "x86-64-linux", "FERN_SANITIZE=1"
				}
				compile := runArm64Bin(armrunner, stage1, "-target", emitTarget, "-emit", "asm", input, root)
				compile.Env = append(os.Environ(), mode)
				var stderr bytes.Buffer
				compile.Stderr = &stderr
				output, err := compile.Output()
				if err != nil {
					t.Fatalf("self-host compilation: %v\n%s", err, stderr.String())
				}
				var run *exec.Cmd
				switch target {
				case "arm64-linux":
					run = runArm64Bin(armrunner, buildBinArm64(t, armgcc, dir, "program", string(output)))
				case "x86-64-linux", "x86-64-sanitize":
					run = runX86_64Bin(runner, buildBin(t, gcc, dir, "program", string(output)))
				case "wasm32-wasi":
					wat := filepath.Join(dir, "program.wat")
					if err := os.WriteFile(wat, output, 0o644); err != nil {
						t.Fatal(err)
					}
					run = exec.Command("wasmtime", "run", wat)
				}
				got, err := run.CombinedOutput()
				if err != nil {
					t.Fatalf("self-host runtime: %v\n%s", err, got)
				}
				if target != "wasm32-wasi" {
					var allocs, frees, live int64
					summary := leakSummaryLine(string(got))
					if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
						t.Fatal(err)
					}
					if allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("unbalanced: %s", summary)
					}
				}
			})
		}
	}
}
