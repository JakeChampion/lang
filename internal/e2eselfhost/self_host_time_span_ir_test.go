package e2eselfhost

import "testing"

// timeSpanIRCases exercise std/time's calendar/absolute arithmetic — `add_span`
// (Date + Span, calendar add with month-end clamp), `add_duration` /
// `duration_since` (Instant ± Duration, i64 + nsec carry/borrow), and
// `days_until` (returns a Span) — through the self-host IR path on x86-64 +
// wasm. This closes the std/time "self-host pending" row (after the pure-i32
// helpers, Date methods, date_parse_iso, and RFC-3339).
//
// New ground vs the earlier std/time coverage: an **8-field struct passed by
// value as a parameter** (`add_span(s: Span)`) and a Duration struct combining
// an i64 `sec` with an i32 `nsec` carry/borrow. The bodies are std/time's
// verbatim except the structs are renamed (`Date`/`Span`/`Duration`/`Instant`
// are reserved built-ins, E010) to `Civil`/`Sp`/`Dur`/`Moment`. No imports are
// needed for the oracle (pure builtins + structs). Each case returns a value
// kept <= 126 and is oracle-checked against the interpreter. FEATURE-AUDIT
// std/time row.
const timeSpanIRPrelude = `struct Civil { year: i32, month: i32, day: i32 }
struct Sp { years: i32, months: i32, weeks: i32, days: i32, hours: i32, minutes: i32, seconds: i32, nanos: i32 }
struct Dur { sec: i64, nsec: i32 }
struct Moment { sec: i64, nsec: i32 }
function is_leap_year(y: i32): boolean { if (y % 4 != 0) { return false; } if (y % 100 != 0) { return true; } return y % 400 == 0; }
function days_in_month(y: i32, m: i32): i32 {
    if (m == 1) { return 31; } if (m == 2) { if (is_leap_year(y)) { return 29; } return 28; }
    if (m == 3) { return 31; } if (m == 4) { return 30; } if (m == 5) { return 31; } if (m == 6) { return 30; }
    if (m == 7) { return 31; } if (m == 8) { return 31; } if (m == 9) { return 30; } if (m == 10) { return 31; }
    if (m == 11) { return 30; } if (m == 12) { return 31; } return 0;
}
function days_from_civil(y_in: i32, m: i32, d: i32): i32 {
    let y: i32 = y_in; if (m <= 2) { y = y - 1; }
    let era: i32 = 0; if (y >= 0) { era = y / 400; } else { era = (y - 399) / 400; }
    let yoe: i32 = y - era * 400; let mp: i32 = 0; if (m > 2) { mp = m - 3; } else { mp = m + 9; }
    let doy: i32 = (153 * mp + 2) / 5 + d - 1; let doe: i32 = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + doe - 719468;
}
function civil_from_days(z_in: i32): Civil {
    let z: i32 = z_in + 719468; let era: i32 = 0; if (z >= 0) { era = z / 146097; } else { era = (z - 146096) / 146097; }
    let doe: i32 = z - era * 146097; let yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y: i32 = yoe + era * 400; let doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp: i32 = (5 * doy + 2) / 153; let d: i32 = doy - (153 * mp + 2) / 5 + 1; let m: i32 = 0;
    if (mp < 10) { m = mp + 3; } else { m = mp - 9; } if (m <= 2) { y = y + 1; }
    return Civil { year: y, month: m, day: d };
}
function (d: Civil) add_days(n: i32): Civil { let z: i32 = days_from_civil(d.year, d.month, d.day); return civil_from_days(z + n); }
function (d: Civil) days_since(other: Civil): i32 {
    let a: i32 = days_from_civil(d.year, d.month, d.day);
    let b: i32 = days_from_civil(other.year, other.month, other.day);
    return a - b;
}
function span_days(n: i32): Sp { return Sp { years: 0, months: 0, weeks: 0, days: n, hours: 0, minutes: 0, seconds: 0, nanos: 0 }; }
function span_weeks(n: i32): Sp { return Sp { years: 0, months: 0, weeks: n, days: 0, hours: 0, minutes: 0, seconds: 0, nanos: 0 }; }
function span_months(n: i32): Sp { return Sp { years: 0, months: n, weeks: 0, days: 0, hours: 0, minutes: 0, seconds: 0, nanos: 0 }; }
function (d: Civil) add_span(s: Sp): Civil {
    let serial_month: i32 = d.year * 12 + (d.month - 1) + s.years * 12 + s.months;
    let new_year: i32 = 0; let new_month_zero: i32 = 0;
    if (serial_month >= 0) { new_year = serial_month / 12; new_month_zero = serial_month - new_year * 12; }
    else { new_year = (serial_month - 11) / 12; new_month_zero = serial_month - new_year * 12; }
    let new_month: i32 = new_month_zero + 1;
    let max_day: i32 = days_in_month(new_year, new_month);
    let clamped_day: i32 = d.day; if (clamped_day > max_day) { clamped_day = max_day; }
    let anchor: Civil = Civil { year: new_year, month: new_month, day: clamped_day };
    let total_days: i32 = s.weeks * 7 + s.days; if (total_days == 0) { return anchor; }
    return anchor.add_days(total_days);
}
function (d: Civil) days_until(other: Civil): Sp { let delta: i32 = other.days_since(d); return span_days(delta); }
function (i: Moment) add_duration(d: Dur): Moment {
    let sec: i64 = i.sec + d.sec; let nsec: i32 = i.nsec + d.nsec; let nps: i32 = 1000000000;
    if (nsec >= nps) { nsec = nsec - nps; sec = sec + (1 as i64); }
    else if (nsec < 0) { nsec = nsec + nps; sec = sec - (1 as i64); }
    return Moment { sec: sec, nsec: nsec };
}
function (i: Moment) duration_since(other: Moment): Dur {
    let sec: i64 = i.sec - other.sec; let nsec: i32 = i.nsec - other.nsec; let nps: i32 = 1000000000;
    if (nsec < 0) { nsec = nsec + nps; sec = sec - (1 as i64); }
    else if (nsec >= nps) { nsec = nsec - nps; sec = sec + (1 as i64); }
    return Dur { sec: sec, nsec: nsec };
}
`

var timeSpanIRCases = []struct {
	name string
	main string
}{
	// add_span month rollover with month-end clamp: Jan 31 + 1 month -> Feb 29
	// (2024 leap). month*2 + day = 2*2 + 29 = 33. (8-field struct by-value param.)
	{"add-span-month-clamp", `let d: Civil = Civil { year: 2024, month: 1, day: 31 }; let e: Civil = d.add_span(span_months(1)); return e.month * 2 + e.day;`},
	// add_span weeks: 2024-03-30 + 1 week -> 2024-04-06. month*10 + day = 46.
	{"add-span-weeks", `let d: Civil = Civil { year: 2024, month: 3, day: 30 }; let e: Civil = d.add_span(span_weeks(1)); return e.month * 10 + e.day;`},
	// add_duration with nsec carry: 999999999 + 2 -> sec+1, nsec=1. return sec (11).
	{"add-duration-carry", `let m: Moment = Moment { sec: 10 as i64, nsec: 999999999 }; let r: Moment = m.add_duration(Dur { sec: 0 as i64, nsec: 2 }); return r.sec as i32;`},
	// add_duration carry, read nsec: -> 1.
	{"add-duration-carry-nsec", `let m: Moment = Moment { sec: 10 as i64, nsec: 999999999 }; let r: Moment = m.add_duration(Dur { sec: 0 as i64, nsec: 2 }); return r.nsec;`},
	// duration_since: 100s - 40s = 60s. return sec (60).
	{"duration-since-sec", `let a: Moment = Moment { sec: 100 as i64, nsec: 0 }; let b: Moment = Moment { sec: 40 as i64, nsec: 0 }; return a.duration_since(b).sec as i32;`},
	// duration_since with nsec borrow: 5.000000000 - 1.000000500 -> 3s + (1e9-500)ns.
	// return sec (3) after the borrow.
	{"duration-since-borrow", `let a: Moment = Moment { sec: 5 as i64, nsec: 0 }; let b: Moment = Moment { sec: 1 as i64, nsec: 500 }; return a.duration_since(b).sec as i32;`},
	// days_until returns a Span carrying the day delta; read .days (10).
	{"days-until", `let a: Civil = Civil { year: 2024, month: 1, day: 1 }; let b: Civil = Civil { year: 2024, month: 1, day: 11 }; return a.days_until(b).days;`},
}

func timeSpanIRSrc(mainBody string) string {
	return timeSpanIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTimeSpanIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostTimeSpanIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range timeSpanIRCases {
		src := timeSpanIRSrc(tc.main)
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
