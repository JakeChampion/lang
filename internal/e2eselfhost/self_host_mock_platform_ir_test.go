package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mockPlatformIRCases exercise std/mock_platform's call-recording log through
// the self-host IR path on x86-64 + wasm (the `std/mock_platform` row was fully
// unaudited). The single-program driver resolves no imports and `MockPlatform`
// / `MockCall` are reserved builtin type names, so the surface is inlined
// verbatim from `internal/stdlib/std/mock_platform.fern` with the types renamed
// to `MPlat` / `MCall`. This verifies the constructs std/mock_platform lowers
// to compile on the IR path: a struct holding an array-of-struct field
// (`MCall[]`), functional struct-spread update appending to that array
// (`MPlat { ...m, calls: m.calls.append(MCall { … }) }`), indexed
// array-of-struct field reads (`m.calls[i].name`), a membership scan, string
// equality, and `find_call`'s `Option[MCall]` (Option of a struct) with a
// payload-binding `match`. Each program returns a small deterministic int
// (<= 126), pinned to the `"ir"` path; expectations are oracle-checked against
// the native interpreter. FEATURE-AUDIT std/mock_platform row.
const mockPlatformIRPrelude = `struct MCall { name: string, args: string }
struct MPlat { calls: MCall[] }
function mplat_new(): MPlat { var empty: MCall[] = []; return MPlat { calls: empty }; }
function (m: MPlat) record(name: string, args: string): MPlat {
    return MPlat { ...m, calls: m.calls.append(MCall { name: name, args: args }) };
}
function (m: MPlat) call_count(): i32 { return m.calls.len(); }
function (m: MPlat) reset(): MPlat { var empty: MCall[] = []; return MPlat { ...m, calls: empty }; }
function (m: MPlat) has_call(name: string): boolean {
    var i: i32 = 0;
    while (i < m.calls.len()) {
        if (m.calls[i].name == name) { return true; }
        i = i + 1;
    }
    return false;
}
function (m: MPlat) find_call(name: string): Option[MCall] {
    var i: i32 = 0;
    while (i < m.calls.len()) {
        if (m.calls[i].name == name) { return Some(m.calls[i]); }
        i = i + 1;
    }
    return None;
}
`

var mockPlatformIRCases = []struct {
	name string
	main string
	want int
}{
	// record appends to the MCall[] log; call_count reports its length: 3.
	{"call-count", `var m: MPlat = mplat_new(); m = m.record("fetch", "a"); m = m.record("kv", "b"); m = m.record("fetch", "c"); return m.call_count();`, 3},
	// indexed array-of-struct field read: calls[1].name == "kv" -> first char 'k' = 107.
	{"indexed-field", `var m: MPlat = mplat_new(); m = m.record("fetch", "a"); m = m.record("kv", "b"); return m.calls[1].name[0];`, 107},
	// has_call membership scan: present -> 1.
	{"has-call-yes", `var m: MPlat = mplat_new(); m = m.record("fetch", "a"); m = m.record("kv", "b"); if (m.has_call("kv")) { return 1; } return 0;`, 1},
	// has_call membership scan: absent -> 9.
	{"has-call-no", `var m: MPlat = mplat_new(); m = m.record("fetch", "a"); if (m.has_call("missing")) { return 1; } return 9;`, 9},
	// find_call returns Some(first match); inspect its args length: "GET" -> 3.
	{"find-some", `var m: MPlat = mplat_new(); m = m.record("fetch", "GET"); m = m.record("kv", "set"); match (m.find_call("fetch")) { Some(c) => { return c.args.len(); }, None => { return 0; }, } return 0;`, 3},
	// find_call on a missing name renders the None arm: 7.
	{"find-none", `var m: MPlat = mplat_new(); m = m.record("fetch", "GET"); match (m.find_call("nope")) { Some(c) => { return 0; }, None => { return 7; }, } return 0;`, 7},
	// reset clears the log: call_count back to 0.
	{"reset", `var m: MPlat = mplat_new(); m = m.record("fetch", "a"); m = m.record("kv", "b"); var m2: MPlat = m.reset(); return m2.call_count();`, 0},
}

func mockPlatformIRSrc(mainBody string) string {
	return mockPlatformIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMockPlatformIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostMockPlatformIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mockPlatformIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mockPlatformIRSrc(tc.main))
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

// TestSelfHostMockPlatformIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostMockPlatformIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host mock_platform wasm IR e2e")
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

	for _, tc := range mockPlatformIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mockPlatformIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "mock_platform_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("mock_platform wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
