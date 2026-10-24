package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type enumValueBlockCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const evbMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func enumValueBlockCases() []enumValueBlockCase {
	const decls = `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []enumValueBlockCase{
		{
			name: "match_expr_read",
			src: decls + `function round(i: i32): i32 {
    let v: E = E.A([i, i + 1]);
    return (match (v) { E.A(xs) => xs.len(), E.B => 0 }) % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			name: "match_stmt_read",
			src: decls + `function round(i: i32): i32 {
    let v: E = E.A([i, i + 1]);
    let n: i32 = 0;
    match (v) { E.A(xs) => { n = xs.len(); }, E.B => { n = 0; } }
    return n % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			name: "helper_reads_param_by_match_expr",
			src: decls + `function consume(e: E): i32 { return match (e) { E.A(xs) => xs.len(), E.B => 0 }; }
function round(i: i32): i32 {
    let v: E = mkv(i);
    return consume(v) % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			name: "helper_reads_param_by_match_stmt",
			src: decls + `function consume(e: E): i32 { let n: i32 = 0; match (e) { E.A(xs) => { n = xs.len(); }, E.B => { n = 0; } } return n; }
function round(i: i32): i32 {
    let v: E = mkv(i);
    return consume(v) % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			name: "helper_wraps_match_expr_in_binary",
			src: decls + `function consume(e: E, k: i32): i32 { return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + k; }
function round(i: i32): i32 {
    let v: E = mkv(i);
    return consume(v, i) % 101;
}
` + evbMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			name: "match_expr_then_if_expr",
			src: decls + `function round(i: i32): i32 {
    let v: E = E.A([i, i + 1]);
    let n: i32 = match (v) { E.A(xs) => xs.len(), E.B => 0 };
    let u: i32 = if (n > 0) { n } else { 0 };
    return u % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			name: "value_block_yields_the_enum_refused",
			src: decls + `function round(i: i32): i32 {
    let v: E = E.A([i, i + 1]);
    let y: E = if (i % 2 == 0) { v } else { mkv(i + 1) };
    return (match (y) { E.A(xs) => xs.len(), E.B => 0 }) % 101;
}
` + evbMain,
			want: 6, allocs: 300, frees: 300,
		},
	}
}

func TestSelfHostEnumValueBlockBorrowX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range enumValueBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "enumvb_"+tc.name, asm)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

func TestSelfHostEnumValueBlockBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum value-block borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range enumValueBlockCases() {
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
			watFile := filepath.Join(dir, "enumvb_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("enum value-block borrow wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostEnumValueBlockBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range enumValueBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "enumvb_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
