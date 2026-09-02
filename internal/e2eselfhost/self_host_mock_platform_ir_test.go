package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mockPlatformIRCases exercise std/mock_platform's recording surface through
// the self-host IR path on x86-64 + wasm (the `std/mock_platform` row was fully
// unaudited). The single-program driver resolves no imports and `MockPlatform`
// / `MockCall` are reserved builtin type names, so the surface is inlined from
// `internal/stdlib/std/mock_platform.fern` with the types renamed to `MPlat` /
// `MCall` and the line split hand-rolled (std/string is not reachable here).
// This verifies the constructs std/mock_platform lowers to compile on the IR
// path: a struct holding a `Cell[string]` field, accumulating into that cell
// (`c.set(c.get() + …)`, the shape #8067 corrupted), byte indexing and
// `slice_unchecked` over the log, building an array-of-struct from the parse,
// indexed array-of-struct reads, a membership scan, string equality, and
// `find_call`'s `Option[MCall]` (Option of a struct) with a payload-binding
// `match`. Each program returns a small deterministic int (<= 126), pinned to
// the `"ir"` path; expectations are oracle-checked against the native
// interpreter. FEATURE-AUDIT std/mock_platform row.
const mockPlatformIRPrelude = `struct MCall { name: string, args: string }
struct MPlat { sink: Cell[string] }
function mplat_new(): MPlat { return MPlat { sink: cell_new("") }; }
function (m: MPlat) record(name: string, args: string): void {
    m.sink.set(m.sink.get() + name + "\t" + args + "\n");
}
function (m: MPlat) calls(): MCall[] {
    var out: MCall[] = [];
    var log: string = m.sink.get();
    var start: i32 = 0;
    var tab: i32 = -1;
    var i: i32 = 0;
    while (i < log.len()) {
        var ch: i32 = log[i] as i32;
        if (ch == 9 && tab < 0) { tab = i; }
        if (ch == 10) {
            if (tab < 0) {
                out = out.append(MCall { name: slice_unchecked(log, start, i) + "", args: "" });
            } else {
                out = out.append(MCall {
                    name: slice_unchecked(log, start, tab) + "",
                    args: slice_unchecked(log, tab + 1, i) + ""
                });
            }
            start = i + 1;
            tab = -1;
        }
        i = i + 1;
    }
    return out;
}
function (m: MPlat) call_count(): i32 { return m.calls().len(); }
function (m: MPlat) reset(): void { m.sink.set(""); }
function (m: MPlat) has_call(name: string): boolean {
    var cs: MCall[] = m.calls();
    var i: i32 = 0;
    while (i < cs.len()) {
        if (cs[i].name == name) { return true; }
        i = i + 1;
    }
    return false;
}
function (m: MPlat) find_call(name: string): Option[MCall] {
    var cs: MCall[] = m.calls();
    var i: i32 = 0;
    while (i < cs.len()) {
        if (cs[i].name == name) { return Some(cs[i]); }
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
	// record accumulates into the shared cell; call_count parses it back: 3.
	{"call-count", `var m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); m.record("fetch", "c"); return m.call_count();`, 3},
	// indexed array-of-struct field read: calls()[1].name == "kv" -> first char 'k' = 107.
	{"indexed-field", `var m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); return m.calls()[1].name[0] as i32;`, 107},
	// the record a HANDLER would make reaches the mock the test still holds,
	// because both hold the same cell: two views, one log.
	{"shared-cell", `var m: MPlat = mplat_new(); var view: MPlat = MPlat { sink: m.sink }; view.record("fetch", "a"); view.record("kv", "b"); return m.call_count();`, 2},
	// has_call membership scan: present -> 1.
	{"has-call-yes", `var m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); if (m.has_call("kv")) { return 1; } return 0;`, 1},
	// has_call membership scan: absent -> 9.
	{"has-call-no", `var m: MPlat = mplat_new(); m.record("fetch", "a"); if (m.has_call("missing")) { return 1; } return 9;`, 9},
	// find_call returns Some(first match); inspect its args length: "GET" -> 3.
	{"find-some", `var m: MPlat = mplat_new(); m.record("fetch", "GET"); m.record("kv", "set"); match (m.find_call("fetch")) { Some(c) => { return c.args.len(); }, None => { return 0; }, } return 0;`, 3},
	// find_call on a missing name renders the None arm: 7.
	{"find-none", `var m: MPlat = mplat_new(); m.record("fetch", "GET"); match (m.find_call("nope")) { Some(c) => { return 0; }, None => { return 7; }, } return 0;`, 7},
	// reset clears the log through the same cell: call_count back to 0.
	{"reset", `var m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); m.reset(); return m.call_count();`, 0},
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
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
