package e2ecompiler

import "testing"

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
    let z: i32 = z_in + 719468; let era: i32 = 0; if (z >= 0) { era = z / 146097; } else { era = (z - 146096) / 146097; }
    let doe: i32 = z - era * 146097; let yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y: i32 = yoe + era * 400; let doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp: i32 = (5 * doy + 2) / 153; let d: i32 = doy - (153 * mp + 2) / 5 + 1; let m: i32 = 0;
    if (mp < 10) { m = mp + 3; } else { m = mp - 9; } if (m <= 2) { y = y + 1; }
    return Civil { year: y, month: m, day: d };
}
function in_zone(i: Moment, z: Tz): Zd { return Zd { instant: i, zone: z }; }
function to_datetime(z: Zd): DT {
    let spd: i64 = 86400 as i64;
    let local_sec: i64 = z.instant.sec + (z.zone.offset_seconds as i64);
    let days: i64 = local_sec / spd; let sec_in_day: i64 = local_sec - days * spd;
    if (sec_in_day < (0 as i64)) { sec_in_day = sec_in_day + spd; days = days - (1 as i64); }
    let d: Civil = civil_from_days(days as i32);
    let sid: i32 = sec_in_day as i32; let h: i32 = sid / 3600; let rem: i32 = sid - h * 3600;
    let mn: i32 = rem / 60; let s: i32 = rem - mn * 60;
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
	{"to-datetime-hour", `let m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; let zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 32400 }); return to_datetime(zd).time.hour;`},
	// Same instant, nested date field readout: day rolls to 13 (still 13 here).
	{"to-datetime-day", `let m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; let zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 32400 }); return to_datetime(zd).date.day;`},
	// Negative offset (-05:00) pulls wall-clock back across midnight: 12:24Z -> 07:24. hour = 7.
	{"to-datetime-neg-offset", `let m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; let zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 0 - 18000 }); return to_datetime(zd).time.hour;`},
	// in_zone preserves the instant: read it back through the nested field.
	{"in-zone-roundtrip", `let m: Moment = Moment { sec: 90 as i64, nsec: 0 }; let zd: Zd = in_zone(m, Tz { name: "x", offset_seconds: 0 }); return zd.instant.sec as i32;`},
	// timezone_iana hit -> Some; read the nested offset (Tokyo = 32400 / 3600 = 9).
	{"iana-hit-offset", `match (timezone_iana("Asia/Tokyo")) { Some(z) => { return z.offset_seconds / 3600; }, None => { return 100; }, }`},
	// timezone_iana UTC -> Some; offset 0.
	{"iana-utc", `match (timezone_iana("UTC")) { Some(z) => { return z.offset_seconds + 5; }, None => { return 100; }, }`},
	// timezone_iana miss -> None -> sentinel 7.
	{"iana-miss", `match (timezone_iana("Mars/Olympus")) { Some(z) => { return 0; }, None => { return 7; }, }`},
	// Compose: look up a zone, then use it through in_zone + to_datetime.
	{"iana-then-datetime", `match (timezone_iana("Asia/Tokyo")) { Some(z) => { let m: Moment = Moment { sec: 1718281496 as i64, nsec: 0 }; return to_datetime(in_zone(m, z)).time.hour; }, None => { return 100; }, }`},
}

func timeZonedIRSrc(mainBody string) string {
	return timeZonedIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeZonedIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostTimeZonedIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range timeZonedIRCases {
		src := timeZonedIRSrc(tc.main)
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
