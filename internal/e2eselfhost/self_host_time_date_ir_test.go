package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// timeDateIRCases exercise std/time's civil-date arithmetic — Howard Hinnant's
// days_from_civil / civil_from_days plus the Date receiver methods
// (is_valid / add_days / days_since / weekday / day_of_year / format_iso) —
// through the self-host IR path on x86-64 + wasm. This is the "Date methods
// (structs)" piece of the std/time "self-host pending" audit gap (after the
// pure-i32 is_leap_year / days_in_month helpers in TestSelfHostTimeIR).
//
// The std/time bodies are inlined verbatim except the struct is named `Civil`
// rather than `Date` (the built-in `Date` name is reserved, E010) and
// `int.int_to_string(n)` is written `n.to_string()` (a self-host builtin). The
// coverage that matters is structural: struct construction + field access, a
// struct-returning function (civil_from_days), receiver methods on a struct,
// and `.to_string()` + string concat — all routed through the IR path.
//
// `import "std/i32"` lets the native interpreter oracle resolve `.to_string()`
// (a std/i32 method natively); the self-host single-program driver ignores the
// import and treats it as a builtin, still taking the IR path. Each case
// returns a value kept <= 126 and is oracle-checked against the interpreter.
// FEATURE-AUDIT std/time row.
const timeDateIRPrelude = `import "std/i32";
struct Civil { year: i32, month: i32, day: i32 }
function is_leap_year(y: i32): boolean {
    if (y % 4 != 0) { return false; }
    if (y % 100 != 0) { return true; }
    return y % 400 == 0;
}
function days_in_month(y: i32, m: i32): i32 {
    if (m == 1) { return 31; }
    if (m == 2) { if (is_leap_year(y)) { return 29; } return 28; }
    if (m == 3) { return 31; }
    if (m == 4) { return 30; }
    if (m == 5) { return 31; }
    if (m == 6) { return 30; }
    if (m == 7) { return 31; }
    if (m == 8) { return 31; }
    if (m == 9) { return 30; }
    if (m == 10) { return 31; }
    if (m == 11) { return 30; }
    if (m == 12) { return 31; }
    return 0;
}
function days_from_civil(y_in: i32, m: i32, d: i32): i32 {
    var y: i32 = y_in;
    if (m <= 2) { y = y - 1; }
    var era: i32 = 0;
    if (y >= 0) { era = y / 400; } else { era = (y - 399) / 400; }
    var yoe: i32 = y - era * 400;
    var mp: i32 = 0;
    if (m > 2) { mp = m - 3; } else { mp = m + 9; }
    var doy: i32 = (153 * mp + 2) / 5 + d - 1;
    var doe: i32 = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + doe - 719468;
}
function civil_from_days(z_in: i32): Civil {
    var z: i32 = z_in + 719468;
    var era: i32 = 0;
    if (z >= 0) { era = z / 146097; } else { era = (z - 146096) / 146097; }
    var doe: i32 = z - era * 146097;
    var yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    var y: i32 = yoe + era * 400;
    var doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
    var mp: i32 = (5 * doy + 2) / 153;
    var d: i32 = doy - (153 * mp + 2) / 5 + 1;
    var m: i32 = 0;
    if (mp < 10) { m = mp + 3; } else { m = mp - 9; }
    if (m <= 2) { y = y + 1; }
    return Civil { year: y, month: m, day: d };
}
function (d: Civil) is_valid(): boolean {
    if (d.month < 1) { return false; }
    if (d.month > 12) { return false; }
    if (d.day < 1) { return false; }
    return d.day <= days_in_month(d.year, d.month);
}
function (d: Civil) add_days(n: i32): Civil {
    var z: i32 = days_from_civil(d.year, d.month, d.day);
    return civil_from_days(z + n);
}
function (d: Civil) days_since(other: Civil): i32 {
    var a: i32 = days_from_civil(d.year, d.month, d.day);
    var b: i32 = days_from_civil(other.year, other.month, other.day);
    return a - b;
}
function (d: Civil) weekday(): i32 {
    var z: i32 = days_from_civil(d.year, d.month, d.day);
    return ((z + 4) % 7 + 7) % 7;
}
function (d: Civil) day_of_year(): i32 {
    var m: i32 = d.month;
    var mp: i32 = 0;
    if (m > 2) { mp = m - 3; } else { mp = m + 9; }
    var doy: i32 = (153 * mp + 2) / 5 + d.day;
    if (m <= 2) { return doy - 306; }
    var add_for_leap: i32 = 0;
    if (is_leap_year(d.year)) { add_for_leap = 1; }
    return doy + 59 + add_for_leap;
}
function pad2(n: i32): string {
    if (n < 10) { return "0" + n.to_string(); }
    return n.to_string();
}
function pad4(n: i32): string {
    if (n < 10) { return "000" + n.to_string(); }
    if (n < 100) { return "00" + n.to_string(); }
    if (n < 1000) { return "0" + n.to_string(); }
    return n.to_string();
}
function (d: Civil) format_iso(): string {
    return pad4(d.year) + "-" + pad2(d.month) + "-" + pad2(d.day);
}
`

var timeDateIRCases = []struct {
	name string
	main string
}{
	// weekday of a known date (0..6, Sunday=0).
	{"weekday", `var d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.weekday();`},
	// add_days within a month: 2026-06-13 + 20 = 2026-07-03 -> day 3.
	{"add-days-day", `var d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.add_days(20).day;`},
	// add_days crossing a year boundary: 2025-12-25 + 10 = 2026-01-04 -> month 1.
	{"add-days-cross-year-month", `var d: Civil = Civil { year: 2025, month: 12, day: 25 }; return d.add_days(10).month;`},
	{"add-days-cross-year-day", `var d: Civil = Civil { year: 2025, month: 12, day: 25 }; return d.add_days(10).day;`},
	// days_since: two Date structs subtracted -> 10.
	{"days-since", `var a: Civil = Civil { year: 2026, month: 6, day: 13 }; var b: Civil = Civil { year: 2026, month: 6, day: 3 }; return a.days_since(b);`},
	// day_of_year: 2024-03-01 in a leap year -> 61.
	{"day-of-year-leap", `var d: Civil = Civil { year: 2024, month: 3, day: 1 }; return d.day_of_year();`},
	// is_valid true (leap Feb 29) -> 1.
	{"is-valid-leap", `var d: Civil = Civil { year: 2024, month: 2, day: 29 }; if (d.is_valid()) { return 1; } return 0;`},
	// is_valid false (non-leap Feb 29) -> sentinel 7.
	{"is-valid-nonleap", `var d: Civil = Civil { year: 2023, month: 2, day: 29 }; if (!d.is_valid()) { return 7; } return 0;`},
	// format_iso length: "2026-06-13" -> 10.
	{"format-iso-len", `var d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.format_iso().len();`},
	// format_iso first byte: '2' (50).
	{"format-iso-firstbyte", `var d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.format_iso()[0] as i32;`},
}

func timeDateIRSrc(mainBody string) string {
	return timeDateIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeDateIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostTimeDateIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range timeDateIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(timeDateIRSrc(tc.main))
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

// TestSelfHostTimeDateIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostTimeDateIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host time-date wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range timeDateIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(timeDateIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "timedate_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("time-date wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
