package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An enum read through a match EXPRESSION keeps its release ---------------
//
// `var v: E = E.A([i, i + 1]); consume(v);` reclaimed NOTHING — 200 allocs / 0
// frees over 100 rounds against native's 200/200, the box and its payload every
// round. The same code with the match written as a STATEMENT was already flat.
//
// A match/if/block expression desugars to a zero-arg IIFE that irlower INLINES,
// so a name read inside it is an ordinary in-scope read. The struct family
// learned that (self_host_match_expr_borrow_test.go) and recursed into the
// STRICT walker, which was right for its own caller and wrong for the walkers
// that carry the match-borrow reading:
//
//   - stmt_unsafe_for_match_borrow, which feeds borrowable_params_of. A callee
//     that reads its param through a match expression had that param marked
//     non-borrowable, so every CALLER refused its own enum local's release.
//   - ef_unsafe_expr, the enum-field fork, which is the caller's own
//     `var t = match (v) { … }` read.
//
// The reading is carried explicitly now (expr_unsafe_for_vb), through the
// borrowing-binop operand path too — `+` and the comparisons hand their operands
// to binop_operand_unsafe_for / expr_unsafe_for_view_pos, which dropped it, so
// `(match (e) { … }) + k` stayed refused until those were threaded as well.
//
// The strict entry point is unchanged and still recurses strictly:
// precise_drop_names reads it and widens to the match-borrow reading for one
// class at a time on purpose, and making value blocks borrow-aware for everyone
// silently widened the enum class too — the plan then claimed a precise drop of
// a box its own consuming-match reuse still owned.
//
// Every want below was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count().

type enumValueBlockCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const evbMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
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
			// THE REPRO, caller side. Base: 200 allocs / 0 frees.
			name: "match_expr_read",
			src: decls + `function round(i: i32): i32 {
    var v: E = E.A([i, i + 1]);
    return (match (v) { E.A(xs) => xs.len(), E.B => 0 }) % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// The same read written as a STATEMENT, which was already flat. It
			// is the control that says the difference was the value block and
			// not the match.
			name: "match_stmt_read",
			src: decls + `function round(i: i32): i32 {
    var v: E = E.A([i, i + 1]);
    var n: i32 = 0;
    match (v) { E.A(xs) => { n = xs.len(); }, E.B => { n = 0; } }
    return n % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// THE REPRO, callee side — and the expensive half. The helper reads
			// its PARAM through a match expression, which marked the param
			// non-borrowable and cost the caller its release. Base: 200 / 0.
			name: "helper_reads_param_by_match_expr",
			src: decls + `function consume(e: E): i32 { return match (e) { E.A(xs) => xs.len(), E.B => 0 }; }
function round(i: i32): i32 {
    var v: E = mkv(i);
    return consume(v) % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// The same helper with a match STATEMENT — already flat, and the
			// control for the callee half.
			name: "helper_reads_param_by_match_stmt",
			src: decls + `function consume(e: E): i32 { var n: i32 = 0; match (e) { E.A(xs) => { n = xs.len(); }, E.B => { n = 0; } } return n; }
function round(i: i32): i32 {
    var v: E = mkv(i);
    return consume(v) % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// The value block nested inside a BINARY expression. `+` and the
			// comparisons take a separate operand path (binop_operand_unsafe_for
			// -> expr_unsafe_for_view_pos), and that path dropped the mode too —
			// so a helper written `return (match (e) { … }) + k;` stayed refused
			// after the first three walkers were threaded. It is the shape the
			// enum field-share suite's call-argument row uses.
			name: "helper_wraps_match_expr_in_binary",
			src: decls + `function consume(e: E, k: i32): i32 { return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + k; }
function round(i: i32): i32 {
    var v: E = mkv(i);
    return consume(v, i) % 101;
}
` + evbMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			// An if-expression value block alongside the match one: the rule is
			// about value blocks, not about `match` specifically.
			name: "match_expr_then_if_expr",
			src: decls + `function round(i: i32): i32 {
    var v: E = E.A([i, i + 1]);
    var n: i32 = match (v) { E.A(xs) => xs.len(), E.B => 0 };
    var u: i32 = if (n > 0) { n } else { 0 };
    return u % 101;
}
` + evbMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// THE NEGATIVE CONTROL, and the reason the carve-out is not a
			// blanket accept: here the block's VALUE is the enum itself, so the
			// name really does leave. The strict walker's StmtReturn arm catches
			// that inside the block exactly as it does outside one, and the name
			// stays refused — 300 / 0, unchanged by this slice. It leaks, which
			// is the safe direction; native reclaims it, and closing that is a
			// different admission.
			name: "value_block_yields_the_enum_refused",
			src: decls + `function round(i: i32): i32 {
    var v: E = E.A([i, i + 1]);
    var y: E = if (i % 2 == 0) { v } else { mkv(i + 1) };
    return (match (y) { E.A(xs) => xs.len(), E.B => 0 }) % 101;
}
` + evbMain,
			want: 6, allocs: 300, frees: 0,
		},
	}
}

// TestSelfHostEnumValueBlockBorrowX86_64 is the leak-accounting leg.
func TestSelfHostEnumValueBlockBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
				t.Errorf("%s: %s — want frees=%d. FEWER means the value-block "+
					"reading stopped reaching a walker; MORE on the refused row "+
					"means the carve-out grew into a blanket accept", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostEnumValueBlockBorrowWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostEnumValueBlockBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum value-block borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range enumValueBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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

// TestSelfHostEnumValueBlockBorrowIRArm64 — the arm64 sibling under qemu.
func TestSelfHostEnumValueBlockBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
