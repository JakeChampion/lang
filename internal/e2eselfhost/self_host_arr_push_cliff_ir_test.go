package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// arrPushCliffIRCases pin `__arr_push_shared_count()` — the rc==1 append cliff
// counter — on the self-host IR path. `__fern_arr_push` mutates in place only
// at rc == 1, so that threshold is a performance-correctness boundary with no
// diagnostic of its own: one stray retain upstream makes every append in a
// threaded accumulator copy the whole buffer, and the program stays CORRECT
// while going quadratic.
//
// BOTH halves are pinned, and the healthy case alone is not enough: the
// self-host lowering has to know the builtin, and a reader wired up without a
// bump site returns 0 forever — which reads as a clean run rather than as
// missing instrumentation. The shared case is what proves the
// counter can fire.
//
// The native backend is the oracle (interp has no refcounts and copies
// nothing, so it reports 0 for both). Exit codes stay well under the
// wasmtime clamp.
var arrPushCliffIRCases = []struct {
	name string
	main string
	want int
}{
	// A threaded accumulator handed back through a borrowed param — the shape
	// every byte-emitter in the self-host compiler is built from. Nothing else
	// holds the buffer, so every append after a grow mutates in place.
	{"healthy-threaded-accumulator", `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`, 0},
	// Crosses the cliff exactly once, deliberately. The loop leaves the buffer
	// with spare capacity; `b` then takes a second reference, so the append
	// that follows cannot mutate in place despite the room and must copy.
	// Reading both afterwards proves the copy really happened — had the append
	// mutated in place, b would see the longer length.
	{"shared-buffer-with-spare-capacity", `function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    var b: i32[] = a;
    var c: i32[] = a.append(99);
    if (b.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 99 || b[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
}

// TestSelfHostArrPushCliffIRX86_64 routes each case through the self-hosted
// x86-64 IR driver and cross-checks against the native backend, which lowers
// the same builtin over its own BSS counter.
func TestSelfHostArrPushCliffIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range arrPushCliffIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			if _, code := compileAndRunX86_64(t, tc.main+"\n"); code != tc.want {
				t.Fatalf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostArrPushCliffIRWasm runs the same cases through the wasm IR
// backend, whose counter lives in a fixed low-memory slot
// (`arr_push_shared_addr`) rather than a BSS word.
func TestSelfHostArrPushCliffIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arr-push-cliff wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrPushCliffIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
			watFile := filepath.Join(dir, "arr_push_cliff_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("arr-push-cliff wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
