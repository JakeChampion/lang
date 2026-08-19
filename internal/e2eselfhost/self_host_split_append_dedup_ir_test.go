package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regression guard for the wasm-IR helper-gating dedup bug: str_split_helper
// (emitted for .split() / .lines()) itself begins with arr_push_helper(), so
// it already defines $__fern_arr_push. The standalone arr_push gate in
// wasm_ir_run.fern used to fire independently, so a module using BOTH a
// split/lines op AND an .append (op_arr_push / op_arr_push_owned / read_dir)
// defined $__fern_arr_push twice — wasmtime rejected the module with
// "duplicate func identifier" before it could run. The gate now skips the
// standalone emit when a split/lines op already pulls the helper in.
//
// These programs use .split()/.lines(), which the self-host irlower recognizes
// directly (no stdlib) but the native checker does not resolve without a
// std/string import — so they're pinned to a hard-coded expected exit rather
// than an interp oracle. Each is a self-contained i32 return < 126.
type splitAppendDedupCase struct {
	name string
	src  string
	exit int
}

var splitAppendDedupCases = []splitAppendDedupCase{
	// split + self-reassign append (op_arr_push_owned) — the original repro.
	{"split-owned-append", `function main(): i32 {
    var s: string = "a,b,c";
    var parts: string[] = s.split(",");
    var xs: i32[] = [];
    xs = xs.append(1);
    xs = xs.append(2);
    return parts.len() * 10 + xs.len();
}`, 32},
	// split + plain (non-reassign) append (op_arr_push).
	{"split-plain-append", `function main(): i32 {
    var s: string = "a,b,c,d";
    var parts: string[] = s.split(",");
    var xs: i32[] = [];
    var ys: i32[] = xs.append(7);
    return parts.len() * 10 + ys.len();
}`, 41},
	// lines + append — str_lines pulls str_split_helper in via the same gate.
	{"lines-append", `function main(): i32 {
    var s: string = "x\ny\nz";
    var ls: string[] = s.lines();
    var xs: i32[] = [];
    xs = xs.append(1);
    return ls.len() * 10 + xs.len();
}`, 31},
}

func TestSelfHostSplitAppendDedupIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host split/append dedup wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "split_dedup_driver")

	for _, tc := range splitAppendDedupCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			// Belt-and-braces: the module must define $__fern_arr_push exactly
			// once (the bug emitted it twice).
			if n := strings.Count(string(wat), "(func $__fern_arr_push "); n != 1 {
				t.Errorf("%s: $__fern_arr_push defined %d times, want 1", tc.name, n)
			}
			watFile := filepath.Join(dir, "split_dedup_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("split/append dedup wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
