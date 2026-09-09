package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const arrayClaimLiveParentSource = `enum E { Full(i32[]), Empty }
@noinline
function take(e: E): i32[] {
    match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } }
}
@noinline
function inspect(e: E): i32 {
    var xs = take(e);
    return xs[0];
}
@noinline
function churn(): i32 { var xs = [91, 92]; return xs[0]; }
function main(): i32 {
    var e = E.Full([7, 8]);
    if (inspect(e) != 7) { return 1; }
    if (churn() != 91) { return 2; }
    match (e) { Full(xs) => { if (xs[0] != 7 || xs[1] != 8) { return 3; } }, Empty => { return 4; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

func TestSelfHostArrayClaimLiveParentX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interp := buildLangBinForInterp(t)
	if got := interpExit(t, interp, arrayClaimLiveParentSource); got != 0 {
		t.Fatalf("interpreter = %d, want 0", got)
	}
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(mode, func(t *testing.T) {
			asm := hevCompile(t, runner, driver, arrayClaimLiveParentSource, []string{mode})
			bin := buildBin(t, gcc, dir, "liveparent", asm)
			stderr, exit := hevRun(t, runner, bin)
			if exit != 0 {
				t.Fatalf("exit = %d, want 0\n%s", exit, stderr)
			}
			t.Log(stderr)
		})
	}
}

type arrayClaimCase struct {
	name, source string
	balanced     bool
}

func arrayClaimCases() []arrayClaimCase {
	cases := []arrayClaimCase{{"live-parent", arrayClaimLiveParentSource, false}}
	for _, values := range []struct{ ty, a, b, c, d string }{
		{"i32", "1", "2", "3", "4"},
		{"i64", "5000000001", "5000000002", "5000000003", "5000000004"},
		{"u64", "9223372036854775809u64", "9223372036854775810u64", "9223372036854775811u64", "9223372036854775812u64"},
		{"f32", "1.5f32", "2.5f32", "3.5f32", "4.5f32"},
		{"f64", "1.5", "2.5", "3.5", "4.5"},
	} {
		cases = append(cases, arrayClaimCase{
			"owned-append-" + values.ty,
			fmt.Sprintf(`@noinline
function grow(own xs: %[1]s[], v: %[1]s): %[1]s[] { return xs.append(v); }
function exercise(): i32 {
    var xs: %[1]s[] = [%[2]s as %[1]s, %[3]s as %[1]s];
    var old = xs;
    xs = grow(xs, %[4]s as %[1]s);
    if (old.len() != 2 || old[0] != %[2]s as %[1]s || old[1] != %[3]s as %[1]s) { return 1; }
    var i = 0;
    while (i < 32) { xs = grow(xs, %[5]s as %[1]s); i = i + 1; }
    if (xs.len() != 35 || xs[2] != %[4]s as %[1]s || xs[34] != %[5]s as %[1]s) { return 2; }
    return 0;
}
function main(): i32 {
    var result = exercise();
    if (__rc_underflow_count() != 0) { return 99; }
    return result;
}`, values.ty, values.a, values.b, values.c, values.d), true,
		})
	}
	cases = append(cases, arrayClaimCase{"owned-append-reads-receiver", `@noinline
function grow(own xs: i32[]): i32[] { return xs.append(xs[0]); }
function exercise(): i32 {
    var xs = [7, 8];
    xs = grow(xs);
    if (xs.len() != 3 || xs[0] != 7 || xs[1] != 8 || xs[2] != 7) { return 1; }
    return 0;
}
function main(): i32 { var r = exercise(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, true})
	cases = append(cases, arrayClaimCase{"conditional-payload-return", `enum E { A(i32[]), B }
@noinline
function take(i: i32): i32[] {
    var e = E.A([i, i + 1]);
    match (e) { A(xs) => { if (i % 2 == 0) { return xs; } }, B => { } }
    return [7];
}
@noinline
function check(i: i32): i32 {
    var xs = take(i);
    var churn = [91, 92];
    if (i % 2 == 0) {
        if (xs.len() != 2 || xs[0] != i || xs[1] != i + 1) { return 1; }
    } else { if (xs.len() != 1 || xs[0] != 7) { return 2; } }
    if (churn[0] != 91) { return 3; }
    return 0;
}
function main(): i32 {
    var i = 0;
    while (i < 32) { var r = check(i); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, true})
	return cases
}

func TestSelfHostArrayClaimContracts(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern", "wasm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	wasm := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	interp := buildLangBinForInterp(t)
	for _, tc := range arrayClaimCases() {
		if got := interpExit(t, interp, tc.source); got != 0 {
			t.Fatalf("%s: interpreter = %d, want 0", tc.name, got)
		}
	}
	for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range arrayClaimCases() {
				t.Run(tc.name, func(t *testing.T) {
					var cmd *exec.Cmd
					switch target {
					case "x86-64-linux", "x86-64-sanitize":
						mode := "FERN_LEAKCHECK=1"
						if target == "x86-64-sanitize" {
							mode = "FERN_SANITIZE=1"
						}
						asm := hevCompile(t, runner, driver, tc.source, []string{mode})
						cmd = runX86_64Bin(runner, buildBin(t, gcc, dir, tc.name, asm))
					case "arm64-linux":
						armgcc, armrunner := arm64Tooling(t)
						asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(tc.source), "-target", target, "-ir")
						cmd = runArm64Bin(armrunner, buildBinArm64(t, armgcc, dir, tc.name, string(asm)))
					case "wasm32-wasi":
						if _, err := exec.LookPath("wasmtime"); err != nil {
							t.Fatal(err)
						}
						wat := runCapture(t, gcc, runner, wasm, []byte(tc.source), "-ir")
						path := filepath.Join(dir, tc.name+".wat")
						if err := os.WriteFile(path, wat, 0o644); err != nil {
							t.Fatal(err)
						}
						cmd = exec.Command("wasmtime", "run", path)
					}
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("runtime: %v\n%s", err, output)
					}
					if (target == "x86-64-linux" || target == "x86-64-sanitize") && tc.balanced {
						var allocs, frees, live int64
						summary := leakSummaryLine(string(output))
						if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
							t.Fatal(err)
						}
						if allocs == 0 || allocs != frees || live != 0 {
							t.Fatalf("unbalanced: %s", summary)
						}
					}
				})
			}
		})
	}
}
