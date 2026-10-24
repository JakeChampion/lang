package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type matchExprBorrowCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const mebMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func matchExprBorrowCases() []matchExprBorrowCase {
	const enumDecls = `struct P { f: E, n: i32 }
enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	const arrDecls = `struct Q { xs: i32[], n: i32 }
function mkxs(i: i32): i32[] { let o: i32[] = [i, i + 1]; return o; }
`
	return []matchExprBorrowCase{
		{
			name: "match_expr_field_read_aliased",
			src: enumDecls + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let p: P = q;
    return ((match (p.f) { E.A(xs) => xs.len(), E.B => 0 }) + p.n) % 101;
}
` + mebMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			name: "plain_field_read_aliased",
			src: enumDecls + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let p: P = q;
    return p.n % 101;
}
` + mebMain,
			want: 3, allocs: 300, frees: 300,
		},
		{
			name: "match_expr_no_alias",
			src: enumDecls + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    return ((match (q.f) { E.A(xs) => xs.len(), E.B => 0 }) + q.n) % 101;
}
` + mebMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			name: "if_expr_field_read_aliased",
			src: arrDecls + `function round(i: i32): i32 {
    let q: Q = Q { xs: mkxs(i), n: i };
    let p: Q = q;
    return ((if (p.n % 2 == 0) { p.xs.len() } else { 0 }) + p.n) % 101;
}
` + mebMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			name: "block_expr_field_read_aliased",
			src: arrDecls + `function round(i: i32): i32 {
    let q: Q = Q { xs: mkxs(i), n: i };
    let p: Q = q;
    return (({ let k: i32 = p.xs.len(); k + 1 }) + p.n) % 101;
}
` + mebMain,
			want: 4, allocs: 200, frees: 200,
		},
		{
			name: "match_expr_strarr_read",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function round(i: i32): i32 {
    let xs: string[] = [w("a"), w("b")];
    return (match (xs.len()) { 2 => xs[0].len(), _ => 0 }) % 101;
}
` + mebMain,
			want: 93, allocs: 300, frees: 300,
		},
		{
			name: "plain_strarr_read",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function round(i: i32): i32 {
    let xs: string[] = [w("a"), w("b")];
    return xs[0].len() % 101;
}
` + mebMain,
			want: 93, allocs: 300, frees: 300,
		},
		{
			name: "real_lambda_capture_refused",
			src: arrDecls + `function apply(f: () => i32): i32 { return f(); }
function round(i: i32): i32 {
    let q: Q = Q { xs: mkxs(i), n: i };
    let p: Q = q;
    return (apply(() => p.xs.len() + p.n)) % 101;
}
` + mebMain,
			want: 5, allocs: 300, frees: 300,
		},
	}
}

func TestSelfHostMatchExprBorrowX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range matchExprBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "matchexprborrow_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a release ran "+
					"under a live claim)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d",
					tc.name, summary, tc.frees)
			}
		})
	}
}

func TestSelfHostMatchExprBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping match-expr borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range matchExprBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "matchexprborrow_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("match-expr borrow wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostMatchExprBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range matchExprBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "matchexprborrow_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
