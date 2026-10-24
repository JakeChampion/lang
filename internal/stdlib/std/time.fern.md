# Dates, times, and durations

This module provides dates, times, durations, and fixed-offset time zones.

## Model

Time values separate calendar dates, clock times, durations, and fixed UTC offsets. Public constants define common unit conversions.

## Contracts

A fixed offset does not describe seasonal clock transitions. Use the time-zone module when local civil-time rules matter.

## Executable example

```fern test name=std-time-example
import "std/time";

function main(): i32 {
  if (time.SECONDS_PER_HOUR != 3600 || time.NANOS_PER_SECOND != 1000000000) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub const NANOS_PER_SECOND: i32>>

<<4 pub function instant_from_unix(sec: i64): Instant>>

<<5 function __days_from_civil(y_in: i32, m: i32, d: i32): i32>>

<<6 pub function is_leap_year(y: i32): boolean>>

<<7 function __pad2(n: i32): string>>

<<8 pub function (d: Date) format_iso(): string>>

<<9 function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean>>

<<10 pub function instant_parse_rfc3339(s: string): Option[Instant]>>

<<11 function __weekday_short(n: i32): string>>

<<12 pub function month_name(m: i32): string>>

<<13 function __month_from_short(s: string, start: i32): i32>>

<<14 pub function http_date_parse(s: string): Option[Instant]>>

<<15 function __http_instant(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Instant]>>

<<16 pub function timezone_fixed_offset(offset_seconds: i32): TimeZone>>

<<17 pub function instant_zoned_parse_rfc3339(s: string): Option[Zoned]>>

<<18 pub function span_years(n: i32): Span>>

<<19 function __rel_unit(n: i64, unit: string): string>>

<<20 pub function (i: Instant) relative_to(now: Instant): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides dates, times, durations, and fixed-offset time zones.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/i64";
import "std/string";
```

### `3 pub const NANOS_PER_SECOND: i32`

```fern
<<3 pub const NANOS_PER_SECOND: i32>>=
pub const NANOS_PER_SECOND: i32 = 1000000000;

pub const NANOS_PER_MILLI: i32 = 1000000;

pub const NANOS_PER_MICRO: i32 = 1000;

pub const SECONDS_PER_MINUTE: i32 = 60;

pub const SECONDS_PER_HOUR: i32 = 3600;

pub const SECONDS_PER_DAY: i32 = 86400;

pub const MINUTES_PER_HOUR: i32 = 60;

pub const HOURS_PER_DAY: i32 = 24;

pub const DAYS_PER_WEEK: i32 = 7;
```

### `4 pub function instant_from_unix(sec: i64): Instant`

```fern
<<4 pub function instant_from_unix(sec: i64): Instant>>=
pub function instant_from_unix(sec: i64): Instant {
  return Instant { sec: sec, nsec: 0 };
}

pub function instant_now(): Instant {
  let ms: i64 = now_unix_ms();
  let sec: i64 = ms / (1000 as i64);
  let rem_ms: i64 = ms - sec * (1000 as i64);
  let ns: i32 = rem_ms as i32 * 1000000;
  return Instant { sec: sec, nsec: ns };
}

pub function date_make(year: i32, month: i32, day: i32): Date {
  return Date { year: year, month: month, day: day };
}

pub function time_make(hour: i32, minute: i32, second: i32): Time {
  return Time { hour: hour, minute: minute, second: second, nsec: 0 };
}

pub function datetime_make(d: Date, t: Time): DateTime {
  return DateTime { date: d, time: t };
}

pub function timezone_utc(): TimeZone {
  return TimeZone { name: "UTC", offset_seconds: 0 };
}

pub function duration_seconds(s: i64): Duration {
  return Duration { sec: s, nsec: 0 };
}

pub function duration_millis(ms: i64): Duration {
  let s: i64 = ms / (1000 as i64);
  let rem_ms: i64 = ms - s * (1000 as i64);
  let ns: i32 = rem_ms as i32 * 1000000;
  return Duration { sec: s, nsec: ns };
}

pub function duration_nanos(ns: i64): Duration {
  let s: i64 = ns / (1000000000 as i64);
  let rem: i64 = ns - s * (1000000000 as i64);
  return Duration { sec: s, nsec: rem as i32 };
}

pub function (d: Duration) as_nanos(): i64 {
  return d.sec * (1000000000 as i64) + d.nsec as i64;
}

pub function (d: Duration) as_millis(): i64 {
  return d.as_nanos() / (1000000 as i64);
}

pub function (d: Duration) to_string(): string {
  let total_ms: i64 = d.sec * (1000 as i64) + (d.nsec / 1000000) as i64;
  if (total_ms == 0 as i64) {
    return "0s";
  }
  let neg: boolean = total_ms < 0 as i64;
  let ms: i64 = total_ms;
  if (neg) {
    ms = 0 as i64 - ms;
  }
  let h: i64 = ms / (3600000 as i64);
  let m: i64 = ms % (3600000 as i64) / (60000 as i64);
  let s: i64 = ms % (60000 as i64) / (1000 as i64);
  let msec: i64 = ms % (1000 as i64);
  let out: string = "";
  if (neg) {
    out = "-";
  }
  if (h > 0 as i64) {
    out = out + h.to_string() + "h";
  }
  if (m > 0 as i64) {
    out = out + m.to_string() + "m";
  }
  if (s > 0 as i64) {
    out = out + s.to_string() + "s";
  }
  if (msec > 0 as i64) {
    out = out + msec.to_string() + "ms";
  }
  return out;
}

pub function span_days(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: n,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}

pub function span_hours(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: 0,
    hours: n, minutes: 0, seconds: 0, nanos: 0,
  };
}
```

### `5 function __days_from_civil(y_in: i32, m: i32, d: i32): i32`

```fern
<<5 function __days_from_civil(y_in: i32, m: i32, d: i32): i32>>=
function __days_from_civil(y_in: i32, m: i32, d: i32): i32 {
  let y: i32 = y_in;
  if (m <= 2) {
    y = y - 1;
  }
  let era: i32 = 0;
  if (y >= 0) {
    era = y / 400;
  } else {
    era = (y - 399) / 400;
  }
  let yoe: i32 = y - era * 400;
  let mp: i32 = 0;
  if (m > 2) {
    mp = m - 3;
  } else {
    mp = m + 9;
  }
  let doy: i32 = (153 * mp + 2) / 5 + d - 1;
  let doe: i32 = yoe * 365 + yoe / 4 - yoe / 100 + doy;
  return era * 146097 + doe - 719468;
}

function __civil_from_days(z_in: i32): Date {
  let z: i32 = z_in + 719468;
  let era: i32 = 0;
  if (z >= 0) {
    era = z / 146097;
  } else {
    era = (z - 146096) / 146097;
  }
  let doe: i32 = z - era * 146097;
  let yoe: i32 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
  let y: i32 = yoe + era * 400;
  let doy: i32 = doe - (365 * yoe + yoe / 4 - yoe / 100);
  let mp: i32 = (5 * doy + 2) / 153;
  let d: i32 = doy - (153 * mp + 2) / 5 + 1;
  let m: i32 = 0;
  if (mp < 10) {
    m = mp + 3;
  } else {
    m = mp - 9;
  }
  if (m <= 2) {
    y = y + 1;
  }
  return Date { year: y, month: m, day: d };
}
```

### `6 pub function is_leap_year(y: i32): boolean`

```fern
<<6 pub function is_leap_year(y: i32): boolean>>=
pub function is_leap_year(y: i32): boolean {
  if (y % 4 != 0) {
    return false;
  }
  if (y % 100 != 0) {
    return true;
  }
  return y % 400 == 0;
}

pub function days_in_month(y: i32, m: i32): i32 {
  if (m == 1) {
    return 31;
  }
  if (m == 2) {
    if (is_leap_year(y)) {
      return 29;
    }
    return 28;
  }
  if (m == 3) {
    return 31;
  }
  if (m == 4) {
    return 30;
  }
  if (m == 5) {
    return 31;
  }
  if (m == 6) {
    return 30;
  }
  if (m == 7) {
    return 31;
  }
  if (m == 8) {
    return 31;
  }
  if (m == 9) {
    return 30;
  }
  if (m == 10) {
    return 31;
  }
  if (m == 11) {
    return 30;
  }
  if (m == 12) {
    return 31;
  }
  return 0;
}

pub function (d: Date) is_valid(): boolean {
  if (d.month < 1) {
    return false;
  }
  if (d.month > 12) {
    return false;
  }
  if (d.day < 1) {
    return false;
  }
  return d.day <= days_in_month(d.year, d.month);
}

pub function (d: Date) add_days(n: i32): Date {
  let z: i32 = __days_from_civil(d.year, d.month, d.day);
  return __civil_from_days(z + n);
}

pub function (d: Date) days_since(other: Date): i32 {
  let a: i32 = __days_from_civil(d.year, d.month, d.day);
  let b: i32 = __days_from_civil(other.year, other.month, other.day);
  return a - b;
}

pub function (d: Date) weekday(): i32 {
  let z: i32 = __days_from_civil(d.year, d.month, d.day);
  return ((z + 4) % 7 + 7) % 7;
}

pub function (d: Date) day_of_year(): i32 {
  let m: i32 = d.month;
  let mp: i32 = 0;
  if (m > 2) {
    mp = m - 3;
  } else {
    mp = m + 9;
  }
  let doy: i32 = (153 * mp + 2) / 5 + d.day;
  if (m <= 2) {
    return doy - 306;
  }
  let add_for_leap: i32 = 0;
  if (is_leap_year(d.year)) {
    add_for_leap = 1;
  }
  return doy + 59 + add_for_leap;
}
```

### `7 function __pad2(n: i32): string`

```fern
<<7 function __pad2(n: i32): string>>=
function __pad2(n: i32): string {
  if (n < 10) {
    return "0" + int.int_to_string(n);
  }
  return int.int_to_string(n);
}

function __pad4(n: i32): string {
  if (n < 10) {
    return "000" + int.int_to_string(n);
  }
  if (n < 100) {
    return "00" + int.int_to_string(n);
  }
  if (n < 1000) {
    return "0" + int.int_to_string(n);
  }
  return int.int_to_string(n);
}

function __parse_digits(s: string, start: i32, end: i32): i32 {
  if (start >= end) {
    return -1;
  }
  let acc: i32 = 0;
  let i: i32 = start;
  while (i < end) {
    let b: i32 = s[i] as i32;
    if (b < 48 || b > 57) {
      return -1;
    }
    acc = acc * 10 + (b - 48);
    i = i + 1;
  }
  return acc;
}
```

### `8 pub function (d: Date) format_iso(): string`

```fern
<<8 pub function (d: Date) format_iso(): string>>=
pub function (d: Date) format_iso(): string {
  return __pad4(d.year) + "-" + __pad2(d.month) + "-" + __pad2(d.day);
}

pub function date_parse_iso(s: string): Option[Date] {
  if (s.len() != 10) {
    return None;
  }
  if (s[4] != 45 || s[7] != 45) {
    return None;
  }
  let y: i32 = __parse_digits(s, 0, 4);
  let m: i32 = __parse_digits(s, 5, 7);
  let d: i32 = __parse_digits(s, 8, 10);
  if (y < 0 || m < 0 || d < 0) {
    return None;
  }
  return Some(Date { year: y, month: m, day: d });
}

pub function (i: Instant) format_rfc3339(): string {
  let sec: i64 = i.sec;
  let spd: i64 = 86400 as i64;
  let days: i64 = sec / spd;
  let sec_in_day: i64 = sec - days * spd;
  if (sec_in_day < 0 as i64) {
    sec_in_day = sec_in_day + spd;
    days = days - 1 as i64;
  }
  let d: Date = __civil_from_days(days as i32);
  let sid: i32 = sec_in_day as i32;
  let h: i32 = sid / 3600;
  let rem: i32 = sid - h * 3600;
  let mn: i32 = rem / 60;
  let s: i32 = rem - mn * 60;
  let head: string = d.format_iso() + "T" + __pad2(h) + ":" + __pad2(mn) + ":" + __pad2(s);
  if (i.nsec == 0) {
    return head + "Z";
  }
  let ns: i32 = i.nsec;
  let div: i32 = 100000000;
  let frac: string = "";
  while (div >= 1) {
    let digit: i32 = ns / div - ns / (div * 10) * 10;
    if (digit == 0) {
      frac = frac + "0";
    } else if (digit == 1) {
      frac = frac + "1";
    } else if (digit == 2) {
      frac = frac + "2";
    } else if (digit == 3) {
      frac = frac + "3";
    } else if (digit == 4) {
      frac = frac + "4";
    } else if (digit == 5) {
      frac = frac + "5";
    } else if (digit == 6) {
      frac = frac + "6";
    } else if (digit == 7) {
      frac = frac + "7";
    } else if (digit == 8) {
      frac = frac + "8";
    } else {
      frac = frac + "9";
    }
    if (div == 1) {
      div = 0;
    } else {
      div = div / 10;
    }
  }
  return head + "." + frac + "Z";
}
```

### `9 function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean`

```fern
<<9 function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean>>=
function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean {
  if (!Date { year: y, month: mo, day: d }.is_valid()) {
    return false;
  }
  if (h > 23 || mn > 59 || sc > 59) {
    return false;
  }
  return true;
}
```

### `10 pub function instant_parse_rfc3339(s: string): Option[Instant]`

```fern
<<10 pub function instant_parse_rfc3339(s: string): Option[Instant]>>=
pub function instant_parse_rfc3339(s: string): Option[Instant] {
  let n: i32 = s.len();
  if (n < 20) {
    return None;
  }
  if (s[4] != 45 || s[7] != 45) {
    return None;
  }
  if (s[10] != 84) {
    return None;
  }
  if (s[13] != 58 || s[16] != 58) {
    return None;
  }
  let y: i32 = __parse_digits(s, 0, 4);
  let mo: i32 = __parse_digits(s, 5, 7);
  let d: i32 = __parse_digits(s, 8, 10);
  let h: i32 = __parse_digits(s, 11, 13);
  let mn: i32 = __parse_digits(s, 14, 16);
  let sc: i32 = __parse_digits(s, 17, 19);
  if (y < 0 || mo < 0 || d < 0 || h < 0 || mn < 0 || sc < 0) {
    return None;
  }
  if (!__rfc3339_fields_valid(y, mo, d, h, mn, sc)) {
    return None;
  }
  let nsec: i32 = 0;
  if (s[19] == 46) {
    let z_idx: i32 = -1;
    let i: i32 = 20;
    while (i < n) {
      if (s[i] == 90) {
        z_idx = i;
        i = n;
      } else {
        i = i + 1;
      }
    }
    if (z_idx < 0) {
      return None;
    }
    if (z_idx == 20) {
      return None;
    }
    if (z_idx - 20 > 9) {
      return None;
    }
    let frac: i32 = __parse_digits(s, 20, z_idx);
    if (frac < 0) {
      return None;
    }
    let pad: i32 = 9 - (z_idx - 20);
    while (pad > 0) {
      frac = frac * 10;
      pad = pad - 1;
    }
    nsec = frac;
  } else if (s[19] == 90) {} else {
    return None;
  }
  let days: i32 = __days_from_civil(y, mo, d);
  let spd: i64 = 86400 as i64;
  let sec: i64 = days as i64 * spd
    + (h * 3600 + mn * 60 + sc) as i64;
  return Some(Instant { sec: sec, nsec: nsec });
}
```

### `11 function __weekday_short(n: i32): string`

```fern
<<11 function __weekday_short(n: i32): string>>=
function __weekday_short(n: i32): string {
  if (n == 0) {
    return "Sun";
  }
  if (n == 1) {
    return "Mon";
  }
  if (n == 2) {
    return "Tue";
  }
  if (n == 3) {
    return "Wed";
  }
  if (n == 4) {
    return "Thu";
  }
  if (n == 5) {
    return "Fri";
  }
  if (n == 6) {
    return "Sat";
  }
  return "";
}

function __month_short(m: i32): string {
  if (m == 1) {
    return "Jan";
  }
  if (m == 2) {
    return "Feb";
  }
  if (m == 3) {
    return "Mar";
  }
  if (m == 4) {
    return "Apr";
  }
  if (m == 5) {
    return "May";
  }
  if (m == 6) {
    return "Jun";
  }
  if (m == 7) {
    return "Jul";
  }
  if (m == 8) {
    return "Aug";
  }
  if (m == 9) {
    return "Sep";
  }
  if (m == 10) {
    return "Oct";
  }
  if (m == 11) {
    return "Nov";
  }
  if (m == 12) {
    return "Dec";
  }
  return "";
}
```

### `12 pub function month_name(m: i32): string`

```fern
<<12 pub function month_name(m: i32): string>>=
pub function month_name(m: i32): string {
  if (m == 1) {
    return "January";
  }
  if (m == 2) {
    return "February";
  }
  if (m == 3) {
    return "March";
  }
  if (m == 4) {
    return "April";
  }
  if (m == 5) {
    return "May";
  }
  if (m == 6) {
    return "June";
  }
  if (m == 7) {
    return "July";
  }
  if (m == 8) {
    return "August";
  }
  if (m == 9) {
    return "September";
  }
  if (m == 10) {
    return "October";
  }
  if (m == 11) {
    return "November";
  }
  if (m == 12) {
    return "December";
  }
  return "";
}

pub function (d: Date) weekday_name(): string {
  let n: i32 = d.weekday();
  if (n == 0) {
    return "Sunday";
  }
  if (n == 1) {
    return "Monday";
  }
  if (n == 2) {
    return "Tuesday";
  }
  if (n == 3) {
    return "Wednesday";
  }
  if (n == 4) {
    return "Thursday";
  }
  if (n == 5) {
    return "Friday";
  }
  return "Saturday";
}

pub function (i: Instant) format_http_date(): string {
  let sec: i64 = i.sec;
  let spd: i64 = 86400 as i64;
  let days: i64 = sec / spd;
  let sec_in_day: i64 = sec - days * spd;
  if (sec_in_day < 0 as i64) {
    sec_in_day = sec_in_day + spd;
    days = days - 1 as i64;
  }
  let d: Date = __civil_from_days(days as i32);
  let sid: i32 = sec_in_day as i32;
  let h: i32 = sid / 3600;
  let rem: i32 = sid - h * 3600;
  let mn: i32 = rem / 60;
  let s: i32 = rem - mn * 60;
  return __weekday_short(d.weekday()) + ", " + __pad2(d.day) + " "
    + __month_short(d.month) + " " + __pad4(d.year) + " "
    + __pad2(h) + ":" + __pad2(mn) + ":" + __pad2(s) + " GMT";
}
```

### `13 function __month_from_short(s: string, start: i32): i32`

```fern
<<13 function __month_from_short(s: string, start: i32): i32>>=
function __month_from_short(s: string, start: i32): i32 {
  if (start + 3 > s.len()) {
    return -1;
  }
  let m: str = s.slice_snap(start, start + 3);
  if (m == "Jan") {
    return 1;
  }
  if (m == "Feb") {
    return 2;
  }
  if (m == "Mar") {
    return 3;
  }
  if (m == "Apr") {
    return 4;
  }
  if (m == "May") {
    return 5;
  }
  if (m == "Jun") {
    return 6;
  }
  if (m == "Jul") {
    return 7;
  }
  if (m == "Aug") {
    return 8;
  }
  if (m == "Sep") {
    return 9;
  }
  if (m == "Oct") {
    return 10;
  }
  if (m == "Nov") {
    return 11;
  }
  if (m == "Dec") {
    return 12;
  }
  return -1;
}
```

### `14 pub function http_date_parse(s: string): Option[Instant]`

```fern
<<14 pub function http_date_parse(s: string): Option[Instant]>>=
pub function http_date_parse(s: string): Option[Instant] {
  if (s.len() == 29) {
    return __imf_fixdate(s);
  }
  if (s.len() == 24) {
    return __asctime_date(s);
  }
  return None;
}

pub function http_date_parse_at(s: string, now: Instant): Option[Instant] {
  match (http_date_parse(s)) {
    Some(i) => {
      return Some(i);
    },
    None => {}
  }
  return __rfc850_date(s, now);
}
```

### `15 function __http_instant(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Instant]`

```fern
<<15 function __http_instant(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Instant]>>=
function __http_instant(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Instant] {
  if (year < 0 || mo < 1 || day < 1 || day > days_in_month(year, mo) || !__time_of_day_ok(h, mn, sc)) {
    return None;
  }
  let days: i32 = __days_from_civil(year, mo, day);
  let sec: i64 = days as i64 * (86400 as i64) + (h * 3600 + mn * 60 + sc) as i64;
  return Some(Instant { sec: sec, nsec: 0 });
}

function __asctime_date(s: string): Option[Instant] {
  if (s[3] != 32 || s[7] != 32 || s[10] != 32 || s[19] != 32 || s[13] != 58 || s[16] != 58) {
    return None;
  }
  let day_from: i32 = 8;
  if (s[8] == 32) {
    day_from = 9;
  }
  return __http_instant(__parse_digits(s, 20, 24), __month_from_short(s, 4), __parse_digits(s, day_from, 10), __parse_digits(s, 11, 13), __parse_digits(s, 14, 16), __parse_digits(s, 17, 19));
}

function __rfc850_date(s: string, now: Instant): Option[Instant] {
  let Some(c) = s.index_of(",") else {
    return None;
  };
  if (c < 6 || s.len() != c + 24 || !__rfc850_punctuation(s, c)) {
    return None;
  }
  let yy: i32 = __parse_digits(s, c + 9, c + 11);
  if (yy < 0) {
    return None;
  }
  return __http_instant(__rfc850_year(yy, now), __month_from_short(s, c + 5), __parse_digits(s, c + 2, c + 4), __parse_digits(s, c + 12, c + 14), __parse_digits(s, c + 15, c + 17), __parse_digits(s, c + 18, c + 20));
}

function __rfc850_punctuation(s: string, c: i32): boolean {
  if (s[c + 1] != 32 || s[c + 4] != 45 || s[c + 8] != 45 || s[c + 11] != 32) {
    return false;
  }
  return s[c + 14] == 58 && s[c + 17] == 58 && s[c + 20] == 32 && s.slice_snap(c + 21, c + 24) == "GMT";
}

function __rfc850_year(yy: i32, now: Instant): i32 {
  let days: i64 = now.sec / (86400 as i64);
  if (now.sec < 0 as i64 && days * (86400 as i64) != now.sec) {
    days = days - 1 as i64;
  }
  let now_year: i32 = __civil_from_days(days as i32).year;
  let y: i32 = now_year - now_year % 100 + yy;
  if (y > now_year + 50) {
    return y - 100;
  }
  if (y + 100 <= now_year + 50) {
    return y + 100;
  }
  return y;
}

function __time_of_day_ok(h: i32, mn: i32, sc: i32): boolean {
  return h >= 0 && h <= 23 && mn >= 0 && mn <= 59 && sc >= 0 && sc <= 60;
}

function __imf_fixdate(s: string): Option[Instant] {
  if (!__imf_punctuation(s)) {
    return None;
  }
  return __http_instant(__parse_digits(s, 12, 16), __month_from_short(s, 8), __parse_digits(s, 5, 7), __parse_digits(s, 17, 19), __parse_digits(s, 20, 22), __parse_digits(s, 23, 25));
}

function __imf_punctuation(s: string): boolean {
  if (s[3] != 44 || s[4] != 32 || s[7] != 32 || s[11] != 32 || s[16] != 32 || s[25] != 32) {
    return false;
  }
  return s[19] == 58 && s[22] == 58 && s.slice_snap(26, 29) == "GMT";
}
```

### `16 pub function timezone_fixed_offset(offset_seconds: i32): TimeZone`

```fern
<<16 pub function timezone_fixed_offset(offset_seconds: i32): TimeZone>>=
pub function timezone_fixed_offset(offset_seconds: i32): TimeZone {
  let abs_sec: i32 = offset_seconds;
  let sign: string = "+";
  if (offset_seconds < 0) {
    abs_sec = -offset_seconds;
    sign = "-";
  }
  let h: i32 = abs_sec / 3600;
  let m: i32 = (abs_sec - h * 3600) / 60;
  let name: string = "UTC" + sign + __pad2(h) + ":" + __pad2(m);
  return TimeZone { name: name, offset_seconds: offset_seconds };
}

pub function (i: Instant) in_zone(z: TimeZone): Zoned {
  return Zoned { instant: i, zone: z };
}

pub function (z: Zoned) to_datetime(): DateTime {
  let spd: i64 = 86400 as i64;
  let local_sec: i64 = z.instant.sec + z.zone.offset_seconds as i64;
  let days: i64 = local_sec / spd;
  let sec_in_day: i64 = local_sec - days * spd;
  if (sec_in_day < 0 as i64) {
    sec_in_day = sec_in_day + spd;
    days = days - 1 as i64;
  }
  let d: Date = __civil_from_days(days as i32);
  let sid: i32 = sec_in_day as i32;
  let h: i32 = sid / 3600;
  let rem: i32 = sid - h * 3600;
  let mn: i32 = rem / 60;
  let s: i32 = rem - mn * 60;
  return DateTime {
    date: d,
    time: Time { hour: h, minute: mn, second: s, nsec: z.instant.nsec },
  };
}

pub function (z: Zoned) format_rfc3339(): string {
  let dt: DateTime = z.to_datetime();
  let head: string = dt.date.format_iso() + "T"
    + __pad2(dt.time.hour) + ":" + __pad2(dt.time.minute) + ":" + __pad2(dt.time.second);
  let tail: str = "";
  if (dt.time.nsec != 0) {
    let tmp: Instant = Instant { sec: 0 as i64, nsec: dt.time.nsec };
    let s: string = tmp.format_rfc3339();
    if let Some(dot) = s.index_of(".") {
      if let Some(z_idx) = s.index_of("Z") {
        tail = slice_unchecked(s, dot, z_idx);
      }
    }
  }
  if (z.zone.offset_seconds == 0) {
    return head + tail + "Z";
  }
  let off: i32 = z.zone.offset_seconds;
  let sign: string = "+";
  if (off < 0) {
    off = -off;
    sign = "-";
  }
  let oh: i32 = off / 3600;
  let om: i32 = (off - oh * 3600) / 60;
  return head + tail + sign + __pad2(oh) + ":" + __pad2(om);
}
```

### `17 pub function instant_zoned_parse_rfc3339(s: string): Option[Zoned]`

```fern
<<17 pub function instant_zoned_parse_rfc3339(s: string): Option[Zoned]>>=
pub function instant_zoned_parse_rfc3339(s: string): Option[Zoned] {
  let n: i32 = s.len();
  if (n < 20) {
    return None;
  }
  if (s[4] != 45 || s[7] != 45) {
    return None;
  }
  if (s[10] != 84) {
    return None;
  }
  if (s[13] != 58 || s[16] != 58) {
    return None;
  }
  let y: i32 = __parse_digits(s, 0, 4);
  let mo: i32 = __parse_digits(s, 5, 7);
  let d: i32 = __parse_digits(s, 8, 10);
  let h: i32 = __parse_digits(s, 11, 13);
  let mn: i32 = __parse_digits(s, 14, 16);
  let sc: i32 = __parse_digits(s, 17, 19);
  if (y < 0 || mo < 0 || d < 0 || h < 0 || mn < 0 || sc < 0) {
    return None;
  }
  if (!__rfc3339_fields_valid(y, mo, d, h, mn, sc)) {
    return None;
  }
  let pos: i32 = 19;
  let nsec: i32 = 0;
  if (s[pos] == 46) {
    let frac_start: i32 = pos + 1;
    let frac_end: i32 = -1;
    let i: i32 = frac_start;
    while (i < n) {
      let b: i32 = s[i] as i32;
      if (b == 90 || b == 43 || b == 45) {
        frac_end = i;
        i = n;
      } else {
        i = i + 1;
      }
    }
    if (frac_end < 0) {
      return None;
    }
    if (frac_end == frac_start) {
      return None;
    }
    if (frac_end - frac_start > 9) {
      return None;
    }
    let frac: i32 = __parse_digits(s, frac_start, frac_end);
    if (frac < 0) {
      return None;
    }
    let pad: i32 = 9 - (frac_end - frac_start);
    while (pad > 0) {
      frac = frac * 10;
      pad = pad - 1;
    }
    nsec = frac;
    pos = frac_end;
  }
  let offset_seconds: i32 = 0;
  let zone_name: string = "";
  if (s[pos] == 90) {
    if (pos + 1 != n) {
      return None;
    }
    zone_name = "UTC";
  } else if (s[pos] == 43 || s[pos] == 45) {
    if (pos + 6 != n) {
      return None;
    }
    if (s[pos + 3] != 58) {
      return None;
    }
    let oh: i32 = __parse_digits(s, pos + 1, pos + 3);
    let om: i32 = __parse_digits(s, pos + 4, pos + 6);
    if (oh < 0 || om < 0) {
      return None;
    }
    if (oh > 23 || om > 59) {
      return None;
    }
    offset_seconds = oh * 3600 + om * 60;
    if (s[pos] == 45) {
      offset_seconds = -offset_seconds;
    }
    let sign_str: string = "+";
    if (s[pos] == 45) {
      sign_str = "-";
    }
    zone_name = "UTC" + sign_str + __pad2(oh) + ":" + __pad2(om);
  } else {
    return None;
  }
  let days: i32 = __days_from_civil(y, mo, d);
  let spd: i64 = 86400 as i64;
  let local_sec: i64 = days as i64 * spd
    + (h * 3600 + mn * 60 + sc) as i64;
  let utc_sec: i64 = local_sec - offset_seconds as i64;
  return Some(Zoned {
    instant: Instant { sec: utc_sec, nsec: nsec },
    zone: TimeZone { name: zone_name, offset_seconds: offset_seconds },
  });
}

pub function span_seconds(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: 0,
    hours: 0, minutes: 0, seconds: n, nanos: 0,
  };
}

pub function span_minutes(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: 0,
    hours: 0, minutes: n, seconds: 0, nanos: 0,
  };
}

pub function span_weeks(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: n, days: 0,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}

pub function span_months(n: i32): Span {
  return Span {
    years: 0, months: n, weeks: 0, days: 0,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}
```

### `18 pub function span_years(n: i32): Span`

```fern
<<18 pub function span_years(n: i32): Span>>=
pub function span_years(n: i32): Span {
  return Span {
    years: n, months: 0, weeks: 0, days: 0,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}

pub function (d: Date) add_span(s: Span): Date {
  let serial_month: i32 = d.year * 12 + (d.month - 1) + s.years * 12 + s.months;
  let new_year: i32 = 0;
  let new_month_zero: i32 = 0;
  if (serial_month >= 0) {
    new_year = serial_month / 12;
    new_month_zero = serial_month - new_year * 12;
  } else {
    new_year = (serial_month - 11) / 12;
    new_month_zero = serial_month - new_year * 12;
  }
  let new_month: i32 = new_month_zero + 1;
  let max_day: i32 = days_in_month(new_year, new_month);
  let clamped_day: i32 = d.day;
  if (clamped_day > max_day) {
    clamped_day = max_day;
  }
  let anchor: Date = Date { year: new_year, month: new_month, day: clamped_day };
  let total_days: i32 = s.weeks * 7 + s.days;
  if (total_days == 0) {
    return anchor;
  }
  return anchor.add_days(total_days);
}

pub function (i: Instant) add_duration(d: Duration): Instant {
  let sec: i64 = i.sec + d.sec;
  let nsec: i32 = i.nsec + d.nsec;
  let nps: i32 = 1000000000;
  if (nsec >= nps) {
    nsec = nsec - nps;
    sec = sec + 1 as i64;
  } else if (nsec < 0) {
    nsec = nsec + nps;
    sec = sec - 1 as i64;
  }
  return Instant { sec: sec, nsec: nsec };
}

pub function (i: Instant) duration_since(other: Instant): Duration {
  let sec: i64 = i.sec - other.sec;
  let nsec: i32 = i.nsec - other.nsec;
  let nps: i32 = 1000000000;
  if (nsec < 0) {
    nsec = nsec + nps;
    sec = sec - 1 as i64;
  } else if (nsec >= nps) {
    nsec = nsec - nps;
    sec = sec + 1 as i64;
  }
  return Duration { sec: sec, nsec: nsec };
}

pub function (d: Date) days_until(other: Date): Span {
  let delta: i32 = other.days_since(d);
  return span_days(delta);
}
```

### `19 function __rel_unit(n: i64, unit: string): string`

```fern
<<19 function __rel_unit(n: i64, unit: string): string>>=
function __rel_unit(n: i64, unit: string): string {
  let s: string = n.to_string() + " " + unit;
  if (n != 1 as i64) {
    s = s + "s";
  }
  return s;
}

function __rel_phrase(secs: i64): string {
  if (secs < 60 as i64) {
    return __rel_unit(secs, "second");
  }
  let mins: i64 = secs / (60 as i64);
  if (mins < 60 as i64) {
    return __rel_unit(mins, "minute");
  }
  let hours: i64 = mins / (60 as i64);
  if (hours < 24 as i64) {
    return __rel_unit(hours, "hour");
  }
  let days: i64 = hours / (24 as i64);
  if (days < 30 as i64) {
    return __rel_unit(days, "day");
  }
  let months: i64 = days / (30 as i64);
  if (months < 12 as i64) {
    return __rel_unit(months, "month");
  }
  let years: i64 = days / (365 as i64);
  return __rel_unit(years, "year");
}
```

### `20 pub function (i: Instant) relative_to(now: Instant): string`

```fern
<<20 pub function (i: Instant) relative_to(now: Instant): string>>=
pub function (i: Instant) relative_to(now: Instant): string {
  let raw: i64 = i.sec - now.sec;
  let mag: i64 = raw;
  if (mag < 0 as i64) {
    mag = 0 as i64 - mag;
  }
  if (mag < 5 as i64) {
    return "just now";
  }
  let phrase: string = __rel_phrase(mag);
  if (raw < 0 as i64) {
    return phrase + " ago";
  }
  return "in " + phrase;
}
```
