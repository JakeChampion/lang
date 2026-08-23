package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
function round(i: i32): i32 { var t: string = w("ab"); return t.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			// The refused alias chain: leaks soundly per round, and the
			// census must SAY so — the half a green exit cannot.
			name: "leak_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; var u: string = v; return u.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "leaky",
		},
		{
			// HEAP-FREE, returning main: the report rides $_start's
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
function round(i: i32): i32 { var t: string = w("ab"); return t.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`

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
