package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// timeZonedIRCases exercise std/time's Zoned / TimeZone surface — fixed-offset
// zone construction, `in_zone`, `to_datetime` (wall-clock split), and an
// IANA-style `Option[TimeZone]` lookup — through the self-host IR path on
// x86-64 + wasm. This is the last std/time "self-host pending" piece (after the
// helpers, Date methods, date_parse_iso, RFC-3339, and Span/Duration).
//
// New ground vs the earlier std/time coverage: **nested structs** — a struct
// field that is itself a struct (`Zoned { instant: Instant, zone: TimeZone }`,
// `DateTime { date: Date, time: Time }`), built and read through two levels
// (`z.instant.sec`, `dt.time.hour`, `dt.date.day`). The bodies mirror std/time
// except the structs are renamed (`Date`/`Instant`/`Time`/`Zoned`/`TimeZone`/
// `DateTime` are reserved built-ins, E010) to
// `Civil`/`Moment`/`Clock`/`Zd`/`Tz`/`DT`. No imports are needed for the oracle
// (pure builtins + structs). Each case returns a value kept <= 126 and is
// oracle-checked against the interpreter. FEATURE-AUDIT std/time row.
const timeZonedIRPrelude = `struct Civil { year: i32, month: i32, day: i32 }
struct Clock { hour: i32, minute: i32, second: i32, nsec: i32 }
struct Moment { sec: i64, nsec: i32 }
struct Tz { name: string, offset_seconds: i32 }
struct Zd { instant: Moment, zone: Tz }
struct DT { date: Civil, time: Clock }
function civil_from_days(z_in: i32): Civil {
    var z: i32 = z_in + 719468; var era: i32 = 0; if (z >= 0) { era = z / 146097; } else { era = (z - 146096) / 146097; }
    var doe: i32 = z - era * 146097; var yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    var y: i32 = yoe + era * 400; var doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
    var mp: i32 = (5 * doy + 2) / 153; var d: i32 = doy - (153 * mp + 2) / 5 + 1; var m: i32 = 0;
    if (mp < 10) { m = mp + 3; } else { m = mp - 9; } if (m <= 2) { y = y + 1; }
    return Civil { year: y, month: m, day: d };
}
function in_zone(i: Moment, z: Tz): Zd { return Zd { instant: i, zone: z }; }
function to_datetime(z: Zd): DT {
    var spd: i64 = 86400 as i64;
    var local_sec: i64 = z.instant.sec + (z.zone.offset_seconds as i64);
    var days: i64 = local_sec / spd; var sec_in_day: i64 = local_sec - days * spd;
    if (sec_in_day < (0 as i64)) { sec_in_day = sec_in_day + spd; days = days - (1 as i64); }
    var d: Civil = civil_from_days(days as i32);
    var sid: i32 = sec_in_day as i32; var h: i32 = sid / 3600; var rem: i32 = sid - h * 3600;
    var mn: i32 = rem / 60; var s: i32 = rem - mn * 60;
    return DT { date: d, time: Clock { hour: h, minute: mn, second: s, nsec: z.instant.nsec } };
}
function timezone_iana(name: string): Option[Tz] {
    if (name == "UTC") { return Some(Tz { name: "UTC", offset_seconds: 0 }); }
    if (name == "Asia/Tokyo") { return Some(Tz { name: "UTC+09:00", offset_seconds: 32400 }); }
    if (name == "America/New_York") { return Some(Tz { name: "UTC-05:00", offset_seconds: 0 - 18000 }); }
    return None;
}
`

var timeZonedIRCases = []struct {
	name string
	main string
}{
	// to_datetime wall-clock hour at +09:00 (Tokyo). 1718281496 UTC is
	// 2024-06-13T12:24:56Z; +9h -> 21:24 local. hour = 21.
	{"to-datetime-hour", `var m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; var zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 32400 }); return to_datetime(zd).time.hour;`},
	// Same instant, nested date field readout: day rolls to 13 (still 13 here).
	{"to-datetime-day", `var m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; var zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 32400 }); return to_datetime(zd).date.day;`},
	// Negative offset (-05:00) pulls wall-clock back across midnight: 12:24Z -> 07:24. hour = 7.
	{"to-datetime-neg-offset", `var m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; var zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 0 - 18000 }); return to_datetime(zd).time.hour;`},
	// in_zone preserves the instant: read it back through the nested field.
	{"in-zone-roundtrip", `var m: Moment = Moment { sec: 90 as i64, nsec: 0 }; var zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 0 }); return zd.instant.sec as i32;`},
	// timezone_iana hit -> Some; read the nested offset (Tokyo = 32400 / 3600 = 9).
	{"iana-hit-offset", `match (timezone_iana("Asia/Tokyo")) { Some(z) => { return z.offset_seconds / 3600; }, None => { return 100; }, }`},
	// timezone_iana UTC -> Some; offset 0.
	{"iana-utc", `match (timezone_iana("UTC")) { Some(z) => { return z.offset_seconds + 5; }, None => { return 100; }, }`},
	// timezone_iana miss -> None -> sentinel 7.
	{"iana-miss", `match (timezone_iana("Mars/Olympus")) { Some(z) => { return 0; }, None => { return 7; }, }`},
	// Compose: look up a zone, then use it through in_zone + to_datetime.
	{"iana-then-datetime", `match (timezone_iana("Asia/Tokyo")) { Some(z) => { var m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; return to_datetime(in_zone(m, z)).time.hour; }, None => { return 100; }, }`},
}

func timeZonedIRSrc(mainBody string) string {
	return timeZonedIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeZonedIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostTimeZonedIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range timeZonedIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(timeZonedIRSrc(tc.main))
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

// TestSelfHostTimeZonedIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostTimeZonedIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host time-zoned wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range timeZonedIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(timeZonedIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "timezoned_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("time-zoned wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
