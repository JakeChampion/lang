package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stateMachineIRCases is a COMBINATION/integration pin: a word-counting state
// machine that exercises, in one module, an interaction no single-construct pin
// covers — a struct-update (`M { ...m, … }`) emitted from inside a `match` arm
// whose scrutinee is itself a struct field (`m.state`, an enum), guarded by
// `if`/else, driven by a `while` loop over string byte indices (`s[i]`), with the
// updated struct flowing through a by-value function parameter and back as a
// struct return, then read field-wise. Each constituent already lowers and is
// individually pinned (struct-update, nested match, while, enum-from-struct-field,
// string byte index, struct-by-value param + struct return); this locks their
// composition on the IR path so a future change can't silently kick the combined
// shape off it.
//
// Routing-pinned via asm_pathprobe_run (assert path == "ir") and oracle-checked
// against the interpreter; every result is <= 126 (wasmtime exit-code truncation,
// cf. #2908). Mirrors self_host_nested_tuple_ir_test.go.
var stateMachineIRCases = []struct {
	name string
	main string
}{
	// "ab cd ef": 3 words, 6 non-space chars -> 3*10 + 6 = 36.
	{"multi-word", stateMachineProg(`ab cd ef`)},
	// "hello": 1 word, 5 chars -> 15.
	{"single-word", stateMachineProg(`hello`)},
	// " a  bb ": 2 words (a, bb), 3 chars -> 2*10 + 3 = 23.
	{"leading-trailing-double-space", stateMachineProg(` a  bb `)},
	// "": loop body never runs; struct-update never fires -> 0.
	{"empty", stateMachineProg(``)},
}

// stateMachineProg builds the word-counter program over the given input string.
func stateMachineProg(s string) string {
	return `enum St { InWord, Between }
struct M { state: St, words: i32, chars: i32 }
function step(m: M, c: i32): M {
    match (m.state) {
        Between => { if (c == 32) { return M { ...m, state: Between }; } return M { ...m, state: InWord, words: m.words + 1, chars: m.chars + 1 }; },
        InWord => { if (c == 32) { return M { ...m, state: Between }; } return M { ...m, chars: m.chars + 1 }; },
    }
}
function main(): i32 {
    var s: string = "` + s + `";
    var m: M = M { state: Between, words: 0, chars: 0 };
    var i: i32 = 0;
    while (i < s.len()) { m = step(m, s[i] as i32); i = i + 1; }
    return m.words * 10 + m.chars;
}`
}

// TestSelfHostStateMachineIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStateMachineIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range stateMachineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostStateMachineIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStateMachineIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host state-machine wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range stateMachineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			watFile := filepath.Join(dir, "state_machine_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("state-machine wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
