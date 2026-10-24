package e2ecompiler

import "testing"

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
    let y: i32 = y_in;
    if (m <= 2) { y = y - 1; }
    let era: i32 = 0;
    if (y >= 0) { era = y / 400; } else { era = (y - 399) / 400; }
    let yoe: i32 = y - era * 400;
    let mp: i32 = 0;
    if (m > 2) { mp = m - 3; } else { mp = m + 9; }
    let doy: i32 = (153 * mp + 2) / 5 + d - 1;
    let doe: i32 = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + doe - 719468;
}
function civil_from_days(z_in: i32): Civil {
    let z: i32 = z_in + 719468;
    let era: i32 = 0;
    if (z >= 0) { era = z / 146097; } else { era = (z - 146096) / 146097; }
    let doe: i32 = z - era * 146097;
    let yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y: i32 = yoe + era * 400;
    let doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp: i32 = (5 * doy + 2) / 153;
    let d: i32 = doy - (153 * mp + 2) / 5 + 1;
    let m: i32 = 0;
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
    let z: i32 = days_from_civil(d.year, d.month, d.day);
    return civil_from_days(z + n);
}
function (d: Civil) days_since(other: Civil): i32 {
    let a: i32 = days_from_civil(d.year, d.month, d.day);
    let b: i32 = days_from_civil(other.year, other.month, other.day);
    return a - b;
}
function (d: Civil) weekday(): i32 {
    let z: i32 = days_from_civil(d.year, d.month, d.day);
    return ((z + 4) % 7 + 7) % 7;
}
function (d: Civil) day_of_year(): i32 {
    let m: i32 = d.month;
    let mp: i32 = 0;
    if (m > 2) { mp = m - 3; } else { mp = m + 9; }
    let doy: i32 = (153 * mp + 2) / 5 + d.day;
    if (m <= 2) { return doy - 306; }
    let add_for_leap: i32 = 0;
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
	{"weekday", `let d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.weekday();`},
	{"add-days-day", `let d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.add_days(20).day;`},
	{"add-days-cross-year-month", `let d: Civil = Civil { year: 2025, month: 12, day: 25 }; return d.add_days(10).month;`},
	{"add-days-cross-year-day", `let d: Civil = Civil { year: 2025, month: 12, day: 25 }; return d.add_days(10).day;`},
	{"days-since", `let a: Civil = Civil { year: 2026, month: 6, day: 13 }; let b: Civil = Civil { year: 2026, month: 6, day: 3 }; return a.days_since(b);`},
	{"day-of-year-leap", `let d: Civil = Civil { year: 2024, month: 3, day: 1 }; return d.day_of_year();`},
	{"is-valid-leap", `let d: Civil = Civil { year: 2024, month: 2, day: 29 }; if (d.is_valid()) { return 1; } return 0;`},
	{"is-valid-nonleap", `let d: Civil = Civil { year: 2023, month: 2, day: 29 }; if (!d.is_valid()) { return 7; } return 0;`},
	{"format-iso-len", `let d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.format_iso().len();`},
	{"format-iso-firstbyte", `let d: Civil = Civil { year: 2026, month: 6, day: 13 }; return d.format_iso()[0] as i32;`},
}

func timeDateIRSrc(mainBody string) string {
	return timeDateIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostTimeDateIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range timeDateIRCases {
		src := timeDateIRSrc(tc.main)
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
