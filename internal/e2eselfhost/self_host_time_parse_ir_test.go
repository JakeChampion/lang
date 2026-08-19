package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// timeParseIRCases exercise std/time's `date_parse_iso` — an `Option`-returning
// parser — through the self-host IR path on x86-64 + wasm. This is the
// `Option`-method piece of the std/time "self-host pending" audit gap (after
// the pure-i32 helpers in TestSelfHostTimeIR and the Date struct methods in
// TestSelfHostTimeDateIR).
//
// The body is std/time's `date_parse_iso` verbatim except the struct is named
// `Civil` (the built-in `Date` name is reserved, E010). The coverage that
// matters: a function returning `Option[Civil]` constructs `Some(Civil{...})`
// / `None`, and `main` discriminates the result with a `match` that binds the
// struct payload and reads its fields — `Option` construction + payload-binding
// match + struct field access, all routed through the IR path.
//
// No imports are needed for the interpreter oracle (the parser is pure
// builtins + struct/Option), and the self-host single-program driver routes it
// through the IR path. Each case returns a value kept <= 126 and is
// oracle-checked against the interpreter. FEATURE-AUDIT std/time row.
const timeParseIRPrelude = `struct Civil { year: i32, month: i32, day: i32 }
function parse_digits(s: string, start: i32, end: i32): i32 {
    if (start >= end) { return -1; }
    var acc: i32 = 0;
    var i: i32 = start;
    while (i < end) {
        var b: i32 = s[i] as i32;
        if (b < 48 || b > 57) { return -1; }
        acc = acc * 10 + (b - 48);
        i = i + 1;
    }
    return acc;
}
function date_parse_iso(s: string): Option[Civil] {
    if (s.len() != 10) { return None; }
    if (s[4] != 45 || s[7] != 45) { return None; }
    var y: i32 = parse_digits(s, 0, 4);
    var m: i32 = parse_digits(s, 5, 7);
    var d: i32 = parse_digits(s, 8, 10);
    if (y < 0 || m < 0 || d < 0) { return None; }
    return Some(Civil { year: y, month: m, day: d });
}
`

var timeParseIRCases = []struct {
	name string
	main string
}{
	// Valid parse -> Some; sum month + day (6 + 13 = 19).
	{"valid-monthday", `match (date_parse_iso("2026-06-13")) { Some(d) => { return d.month + d.day; }, None => { return 100; }, }`},
	// Valid parse -> Some; read the year field (2026 - 2000 = 26).
	{"valid-year", `match (date_parse_iso("2026-06-13")) { Some(d) => { return d.year - 2000; }, None => { return 100; }, }`},
	// Wrong length ("2026-6-13" is 9 chars) -> None -> sentinel 7.
	{"bad-length", `match (date_parse_iso("2026-6-13")) { Some(d) => { return 0; }, None => { return 7; }, }`},
	// Wrong separators (slashes) -> None -> sentinel 8.
	{"bad-separator", `match (date_parse_iso("2026/06/13")) { Some(d) => { return 0; }, None => { return 8; }, }`},
	// Non-digit byte in the year slice -> None -> sentinel 9.
	{"bad-digit", `match (date_parse_iso("20x6-06-13")) { Some(d) => { return 0; }, None => { return 9; }, }`},
	// Leap-day parses structurally (no calendar validation) -> Some -> day 29.
	{"valid-leapday", `match (date_parse_iso("2024-02-29")) { Some(d) => { return d.day; }, None => { return 100; }, }`},
}

func timeParseIRSrc(mainBody string) string {
	return timeParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeParseIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostTimeParseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range timeParseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(timeParseIRSrc(tc.main))
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

// TestSelfHostTimeParseIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostTimeParseIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host time-parse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range timeParseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(timeParseIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "timeparse_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("time-parse wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
