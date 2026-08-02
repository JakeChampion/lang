package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapValuePtrIRCases pin the #3495 fix: a `Map[K, <pointer>]` value (string /
// array / …) must be RC-retained by `__fern_map_set` on the wasm IR backend.
// Pre-fix, `op_map_set` left the wasm `vis` flag hardcoded 0, so a pointer value
// inserted through a map-threading helper was freed by the caller's `dec` under
// the map; the next allocation reused the buffer, aliasing a sibling key's
// value. The trigger needs the get/insert to happen inside a helper that returns
// the map (the inline form decremented differently and stayed correct), with the
// value array built in the helper's `None` branch and a later append observed on
// a sibling key. x86-64 was always correct (its map runtime never RC-manages
// values; its `arr_dec` is leak-only). Each case returns a small deterministic
// int, pinned to the `"ir"` path; expectations verified against native + x86-64.
const mapValuePtrIRPrelude = `function ap(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    match (m.get(k)) {
        Some(e) => { return m.insert(k, e.append(v)); },
        None => { var a: string[] = [v]; return m.insert(k, a); },
    }
    return m;
}
function vcount(m: Map[string, string[]], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v.len(); }, None => { return 0; }, }
    return 0;
}
function iput(m: Map[string, i32], k: string, v: i32): Map[string, i32] { return m.insert(k, v); }
function iget(m: Map[string, i32], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v; }, None => { return 0; }, }
    return 0;
}
`

var mapValuePtrIRCases = []struct {
	name string
	main string
	want int
}{
	// #3495: append to "a" must NOT leak into sibling "b". a:[1,3]=2, b:[2]=1 -> 21.
	{"dup-key-sibling", `var m: Map[string, string[]] = Map {}; m = ap(m, "a", "1"); m = ap(m, "b", "2"); m = ap(m, "a", "3"); return vcount(m, "a") * 10 + vcount(m, "b");`, 21},
	// three distinct keys each with one value, via the helper -> none corrupted.
	{"three-keys", `var m: Map[string, string[]] = Map {}; m = ap(m, "x", "1"); m = ap(m, "y", "2"); m = ap(m, "z", "3"); return vcount(m, "x") * 100 + vcount(m, "y") * 10 + vcount(m, "z");`, 111},
	// scalar (i32) values via a helper must stay correct (vis=0, no regression).
	{"scalar-values", `var m: Map[string, i32] = Map {}; m = iput(m, "a", 5); m = iput(m, "b", 7); return iget(m, "a") + iget(m, "b");`, 12},
}

func mapValuePtrIRSrc(mainBody string) string {
	return mapValuePtrIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMapValuePtrIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pinned to the "ir" path.
func TestSelfHostMapValuePtrIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapValuePtrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mapValuePtrIRSrc(tc.main))
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

// TestSelfHostMapValuePtrIRWasm runs the same cases through the wasm IR backend —
// the backend the #3495 bug was specific to.
func TestSelfHostMapValuePtrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-value-ptr wasm IR e2e")
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

	for _, tc := range mapValuePtrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mapValuePtrIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "mapvalueptr_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("map-value-ptr wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
