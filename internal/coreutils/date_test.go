package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// date(1) is one instant and one format, so the corpus is the grammar
// behind `-d` — every item kind, the fixed and local zone words, the
// relative words, the `@` form, the `TZ="…"` prefix, the gaps and
// repeated hours a real zone has — the strftime conversions with every
// flag and width, the option faults, the `-f` batch, and the `--debug`
// commentary, which is compared line for line.
//
// The harness pins TZ=UTC; the zone-sensitive cases pass their own.
// Nothing here reads the clock: a bare `date` cannot be compared
// across two runs, and every valid `-s STRING` or `MMDDhhmm` operand
// would SET the clock when the suite runs as root, so only invalid
// spellings of those appear.

func init() {
	registerCorpus("date", dateCases)
}

// dateTree seeds a batch file, a reference file with a fixed
// modification time, and a directory.
func dateTree(t *testing.T, dir string) {
	t.Helper()
	lines := "2024-01-01\nfoo\n\n2024-02-02 12:00\n@0\n  2024-03-03T04:05:06Z  \n"
	if err := os.WriteFile(filepath.Join(dir, "lines"), []byte(lines), 0o644); err != nil {
		t.Fatalf("write lines: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unterminated"), []byte("2024-01-01"), 0o644); err != nil {
		t.Fatalf("write unterminated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty"), nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ref"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write ref: %v", err)
	}
	stamp := time.Unix(1718434196, 123456789)
	if err := os.Chtimes(filepath.Join(dir, "ref"), stamp, stamp); err != nil {
		t.Fatalf("chtimes ref: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir d: %v", err)
	}
	if err := os.Symlink("ref", filepath.Join(dir, "lref")); err != nil {
		t.Fatalf("symlink lref: %v", err)
	}
}

func dateCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }
	// d runs `-d STR` under the pinned UTC with a fixed format.
	d := func(str string) {
		add(invocation{name: "-d " + str, args: []string{"-d", str, "+%F %T %Z %z %s %N"}})
	}
	// dn runs a string that starts from the clock: the two runs are
	// moments apart, so only the day and the zone are compared.
	dn := func(str string) {
		add(invocation{name: "-d " + str, args: []string{"-d", str, "+%F %Z %z"}})
	}
	// zoned runs `-d STR` under TZ=zone.
	zoned := func(zone, str string) {
		add(invocation{name: zone + " -d " + str, args: []string{"-d", str, "+%F %T %Z %z %s"}, env: []string{"TZ=" + zone}})
	}
	// dbg compares the --debug commentary too, so its strings name a
	// day: one that starts from the clock prints the current second,
	// and two runs a moment apart straddle one.
	dbg := func(zone, str string) {
		add(invocation{name: "debug " + zone + " -d " + str, args: []string{"--debug", "-d", str, "+%F %T %Z"}, env: []string{"TZ=" + zone}})
	}
	// fmt renders one fixed instant with FORMAT.
	fmtc := func(format string) {
		add(invocation{name: "format " + format, args: []string{"-d", "2024-06-15 12:34:56.123456789", "+" + format}})
	}

	// ---- the grammar, in UTC: strings that name their day ----
	for _, s := range []string{
		"2024-06-15", "2024-6-5", "24-05-03", "5-06-15", "0024-06-15", "99999-01-01", "2000000-01-01",
		"2024-01-01T12", "2024-01-01T12:00", "2024-01-01T12+0100", "2024-01-01 T 12:00",
		"2024-01-01T12:00:30.25-0130", "2024-01-01T12:00:30 +01", "2024-01-01T1200", "2024-06-15T12:00:00Z",
		"2024-06-15T12:00:00.5Z", "20240615T120000Z", "20240615T1200", "20240615 12:00", "20240615 1200",
		"20240615 +1200", "20240615 +1 day", "20240615 1 day", "jun 15, 2024", "15 june 2024 pm", "15 june 24",
		"15 june 0024", "june 15 24", "june 15 -24", "june -15 -24", "-15 june -24", "15-june-24", "15 june-24",
		"17-JUN-1992", "JUN-17-1992", "JUN+17+1992", "2024+05+03", "2024/06/15", "06/15/2024", "06/15/24",
		"99999/1/1", "15/6/2024", "5:04:03.5 +0100", "12 +0100", "12:00 +2359", "12:00 +2400", "12:00 -2359",
		"12:00 +2401", "20240615", "2024061512", "202406151234",
		"1234", "12345", "123456", "9999999999999", "2024-06-15 5", "2024-06-15 12", "12:00 2024",
		"2024-06-15 99999", "2024-06-15 12 99", "jun 15 2024 3", "2024-06-15 12:00:00 000", "2024-06-15 12:00 007",
		"2024-06-15 12:00 202", "2024-06-15 12:00 2024 12:00", "2024 2024", "2024-13-01", "2024-02-30",
		"2024-02-29", "2023-02-29", "1700-02-29", "1600-02-29", "@1.5", "@-1.5", "@1 tomorrow", "@ 17",
		"@-0.000000001", "@0", "@-1", "@9223372036854775807", "@99999999999999999999", "2024-01-01 (unterminated",
		"2024-01-01 (a (b) c) 12:00", "2024-06-15 UTC +0100", "2024-06-15 12:00 +0100 UTC",
		"2024-06-15 12:00 +0100 +0200", "2024-06-15 12:00 +1 day", "2024-06-15 -1 day 12:00",
		"12:00 2024-06-15 -1 day", "2024-06-15 12:00 -00 +1 min", "2024-06-15 12:00 UTC DST",
		"2024-06-15 12:00 UTC DST tomorrow", "2024-06-15 12:00 GMT+2", "2024-06-15 12:00 GMT -2",
		"2024-06-15 12:00 GMT-02:30", "2024-06-15 12:00 GMT +2 days", "2024-06-15 12:00 GMT 2 days",
		"2024-06-15 12:00 +0100 DST", "2024-06-15 12:00 DST", "2024-06-15 12:00 T", "2024-06-15 12:00 T +1 day",
		"2024-06-15 T12:00", "2024-06-15T 12:00", "2024-06-15t12:00", "2024-06-15 12:00 X", "2024-06-15 fri",
		"2024-06-15 friday 12:00", "2024-06-15 sat", "2024-06-15 next sat", "2024-06-15 last sat",
		"2024-06-15 this sat", "2024-06-15 12:00 sat", "2024-06-15 sat 12:00", "2024-06-15 2 sat",
		"sat 2024-06-15", "sat 12:00 2024-06-15", "2024-06-15 12 pm", "2024-06-15 12pm", "2024-06-15 12:30pm",
		"2024-06-15 12:30:15.5pm", "2024-06-15 0:30am", "2024-06-15 11:59 pm", "2024-06-15 12:00 pm +0100",
		"2024-06-15 12:00 +0100 pm", "2024-06-15 12:00:00 +0100 tomorrow yesterday",
		"2024-06-15 12:00:00.123456789", "2024-06-15 12:00:00.1234567891", "2024-06-15 12:00:00.5 -0.5 seconds",
		"2024-06-15 12:00:00.5 -0.6 seconds", "2024-06-15 12:00:00.5 +0.6 seconds",
		"2024-06-15 12:00:00 -1 second", "2024-06-15 12:00:00 1 second ago", "2024-06-15 12:00:00 -1 second ago",
		"2024-01-31 +1 month", "2024-03-31 -1 month", "2024-02-29 +1 year", "2024-02-29 1 year ago",
		"2024-06-15 12:00 +1 month -1 day", "2024-06-15 12:00 next month", "2024-06-15 12:00 last year",
		"2024-06-15 12:00 12 hours", "2024-06-15 12:00 25 hours", "2024-06-15 12:00 +36 hours ago",
		"2024-06-15 12:00 -0 hours", "2024-06-15 12:00 +2 days ago", "2024-06-15 12:00:00 +1 hour +1 min +1 sec",
		"1969-12-31 23:59:59", "1969-12-31 23:59:59.5", "1901-12-13 20:45:52", "1800-01-01", "0001-01-01",
		"0000-01-01", "1-1-1", "0-1-1", "-1-1-1", "1970-01-01 00:00:00 -1 second",
		"1970-01-01 00:00:00.000000001 -1 second", "1970-01-01 00:00:00 -0.5 second", "9999-12-31 23:59:59",
		"10000-01-01", "2038-01-19 03:14:08", "2106-02-07 06:28:16", "1000000000000000000 years",
		"2024-06-15 9223372036854775807 days", "2024-06-15 12:00:00 +99999999999999 hours",
		"2024-06-15 12:00:00 999999999999 minutes", "2024-06-15 12:00:00 9999999999999999 seconds",
		"2024-06-15 12:00:00 9223372036854775807 seconds", "2024-06-15 12:00:00 99999999 months",
		"2024-06-15 12:00:00 2147483647 months", "2024-06-15 12:00:00 2147483647 years",
		"2024-06-15 12:00:00 2147481700 years", "2024-06-15 12:00:00 -2147481700 years", "2147483647-01-01",
		"2147483648-01-01", "2147485547-01-01", "2147485548-01-01", "-2147481748-01-01",
		"2024-06-15 12:00:00 2147483647 days", "2024-06-15 12:00:00 2147483648 days",
		"2024-06-15 12:00:00 -2147483649 days", "2024-06-15 12:00:00 4294967296 hours", "99999999999999999999",
		"9223372036854775807", "9223372036854775808", "TZ=\"Asia/Tokyo\" 12:00 2024-06-15",
		"TZ=\"Asia/Tokyo\" 2024-06-15", "TZ=\"EST5\" 2024-06-15 12:00", "TZ=\"a\\\"b\" 2024-06-15 12:00",
		"TZ=\"\" 2024-06-15 12:00", "  TZ=\"Asia/Tokyo\"   2024-06-15 12:00",
		"TZ=\"America/New_York\" 2024-03-10 02:30", "TZ=\"America/New_York\" 2024-11-03 01:30",
		"TZ=\"America/New_York\" 2024-03-09 02:30 tomorrow", "TZ=\"America/New_York\" 2024-06-15 12:00 EST",
		"TZ=\"America/New_York\" 2024-06-15 12:00 EDT", "TZ=\":America/New_York\" 2024-06-15 12:00",
		"TZ=\"Not/AZone\" 2024-06-15 12:00", "TZ=\"XXX-5:30\" 2024-06-15 12:00", "TZ=\"<-03>3\" 2024-06-15 12:00",
		"2024-06-15 12:00 foo", "jan 1 2024 foo", "2024-06-15 ;", "2024-06-15\n12:00", "2024-06-15\t12:00",
		"2024-06-15 \xff",
		// The European dotted date, and the decimals it has to stay
		// distinct from: the lexer decides between them by whether a
		// second '.' turns up where the fraction would be.
		"1.2.3", "31.12.99", "1.2.2024", "01.02.2024", "13.13.13", "29.2.24", "2024.06.15",
		"1.2", "1.2.3.4", "5.6.7890", "17.6.", "17.6", "1.2.3 4:5:6", "1,2,3", "1.2,3",
		"1.234567890.5", "1.23456789.5", "0.0.0", "1.2.3 UTC", "12.12.12 12:12:12",
		"1.2.3.", ".1.2", "1..2", "1.2.3 bogus", "1.2.3 +1 day", "@1.2.3",
		// The most negative epoch second there is, which the lexer only
		// holds because it accumulates the sign with each digit.
		"@-9223372036854775808", "@-9223372036854775807", "@-99999999999999999999",
	} {
		d(s)
	}
	// ---- and strings that start from the clock, compared by day ----
	for _, s := range []string{
		"-0.0000000001 seconds", "1.9999999999 seconds", "+99999999 days", "", " ", "0", "now", "today", "tomorrow", "yesterday", "- tomorrow", "-tomorrow", "- -1 day",
		"1 jan 12:00", "jan 1 12:00", "june", "jan 1", "jun 15,", "june 15, 24", "15 -june 24", "sept 5", "thur",
		"wednes", "mon.", "monday,", "monday, 12:00", "mondays", "jan.", "janu", "janua.", "ja", "6/15", "5:4:3",
		"5:4", "25:00", "24:00", "23:60", "12:00:60", "5:04:03.5", "5:04:03,5", "5:04:03.5 pm", "3 a.m.", "3 A.M.",
		"a.m.", "12 am", "0 am", "0 pm", "12 pm", "13 pm", "a. m.", "p.m", "am.", "a.m", "12:00 +5", "12:00 +5:30",
		"12:00 -0:30", "12:00 -00:30", "12:00 +530", "12:00 +05:30 tomorrow", "12:00 +23:60", "12:30 -5 days",
		"+ 12:00", "EST5", "EST +5", "EST -1", "EST DST", "EST5EDT", "PST8PDT", "GMT+1", "GMT +1", "GMT-1",
		"UTC+1", "UTC +1:30", "T", "A", "J", "Z", "12:00 J", "J J", "J DST", "J +1", "b", "y", "n", "m", "k", "i",
		"x", "zulu", "e.s.t", "e.s.t.", "u.t.c", "d.s.t", "BST", "WEST", "CEST", "MESZ", "MEZ", "IST", "NST",
		"NDT", "HADT", "GST", "SST", "AKDT", "CLST", "BRST", "ADT", "HAST", "WAT", "SAST", "EAT", "CAT", "EET",
		"EEST", "MSK", "MSD", "SGT", "KST", "JST", "NZST", "NZDT", "MET", "MEST", "ART", "BRT", "CLT", "UT", "AST",
		"MST", "MDT", "HST", "CST", "CDT", "PDT", "AKST", "WET", "CET", "GMT", "1 fortnight ago", "next fortnight",
		"this week", "third monday", "3 monday", "0 monday", "last monday", "first monday", "second monday",
		"twelfth", "last", "hence", "2 hours hence", "year", "years", "sec", "secs", "mins", "min",
		"1 year 2 months 3 days 4 hours 5 minutes 6 seconds ago", "1 year ago 2 days", "-1 year ago", "+1 year",
		"-5", "10.5 seconds", "-10.5 seconds", "-0.5 seconds", "99", "999", "yesterday 3", "tomorrow tomorrow",
		"1.", "1. 2 days", "1.2.3", "TZ=\"Asia/Tokyo\" ", "TZ=\"a\\b\" 12:00", "TZ=\"unterminated", "foo", "x y",
		"12 foo", "=", "%", "\xff", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"tomorrowtomorrowtomorrowtomorrowtomorrowtomorrowtomorrow",
	} {
		dn(s)
	}

	// ---- the local zone's own words, gaps and repeated hours ----
	for _, zone := range []string{"America/New_York", "Australia/Sydney", "Europe/Berlin", "Asia/Kolkata", "America/Whitehorse", "EST5EDT,M3.2.0/2,M11.1.0/2", "UTC0", "XXX-5:30"} {
		for _, s := range []string{
			"2024-06-15 12:00", "2024-01-15 12:00", "2024-06-15 12:00 EST", "2024-06-15 12:00 EDT", "2024-01-15 12:00 EST",
			"2024-01-15 12:00 EDT", "2024-01-15 12:00 EDT +1 day", "2024-06-15 12:00 EST DST", "2024-06-15 12:00 EDT DST",
			"2024-01-15 12:00 EST DST", "2024-06-15 12:00 AEST", "2024-06-15 12:00 AEDT", "2024-01-15 12:00 AEDT",
			"2024-06-15 12:00 CET", "2024-06-15 12:00 CEST", "2024-06-15 12:00 IST", "2024-06-15 12:00 IST DST",
			"2024-06-15 12:00 MST", "2024-06-15 12:00 MST DST", "2016-06-15 12:00 MST DST", "2016-06-15 12:00 PDT",
			"2016-06-15 12:00 PST", "2024-06-15 12:00 UTC", "2024-06-15 12:00 UTC DST", "2024-06-15 12:00 J", "2024-01-15 12:00 J",
			"2024-03-10 02:30", "2024-03-10 02:30 EST", "2024-03-10 02:30 -0500", "2024-03-10 02:30 +0100", "2024-03-10 02:30 UTC",
			"2024-03-10 02:30 -0500 +1 day", "2024-11-03 01:30", "2024-11-03 01:30 EDT", "2024-11-03 01:30 EST",
			"2024-11-03 01:30 -0400", "2024-03-09 02:30 +1 day", "2024-03-09 02:30 tomorrow", "2024-03-09 02:30 next day",
			"2024-03-11 02:30 yesterday", "2024-11-02 01:30 tomorrow", "2024-11-04 01:30 yesterday", "2024-11-03 01:30 EDT tomorrow",
			"2024-03-10 monday", "2024-03-09 12:00 next monday", "2024-03-09 02:30 next sunday", "2024-03-03 02:30 next sunday",
			"2024-11-03 01:30 next sunday", "2024-10-27 01:30 next sunday", "2024-11-03 EST", "2024-03-31 02:30",
			"2024-03-31 02:30 +1 day", "2024-10-27 02:30", "2024-10-06 02:30", "2024-04-07 02:30", "2024-04-06 02:30 tomorrow",
			"2024-10-05 02:30 tomorrow", "2024-06-15 12:00 +5:30 tomorrow", "jun 15 2024 12:00 EST monday",
			"2024-06-15 12:00:00 -1 second", "2024-06-15 12:00:00 999999999999 minutes", "2024-11-03 01:30:00 -1 hour",
			"2024-11-03 01:30:00 EDT -1 hour", "2024-03-10 03:30:00 -1 hour", "2024-03-10 01:30:00 +1 hour",
			"@1710055800", "@1730611800", "1945-08-15 12:00", "1918-03-31 02:30", "1883-11-18 12:00",
		} {
			zoned(zone, s)
		}
	}

	// ---- --debug, line for line ----
	for _, zone := range []string{"UTC0", "America/New_York", "Asia/Tokyo"} {
		for _, s := range []string{
			"2024-06-15 12:00", "2024-06-15", "12:00", "12:00 pm", "12:34:56.5 +0100", "next monday", "3 monday",
			"tomorrow 3pm", "EST", "EST DST", "PST", "J", "12:00 J", "2024-06-15 12:00 -0500", "2024-06-15 12:00 EDT",
			"2024-01-15 12:00 EDT", "2024-06-15 12:00 1 year 2 months 3 days 4 hours 5 minutes 6 seconds ago", "2024-01-31 +1 month",
			"2024-02-29 +1 year", "2024-06-15 12:00 +1 day", "2024-06-15 12:00 25 hours", "2024-11-02 01:30 tomorrow",
			"2024-11-03 01:30:00 EDT 24 hours ago", "2024-03-10 02:30", "2024-03-10 02:30 -0500", "12:00:60", "2024+05+03",
			"JUN+17+1992", "12345", "1700-02-29", "2024-13-01", "foo", "12 foo", "jan 1 2024 foo", "1 jan 12:00", "a.m.",
			"d.s.t", "2024-06-15 12:00 foo bar", "@1718434196.5", "@1 tomorrow", "06/15/2024", "2024/06/15", "24-06-15",
			"5-06-15", "0024-06-15", "-1-1-1", "2024-06-15 12:00 UTC +0100", "2024-06-15 12:00 +0100 +0200", "monday monday",
			"12:00 13:00", "2024-06-15 2024-06-16", "EST EDT", "EST DST DST", "2024-06-15 1000000000000000000 years",
			"2024-06-15 12:00:00 9223372036854775807 seconds", "2024-06-15 2147483647 years", "2147485548-01-01",
			"2024-06-15 12:00:00 999999999999 minutes", "2024-06-15 12:00 +99999999999999 hours", "", "TZ=\"Asia/Tokyo\" 12:00",
			"TZ=\"Asia/Tokyo\" 2024-06-15 12:00 +0100", "TZ=\"unterminated", "12:00 +0100", "13 pm", "0 am", "99999-01-01",
			"2024-06-15 12:00 -0700 +1 hour", "2024-06-15 2 sat", "2024-06-15 sat", "sat 12:00", "last sat 12:00", "12:00 2024",
		} {
			dbg(zone, s)
		}
	}
	add(invocation{name: "debug -u", args: []string{"--debug", "-u", "-d", "2024-06-15 12:00", "+%F %T %Z"}, env: []string{"TZ=America/New_York"}})
	add(invocation{name: "debug -u local word", args: []string{"--debug", "-u", "-d", "2024-06-15 12:00 EST", "+%F %T %Z"}, env: []string{"TZ=America/New_York"}})
	add(invocation{name: "debug repeated -d", args: []string{"--debug", "-d", "2024-01-01", "-d", "2024-02-02", "+%F"}})
	add(invocation{name: "repeated -d without debug", args: []string{"-d", "2024-01-01", "-d", "2024-02-02", "+%F"}})
	add(invocation{name: "debug repeated -s invalid", args: []string{"--debug", "-s", "foo", "-s", "bar"}})
	add(invocation{name: "debug with -r", args: []string{"--debug", "-r", "ref", "+%s.%N"}, seedTree: dateTree})
	add(invocation{name: "debug with --resolution", args: []string{"--debug", "--resolution"}})
	add(invocation{name: "debug batch", args: []string{"--debug", "-f", "lines", "+%F %T"}, seedTree: dateTree})

	// ---- formats ----
	for _, c := range []string{"a", "A", "b", "B", "c", "C", "d", "D", "e", "F", "g", "G", "h", "H", "I", "j", "k", "l", "m", "M", "n", "N", "p", "P", "q", "r", "R", "s", "S", "t", "T", "u", "U", "V", "w", "W", "x", "X", "y", "Y", "z", "Z", "%", "Q", "E", "O"} {
		for _, flag := range []string{"", "-", "_", "0", "^", "#", "+", "_#", "0+", "+0", "+_", "^0"} {
			for _, w := range []string{"", "1", "3", "5", "12"} {
				fmtc("%" + flag + w + c)
			}
		}
	}
	for _, f := range []string{
		"%:z", "%::z", "%:::z", "%::::z", "%-:z", "%_::z", "%:5z", "%:Z", "%Ez", "%Oy", "%EY", "%OY", "%OS", "%E", "%O",
		"%5", "%", "%%%", "abc%", "%-", "%_5", "%:", "%10%", "%^%", "%5n", "%-5t", "%^a%^b %#Z %#a %#p %^p %#P %^P", "%+%",
		"%:::5z", "%-::z", "%12:z", "%012:z", "%_12:z", "%+12:z", "%-12:z", "%3z", "%_3z", "%12z", "%+z", "%^z", "%E%",
		"%5E", "%EE", "%5Ez", "%Ea", "%OA", "%Ob", "%Ec", "%Oc", "%Ex", "%Ox", "%EX", "%OX", "%Ed", "%Od", "%Ee", "%OH",
		"%EH", "%Ej", "%Oj", "%EN", "%ON", "%Ep", "%OP", "%Eq", "%Es", "%ES", "%Eu", "%OU", "%EU", "%EV", "%EG", "%Eg",
		"%EW", "%Ew", "%EC", "%OC", "%EZ", "%Oz", "%En", "%Et", "%ED", "%OD", "%EF", "%OF", "%Er", "%ER", "%ET", "%OT",
		"%Ey", "%12Ec", "%-Ex", "%^Oc", "%Q%", "%%5%", "%5%5%", "", " ", "x", "%-N", "%%-N", "%-N%-N", "%-3N", "%--N",
		"%9N", "%N%N", "%s.%N", "%a, %d %b %Y %H:%M:%S %z", "%Y-%m-%dT%H:%M:%S,%N%:z", "%Y-%m-%d %H:%M:%S.%N%:z",
		"%FT%T%:z", "%c%n%c", "%t%T%t", "\xff%Y\xfe", "%Y\n%m", "%%%%", "%%Y", "%^_#0-+12Y", "%00000000012Y",
	} {
		fmtc(f)
	}
	// Formats at other instants: the epoch, before it, a leap day, the
	// year-boundary ISO weeks, five-digit years and the biggest year.
	for _, when := range []string{"@0", "@-1", "@-86401", "2024-02-29 00:00:00", "2024-12-30", "2021-01-03 01:02:03", "2020-12-31", "2027-01-01", "10000-01-01 23:59:59", "275817-05-26 04:05:06", "1900-01-01 00:00:00", "0001-01-01", "0000-01-01", "2147485547-12-31 23:59:59", "1969-12-31 23:59:59.999999999"} {
		for _, f := range []string{"%F %T %N %s", "%c", "%C %y %g %G %V %U %W %j %u %w %q", "%+F %+Y %+C %+G %+g %+y", "%12Y %-Y %_Y %012Y %+12Y", "%x %X %D %r %R %e %k %l %I %p %P"} {
			add(invocation{name: "format " + f + " at " + when, args: []string{"-d", when, "+" + f}})
		}
	}
	// A zone with seconds in its offset and one west of Greenwich.
	for _, zone := range []string{"Africa/Monrovia", "Asia/Kathmandu", "America/New_York", "Pacific/Chatham", "XXX-5:45:30", "YYY8"} {
		for _, when := range []string{"1970-01-01 00:00:00", "1971-06-15 12:00", "2024-06-15 12:34:56.5", "2024-01-15 12:34:56.5"} {
			add(invocation{name: zone + " format at " + when, args: []string{"-d", when, "+%z %:z %::z %:::z %Z %_12::z %-:z %12z"}, env: []string{"TZ=" + zone}})
		}
	}
	add(invocation{name: "negative zero zone", args: []string{"-d", "2024-06-15 12:00", "+%z %:z %::z %:::z %Z"}, env: []string{"TZ=<-00>0"}})
	add(invocation{name: "TZ empty", args: []string{"-d", "2024-06-15 12:00", "+%F %T %Z %z"}, env: []string{"TZ="}})
	add(invocation{name: "TZ colon only", args: []string{"-d", "2024-06-15 12:00", "+%F %T %Z %z"}, env: []string{"TZ=:"}})
	add(invocation{name: "TZ bogus", args: []string{"-d", "2024-06-15 12:00", "+%F %T %Z %z"}, env: []string{"TZ=Not/AZone"}})

	// ---- the fixed output formats ----
	for _, o := range [][]string{{"-I"}, {"-Id"}, {"-Ih"}, {"-Im"}, {"-Is"}, {"-Ins"}, {"-Idate"}, {"-Ihours"}, {"-Iminutes"}, {"-Iseconds"}, {"-Ins"}, {"--iso-8601"}, {"--iso-8601=date"}, {"--iso-8601=s"}, {"--iso=n"}, {"--i=h"}, {"-R"}, {"--rfc-email"}, {"--rfc-822"}, {"--rfc-2822"}, {"--rfc-8"}, {"--rfc-e"}, {"--rfc-3339=date"}, {"--rfc-3339=d"}, {"--rfc-3339=seconds"}, {"--rfc-3339=s"}, {"--rfc-3339=ns"}, {"--rfc-3339=n"}, {"--rfc-3339", "date"}, {"-I", "-d", "2024-06-15 12:34:56.5"}} {
		args := append([]string{}, o...)
		if !strings.Contains(strings.Join(o, " "), "-d") {
			args = append(args, "-d", "2024-06-15 12:34:56.123456789")
		}
		add(invocation{name: strings.Join(o, " "), args: args})
		add(invocation{name: "Tokyo " + strings.Join(o, " "), args: args, env: []string{"TZ=Asia/Tokyo"}})
	}
	add(invocation{name: "-I with a separate operand", args: []string{"-I", "h", "-d", "2024-06-15"}})
	add(invocation{name: "-I= empty", args: []string{"-I=", "-d", "2024-06-15"}})
	add(invocation{name: "-I=x", args: []string{"-I=x"}})
	add(invocation{name: "-Ix", args: []string{"-Ix"}})
	add(invocation{name: "-Ida ambiguous", args: []string{"-Ida"}})
	add(invocation{name: "--iso-8601=hour prefix", args: []string{"--iso-8601=hour", "-d", "2024-06-15"}})
	add(invocation{name: "--rfc-3339 needs a value", args: []string{"--rfc-3339"}})
	add(invocation{name: "--rfc-3339=x", args: []string{"--rfc-3339=x"}})
	add(invocation{name: "--rfc-3339=hours", args: []string{"--rfc-3339=hours"}})
	add(invocation{name: "--rfc-3339=minutes", args: []string{"--rfc-3339=minutes"}})
	add(invocation{name: "--rfc-3339=", args: []string{"--rfc-3339="}})
	add(invocation{name: "--rfc-3339=n'x", args: []string{"--rfc-3339=n'x"}})
	add(invocation{name: "-R -I", args: []string{"-R", "-I"}})
	add(invocation{name: "-I -R", args: []string{"-I", "-R"}})
	add(invocation{name: "-I -I", args: []string{"-I", "-I"}})
	add(invocation{name: "-R then +FORMAT", args: []string{"-R", "+%F"}})
	add(invocation{name: "+FORMAT then -R", args: []string{"+%F", "-R"}})
	add(invocation{name: "--rfc-3339 then -I", args: []string{"--rfc-3339=date", "-I"}})
	add(invocation{name: "-R -x", args: []string{"-R", "-x"}})
	add(invocation{name: "-R -I -x", args: []string{"-R", "-I", "-x"}})

	// ---- operands ----
	add(invocation{name: "two formats", args: []string{"+%F", "+%T"}})
	add(invocation{name: "format then junk", args: []string{"+%F", "x"}})
	add(invocation{name: "an operand with -d", args: []string{"-d", "2024-01-01", "2024"}})
	add(invocation{name: "an operand with -r", args: []string{"-r", "ref", "x"}, seedTree: dateTree})
	add(invocation{name: "an operand with --resolution", args: []string{"--resolution", "x"}})
	add(invocation{name: "an operand with -s", args: []string{"-s", "foo", "x"}})
	add(invocation{name: "an operand with -f", args: []string{"-f", "lines", "x"}, seedTree: dateTree})
	add(invocation{name: "a quoted operand", args: []string{"-d", "2024-01-01", "it's"}})
	add(invocation{name: "an invalid operand", args: []string{"2024"}})
	add(invocation{name: "an invalid operand with a bad month", args: []string{"13011200"}})
	add(invocation{name: "an invalid operand with a bad day", args: []string{"01321200"}})
	add(invocation{name: "an invalid operand with a bad hour", args: []string{"01012500"}})
	add(invocation{name: "an invalid operand with bad seconds", args: []string{"01011200.61"}})
	add(invocation{name: "an invalid operand with a short fraction", args: []string{"01011200.6"}})
	add(invocation{name: "an invalid operand with letters", args: []string{"0101120a"}})
	add(invocation{name: "an invalid operand that is odd", args: []string{"010112001"}})
	add(invocation{name: "an invalid operand feb 30", args: []string{"02301200"}})
	add(invocation{name: "an invalid operand with -u", args: []string{"-u", "2024"}})
	add(invocation{name: "an empty operand", args: []string{""}})
	add(invocation{name: "a plus alone", args: []string{"+"}})
	add(invocation{name: "a plus with -d", args: []string{"-d", "2024-06-15", "+"}})
	add(invocation{name: "a percent alone", args: []string{"-d", "2024-06-15", "+%"}})
	add(invocation{name: "an empty format", args: []string{"-d", "2024-06-15", "--", "+"}})
	add(invocation{name: "dashdash then a format", args: []string{"-d", "2024-06-15", "--", "+%F"}})
	add(invocation{name: "dashdash then an invalid operand", args: []string{"--", "-d"}})
	add(invocation{name: "a format before -d", args: []string{"+%F", "-d", "2024-06-15"}})
	add(invocation{name: "an option-shaped format", args: []string{"-d", "2024-06-15", "+-x"}})

	// ---- the date sources ----
	add(invocation{name: "-d and -r", args: []string{"-d", "x", "-r", "ref"}, seedTree: dateTree})
	add(invocation{name: "-d and -f", args: []string{"-d", "x", "-f", "lines"}, seedTree: dateTree})
	add(invocation{name: "-r and --resolution", args: []string{"-r", "ref", "--resolution"}, seedTree: dateTree})
	add(invocation{name: "-d and -s", args: []string{"-d", "x", "-s", "y"}})
	add(invocation{name: "-s and -r", args: []string{"-s", "y", "-r", "ref"}, seedTree: dateTree})
	add(invocation{name: "-s and --resolution", args: []string{"-s", "y", "--resolution"}})
	add(invocation{name: "-s invalid", args: []string{"-s", "foo"}})
	add(invocation{name: "-s invalid with a format", args: []string{"-s", "foo", "+%F"}})
	add(invocation{name: "--set invalid", args: []string{"--set=2024-13-01"}})
	add(invocation{name: "-s with a bad -I", args: []string{"-s", "foo", "-Ix"}})
	add(invocation{name: "-r a file", args: []string{"-r", "ref", "+%s.%N %F %T"}, seedTree: dateTree})
	add(invocation{name: "-r through a symlink", args: []string{"-r", "lref", "+%s.%N"}, seedTree: dateTree})
	add(invocation{name: "-r a file in Tokyo", args: []string{"-r", "ref", "+%s.%N %F %T %Z"}, seedTree: dateTree, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-r a file with -u", args: []string{"-u", "-r", "ref", "+%s.%N %F %T %Z"}, seedTree: dateTree, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-r a file with -I", args: []string{"-r", "ref", "-Ins"}, seedTree: dateTree})
	add(invocation{name: "-r a file with -R", args: []string{"-r", "ref", "-R"}, seedTree: dateTree})
	add(invocation{name: "--reference a file", args: []string{"--reference=ref", "+%s.%N"}, seedTree: dateTree})
	add(invocation{name: "--ref a file", args: []string{"--ref", "ref", "+%s.%N"}, seedTree: dateTree})
	add(invocation{name: "--r a file", args: []string{"--r", "ref", "+%s.%N"}, seedTree: dateTree})
	add(invocation{name: "-r a missing file", args: []string{"-r", "missing"}})
	add(invocation{name: "-r a missing file with a format", args: []string{"-r", "missing", "+%F"}})
	add(invocation{name: "-r a quoted missing name", args: []string{"-r", "mis'sing"}})
	add(invocation{name: "-r a directory", args: []string{"-r", "d", "+%F"}, seedTree: dateTree})
	add(invocation{name: "-r a dangling symlink", args: []string{"-r", "dangling", "+%F"}, seedTree: func(t *testing.T, dir string) {
		if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
	}})
	add(invocation{name: "-r a missing component", args: []string{"-r", "nodir/f"}})
	add(invocation{name: "-r twice", args: []string{"-r", "missing", "-r", "ref", "+%s.%N"}, seedTree: dateTree})
	add(invocation{name: "-r an empty name", args: []string{"-r", ""}})
	add(invocation{name: "--resolution", args: []string{"--resolution"}})
	add(invocation{name: "--resolution with a format", args: []string{"--resolution", "+%s %N %F %T"}})
	add(invocation{name: "--resolution with -u", args: []string{"--resolution", "-u"}})
	add(invocation{name: "--resolution with -I", args: []string{"--resolution", "-Ins"}})
	add(invocation{name: "--res", args: []string{"--res"}})
	add(invocation{name: "--r is ambiguous", args: []string{"--r"}})
	add(invocation{name: "--rfc is ambiguous", args: []string{"--rfc"}})
	add(invocation{name: "--u is ambiguous", args: []string{"--u", "-d", "2024-06-15", "+%Z"}})
	add(invocation{name: "--ut", args: []string{"--ut", "-d", "2024-06-15", "+%Z"}})
	add(invocation{name: "--uct", args: []string{"--uct", "-d", "2024-06-15", "+%Z"}})
	add(invocation{name: "--universal", args: []string{"--universal", "-d", "2024-06-15", "+%Z"}})
	add(invocation{name: "--univ", args: []string{"--univ", "-d", "2024-06-15", "+%Z"}})
	add(invocation{name: "-u in Tokyo", args: []string{"-u", "-d", "2024-06-15 12:00", "+%F %T %Z %z"}, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-u after -d in Tokyo", args: []string{"-d", "2024-06-15 12:00", "-u", "+%F %T %Z %z"}, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-u with a prefix zone", args: []string{"-u", "-d", "TZ=\"Asia/Tokyo\" 2024-06-15 12:00", "+%F %T %Z %z"}, env: []string{"TZ=America/New_York"}})
	add(invocation{name: "-u twice", args: []string{"-uu", "-d", "2024-06-15 12:00", "+%F %T %Z"}, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-u with an invalid operand", args: []string{"-u", "x"}})
	add(invocation{name: "-d in the -u zone words", args: []string{"-u", "-d", "2024-06-15 12:00 UTC DST", "+%F %T %Z"}})
	add(invocation{name: "-ud glued", args: []string{"-ud", "2024-06-15 12:00", "+%F %T %Z"}, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-d needs a value", args: []string{"-d"}})
	add(invocation{name: "--date needs a value", args: []string{"--date"}})
	add(invocation{name: "--date=", args: []string{"--date=", "+%F"}})
	add(invocation{name: "--da", args: []string{"--da=2024-06-15", "+%F"}})
	add(invocation{name: "--d is ambiguous", args: []string{"--d=2024-06-15", "+%F"}})
	add(invocation{name: "--debug alone", args: []string{"--debug", "-d", "2024-06-15", "+%F"}})
	add(invocation{name: "--deb", args: []string{"--deb", "-d", "2024-06-15", "+%F"}})
	add(invocation{name: "--debug=x", args: []string{"--debug=x", "-d", "2024-06-15"}})
	add(invocation{name: "-f needs a value", args: []string{"-f"}})
	add(invocation{name: "-x", args: []string{"-x"}})
	add(invocation{name: "--foo", args: []string{"--foo"}})
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "-dx cluster", args: []string{"-dx"}})
	add(invocation{name: "-Rd", args: []string{"-Rd", "2024-06-15"}})
	add(invocation{name: "-dR", args: []string{"-dR"}})
	add(invocation{name: "-uR", args: []string{"-uR", "-d", "2024-06-15 12:34:56"}, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "option after operand", args: []string{"+%F", "-d", "2024-06-15"}})
	add(invocation{name: "POSIXLY_CORRECT stops at the operand", args: []string{"+%F", "-d", "2024-06-15"}, env: []string{"POSIXLY_CORRECT=1"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	// ---- -f ----
	add(invocation{name: "-f a file", args: []string{"-f", "lines", "+%F %T %N"}, seedTree: dateTree})
	add(invocation{name: "-f a file with the default format", args: []string{"-f", "lines"}, seedTree: dateTree})
	add(invocation{name: "-f a file in Tokyo", args: []string{"-f", "lines", "+%F %T %Z"}, seedTree: dateTree, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-f a file with -u", args: []string{"-u", "-f", "lines", "+%F %T %Z"}, seedTree: dateTree, env: []string{"TZ=Asia/Tokyo"}})
	add(invocation{name: "-f a file with -I", args: []string{"-f", "lines", "-Ins"}, seedTree: dateTree})
	add(invocation{name: "-f a file with -R", args: []string{"-f", "lines", "-R"}, seedTree: dateTree})
	add(invocation{name: "-f an unterminated file", args: []string{"-f", "unterminated", "+%F"}, seedTree: dateTree})
	add(invocation{name: "-f an empty file", args: []string{"-f", "empty", "+%F"}, seedTree: dateTree})
	add(invocation{name: "-f a missing file", args: []string{"-f", "missing"}})
	add(invocation{name: "-f a quoted missing file", args: []string{"-f", "mis sing"}})
	add(invocation{name: "-f a directory", args: []string{"-f", "d", "+%F"}, seedTree: dateTree})
	add(invocation{name: "-f stdin", args: []string{"-f", "-", "+%F %T"}, stdin: "2024-01-01\nfoo\n\n2024-02-02 12:00\n"})
	add(invocation{name: "-f stdin unterminated", args: []string{"-f", "-", "+%F %T"}, stdin: "2024-01-01"})
	add(invocation{name: "-f empty stdin", args: []string{"-f", "-", "+%F %T"}})
	add(invocation{name: "-f stdin with a NUL", args: []string{"-f", "-", "+%F %T"}, stdin: "2024-01-01\x0012:00\n2024-02-02\n"})
	add(invocation{name: "-f stdin with CRLF", args: []string{"-f", "-", "+%F %T"}, stdin: "2024-01-01\r\n2024-02-02\r\n"})
	add(invocation{name: "-f stdin from a directory", args: []string{"-f", "-", "+%F"}, stdinPath: "."})
	add(invocation{name: "--file", args: []string{"--file=lines", "+%F"}, seedTree: dateTree})
	add(invocation{name: "--fi", args: []string{"--fi", "lines", "+%F"}, seedTree: dateTree})
	add(invocation{name: "-f with an option-shaped line", args: []string{"-f", "-", "+%F"}, stdin: "-d 2024-01-01\n"})
	add(invocation{name: "-f each line keeps its own offset guess", args: []string{"-f", "-", "+%F %T %Z"}, stdin: "2024-11-03 01:30\n2024-11-03 01:30\n2024-03-10 03:30\n2024-11-03 01:30\n", env: []string{"TZ=America/New_York"}})
	add(invocation{name: "-f twice", args: []string{"-f", "missing", "-f", "lines", "+%F"}, seedTree: dateTree})

	// ---- the write-failure paths ----
	add(invocation{name: "a closed stdout", args: []string{"-d", "2024-06-15"}, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with an invalid date", args: []string{"-d", "x y"}, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with -f", args: []string{"-f", "lines"}, seedTree: dateTree, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"-d", "2024-06-15"}, stdout: stdoutFull})
	add(invocation{name: "a full stdout with -f", args: []string{"-f", "lines"}, seedTree: dateTree, stdout: stdoutFull})
	add(invocation{name: "a full stdout with --debug", args: []string{"--debug", "-d", "2024-06-15"}, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a usage error", args: []string{"-x"}, stdout: stdoutFull})

	return cases
}

func TestDateParity(t *testing.T) {
	requireParity(t, "date", dateCases(t))
}

func TestDateHelpVersion(t *testing.T) {
	requireHelp(t, "date", []string{"--help"}, 0)
	requireHelp(t, "date", []string{"--hel"}, 0)
	requireHelp(t, "date", []string{"--help", "extra"}, 0)
	requireHelp(t, "date", []string{"-d", "x", "--help"}, 0)
	requireVersion(t, "date", []string{"--version"}, 0)
	requireVersion(t, "date", []string{"--vers"}, 0)
	requireVersion(t, "date", []string{"--version", "-d", "x"}, 0)
}
