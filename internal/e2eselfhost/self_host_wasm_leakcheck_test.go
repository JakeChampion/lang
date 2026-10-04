package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// --- The wasm census (#5362's wasm half, in the self-host) -------------------
//
// The last backend without the leak census. FERN_LEAKCHECK reaches the wasm
// emitter (mode 0, the command core the harness runs under wasmtime): counter
// globals outside the heap gate, bumps in $__fern_alloc (before the freelist
// pop — a pop is an alloc too), in $__fern_arr_dec's rc==1 free and
// $__fern_alloc_reuse's mispaired-donor free (each before the bsz slot is
// overwritten by the freelist next-pointer), and a $__fern_lc_report on
// stderr wired through a flag-gated reporting $proc_exit — so the exit() op
// and $_start both report through one definition, with the raw preview1
// import renamed underneath it.
//
// Counts are asserted as properties (balanced / unbalanced / zero), matching
// the arm64 suite: the x86 legs pin exact counts per shape, and this suite
// gates the instrument. Note the wasm reclaim differs from the register
// backends (rc-headered strings, one shared $__fern_arr_dec), so a shape's
// balance here is its own fact, not a transcription of the x86 row.

func wasmLcCompile(t *testing.T, runner []string, driverBin, src string, env []string) string {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm driver failed: %v", err)
	}
	return string(wat)
}

func wasmLcRun(t *testing.T, dir, name, wat string) (stderr string, exit int) {
	t.Helper()
	watFile := filepath.Join(dir, name+".wat")
	if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watFile)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for %q", name)
	}
	return errBuf.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostWasmLeakcheck(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name    string
		src     string
		want    int
		verdict string // balanced | leaky | zero
	}{
		{
			// String churn, reclaimed: rc-headered wasm strings free through
			// the shared $__fern_arr_dec, so the census balances. Exit 21 is
			// the family's oracle-confirmed number for this shape.
			name: "clean_string_churn",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); return t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			// A reassigned alias: the typed lowering releases the superseded
			// string, so the census balances.
			name: "alias_reassign_balanced",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; v = w("cd"); return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			// A leak, and the census must SAY so — the half a green exit cannot.
			// A raw __alloc block is never released, so every round leaks one.
			name: "leak_raw_alloc",
			src: `function round(i: i32): i32 { let p: usize = __alloc(24); return i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 53, verdict: "leaky",
		},
		{
			// HEAP-FREE, returning main: the report goes through $_start's
			// $proc_exit and must link with no allocator emitted.
			name: "heapfree_return",
			src:  `function main(): i32 { return 7; }`,
			want: 7, verdict: "zero",
		},
		{
			// HEAP-FREE through the exit() op — the other path into the
			// reporting $proc_exit.
			name: "heapfree_exit_builtin",
			src:  `function main(): i32 { exit(9); return 0; }`,
			want: 9, verdict: "zero",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "lc_"+tc.name, wat)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — the census must not disturb the program", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary on stderr (%q)", tc.name, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			switch tc.verdict {
			case "balanced":
				if allocs == 0 {
					t.Errorf("%s: allocs=0 — the probe exercised no allocation", tc.name)
				}
				if allocs != frees || live != 0 {
					t.Errorf("%s: %s — want a balanced census", tc.name, summary)
				}
			case "leaky":
				if allocs <= frees || live <= 0 {
					t.Errorf("%s: %s — this shape leaks soundly per round; a balanced census "+
						"means a bump site is miscounting", tc.name, summary)
				}
			case "zero":
				if allocs != 0 || frees != 0 || live != 0 {
					t.Errorf("%s: %s — a heap-free program must report zeros", tc.name, summary)
				}
			}
		})
	}
}

// wasmLcInvoke runs the core with `--invoke main`, the way the e2e harness
// reads a result: wasmtime prints main's result on stdout and exits 0.
func wasmLcInvoke(t *testing.T, dir, name, wat string) (stdout, stderr string, exit int) {
	t.Helper()
	watFile := filepath.Join(dir, name+".wat")
	if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", watFile)
	var outBuf, errBuf strings.Builder
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for %q", name)
	}
	return outBuf.String(), errBuf.String(), cmd.ProcessState.ExitCode()
}

// Under FERN_SANITIZE the census is on and gains the register backends'
// verdict line, `fern-sanitizer: leak <K> bytes in <N> blocks`, for a positive
// balance only; a balanced run is silent beyond the summary.
func TestSelfHostWasmSanitizeLeakVerdict(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm sanitize e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	verdict := regexp.MustCompile(`fern-sanitizer: leak (\d+) bytes in (\d+) blocks\n`)

	leaky := `function round(i: i32): i32 { let p: usize = __alloc(24); return i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`
	wat := wasmLcCompile(t, runner, driverBin, leaky, []string{"FERN_SANITIZE=1"})
	stderr, exit := wasmLcRun(t, dir, "san_leaky", wat)
	if exit != 53 {
		t.Fatalf("leaky exited %d, want 53 — a leak verdict must not move the exit status", exit)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != 100 || frees != 0 || live != 2400 {
		t.Fatalf("leaky census %q, want allocs=100 frees=0 live_bytes=2400 (one 24-byte block per round, never freed)", leakSummaryLine(stderr))
	}
	m := verdict.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict in stderr: %q", stderr)
	}
	if m[1] != strconv.FormatInt(live, 10) || m[2] != strconv.FormatInt(allocs-frees, 10) {
		t.Errorf("verdict %q disagrees with the summary %q", m[0], leakSummaryLine(stderr))
	}

	clean := `function main(): i32 { let i: i32 = 0; while (i < 100) { let a: usize = __alloc(64); __free(a, 64); i = i + 1; } return 0; }`
	wat = wasmLcCompile(t, runner, driverBin, clean, []string{"FERN_SANITIZE=1"})
	stderr, exit = wasmLcRun(t, dir, "san_clean", wat)
	if exit != 0 {
		t.Fatalf("clean exited %d, want 0", exit)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean program drew a verdict: %q", stderr)
	}
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("clean census %q, want allocs=100 frees=100 live_bytes=0", leakSummaryLine(stderr))
	}
}

// `--invoke main` calls the export and never reaches $proc_exit, so the export
// reports on its return — once, with the result still printed. A main that
// leaves through exit() reports through $proc_exit instead, and only there.
func TestSelfHostWasmLeakcheckReportsOnInvoke(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck invoke e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name, src, want string
	}{
		{"returning", `function main(): i32 { let p: usize = __alloc(24); return 7; }`, "7"},
		{"exiting", `function main(): i32 { let p: usize = __alloc(24); exit(9); return 1; }`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stdout, stderr, exit := wasmLcInvoke(t, dir, "invoke_"+tc.name, wat)
			if tc.want != "" && (exit != 0 || strings.TrimSpace(stdout) != tc.want) {
				t.Fatalf("exit %d stdout %q, want 0 and %q; stderr: %s", exit, stdout, tc.want, stderr)
			}
			if tc.want == "" && exit != 9 {
				t.Fatalf("exit %d, want 9 (exit() carries its status); stderr: %s", exit, stderr)
			}
			if n := strings.Count(stderr, "leakcheck: allocs="); n != 1 {
				t.Fatalf("%d census lines, want exactly 1: %q", n, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != 1 || frees != 0 || live != 24 {
				t.Errorf("census %q, want allocs=1 frees=0 live_bytes=24", leakSummaryLine(stderr))
			}
		})
	}
}

// Flag off, nothing reaches the emitted wat and the run is silent — with the
// flag-on companion assertions, a gate test rather than a typo test.
func TestSelfHostWasmLeakcheckOffEmitsNothing(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck off e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); return t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`

	off := wasmLcCompile(t, runner, driverBin, src, nil)
	for _, marker := range []string{"__fern_lc_", "__fern_proc_exit_raw", "leakcheck"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off wat contains %q — the census is not fully gated", marker)
		}
	}
	stderr, exit := wasmLcRun(t, dir, "lc_off", off)
	if exit != 21 {
		t.Fatalf("flag-off run exited %d, want 21", exit)
	}
	if stderr != "" {
		t.Errorf("flag-off run wrote stderr: %q", stderr)
	}

	on := wasmLcCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	for _, want := range []string{"$__fern_lc_report", "$__fern_lc_alloc_count", "$__fern_proc_exit_raw"} {
		if !strings.Contains(on, want) {
			t.Errorf("flag-on wat is missing %q", want)
		}
	}
}
