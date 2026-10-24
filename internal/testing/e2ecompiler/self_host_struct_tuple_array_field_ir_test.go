package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var structTupleArrayFieldIRCases = []struct {
	name string
	src  string
}{
	{"foreach_field", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    let r = Row { pairs: [(1, "a"), (2, "bb"), (3, "ccc")] };
    let s = 0;
    for p in r.pairs { s = s + p.1.len(); }
    return s;
}`},
	{"foreach_both", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    let r = Row { pairs: [(1, "a"), (2, "bb"), (3, "ccc")] };
    let s = 0;
    for p in r.pairs { s = s + p.0 * 10 + p.1.len(); }
    return s;
}`},
	{"index_bind", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    let r = Row { pairs: [(1, "a"), (2, "bb")] };
    let p = r.pairs[0];
    return p.0 + p.1.len();
}`},
	{"direct_index", `struct Row { pairs: (i32, string)[] }
function main(): i32 {
    let r = Row { pairs: [(1, "a"), (2, "bb"), (3, "ccc")] };
    return r.pairs[2].0 + r.pairs[2].1.len();
}`},
	{"string_string", `struct Row { pairs: (string, string)[] }
function main(): i32 {
    let r = Row { pairs: [("a", "xx"), ("bb", "yyy")] };
    let s = 0;
    for p in r.pairs { s = s + p.1.len(); }
    return s;
}`},
	{"mixed_nested", `struct Row { n: i32, pairs: (i32, (i32, string))[] }
function main(): i32 {
    let r = Row { n: 5, pairs: [(1, (2, "ab")), (3, (4, "cde"))] };
    let s = r.n;
    for p in r.pairs { s = s + p.0 + p.1.0 + p.1.1.len(); }
    return s;
}`},
	{"rc_loop", `struct Row { pairs: (i32, string)[] }
function sumrow(r: Row): i32 { let s = 0; for p in r.pairs { s = s + p.1.len(); } return s; }
function main(): i32 {
    let total = 0;
    let i = 0;
    while (i < 100) {
        let r = Row { pairs: [(i, "x"), (i, "yy")] };
        total = total + sumrow(r);
        i = i + 1;
    }
    return total % 256;
}`},
}

func TestSelfHostStructTupleArrayFieldIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "staf")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range structTupleArrayFieldIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "staf_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "staf_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostStructTupleArrayFieldWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct-tuple-array-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range structTupleArrayFieldIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "wstaf_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s wasm = %d, want %d (native oracle)", tc.name, got, want)
			}
		})
	}
}
